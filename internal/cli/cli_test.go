package cli

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/kucukkanat/bunker/internal/core"
	"github.com/kucukkanat/bunker/internal/selfupdate"
	"github.com/kucukkanat/bunker/internal/server"

	_ "github.com/kucukkanat/bunker/internal/auth"
	_ "github.com/kucukkanat/bunker/internal/mcp"
	_ "github.com/kucukkanat/bunker/internal/sites"
	_ "github.com/kucukkanat/bunker/internal/web"
)

func TestParseArgs(t *testing.T) {
	c := findCommand("deploy")
	a, err := parseArgs([]string{"site", "-m", "hello world", "--draft", "--site=lunch", "--no-build"}, c)
	if err != nil {
		t.Fatal(err)
	}
	if a.Arg(0) != "site" || a.Str("message") != "hello world" || !a.Bool("draft") || a.Str("site") != "lunch" || a.Bool("no-build") != true {
		t.Fatalf("%+v", a)
	}
	if _, err := parseArgs([]string{"--sitee", "x"}, c); err == nil || !strings.Contains(err.Error(), "--site") {
		t.Fatalf("unknown flag should suggest --site: %v", err)
	}
	if _, err := parseArgs([]string{"--message"}, c); err == nil {
		t.Fatal("missing value must fail")
	}
	a, _ = parseArgs([]string{"-", "--", "--not-a-flag"}, c)
	if len(a.Pos) != 2 || a.Pos[0] != "-" || a.Pos[1] != "--not-a-flag" {
		t.Fatalf("positionals: %v", a.Pos)
	}
	// --build negation via --no-build maps to build=false.
	a, _ = parseArgs([]string{"--no-open"}, c)
	if a.Bool("open") || !a.Has("open") {
		t.Fatal("--no-open")
	}
	// Loose commands accept any flag.
	api := findCommand("api")
	a, err = parseArgs([]string{"sites.list", "--filter", "mine", "--n", "-3", "--flag", "--data={}"}, api)
	if err != nil || a.Str("filter") != "mine" || a.Str("n") != "-3" || a.Str("flag") != "true" || a.Str("data") != "{}" {
		t.Fatalf("loose: %+v %v", a, err)
	}
	// Optional-value flag.
	login := findCommand("login")
	a, _ = parseArgs([]string{"https://x", "--token"}, login)
	if !a.Has("token") || a.Str("token") != "" {
		t.Fatalf("--token without value: %+v", a)
	}
	a, _ = parseArgs([]string{"--token", "bk_1", "https://x"}, login)
	if a.Str("token") != "bk_1" || a.Arg(0) != "https://x" {
		t.Fatalf("--token value: %+v", a)
	}
}

func TestParseGlobals(t *testing.T) {
	x := &App{}
	rest, err := x.parseGlobals([]string{"deploy", "--json", "dir", "-y", "--server", "b.co", "--profile=work", "--help"})
	if err != nil {
		t.Fatal(err)
	}
	if !x.JSON || !x.Yes || x.Server != "b.co" || x.Profile != "work" {
		t.Fatalf("%+v", x)
	}
	if strings.Join(rest, " ") != "deploy dir --help" {
		t.Fatalf("rest: %v", rest)
	}
}

func TestNormalizeServer(t *testing.T) {
	for in, want := range map[string]string{
		"bunker.corp.com":          "https://bunker.corp.com",
		"https://bunker.corp.com/": "https://bunker.corp.com",
		"localhost:8080":           "http://localhost:8080",
		"127.0.0.1:9":              "http://127.0.0.1:9",
		"http://x.local/base/":     "http://x.local/base",
	} {
		if got := normalizeServer(in); got != want {
			t.Errorf("%s: got %s want %s", in, got, want)
		}
	}
}

