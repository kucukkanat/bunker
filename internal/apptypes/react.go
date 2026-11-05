package apptypes

import (
	"fmt"
	"path"
	"regexp"
	"sort"
	"strings"

	"github.com/evanw/esbuild/pkg/api"

	"github.com/kucukkanat/bunker/internal/apptypes/vendor"
	"github.com/kucukkanat/bunker/internal/core"
	"github.com/kucukkanat/bunker/internal/vendorlib"
)

// globalLib maps a bare import ("chart.js/auto", "lodash/debounce",
// "bootstrap/dist/css/bootstrap.min.css") to a built-in UMD library.
// React itself is served by preact/compat, so it's excluded here.
func globalLib(spec string) *vendorlib.Lib {
	parts := strings.Split(spec, "/")
	name := parts[0]
	if strings.HasPrefix(name, "@") && len(parts) > 1 {
		name += "/" + parts[1]
	}
	if name == "react" || name == "react-dom" {
		return nil
	}
	for _, cand := range []string{spec, name} {
		if l := findLib(cand); l != nil && l.Global != "" && l.JS != "" {
			return l
		}
	}
	return nil
}

// React/JSX/TSX/TS single files and small projects without a build step
// are bundled at deploy time with esbuild (pure Go, in-process). Bare
// imports resolve against the vendored set (React runs on preact/compat).

var entryCandidates = []string{
	"index.tsx", "index.jsx", "main.tsx", "main.jsx", "App.tsx", "App.jsx", "app.tsx", "app.jsx",
	"src/main.tsx", "src/main.jsx", "src/index.tsx", "src/index.jsx", "src/App.tsx", "src/App.jsx",
	"index.ts", "main.ts", "app.ts", "src/main.ts", "src/index.ts",
	"index.js", "main.js", "app.js", "src/main.js", "src/index.js",
}

// codeEntry picks the file to bundle, or "" if the upload isn't a script app.
func codeEntry(files map[string][]byte, code []string) string {
	for _, c := range entryCandidates {
		if _, ok := files[c]; ok {
			return c
		}
	}
	var real, jsx []string
	for _, p := range code {
		b := path.Base(p)
		if strings.Contains(b, ".config.") || strings.HasSuffix(b, ".d.ts") || strings.Contains(b, ".test.") || strings.Contains(b, ".spec.") || strings.HasPrefix(p, AssetDir) {
			continue
		}
		real = append(real, p)
		if e := strings.ToLower(path.Ext(p)); e == ".jsx" || e == ".tsx" {
			jsx = append(jsx, p)
		}
	}
	if len(real) == 1 {
		return real[0]
	}
	if len(jsx) == 1 {
		return jsx[0]
	}
	return ""
}

var reactish = regexp.MustCompile(`(?m)(from\s+['"](react|preact|preact/compat|htm/preact)['"]|require\(['"]react['"]\))`)
var renderCall = regexp.MustCompile(`\b(createRoot|hydrateRoot|ReactDOM\.render|render)\s*\(`)

func renderCode(files map[string][]byte, entry string) (string, error) {
	js, css, libs, err := bundle(files, entry)
	if err != nil {
		return "", err
	}
	files[AssetDir+"bundle.js"] = js
	head := `<link rel="stylesheet" href="/_/kit.css">` + libs
	if len(css) > 0 {
		files[AssetDir+"bundle.css"] = css
		head += `<link rel="stylesheet" href="` + AssetDir + `bundle.css">`
	}
	title := niceName(entry)
	if t := strings.ToLower(title); t == "index" || t == "main" || t == "app" || strings.HasPrefix(t, "src") {
		title = "App"
	}
	files["index.html"] = []byte(`<!doctype html><html lang="en"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1"><title>` + esc(title) + `</title>` + head +
		`<style>body{margin:0;font-family:Inter,system-ui,-apple-system,"Segoe UI",sans-serif}</style></head><body><div id="root"></div><script type="module" src="` + AssetDir + `bundle.js"></script></body></html>`)
	return "react", nil
}

var moduleScriptRe = regexp.MustCompile(`(?i)<script\b[^>]*\btype\s*=\s*["']module["'][^>]*\bsrc\s*=\s*["']\.?/?([^"']+\.(?:tsx|jsx|ts|mts))["'][^>]*>\s*</script>`)
var moduleScriptRe2 = regexp.MustCompile(`(?i)<script\b[^>]*\bsrc\s*=\s*["']\.?/?([^"']+\.(?:tsx|jsx|ts|mts))["'][^>]*>\s*</script>`)

