// Package vendorlib bundles popular front-end libraries into the binary and
// rewrites CDN references (jsDelivr, unpkg, cdnjs, Tailwind's play CDN,
// code.jquery.com) to these local copies at publish time. Pages can't reach
// CDNs from the sandbox, so pasted or AI-generated HTML "just works".
package vendorlib

import (
	"bytes"
	"context"
	"crypto/sha256"
	"embed"
	"encoding/hex"
	"net/http"
	"path"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/kucukkanat/bunker/internal/core"
	"github.com/kucukkanat/bunker/internal/sites"
)

//go:embed files
var files embed.FS

// Lib describes one vendored library.
type Lib struct {
	Name    string   `json:"name"`
	Version string   `json:"version"`
	JS      string   `json:"js,omitempty"`
	CSS     string   `json:"css,omitempty"`
	Global  string   `json:"global,omitempty"`
	Aliases []string `json:"-"` // CDN package names (npm + cdnjs)
}

// Libs is the catalogue (also shown to the AI builder and in docs).
var Libs = []Lib{
	{Name: "chart.js", Version: "4.6.1", JS: "chart.umd.min.js", Global: "Chart", Aliases: []string{"chart.js", "Chart.js", "chart.js/auto"}},
	{Name: "chartjs-adapter-date-fns", Version: "3.0.0", JS: "chartjs-adapter-date-fns.bundle.min.js", Aliases: []string{"chartjs-adapter-date-fns"}},
	{Name: "alpinejs", Version: "3.17.5", JS: "cdn.min.js", Global: "Alpine", Aliases: []string{"alpinejs", "alpinejs/dist"}},
	{Name: "react", Version: "18.3.1", JS: "react.production.min.js", Global: "React", Aliases: []string{"react"}},
	{Name: "react-dom", Version: "18.3.1", JS: "react-dom.production.min.js", Global: "ReactDOM", Aliases: []string{"react-dom"}},
	{Name: "vue", Version: "3.5.44", JS: "vue.global.prod.js", Global: "Vue", Aliases: []string{"vue"}},
	{Name: "d3", Version: "7.9.2", JS: "d3.min.js", Global: "d3", Aliases: []string{"d3"}},
	{Name: "dayjs", Version: "1.11.23", JS: "dayjs.min.js", Global: "dayjs", Aliases: []string{"dayjs"}},
	{Name: "marked", Version: "14.1.4", JS: "marked.min.js", Global: "marked", Aliases: []string{"marked"}},
	{Name: "tailwindcss", Version: "4.3.4", JS: "browser.js", Aliases: []string{"@tailwindcss/browser", "tailwindcss"}},
	{Name: "bootstrap", Version: "5.3.8", JS: "bootstrap.bundle.min.js", CSS: "bootstrap.min.css", Global: "bootstrap", Aliases: []string{"bootstrap", "twitter-bootstrap"}},
	{Name: "htmx.org", Version: "2.0.11", JS: "htmx.min.js", Global: "htmx", Aliases: []string{"htmx.org", "htmx"}},
	{Name: "lodash", Version: "4.18.1", JS: "lodash.min.js", Global: "_", Aliases: []string{"lodash", "lodash.js"}},
	{Name: "papaparse", Version: "5.7.0", JS: "papaparse.min.js", Global: "Papa", Aliases: []string{"papaparse", "PapaParse"}},
	{Name: "sortablejs", Version: "1.15.7", JS: "Sortable.min.js", Global: "Sortable", Aliases: []string{"sortablejs", "Sortable"}},
	{Name: "canvas-confetti", Version: "1.9.4", JS: "confetti.browser.js", Global: "confetti", Aliases: []string{"canvas-confetti"}},
	{Name: "jquery", Version: "3.7.1", JS: "jquery.min.js", Global: "jQuery", Aliases: []string{"jquery"}},
	// Map tiles need the internet, so pages use Leaflet with L.CRS.Simple +
	// an image overlay (floor plans, site maps) or GeoJSON shapes.
	{Name: "leaflet", Version: "1.9.4", JS: "leaflet.js", CSS: "leaflet.css", Global: "L", Aliases: []string{"leaflet"}},
}

var byAlias = map[string]*Lib{}