func TestIgnorer(t *testing.T) {
	ig := &Ignorer{}
	ig.Add(strings.Join(defaultIgnore, "\n"))
	ig.Add("# comment\n*.log\n/secret.txt\ndrafts/\ndocs/**/*.tmp\n!keep.log\n")
	cases := map[string]bool{
		"index.html":        false,
		"app.log":           true,
		"sub/app.log":       true,
		"keep.log":          false,
		"secret.txt":        true,
		"sub/secret.txt":    false,
		"docs/a/b/x.tmp":    true,
		"docs/x.tmp":        true,
		".env":              true,
		".env.local":        true,
		".env.example":      false,
		"bunker.json":       true,
		"sub/bunker.json":   false,
		".DS_Store":         true,
		"a/.DS_Store":       true,
		"AGENTS.md":         true,
		".well-known/x.txt": false,
	}
	for p, want := range cases {
		if got := ig.Match(p, false); got != want {
			t.Errorf("%s: got %v want %v", p, got, want)
		}
	}
	for d, want := range map[string]bool{"node_modules": true, "a/node_modules": true, ".git": true, "drafts": true, "src": false} {
		if got := ig.Match(d, true); got != want {
			t.Errorf("dir %s: got %v want %v", d, got, want)
		}
	}
	if ig.Match("drafts", false) {
		t.Error("dir-only rule must not match files")
	}
}