// bundleIndexScripts handles an unbuilt Vite-style project: index.html
// pointing at /src/main.tsx gets that entry bundled and the tag rewritten.
func bundleIndexScripts(files map[string][]byte) (string, error) {
	idx := files["index.html"]
	m := moduleScriptRe.FindSubmatchIndex(idx)
	if m == nil {
		m = moduleScriptRe2.FindSubmatchIndex(idx)
	}
	if m == nil {
		return "", nil
	}
	entry := string(idx[m[2]:m[3]])
	if _, ok := files[entry]; !ok {
		return "", nil
	}
	js, css, libs, err := bundleOpts(files, entry, false)
	if err != nil {
		return "", err
	}
	files[AssetDir+"bundle.js"] = js
	tag := libs + `<script type="module" src="` + AssetDir + `bundle.js"></script>`
	if len(css) > 0 {
		files[AssetDir+"bundle.css"] = css
		tag = `<link rel="stylesheet" href="` + AssetDir + `bundle.css">` + tag
	}
	out := append([]byte{}, idx[:m[0]]...)
	out = append(out, tag...)
	out = append(out, idx[m[1]:]...)
	files["index.html"] = out
	return "react", nil
}

func bundle(files map[string][]byte, entry string) ([]byte, []byte, string, error) {
	src := string(files[entry])
	ext := strings.ToLower(path.Ext(entry))
	auto := (ext == ".jsx" || ext == ".tsx" || reactish.MatchString(src)) && !renderCall.MatchString(src)
	return bundleOpts(files, entry, auto)
}

const entryNS = "bunker-entry"

// bundleOpts returns the JS and CSS bundles plus <script>/<link> tags for
// built-in UMD libraries (/_/lib) the code imports.
func bundleOpts(files map[string][]byte, entry string, autoMount bool) ([]byte, []byte, string, error) {
	entryPath := "/" + entry
	var libTags []string
	seenLib := map[string]bool{}
	useLib := func(l *vendorlib.Lib, css bool) {
		key := l.Name + map[bool]string{true: ".css", false: ".js"}[css]
		if seenLib[key] {
			return
		}
		seenLib[key] = true
		if css {
			libTags = append(libTags, `<link rel="stylesheet" href="/_/lib/`+l.Name+`/`+l.CSS+`">`)
		} else {
			libTags = append(libTags, `<script src="/_/lib/`+l.Name+`/`+l.JS+`"></script>`)
		}
	}
	wrapper := ""
	if autoMount {
		// Mount the default export (a component) into #root.
		wrapper = `import * as M from ` + jsString(entryPath) + `;
import { createElement, render } from "preact/compat";
const C = M.default || M.App;
const root = document.getElementById("root");
if (typeof C === "function" && root && !root.firstChild) render(createElement(C), root);
`
	}
	plugin := api.Plugin{Name: "bunker", Setup: func(b api.PluginBuild) {
		b.OnResolve(api.OnResolveOptions{Filter: ".*"}, func(a api.OnResolveArgs) (api.OnResolveResult, error) {
			if a.Kind == api.ResolveEntryPoint {
				if wrapper != "" {
					return api.OnResolveResult{Path: "/__bunker_entry.js", Namespace: entryNS}, nil
				}
				return api.OnResolveResult{Path: entryPath, Namespace: "mem"}, nil
			}
			spec := a.Path
			if strings.HasPrefix(spec, ".") || strings.HasPrefix(spec, "/") {
				if a.Namespace == "vendor" {
					return api.OnResolveResult{}, fmt.Errorf("unexpected relative import %s in %s", spec, a.Importer)
				}
				p := spec
				if !strings.HasPrefix(spec, "/") {
					p = path.Join(path.Dir(a.Importer), spec)
				}
				p = path.Clean(p)
				if r, ok := resolveMem(files, p); ok {
					return api.OnResolveResult{Path: r, Namespace: "mem"}, nil
				}
				return api.OnResolveResult{}, fmt.Errorf("can't find %s (imported from %s)", spec, strings.TrimPrefix(a.Importer, "/"))
			}
			if name, ok := vendor.Resolve(spec); ok {
				return api.OnResolveResult{Path: name, Namespace: "vendor"}, nil
			}
			if l := globalLib(spec); l != nil {
				if strings.HasSuffix(spec, ".css") {
					if l.CSS != "" {
						useLib(l, true)
					}
					return api.OnResolveResult{Path: spec, Namespace: "empty"}, nil
				}
				useLib(l, false)
				return api.OnResolveResult{Path: l.Name, Namespace: "global"}, nil
			}
			if strings.HasSuffix(spec, ".css") {
				// e.g. "bootstrap/dist/css/bootstrap.css": not available offline; skip it.
				return api.OnResolveResult{Path: spec, Namespace: "empty"}, nil
			}
			return api.OnResolveResult{}, fmt.Errorf("the package %q isn't available in Bunker (pages can't download code). Built in: react, react-dom, preact, htm, chart.js, d3, dayjs, lodash, marked, papaparse and more", spec)
		})
		b.OnLoad(api.OnLoadOptions{Filter: ".*", Namespace: entryNS}, func(a api.OnLoadArgs) (api.OnLoadResult, error) {
			return api.OnLoadResult{Contents: &wrapper, Loader: api.LoaderJS}, nil
		})
		b.OnLoad(api.OnLoadOptions{Filter: ".*", Namespace: "empty"}, func(a api.OnLoadArgs) (api.OnLoadResult, error) {
			s := ""
			return api.OnLoadResult{Contents: &s, Loader: api.LoaderCSS}, nil
		})
		b.OnLoad(api.OnLoadOptions{Filter: ".*", Namespace: "global"}, func(a api.OnLoadArgs) (api.OnLoadResult, error) {
			l := findLib(a.Path)
			if l == nil {
				return api.OnLoadResult{}, fmt.Errorf("library %s missing", a.Path)
			}
			// The UMD build sets a global; re-export it (and itself as a
			// named export, e.g. import { Chart } from "chart.js").
			src := fmt.Sprintf(`var g = globalThis[%q]; if (g && !(%q in g)) { try { g[%q] = g } catch (e) {} } module.exports = g;`, l.Global, l.Global, l.Global)
			return api.OnLoadResult{Contents: &src, Loader: api.LoaderJS}, nil
		})
		b.OnLoad(api.OnLoadOptions{Filter: ".*", Namespace: "vendor"}, func(a api.OnLoadArgs) (api.OnLoadResult, error) {
			src, ok := vendor.Source(a.Path)
			if !ok {
				return api.OnLoadResult{}, fmt.Errorf("vendored module %s missing", a.Path)
			}
			s := string(src)
			return api.OnLoadResult{Contents: &s, Loader: api.LoaderJS}, nil
		})
		b.OnLoad(api.OnLoadOptions{Filter: ".*", Namespace: "mem"}, func(a api.OnLoadArgs) (api.OnLoadResult, error) {
			data, ok := files[strings.TrimPrefix(a.Path, "/")]
			if !ok {
				return api.OnLoadResult{}, fmt.Errorf("can't find %s", a.Path)
			}
			s := string(data)
			return api.OnLoadResult{Contents: &s, Loader: loaderFor(a.Path)}, nil
		})
	}}
	res := api.Build(api.BuildOptions{
		EntryPoints:       []string{entry},
		Bundle:            true,
		Write:             false,
		Outdir:            "/out",
		EntryNames:        "bundle",
		Format:            api.FormatESModule,
		Target:            api.ES2020,
		Platform:          api.PlatformBrowser,
		JSX:               api.JSXAutomatic,
		JSXImportSource:   "react",
		MinifyWhitespace:  true,
		MinifySyntax:      true,
		MinifyIdentifiers: true,
		Define:            map[string]string{"process.env.NODE_ENV": `"production"`},
		Plugins:           []api.Plugin{plugin},
		LogLevel:          api.LogLevelSilent,
		Charset:           api.CharsetUTF8,
	})
	if len(res.Errors) > 0 {
		var msgs []string
		for i, e := range res.Errors {
			if i == 3 {
				break
			}
			m := e.Text
			if e.Location != nil && e.Location.File != "" {
				m = fmt.Sprintf("%s:%d: %s", strings.TrimPrefix(strings.TrimPrefix(e.Location.File, "mem:"), "/"), e.Location.Line, m)
			}
			msgs = append(msgs, m)
		}
		return nil, nil, "", core.NewErr(422, "build_failed", "Bunker couldn't build "+entry+": "+strings.Join(msgs, "; "))
	}
	var js, css []byte
	for _, f := range res.OutputFiles {
		switch path.Ext(f.Path) {
		case ".js":
			js = f.Contents
		case ".css":
			css = f.Contents
		}
	}
	if js == nil {
		return nil, nil, "", core.NewErr(422, "build_failed", "Bunker couldn't build "+entry)
	}
	return js, css, strings.Join(libTags, ""), nil
}

