package cli

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
)

// Project is bunker.json: which server and site a folder deploys to.
type Project struct {
	Server string `json:"server,omitempty"`
	Site   string `json:"site,omitempty"`
	// Dir is the folder to upload (relative), e.g. "dist". Optional.
	Dir string `json:"dir,omitempty"`
	// Build overrides the build command, e.g. "npm run build". Optional.
	Build string `json:"build,omitempty"`

	path string
}

const projectFile = "bunker.json"

func readProject(dir string) (*Project, error) {
	p := &Project{path: filepath.Join(dir, projectFile)}
	b, err := os.ReadFile(p.path)
	if errors.Is(err, fs.ErrNotExist) {
		return p, nil
	}
	if err != nil {
		return nil, err
	}
	if err := json.Unmarshal(b, p); err != nil {
		return nil, fmt.Errorf("%s is not valid JSON: %v", p.path, err)
	}
	return p, nil
}

func (p *Project) exists() bool {
	_, err := os.Stat(p.path)
	return err == nil
}

func (p *Project) save() error {
	b, _ := json.MarshalIndent(p, "", "  ")
	return os.WriteFile(p.path, append(b, '\n'), 0o644)
}

// cwdProject reads bunker.json in the current directory (or an empty one).
func cwdProject() *Project {
	p, err := readProject(".")
	if err != nil || p == nil {
		return &Project{path: projectFile}
	}
	return p
}

// ---- ignore rules (.bunkerignore / .gitignore basics) ----

type ignoreRule struct {
	neg, dirOnly, anchored bool
	segs                   []string
}

// Ignorer matches paths against gitignore-style rules; the last match wins.
type Ignorer struct{ rules []ignoreRule }

var defaultIgnore = []string{
	".git/", ".svn/", ".hg/", "node_modules/", ".DS_Store", "Thumbs.db", "desktop.ini", "._*",
	".env", ".env.*", "!.env.example", "/bunker.json", ".bunkerignore", ".gitignore", ".gitattributes",
	"npm-debug.log*", "yarn-debug.log*", "yarn-error.log*", "pnpm-debug.log*", "*.swp", "*~",
	".idea/", ".vscode/", ".vercel/", ".netlify/", ".cache/", ".turbo/", "__MACOSX/",
	"/AGENTS.md", "/CLAUDE.md", ".claude/", ".cursor/",
}

