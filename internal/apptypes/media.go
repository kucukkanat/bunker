package apptypes

import (
	"encoding/json"
	"path"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/kucukkanat/bunker/internal/apptypes/bundled"
	"github.com/kucukkanat/bunker/internal/core"
)

func humanBytes(n int) string {
	switch {
	case n >= 1<<20:
		return strconv.FormatFloat(float64(n)/(1<<20), 'f', 1, 64) + " MB"
	case n >= 1<<10:
		return strconv.Itoa(n>>10) + " KB"
	}
	return strconv.Itoa(n) + " B"
}

// ---- gallery ----

func renderGallery(files map[string][]byte, visual, audio []string) (string, error) {
	title := "Gallery"
	if dir := commonDir(visual); dir != "" {
		title = niceName(dir)
	} else if len(visual) == 1 {
		title = niceName(visual[0])
	}
	var b strings.Builder
	b.WriteString(topBar(title, strconv.Itoa(len(visual))+" item"+plural(len(visual)), ""))
	b.WriteString(`<main class="gallery" id="gallery">`)
	for i, p := range visual {
		cap := esc(path.Base(p))
		src := esc(href(p))
		if categorize(p) == catVideo {
			b.WriteString(`<figure tabindex="0" data-i="` + strconv.Itoa(i) + `" data-kind="video" data-src="` + src + `" data-cap="` + cap + `"><video src="` + src + `#t=0.1" preload="metadata" muted playsinline></video><figcaption>▶ ` + cap + `</figcaption></figure>`)
			continue
		}
		b.WriteString(`<figure tabindex="0" data-i="` + strconv.Itoa(i) + `" data-kind="image" data-src="` + src + `" data-cap="` + cap + `"><img src="` + src + `" alt="` + esc(niceName(p)) + `" loading="lazy" decoding="async"><figcaption>` + cap + `</figcaption></figure>`)
	}
	b.WriteString(`</main>`)
	if len(audio) > 0 {
		b.WriteString(`<section class="media"><h2>Audio</h2>`)
		for _, p := range audio {
			b.WriteString(`<p>` + esc(path.Base(p)) + `<br><audio controls preload="none" src="` + esc(href(p)) + `"></audio></p>`)
		}
		b.WriteString(`</section>`)
	}
	b.WriteString(`<div class="lightbox" id="lb" role="dialog" aria-modal="true" aria-label="Viewer"><button class="close" aria-label="Close">×</button><button class="prev" aria-label="Previous">‹</button><div id="lb-body"></div><button class="next" aria-label="Next">›</button><div class="cap" id="lb-cap"></div></div>`)
	files["index.html"] = page{Title: title, Body: b.String(), JS: []string{"gallery.js"}}.render()
	addBase(files, "gallery.js")
	return "gallery", nil
}

func plural(n int) string {
	if n == 1 {
		return ""
	}
	return "s"
}

func commonDir(ps []string) string {
	if len(ps) < 2 {
		return ""
	}
	d := path.Dir(ps[0])
	for _, p := range ps[1:] {
		if path.Dir(p) != d {
			return ""
		}
	}
	if d == "." {
		return ""
	}
	return d
}

// ---- audio / video ----

func renderMedia(files map[string][]byte, items []string) (string, error) {
	title := niceName(items[0])
	if len(items) > 1 {
		title = "Media"
		if d := commonDir(items); d != "" {
			title = niceName(d)
		}
	}
	var b strings.Builder
	b.WriteString(topBar(title, "", ""))
	b.WriteString(`<main class="media"><div id="player"></div><h2 id="now" style="font-size:1.2rem"></h2>`)
	if len(items) > 1 {
		b.WriteString(`<ol class="playlist" id="list">`)
		for _, p := range items {
			k := "audio"
			if categorize(p) == catVideo {
				k = "video"
			}
			b.WriteString(`<li><button type="button" data-src="` + esc(href(p)) + `" data-kind="` + k + `" data-name="` + esc(path.Base(p)) + `"><span>` + map[string]string{"audio": "♪", "video": "▶"}[k] + `</span>` + esc(path.Base(p)) + `<span class="muted" style="margin-left:auto">` + humanBytes(len(files[p])) + `</span></button></li>`)
		}
		b.WriteString(`</ol>`)
	} else {
		k := "audio"
		if categorize(items[0]) == catVideo {
			k = "video"
		}
		b.WriteString(`<ol class="playlist" id="list" hidden><li><button type="button" data-src="` + esc(href(items[0])) + `" data-kind="` + k + `" data-name="` + esc(path.Base(items[0])) + `">` + esc(path.Base(items[0])) + `</button></li></ol>`)
	}
	b.WriteString(`<p class="muted"><a id="dl" download>Download</a></p></main>`)
	files["index.html"] = page{Title: title, Body: b.String(), JS: []string{"media.js"}}.render()
	addBase(files, "media.js")
	return "media", nil
}

// ---- PDF ----