func jsString(s string) string { return fmt.Sprintf("%q", s) }

var resolveExts = []string{"", ".tsx", ".ts", ".jsx", ".js", ".mjs", ".json", "/index.tsx", "/index.ts", "/index.jsx", "/index.js"}

func resolveMem(files map[string][]byte, p string) (string, bool) {
	rel := strings.TrimPrefix(p, "/")
	for _, e := range resolveExts {
		if _, ok := files[rel+e]; ok {
			return "/" + rel + e, true
		}
	}
	return "", false
}

func loaderFor(p string) api.Loader {
	switch strings.ToLower(path.Ext(p)) {
	case ".tsx":
		return api.LoaderTSX
	case ".ts", ".mts":
		return api.LoaderTS
	case ".jsx":
		return api.LoaderJSX
	case ".js", ".mjs":
		return api.LoaderJSX // JSX in .js files is common in artifacts
	case ".json":
		return api.LoaderJSON
	case ".css":
		return api.LoaderCSS
	case ".txt", ".md":
		return api.LoaderText
	}
	return api.LoaderDataURL // images, fonts
}

// findLib looks a built-in library up by name or CDN alias.
func findLib(name string) *vendorlib.Lib {
	for i := range vendorlib.Libs {
		l := &vendorlib.Libs[i]
		if strings.EqualFold(l.Name, name) {
			return l
		}
		for _, a := range l.Aliases {
			if strings.EqualFold(a, name) {
				return l
			}
		}
	}
	return nil
}

// SupportedImports lists the bare imports React-style pages may use.
func SupportedImports() []string {
	s := vendor.Supported()
	sort.Strings(s)
	return s
}
