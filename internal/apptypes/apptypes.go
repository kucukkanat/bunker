// Package apptypes makes "drop anything, get an app" true. It registers a
// deploy-time transformer that turns uploads without an index.html into
// a polished, self-contained app:
//
//	.md / folder of .md   → docs site (or a slide deck with `marp: true` / `slides: true`)
//	.csv .tsv .xlsx       → interactive table app (sort, filter, search, charts, export)
//	.json                 → collapsible viewer
//	images (+ videos)     → gallery with lightbox
//	.pdf                  → pdf.js viewer (Chrome's built-in viewer is blocked in sandboxed frames)
//	video / audio         → player with playlist
//	.ipynb                → rendered notebook
//	.txt                  → readable page
//	.jsx .tsx .ts .js     → bundled with esbuild; React/Preact/htm from the vendored set
//
// Generated pages reference shared assets under _bunker/ (relative URLs)
// and never load anything from the internet.
package apptypes

import (
	"bytes"
	"context"
	"embed"
	"encoding/json"
	"html"
	"net/url"
	"path"
	"sort"
	"strings"

	"github.com/kucukkanat/bunker/internal/sites"
)

//go:embed assets
var assets embed.FS

// AssetDir is where generated support files are written in the site.
const AssetDir = "_bunker/"

func init() {
	sites.RegisterTransformer(sites.Transformer{Name: "apptypes", Priority: 50, Apply: Apply})
}

type category int

const (
	catOther category = iota
	catMarkdown
	catData
	catNotebook
	catJSON
	catImage
	catVideo
	catAudio
	catPDF
	catText
	catCode
	catStyle // css, fonts: harmless companions
)

func categorize(p string) category {
	switch strings.ToLower(path.Ext(p)) {
	case ".md", ".markdown", ".mdown", ".mkd":
		return catMarkdown
	case ".csv", ".tsv", ".xlsx", ".xlsm":
		return catData
	case ".ipynb":
		return catNotebook
	case ".json", ".geojson":
		return catJSON
	case ".png", ".jpg", ".jpeg", ".gif", ".webp", ".avif", ".svg", ".bmp", ".ico":
		return catImage
	case ".mp4", ".webm", ".mov", ".m4v", ".ogv":
		return catVideo
	case ".mp3", ".wav", ".ogg", ".oga", ".m4a", ".aac", ".flac", ".opus":
		return catAudio
	case ".pdf":
		return catPDF
	case ".txt", ".text", ".log":
		return catText
	case ".jsx", ".tsx", ".ts", ".mts", ".js", ".mjs":
		return catCode
	case ".css", ".woff", ".woff2", ".ttf", ".otf", ".map":
		return catStyle
	}
	return catOther
}

// serverExts are code files that are server functions when under api/.
var serverExts = map[string]bool{".js": true, ".mjs": true, ".ts": true, ".mts": true}

// Apply is the transformer entry point. It only acts when the upload has
// no HTML entry page; it returns the detected kind ("" when it left the
// files alone).
func Apply(ctx context.Context, files map[string][]byte) (string, error) {
	// In-browser Babel (<script type="text/babel">) → precompiled scripts.
	babel, err := transpileBabelPages(files)
	if err != nil {
		return "", err
	}
	if _, ok := files["index.html"]; ok {
		// An unbuilt Vite-style project (index.html → /src/main.tsx).
		k, err := bundleIndexScripts(files)
		if k == "" && babel && err == nil {
			k = "react"
		}
		return k, err
	}
	if babel {
		return "react", nil
	}
	by := map[category][]string{}
	for p := range files {
		ext := strings.ToLower(path.Ext(p))
		if ext == ".html" || ext == ".htm" {
			return "", nil // sites picks the single HTML file as the entry
		}
		if strings.HasPrefix(p, AssetDir) {
			continue
		}
		if strings.HasPrefix(p, "api/") && serverExts[strings.ToLower(path.Ext(p))] {
			continue // server functions run on Bunker, not in the page
		}
		c := categorize(p)
		if c == catJSON && (path.Base(p) == "package.json" || path.Base(p) == "tsconfig.json" || strings.HasSuffix(p, "package-lock.json")) {
			c = catOther
		}
		by[c] = append(by[c], p)
	}
	for _, v := range by {
		sort.Strings(v)
	}
	n := func(cs ...category) int {
		t := 0
		for _, c := range cs {
			t += len(by[c])
		}
		return t
	}
	total := 0
	for _, v := range by {
		total += len(v)
	}
	media := n(catImage, catVideo, catAudio)
	switch {
	case n(catMarkdown) > 0:
		return renderMarkdown(files, by[catMarkdown])
	case n(catCode) > 0 && codeEntry(files, by[catCode]) != "":
		return renderCode(files, codeEntry(files, by[catCode]))
	case n(catNotebook) > 0:
		return renderNotebooks(files, by[catNotebook])
	case n(catData) > 0 && n(catData)+n(catStyle, catText, catJSON) == total:
		return renderTable(files, by[catData])
	case n(catPDF) > 0 && n(catPDF)+n(catText) == total:
		return renderPDF(files, by[catPDF])
	case n(catImage) > 0 && media == total-n(catText, catStyle):
		return renderGallery(files, append(by[catImage], by[catVideo]...), by[catAudio])
	case media > 0 && media == total-n(catText, catStyle):
		return renderMedia(files, append(by[catVideo], by[catAudio]...))
	case n(catJSON) == 1 && total == 1:
		return renderJSON(files, by[catJSON][0])
	case n(catText) > 0 && n(catText) == total:
		return renderText(files, by[catText])
	}
	return "", nil
}