// Chrome's built-in PDF viewer refuses to render inside sandboxed frames
// (and CSP object-src 'none' blocks <embed>/<object>), so PDFs get a
// pdf.js viewer. A download link is always offered as a fallback.
func renderPDF(files map[string][]byte, pdfs []string) (string, error) {
	lib, worker := bundled.PDFJS()
	if len(lib) == 0 || len(worker) == 0 {
		return "", core.Internal("the PDF viewer is missing from this build")
	}
	files[AssetDir+"pdfjs/pdf.min.mjs"] = lib
	files[AssetDir+"pdfjs/pdf.worker.min.mjs"] = worker
	title := niceName(pdfs[0])
	if len(pdfs) > 1 {
		title = "Documents"
	}
	var b strings.Builder
	actions := `<button class="btn" id="zout" type="button" aria-label="Zoom out">−</button><button class="btn" id="zin" type="button" aria-label="Zoom in">+</button><a class="btn primary" id="dl" download>Download</a>`
	b.WriteString(topBar(title, "", `<span class="meta" id="pg"></span>`+actions))
	if len(pdfs) > 1 {
		b.WriteString(`<div class="tabs" role="tablist" id="tabs">`)
		for i, p := range pdfs {
			sel := "false"
			if i == 0 {
				sel = "true"
			}
			b.WriteString(`<button role="tab" type="button" aria-selected="` + sel + `" data-src="` + esc(href(p)) + `" data-name="` + esc(path.Base(p)) + `">` + esc(niceName(p)) + `</button>`)
		}
		b.WriteString(`</div>`)
	}
	b.WriteString(`<main class="pdf-pages" id="pages" data-src="` + esc(href(pdfs[0])) + `" data-name="` + esc(path.Base(pdfs[0])) + `" aria-live="polite"><p class="muted">Loading…</p></main>`)
	b.WriteString(`<div class="pdf-fallback" id="fallback" hidden><h2>Can't show this PDF here</h2><p class="muted">Your browser couldn't display it inside Bunker. Download it to read it.</p><a class="btn primary" id="dl2" download>Download PDF</a></div>`)
	b.WriteString(`<script type="module" src="` + AssetDir + `pdf-viewer.js"></script>`)
	files["index.html"] = page{Title: title, Body: b.String()}.render()
	addBase(files, "pdf-viewer.js")
	return "pdf", nil
}

// ---- JSON ----

func renderJSON(files map[string][]byte, p string) (string, error) {
	data := files[p]
	var v any
	if err := json.Unmarshal(data, &v); err != nil {
		// Not valid JSON: show it as text instead.
		return renderText(files, []string{p})
	}
	title := niceName(p)
	var b strings.Builder
	b.WriteString(topBar(title, humanBytes(len(data)), `<input class="input" id="q" type="search" placeholder="Find…" aria-label="Find in JSON" style="width:200px"><button class="btn" id="expand" type="button">Expand all</button><button class="btn" id="collapse" type="button">Collapse</button><button class="btn" id="raw" type="button" aria-pressed="false">Raw</button>`))
	b.WriteString(`<main class="json" id="tree"></main><pre class="json" id="rawv" hidden></pre>`)
	b.WriteString(`<script type="application/json" id="bk-data">` + string(jsonSafe(data)) + `</script>`)
	files["index.html"] = page{Title: title, Body: b.String(), JS: []string{"json.js"}}.render()
	addBase(files, "json.js")
	return "json", nil
}

// jsonSafe re-encodes JSON so it can't close the surrounding <script>.
func jsonSafe(b []byte) []byte {
	var v any
	if json.Unmarshal(b, &v) != nil {
		return []byte("null")
	}
	out, _ := json.Marshal(v)
	return out
}

// ---- text ----

func renderText(files map[string][]byte, ps []string) (string, error) {
	if len(ps) > 1 {
		var b strings.Builder
		b.WriteString(topBar("Documents", "", ""))
		b.WriteString(`<main class="cards">`)
		for _, p := range ps {
			out := strings.TrimSuffix(p, path.Ext(p)) + ".html"
			files[out] = textPage(p, files[p], strings.Count(out, "/"))
			b.WriteString(`<a href="` + esc(href(out)) + `"><b>` + esc(niceName(p)) + `</b><br><small>` + esc(p) + ` · ` + humanBytes(len(files[p])) + `</small></a>`)
		}
		b.WriteString(`</main>`)
		files["index.html"] = page{Title: "Documents", Body: b.String()}.render()
	} else {
		files["index.html"] = textPage(ps[0], files[ps[0]], 0)
	}
	addBase(files)
	return "text", nil
}

func textPage(p string, data []byte, depth int) []byte {
	s := string(data)
	if !utf8.ValidString(s) {
		s = strings.ToValidUTF8(s, "�")
	}
	cls := "text-page"
	// Code-like or columnar text reads better monospaced.
	if strings.Contains(s, "\t") || strings.Contains(s, "    ") || strings.HasSuffix(strings.ToLower(p), ".log") {
		cls += " mono"
	}
	title := niceName(p)
	body := topBar(title, humanBytes(len(data)), `<a class="btn" href="`+esc(href(path.Base(p)))+`" download>Download</a>`) +
		`<main class="` + cls + `"><pre>` + esc(s) + `</pre></main>`
	return page{Title: title, Depth: depth, Body: body}.render()
}