func init() {
	for i := range Libs {
		for _, a := range Libs[i].Aliases {
			byAlias[strings.ToLower(a)] = &Libs[i]
		}
	}
	core.Register(core.Module{Name: "vendorlib", Order: 30, Init: initLib})
	sites.RegisterTransformer(sites.Transformer{Name: "cdn-rewrite", Priority: 90, Apply: rewriteAll})
}

type asset struct {
	b, br []byte
	etag  string
	ct    string
}

var (
	cacheMu sync.Mutex
	cache   = map[string]*asset{}
)

func get(name, file string) *asset {
	key := name + "/" + file
	cacheMu.Lock()
	defer cacheMu.Unlock()
	if a, ok := cache[key]; ok {
		return a
	}
	b, err := files.ReadFile("files/" + key)
	if err != nil {
		cache[key] = nil
		return nil
	}
	sum := sha256.Sum256(b)
	ct := "text/javascript; charset=utf-8"
	var br []byte
	switch path.Ext(file) {
	case ".css":
		ct = "text/css; charset=utf-8"
	case ".png":
		ct = "image/png"
	case ".svg":
		ct = "image/svg+xml"
	case ".js", ".mjs":
	default:
		ct = "text/plain; charset=utf-8"
	}
	if ct != "image/png" {
		br = core.CompressBrotli(b)
	}
	a := &asset{b: b, br: br, etag: `"` + hex.EncodeToString(sum[:8]) + `"`, ct: ct}
	cache[key] = a
	return a
}

func initLib(a *core.App) error {
	// {file...}: libraries with assets in subfolders (leaflet/images/*.png).
	a.Mux.HandleFunc("GET /_/lib/{name}/{file...}", func(w http.ResponseWriter, r *http.Request) {
		as := get(r.PathValue("name"), r.PathValue("file"))
		if as == nil {
			http.NotFound(w, r)
			return
		}
		h := w.Header()
		h.Set("Content-Type", as.ct)
		h.Set("ETag", as.etag)
		h.Set("Cache-Control", "public, max-age=31536000, immutable")
		h.Set("Cross-Origin-Resource-Policy", "cross-origin")
		h.Set("Access-Control-Allow-Origin", "*")
		h.Set("Vary", "Accept-Encoding")
		if r.Header.Get("If-None-Match") == as.etag {
			w.WriteHeader(http.StatusNotModified)
			return
		}
		body := as.b
		if as.br != nil && strings.Contains(r.Header.Get("Accept-Encoding"), "br") {
			body = as.br
			h.Set("Content-Encoding", "br")
		}
		http.ServeContent(w, r, "", time.Time{}, bytes.NewReader(body))
	})
	a.Handle(core.Op{Name: "libraries.list", Method: "GET", Path: "/api/v1/libraries", Summary: "Front-end libraries built into Bunker (usable without a CDN)", Access: core.AccessPublic,
		Fn: func(r *http.Request) (any, error) {
			out := []map[string]any{}
			for _, l := range Libs {
				m := map[string]any{"name": l.Name, "version": l.Version, "global": l.Global}
				if l.JS != "" {
					m["js"] = "/_/lib/" + l.Name + "/" + l.JS
				}
				if l.CSS != "" {
					m["css"] = "/_/lib/" + l.Name + "/" + l.CSS
				}
				out = append(out, m)
			}
			return map[string]any{"libraries": out}, nil
		}})
	return nil
}

var (
	tagRe       = regexp.MustCompile(`(?is)<(script|link)\b[^>]*>`)
	urlAttrRe   = regexp.MustCompile(`(?i)\b(src|href)\s*=\s*(["'])((?:https?:)?//[^"']+)(["'])`)
	sriRe       = regexp.MustCompile(`(?i)\s(integrity|crossorigin)\s*=\s*(["'])[^"']*(["'])|\scrossorigin\b`)
	fontsLinkRe = regexp.MustCompile(`(?is)<link\b[^>]*(fonts\.googleapis\.com|fonts\.gstatic\.com|use\.typekit\.net|fonts\.bunny\.net)[^>]*>`)
	cssImportRe = regexp.MustCompile(`(?i)@import\s+url\(\s*["']?https?://fonts\.googleapis\.com[^)]*\)\s*;?`)
)