func writeTree(t *testing.T, root string, files map[string]string) {
	t.Helper()
	for p, c := range files {
		fp := filepath.Join(root, filepath.FromSlash(p))
		os.MkdirAll(filepath.Dir(fp), 0o755)
		if err := os.WriteFile(fp, []byte(c), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

func TestCollectAndHash(t *testing.T) {
	dir := t.TempDir()
	writeTree(t, dir, map[string]string{
		"index.html": "<title> My  App </title>", "css/a.css": "a{}", "node_modules/x/y.js": "x",
		".git/HEAD": "ref", "notes.md": "n", ".gitignore": "*.md\n", "bunker.json": "{}", "dist/keep.js": "k",
	})
	files, err := collectFiles(dir, NewIgnorer(dir))
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, f := range files {
		got = append(got, f.Rel)
	}
	sort.Strings(got)
	if strings.Join(got, ",") != "css/a.css,dist/keep.js,index.html" {
		t.Fatalf("files: %v", got)
	}
	if err := hashAll(files, 3); err != nil {
		t.Fatal(err)
	}
	for _, f := range files {
		if sum := sha256.Sum256([]byte("a{}")); f.Rel == "css/a.css" && f.Hash != hex.EncodeToString(sum[:]) {
			t.Fatalf("hash mismatch")
		}
	}
	b, _ := os.ReadFile(filepath.Join(dir, "index.html"))
	if htmlTitle(b) != "My App" {
		t.Fatalf("title: %q", htmlTitle(b))
	}
	if prettyName("team-lunch_app") != "Team Lunch App" {
		t.Fatal(prettyName("team-lunch_app"))
	}
}

func TestDetectBuild(t *testing.T) {
	dir := t.TempDir()
	if detectBuild(dir, &Project{}) != nil {
		t.Fatal("no package.json → no build")
	}
	writeTree(t, dir, map[string]string{"package.json": `{"scripts":{"dev":"vite"}}`})
	if detectBuild(dir, &Project{}) != nil {
		t.Fatal("no build script → no build")
	}
	writeTree(t, dir, map[string]string{"package.json": `{"scripts":{"build":"vite build"}}`, "pnpm-lock.yaml": ""})
	p := detectBuild(dir, &Project{})
	if p == nil || p.PM != "pnpm" || strings.Join(p.Build, " ") != "pnpm run build" || strings.Join(p.Install, " ") != "pnpm install" || p.Out != "" {
		t.Fatalf("pnpm plan: %+v", p)
	}
	writeTree(t, dir, map[string]string{"node_modules/.keep": "", "build/index.html": "x"})
	p = detectBuild(dir, &Project{})
	if p.Install != nil || p.Out != "build" {
		t.Fatalf("installed + built: %+v", p)
	}
	writeTree(t, dir, map[string]string{"package.json": `{"packageManager":"yarn@4.1.0","scripts":{"build":"x"}}`})
	if p := detectBuild(dir, &Project{}); p.PM != "yarn" || strings.Join(p.Build, " ") != "yarn build" {
		t.Fatalf("yarn: %+v", p)
	}
}

func TestUpsertSection(t *testing.T) {
	s := upsertSection("", "one")
	if !strings.Contains(s, agentsStart+"\none\n"+agentsEnd) {
		t.Fatal(s)
	}
	s2 := upsertSection("# Mine\n\nkeep me\n\n"+s, "two")
	if strings.Count(s2, agentsStart) != 1 || !strings.Contains(s2, "two") || strings.Contains(s2, "one\n") || !strings.Contains(s2, "keep me") {
		t.Fatal(s2)
	}
}

func TestFindChecksum(t *testing.T) {
	sums := "aaa  bunker_linux_amd64.tar.gz\nbbb *bunker_darwin_arm64.tar.gz\n"
	if selfupdate.FindChecksum(sums, "bunker_darwin_arm64.tar.gz") != "bbb" || selfupdate.FindChecksum(sums, "x") != "" {
		t.Fatal("checksum lookup")
	}
	h := strings.Repeat("a", 64)
	if selfupdate.FindChecksum(h+"\n", "anything") != h {
		t.Fatal("bare hash file")
	}
}

// run executes the CLI in-process.
func run(t *testing.T, env map[string]string, stdin string, args ...string) (string, string, int) {
	t.Helper()
	var out, errb bytes.Buffer
	x := &App{Stdout: &out, Stderr: &errb, Stdin: strings.NewReader(stdin), Getenv: func(k string) string { return env[k] }}
	code := x.Run(context.Background(), args)
	return out.String(), errb.String(), code
}

func TestDeployInProcess(t *testing.T) {
	t.Setenv("BUNKER_DEV", "1")
	t.Setenv("BUNKER_NO_CLIPBOARD", "1")
	cfg := &core.Config{Dev: true, DataDir: t.TempDir(), Listen: "127.0.0.1:0", TLS: "off"}
	a, err := server.New(cfg, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatal(err)
	}
	ts := httptest.NewServer(server.Handler(a))
	defer ts.Close()
	req, _ := http.NewRequest("POST", ts.URL+"/api/v1/tokens", strings.NewReader(`{"name":"t"}`))
	req.Header.Set("X-Bunker", "1")
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	var tok struct{ Token string }
	json.NewDecoder(resp.Body).Decode(&tok)
	resp.Body.Close()

	env := map[string]string{"BUNKER_CONFIG": filepath.Join(t.TempDir(), "c.json")}
	if _, e, code := run(t, env, tok.Token+"\n", "login", ts.URL, "--token", "-"); code != 0 {
		t.Fatalf("login: %s", e)
	}
	dir := t.TempDir()
	writeTree(t, dir, map[string]string{"index.html": "<title>Unit Site</title><p>hi", "a.js": "1"})
	out, e, code := run(t, env, "", "deploy", dir, "--json")
	if code != 0 {
		t.Fatalf("deploy: %s", e)
	}
	var r deployResult
	json.Unmarshal([]byte(out), &r)
	if r.Site != "unit-site" || r.Uploaded != 2 || !r.Created || r.Version != 1 {
		t.Fatalf("first deploy: %s", out)
	}
	out, _, _ = run(t, env, "", "deploy", dir, "--json", "--draft")
	json.Unmarshal([]byte(out), &r)
	if r.Uploaded != 0 || !r.Draft || !strings.HasSuffix(r.URL, "?draft=1") {
		t.Fatalf("draft redeploy: %s", out)
	}
	// Usage errors exit 2 with a hint; API errors exit 1 with JSON in --json mode.
	if _, e, code := run(t, env, "", "versions", "--bogus"); code != 2 || !strings.Contains(e, "Usage") {
		t.Fatalf("usage error: %d %s", code, e)
	}
	if _, e, code := run(t, env, "", "versions", "missing-site", "--json"); code != 1 || !strings.Contains(e, `"not_found"`) {
		t.Fatalf("json error: %d %s", code, e)
	}
	// Env overrides (CI).
	ci := map[string]string{"BUNKER_CONFIG": filepath.Join(t.TempDir(), "none.json"), "BUNKER_SERVER": ts.URL, "BUNKER_TOKEN": tok.Token}
	if out, e, code := run(t, ci, "", "whoami", "--json"); code != 0 || !strings.Contains(out, "you@localhost") {
		t.Fatalf("env auth: %s %s", out, e)
	}
	if _, e, code := run(t, map[string]string{"BUNKER_CONFIG": filepath.Join(t.TempDir(), "none.json")}, "", "whoami"); code != 1 || !strings.Contains(e, "bunker login") {
		t.Fatalf("not signed in: %s", e)
	}
}