// ---- shared page helpers ----

type page struct {
	Title string
	Depth int    // folder depth of the page within the site
	Body  string // inner HTML of <body>
	Class string // body class
	CSS   []string
	JS    []string // asset names under _bunker/
	Head  string
}

func (p page) rel() string { return strings.Repeat("../", p.Depth) }

func (p page) render() []byte {
	var b bytes.Buffer
	b.WriteString(`<!doctype html><html lang="en"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1">`)
	b.WriteString(`<title>` + esc(p.Title) + `</title>`)
	b.WriteString(`<link rel="stylesheet" href="/_/kit.css">`)
	b.WriteString(`<link rel="stylesheet" href="` + p.rel() + AssetDir + `app.css">`)
	for _, c := range p.CSS {
		b.WriteString(`<link rel="stylesheet" href="` + p.rel() + c + `">`)
	}
	b.WriteString(p.Head)
	b.WriteString(`</head><body`)
	if p.Class != "" {
		b.WriteString(` class="` + esc(p.Class) + `"`)
	}
	b.WriteString(`>`)
	b.WriteString(p.Body)
	for _, j := range p.JS {
		b.WriteString(`<script src="` + p.rel() + AssetDir + j + `"></script>`)
	}
	b.WriteString(`</body></html>`)
	return b.Bytes()
}

// addAsset copies an embedded asset into the site.
func addAsset(files map[string][]byte, name string) {
	b, err := assets.ReadFile("assets/" + name)
	if err == nil {
		files[AssetDir+name] = b
	}
}

func addBase(files map[string][]byte, js ...string) {
	addAsset(files, "app.css")
	for _, j := range js {
		addAsset(files, j)
	}
}

func esc(s string) string { return html.EscapeString(s) }

// href builds a relative URL for a site path, escaping each segment.
func href(p string) string {
	parts := strings.Split(p, "/")
	for i, s := range parts {
		parts[i] = url.PathEscape(s)
	}
	return strings.Join(parts, "/")
}

// jsonData returns JSON safe to embed inside a <script type="application/json">.
func jsonData(v any) string {
	b, _ := json.Marshal(v) // escapes <, >, & as < etc.
	return string(b)
}

func dataScript(id string, v any) string {
	return `<script type="application/json" id="` + id + `">` + jsonData(v) + `</script>`
}

// niceName turns "q3-sales_report.csv" into "Q3 sales report".
func niceName(p string) string {
	b := path.Base(p)
	b = strings.TrimSuffix(b, path.Ext(b))
	b = strings.NewReplacer("_", " ", "-", " ").Replace(b)
	b = strings.Join(strings.Fields(b), " ")
	if b == "" {
		return "Untitled"
	}
	r := []rune(b)
	if r[0] >= 'a' && r[0] <= 'z' {
		r[0] -= 32
	}
	return string(r)
}

func topBar(title, meta, actions string) string {
	s := `<header class="bk-top"><h1>` + esc(title) + `</h1>`
	if meta != "" {
		s += `<span class="meta">` + esc(meta) + `</span>`
	}
	return s + actions + `</header>`
}