// Add parses gitignore-style text.
func (ig *Ignorer) Add(text string) {
	for _, line := range strings.Split(text, "\n") {
		line = strings.TrimRight(line, "\r")
		if strings.TrimSpace(line) == "" || strings.HasPrefix(line, "#") {
			continue
		}
		line = strings.TrimRight(line, " ")
		r := ignoreRule{}
		if strings.HasPrefix(line, "!") {
			r.neg, line = true, line[1:]
		} else if strings.HasPrefix(line, `\`) {
			line = line[1:]
		}
		if strings.HasSuffix(line, "/") {
			r.dirOnly, line = true, strings.TrimRight(line, "/")
		}
		if strings.HasPrefix(line, "/") {
			r.anchored, line = true, line[1:]
		} else if strings.Contains(line, "/") {
			r.anchored = true // a slash in the middle anchors to the root
		}
		if line == "" {
			continue
		}
		if strings.HasPrefix(line, "**/") {
			line = strings.TrimPrefix(line, "**/")
			r.anchored = strings.Contains(line, "/")
			if !r.anchored {
				// "**/foo" == "foo"
			} else {
				line = "**/" + line
			}
		}
		r.segs = strings.Split(line, "/")
		ig.rules = append(ig.rules, r)
	}
}

// Match reports whether rel (slash-separated) is ignored.
func (ig *Ignorer) Match(rel string, isDir bool) bool {
	rel = strings.Trim(rel, "/")
	parts := strings.Split(rel, "/")
	ignored := false
	for _, r := range ig.rules {
		if r.dirOnly && !isDir {
			continue
		}
		var m bool
		if r.anchored {
			m = matchSegs(r.segs, parts)
		} else {
			m = segMatch(r.segs[0], parts[len(parts)-1])
		}
		if m {
			ignored = !r.neg
		}
	}
	return ignored
}

func segMatch(p, s string) bool {
	ok, err := path.Match(p, s)
	return err == nil && ok
}

func matchSegs(pat, parts []string) bool {
	if len(pat) == 0 {
		return len(parts) == 0
	}
	if pat[0] == "**" {
		for i := 0; i <= len(parts); i++ {
			if matchSegs(pat[1:], parts[i:]) {
				return true
			}
		}
		return false
	}
	if len(parts) == 0 || !segMatch(pat[0], parts[0]) {
		return false
	}
	return matchSegs(pat[1:], parts[1:])
}

// NewIgnorer builds the rules for uploading dir.
func NewIgnorer(dir string) *Ignorer {
	ig := &Ignorer{}
	ig.Add(strings.Join(defaultIgnore, "\n"))
	for _, f := range []string{".gitignore", ".bunkerignore"} {
		if b, err := os.ReadFile(filepath.Join(dir, f)); err == nil {
			ig.Add(string(b))
		}
	}
	return ig
}

// localFile is a file to upload.
type localFile struct {
	Rel  string
	Abs  string
	Size int64
	Hash string
}

const maxFiles = 20000

// collectFiles walks dir honoring the ignore rules.
func collectFiles(dir string, ig *Ignorer) ([]*localFile, error) {
	var out []*localFile
	realRoot, rerr := filepath.EvalSymlinks(dir)
	if rerr != nil {
		realRoot = dir
	}
	err := filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if p == dir {
			return nil
		}
		rel, _ := filepath.Rel(dir, p)
		rel = filepath.ToSlash(rel)
		isDir := d.IsDir()
		info, statErr := os.Stat(p) // follow symlinks
		if statErr != nil {
			return nil // dangling symlink
		}
		if d.Type()&fs.ModeSymlink != 0 {
			isDir = info.IsDir()
			if isDir {
				return nil // don't follow directory symlinks (loops)
			}
			// Never publish files from outside the folder (a symlink to
			// ~/.ssh/id_ed25519 in an untrusted project, say).
			if t, err := filepath.EvalSymlinks(p); err != nil || !within(realRoot, t) {
				return nil
			}
		}
		if ig.Match(rel, isDir) {
			if isDir {
				return filepath.SkipDir
			}
			return nil
		}
		if isDir || !info.Mode().IsRegular() {
			return nil
		}
		out = append(out, &localFile{Rel: rel, Abs: p, Size: info.Size()})
		if len(out) > maxFiles {
			return fmt.Errorf("more than %d files in %s; add a .bunkerignore or deploy the build folder", maxFiles, dir)
		}
		return nil
	})
	return out, err
}

func hashFile(p string) (string, error) {
	f, err := os.Open(p)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// hashAll fills in Hash for every file using a bounded worker pool.
func hashAll(files []*localFile, workers int) error {
	var wg sync.WaitGroup
	ch := make(chan *localFile)
	var mu sync.Mutex
	var first error
	for range workers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for f := range ch {
				h, err := hashFile(f.Abs)
				if err != nil {
					mu.Lock()
					if first == nil {
						first = err
					}
					mu.Unlock()
					continue
				}
				f.Hash = h
			}
		}()
	}
	for _, f := range files {
		ch <- f
	}
	close(ch)
	wg.Wait()
	return first
}

var titleRe = regexp.MustCompile(`(?is)<title[^>]*>(.*?)</title>`)

// htmlTitle extracts <title> from an HTML document.
func htmlTitle(b []byte) string {
	if m := titleRe.FindSubmatch(b); m != nil {
		t := strings.Join(strings.Fields(string(m[1])), " ")
		if t != "" && len(t) < 100 {
			return t
		}
	}
	return ""
}

// prettyName turns "team-lunch_app" into "Team Lunch App".
func prettyName(s string) string {
	s = strings.TrimSuffix(s, filepath.Ext(s))
	f := strings.FieldsFunc(s, func(r rune) bool { return r == '-' || r == '_' || r == '.' || r == ' ' })
	for i, w := range f {
		if w != "" {
			f[i] = strings.ToUpper(w[:1]) + w[1:]
		}
	}
	if len(f) == 0 {
		return "My site"
	}
	return strings.Join(f, " ")
}

// ---- build detection ----

type buildPlan struct {
	PM      string   // npm | pnpm | yarn | bun
	Install []string // nil when node_modules exists
	Build   []string
	Out     string // output dir (relative), "" = unknown until built
}

var outputDirs = []string{"dist", "build", "out", ".output/public", "_site"}

func exists(p string) bool { _, err := os.Stat(p); return err == nil }

func detectPM(dir string, pkg map[string]any) string {
	if pmf, _ := pkg["packageManager"].(string); pmf != "" {
		for _, n := range []string{"pnpm", "yarn", "bun", "npm"} {
			if strings.HasPrefix(pmf, n+"@") || pmf == n {
				return n
			}
		}
	}
	switch {
	case exists(filepath.Join(dir, "bun.lockb")) || exists(filepath.Join(dir, "bun.lock")):
		return "bun"
	case exists(filepath.Join(dir, "pnpm-lock.yaml")):
		return "pnpm"
	case exists(filepath.Join(dir, "yarn.lock")):
		return "yarn"
	}
	return "npm"
}

// detectBuild inspects package.json. It returns nil when there is no
// build script.
func detectBuild(dir string, proj *Project) *buildPlan {
	b, err := os.ReadFile(filepath.Join(dir, "package.json"))
	if err != nil && proj.Build == "" {
		return nil
	}
	var pkg map[string]any
	_ = json.Unmarshal(b, &pkg)
	scripts, _ := pkg["scripts"].(map[string]any)
	_, hasBuild := scripts["build"].(string)
	if !hasBuild && proj.Build == "" {
		return nil
	}
	pm := detectPM(dir, pkg)
	plan := &buildPlan{PM: pm}
	if proj.Build != "" {
		plan.Build = []string{"sh", "-c", proj.Build}
		if isWindows {
			plan.Build = []string{"cmd", "/C", proj.Build}
		}
	} else {
		switch pm {
		case "yarn":
			plan.Build = []string{"yarn", "build"}
		default:
			plan.Build = []string{pm, "run", "build"}
		}
	}
	if b != nil && !exists(filepath.Join(dir, "node_modules")) {
		switch pm {
		case "npm":
			if exists(filepath.Join(dir, "package-lock.json")) {
				plan.Install = []string{"npm", "ci"}
			} else {
				plan.Install = []string{"npm", "install"}
			}
		default:
			plan.Install = []string{pm, "install"}
		}
	}
	plan.Out = findOutput(dir, proj)
	return plan
}

func findOutput(dir string, proj *Project) string {
	if proj.Dir != "" {
		return proj.Dir
	}
	for _, d := range outputDirs {
		if exists(filepath.Join(dir, d, "index.html")) {
			return d
		}
	}
	for _, d := range outputDirs {
		if st, err := os.Stat(filepath.Join(dir, d)); err == nil && st.IsDir() {
			return d
		}
	}
	return ""
}

// within reports whether path p is root or inside it.
func within(root, p string) bool {
	rel, err := filepath.Rel(root, p)
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}
