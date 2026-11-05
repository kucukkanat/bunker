package functions

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"path"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/evanw/esbuild/pkg/api"

	"github.com/kucukkanat/bunker/internal/sites"
)

// Dir is the folder server functions live in.
const Dir = "api/"

var codeExts = map[string]bool{".js": true, ".mjs": true, ".ts": true, ".mts": true}

var defaultExportRe = regexp.MustCompile(`\bexport\s+default\b|\bexport\s*\{[^}]*\bas\s+default\b`)

// Config is a function's `export const config`.
type Config struct {
	Elevated  bool       `json:"elevated"`
	Schedule  string     `json:"schedule"`
	OnWrite   stringList `json:"onWrite"`
	TimeoutMs int        `json:"timeoutMs"`
}

// stringList accepts "orders" or ["orders","items"].
type stringList []string

func (s *stringList) UnmarshalJSON(b []byte) error {
	var one string
	if json.Unmarshal(b, &one) == nil {
		if one != "" {
			*s = []string{one}
		}
		return nil
	}
	var many []string
	if err := json.Unmarshal(b, &many); err != nil {
		return fmt.Errorf("onWrite must be a collection name or a list of them")
	}
	*s = many
	return nil
}

// Def is one function in a version.
type Def struct {
	Name   string `json:"name"`
	Path   string `json:"path"`
	Config Config `json:"-"`
	Code   []byte `json:"-"`
	Err    string `json:"error,omitempty"`
}

// Catalog is the set of functions in one manifest.
type Catalog struct {
	Fns map[string]*Def
	// hidden are api/ code files never served to browsers.
	hidden map[string]bool
}

// Names returns the sorted function names.
func (c *Catalog) Names() []string {
	out := make([]string, 0, len(c.Fns))
	for n := range c.Fns {
		out = append(out, n)
	}
	sort.Strings(out)
	return out
}

// isCode reports whether p is a server code file under api/.
func isCode(p string) bool {
	return strings.HasPrefix(p, Dir) && codeExts[strings.ToLower(path.Ext(p))] && !strings.HasSuffix(p, ".d.ts")
}

// isModule reports whether p is a shared (non-callable) module: any path
// segment under api/ starts with "_".
func isModule(p string) bool {
	for _, seg := range strings.Split(strings.TrimPrefix(p, Dir), "/") {
		if strings.HasPrefix(seg, "_") {
			return true
		}
	}
	return false
}

// nameOf maps api/orders/refund.ts → "orders/refund".
func nameOf(p string) string {
	n := strings.TrimPrefix(p, Dir)
	return strings.TrimSuffix(n, path.Ext(n))
}

var nameRe = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.-]{0,63}(/[A-Za-z0-9][A-Za-z0-9_.-]{0,63}){0,4}$`)

// ---- catalog cache (keyed by the api/ files' content hashes) ----

type centry struct {
	once sync.Once
	cat  *Catalog
	used time.Time
}

var (
	catMu    sync.Mutex
	catCache = map[string]*centry{}
)

const catCacheMax = 256

func catalogKey(m sites.Manifest) string {
	var ps []string
	for p, f := range m {
		if isCode(p) {
			ps = append(ps, p+"\x00"+f.Hash)
		}
	}
	if len(ps) == 0 {
		return ""
	}
	sort.Strings(ps)
	h := sha256.New()
	for _, p := range ps {
		h.Write([]byte(p))
		h.Write([]byte{0})
	}
	return hex.EncodeToString(h.Sum(nil))
}

var emptyCatalog = &Catalog{Fns: map[string]*Def{}, hidden: map[string]bool{}}

// CatalogFor returns the functions in a manifest, bundling and inspecting
// them once per distinct set of api/ files.
func CatalogFor(ctx context.Context, m sites.Manifest) *Catalog {
	key := catalogKey(m)
	if key == "" {
		return emptyCatalog
	}
	catMu.Lock()
	e, ok := catCache[key]
	if !ok {
		if len(catCache) >= catCacheMax {
			var oldK string
			var oldT time.Time
			for k, v := range catCache {
				if oldK == "" || v.used.Before(oldT) {
					oldK, oldT = k, v.used
				}
			}
			delete(catCache, oldK)
		}
		e = &centry{}
		catCache[key] = e
	}
	e.used = time.Now()
	catMu.Unlock()
	e.once.Do(func() { e.cat = build(context.WithoutCancel(ctx), m) })
	return e.cat
}

func build(ctx context.Context, m sites.Manifest) *Catalog {
	files := map[string][]byte{}
	for p, f := range m {
		if !isCode(p) {
			continue
		}
		b, err := app.Blobs.Read(f.Hash)
		if err != nil {
			continue
		}
		files[p] = b
	}
	cat := &Catalog{Fns: map[string]*Def{}, hidden: map[string]bool{}}
	for p, b := range files {
		if isModule(p) || !defaultExportRe.Match(b) {
			continue
		}
		cat.hidden[p] = true
		d := &Def{Name: nameOf(p), Path: p}
		cat.Fns[d.Name] = d
		if !nameRe.MatchString(d.Name) {
			d.Err = "function file names use letters, numbers, dots, dashes and underscores (up to 5 folders deep)"
			continue
		}
		code, err := bundle(files, p)
		if err != nil {
			d.Err = err.Error()
			continue
		}
		d.Code = code
		raw, err := engine().Inspect(ctx, string(code))
		if err != nil {
			d.Err = "the function failed to load: " + errText(err)
			continue
		}
		if len(raw) > 0 && string(raw) != "null" {
			if err := json.Unmarshal(raw, &d.Config); err != nil {
				d.Err = "config: " + err.Error()
				continue
			}
		}
		if d.Config.Schedule != "" {
			if _, err := parseCron(d.Config.Schedule); err != nil {
				d.Err = "config.schedule: " + err.Error()
			}
		}
		for _, c := range d.Config.OnWrite {
			if !collRe.MatchString(c) {
				d.Err = "config.onWrite: " + c + " isn't a valid collection name"
			}
		}
	}
	if len(cat.Fns) > 0 {
		// Shared modules of an app with functions are server code too.
		for p := range files {
			if isModule(p) {
				cat.hidden[p] = true
			}
		}
	}
	return cat
}

var collRe = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.-]{0,63}$`)