// Resolve maps a CDN URL to a vendored file path ("" if unknown).
func Resolve(u string) string {
	u = strings.TrimPrefix(strings.TrimPrefix(u, "https:"), "http:")
	u = strings.TrimPrefix(u, "//")
	if i := strings.IndexAny(u, "?#"); i >= 0 {
		u = u[:i]
	}
	host, p, _ := strings.Cut(u, "/")
	isCSS := strings.HasSuffix(strings.ToLower(p), ".css")
	var pkg string
	switch strings.ToLower(host) {
	case "cdn.tailwindcss.com":
		return "/_/lib/tailwindcss/browser.js"
	case "code.jquery.com":
		if strings.HasPrefix(p, "jquery") {
			return "/_/lib/jquery/jquery.min.js"
		}
		return ""
	case "cdn.jsdelivr.net":
		p = strings.TrimPrefix(p, "npm/")
		pkg = npmPkg(p)
	case "unpkg.com", "esm.sh", "cdn.skypack.dev":
		pkg = npmPkg(p)
	case "cdnjs.cloudflare.com":
		parts := strings.Split(strings.TrimPrefix(p, "ajax/libs/"), "/")
		if len(parts) > 0 {
			pkg = parts[0]
		}
	case "stackpath.bootstrapcdn.com", "maxcdn.bootstrapcdn.com":
		pkg = "bootstrap"
	default:
		return ""
	}
	// react-dom is often loaded as react-dom@18/umd/...; prefer the most specific alias.
	if strings.Contains(p, "react-dom") {
		pkg = "react-dom"
	}
	l := byAlias[strings.ToLower(pkg)]
	if l == nil {
		return ""
	}
	if isCSS {
		if l.CSS == "" {
			return ""
		}
		return "/_/lib/" + l.Name + "/" + l.CSS
	}
	if l.JS == "" {
		return ""
	}
	return "/_/lib/" + l.Name + "/" + l.JS
}

func npmPkg(p string) string {
	parts := strings.Split(p, "/")
	if len(parts) == 0 {
		return ""
	}
	name := parts[0]
	if strings.HasPrefix(name, "@") && len(parts) > 1 {
		name = name + "/" + parts[1]
	}
	if i := strings.LastIndex(name, "@"); i > 0 {
		name = name[:i]
	}
	return name
}

// RewriteHTML swaps CDN tags for local copies, drops SRI attributes on
// swapped tags and removes web-font links that can't load anyway.
// It returns the new HTML and any external URLs it couldn't replace.
func RewriteHTML(b []byte) ([]byte, []string) {
	var unknown []string
	out := tagRe.ReplaceAllFunc(b, func(tag []byte) []byte {
		m := urlAttrRe.FindSubmatchIndex(tag)
		if m == nil {
			return tag
		}
		u := string(tag[m[6]:m[7]])
		local := Resolve(u)
		if local == "" {
			low := strings.ToLower(string(tag))
			if fontsLinkRe.Match(tag) {
				return tag // removed below
			}
			// Ignore non-loading links (canonical, icons pointing elsewhere are harmless).
			if bytes.HasPrefix(bytes.ToLower(tag), []byte("<script")) || strings.Contains(low, "stylesheet") {
				unknown = append(unknown, u)
			}
			return tag
		}
		nt := append([]byte{}, tag[:m[6]]...)
		nt = append(nt, local...)
		nt = append(nt, tag[m[7]:]...)
		return sriRe.ReplaceAll(nt, nil)
	})
	out = fontsLinkRe.ReplaceAll(out, nil)
	out = cssImportRe.ReplaceAll(out, nil)
	return out, unknown
}

func rewriteAll(ctx context.Context, files map[string][]byte) (string, error) {
	for p, b := range files {
		ext := strings.ToLower(path.Ext(p))
		switch ext {
		case ".html", ".htm":
			if !bytes.Contains(b, []byte("//")) {
				continue
			}
			nb, unknown := RewriteHTML(b)
			files[p] = nb
			for _, u := range unknown {
				sites.AddWarning(ctx, p+": "+u+" won't load (pages can't reach the internet). Download it into the site, or use a built-in library from /_/lib.")
			}
		case ".css":
			if bytes.Contains(b, []byte("fonts.googleapis.com")) {
				files[p] = cssImportRe.ReplaceAll(b, nil)
			}
		}
	}
	return "", nil
}