// bundle turns one function file and its relative imports into a single
// ES module.
func bundle(files map[string][]byte, entry string) ([]byte, error) {
	plugin := api.Plugin{Name: "bunker-fn", Setup: func(b api.PluginBuild) {
		b.OnResolve(api.OnResolveOptions{Filter: ".*"}, func(a api.OnResolveArgs) (api.OnResolveResult, error) {
			if a.Kind == api.ResolveEntryPoint {
				return api.OnResolveResult{Path: "/" + entry, Namespace: "mem"}, nil
			}
			spec := a.Path
			if strings.HasPrefix(spec, "qjs:") || spec == "std" || spec == "os" {
				return api.OnResolveResult{}, fmt.Errorf("%s isn't available in server functions", spec)
			}
			if !strings.HasPrefix(spec, ".") && !strings.HasPrefix(spec, "/") {
				return api.OnResolveResult{}, fmt.Errorf("packages can't be installed in server functions (%q). Import files from your api/ folder instead; bunker and fetch are already there", spec)
			}
			p := spec
			if !strings.HasPrefix(spec, "/") {
				p = path.Join(path.Dir(a.Importer), spec)
			}
			p = strings.TrimPrefix(path.Clean(p), "/")
			for _, c := range []string{p, p + ".js", p + ".ts", p + ".mjs", p + "/index.js", p + "/index.ts"} {
				if _, ok := files[c]; ok {
					return api.OnResolveResult{Path: "/" + c, Namespace: "mem"}, nil
				}
			}
			return api.OnResolveResult{}, fmt.Errorf("can't find %s (imported from %s); server functions can import files in api/", spec, strings.TrimPrefix(a.Importer, "/"))
		})
		b.OnLoad(api.OnLoadOptions{Filter: ".*", Namespace: "mem"}, func(a api.OnLoadArgs) (api.OnLoadResult, error) {
			p := strings.TrimPrefix(a.Path, "/")
			src := string(files[p])
			loader := api.LoaderJS
			if strings.HasSuffix(p, ".ts") || strings.HasSuffix(p, ".mts") {
				loader = api.LoaderTS
			}
			return api.OnLoadResult{Contents: &src, Loader: loader, ResolveDir: "/" + path.Dir(p)}, nil
		})
	}}
	res := api.Build(api.BuildOptions{
		EntryPoints: []string{entry},
		Bundle:      true,
		Write:       false,
		Format:      api.FormatESModule,
		Platform:    api.PlatformNeutral,
		Target:      api.ES2023,
		Plugins:     []api.Plugin{plugin},
		LogLevel:    api.LogLevelSilent,
		Outfile:     "main.js",
	})
	if len(res.Errors) > 0 {
		e := res.Errors[0]
		msg := e.Text
		if e.Location != nil {
			msg = fmt.Sprintf("%s:%d: %s", strings.TrimPrefix(e.Location.File, "mem:/"), e.Location.Line, e.Text)
		}
		return nil, fmt.Errorf("%s", msg)
	}
	if len(res.OutputFiles) == 0 {
		return nil, fmt.Errorf("nothing to run")
	}
	return res.OutputFiles[0].Contents, nil
}

// hidden is registered with sites so function code is never served.
func hidden(m sites.Manifest, name string) bool {
	if !isCode(name) {
		return false
	}
	return CatalogFor(context.Background(), m).hidden[name]
}
