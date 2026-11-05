package apptypes

import (
	"bytes"
	"path"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/yuin/goldmark"
	"github.com/yuin/goldmark/ast"
	"github.com/yuin/goldmark/extension"
	"github.com/yuin/goldmark/parser"
	"github.com/yuin/goldmark/renderer"
	gmhtml "github.com/yuin/goldmark/renderer/html"
	"github.com/yuin/goldmark/text"
	"github.com/yuin/goldmark/util"
)

var md = goldmark.New(
	goldmark.WithExtensions(extension.GFM, extension.Footnote, extension.DefinitionList, extension.Typographer),
	goldmark.WithParserOptions(parser.WithAutoHeadingID()),
	goldmark.WithRendererOptions(gmhtml.WithUnsafe(), renderer.WithNodeRenderers(util.Prioritized(codeRenderer{}, 100))),
)

// codeRenderer highlights fenced and indented code blocks.
type codeRenderer struct{}

func (codeRenderer) RegisterFuncs(reg renderer.NodeRendererFuncRegisterer) {
	reg.Register(ast.KindFencedCodeBlock, renderCodeBlock)
	reg.Register(ast.KindCodeBlock, renderCodeBlock)
}

func renderCodeBlock(w util.BufWriter, src []byte, n ast.Node, entering bool) (ast.WalkStatus, error) {
	if !entering {
		return ast.WalkContinue, nil
	}
	lang := ""
	if f, ok := n.(*ast.FencedCodeBlock); ok && f.Info != nil {
		lang = strings.Fields(string(f.Info.Segment.Value(src)) + " ")[0]
	}
	var code bytes.Buffer
	lines := n.Lines()
	for i := 0; i < lines.Len(); i++ {
		s := lines.At(i)
		code.Write(s.Value(src))
	}
	w.WriteString(`<pre`)
	if lang != "" {
		w.WriteString(` data-lang="` + esc(lang) + `"`)
	}
	w.WriteString(`><code>`)
	w.WriteString(highlight(code.String(), lang))
	w.WriteString("</code></pre>\n")
	return ast.WalkSkipChildren, nil
}

// frontMatter parses a leading "---" block of simple "key: value" lines.
func frontMatter(src []byte) (map[string]string, []byte) {
	fm := map[string]string{}
	s := string(bytes.TrimPrefix(src, []byte("\xef\xbb\xbf")))
	if !strings.HasPrefix(s, "---\n") && !strings.HasPrefix(s, "---\r\n") {
		return fm, []byte(s)
	}
	rest := s[strings.IndexByte(s, '\n')+1:]
	end := -1
	for off := 0; off < len(rest); {
		nl := strings.IndexByte(rest[off:], '\n')
		line := rest[off:]
		if nl >= 0 {
			line = rest[off : off+nl]
		}
		if strings.TrimRight(line, "\r ") == "---" || strings.TrimRight(line, "\r ") == "..." {
			end = off
			break
		}
		if nl < 0 {
			break
		}
		off += nl + 1
	}
	if end < 0 {
		return fm, []byte(s)
	}
	for _, line := range strings.Split(rest[:end], "\n") {
		k, v, ok := strings.Cut(line, ":")
		if !ok || strings.HasPrefix(line, " ") {
			continue
		}
		v = strings.TrimSpace(v)
		v = strings.Trim(v, `"'`)
		fm[strings.ToLower(strings.TrimSpace(k))] = v
	}
	body := rest[end:]
	body = body[strings.IndexByte(body+"\n", '\n')+1:]
	if len(body) > 0 && body[0] == '\n' {
		body = body[1:]
	}
	return fm, []byte(body)
}

func truthy(s string) bool {
	s = strings.ToLower(s)
	return s == "true" || s == "yes" || s == "1" || s == "on"
}

type heading struct {
	Level int
	ID    string
	Text  string
}

type mdDoc struct {
	bud    *mdBudget
	src    string // source path
	out    string // output html path
	title  string
	order  float64
	hasOrd bool
	fm     map[string]string
	body   []byte
	slides bool
	html   string
	heads  []heading
	hasH1  bool
}

// mdOut maps a markdown path to the generated page path.
func mdOut(p string) string {
	dir, base := path.Split(p)
	lb := strings.ToLower(base)
	stem := strings.TrimSuffix(base, path.Ext(base))
	if strings.HasPrefix(lb, "readme.") || strings.HasPrefix(lb, "index.") {
		return dir + "index.html"
	}
	return dir + stem + ".html"
}

var numPrefix = regexp.MustCompile(`^(\d+)[-_. ]+`)

func renderMarkdown(files map[string][]byte, mds []string) (string, error) {
	bud := newMDBudget()
	outOf := map[string]string{}
	var docs []*mdDoc
	for _, p := range mds {
		fm, body := frontMatter(files[p])
		d := &mdDoc{src: p, out: mdOut(p), fm: fm, body: body, bud: bud}
		if _, taken := files[d.out]; taken && !strings.HasSuffix(d.out, "index.html") {
			d.out = strings.TrimSuffix(d.out, ".html") + "-md.html"
		}
		d.slides = truthy(fm["marp"]) || truthy(fm["slides"])
		for _, k := range []string{"order", "nav_order", "sidebar_position", "weight"} {
			if v, err := strconv.ParseFloat(fm[k], 64); err == nil {
				d.order, d.hasOrd = v, true
				break
			}
		}
		if !d.hasOrd {
			if m := numPrefix.FindStringSubmatch(path.Base(p)); m != nil {
				v, _ := strconv.ParseFloat(m[1], 64)
				d.order, d.hasOrd = v, true
			}
		}
		outOf[p] = d.out
		docs = append(docs, d)
	}
	// Two md files mapping to the same page (README.md + index.md): keep the first.
	seen := map[string]bool{}
	uniq := docs[:0]
	for _, d := range docs {
		if seen[d.out] {
			continue
		}
		seen[d.out] = true
		uniq = append(uniq, d)
	}
	docs = uniq
	for _, d := range docs {
		d.render(outOf)
	}
	sortDocs(docs)

	// One file that's a deck: just the deck.
	if len(docs) == 1 && docs[0].slides {
		d := docs[0]
		files["index.html"] = slidesPage(d, 0)
		addBase(files, "slides.js")
		return "slides", nil
	}
	siteTitle := ""
	for _, d := range docs {
		if d.out == "index.html" {
			siteTitle = firstNonEmpty(d.fm["site"], d.fm["sitetitle"], d.title)
		}
	}
	if siteTitle == "" {
		if len(docs) == 1 {
			siteTitle = docs[0].title
		} else {
			siteTitle = "Docs"
		}
	}
	for i, d := range docs {
		depth := strings.Count(d.out, "/")
		if d.slides {
			files[d.out] = slidesPage(d, depth)
			continue
		}
		var prev, next *mdDoc
		if i > 0 {
			prev = docs[i-1]
		}
		if i+1 < len(docs) {
			next = docs[i+1]
		}
		files[d.out] = docPage(d, docs, siteTitle, prev, next, depth)
	}
	if _, ok := files["index.html"]; !ok {
		// No README/index: the first page doubles as the home page.
		first := docs[0]
		if !strings.Contains(first.out, "/") {
			if first.slides {
				files["index.html"] = slidesPage(first, 0)
			} else {
				var next *mdDoc
				if len(docs) > 1 {
					next = docs[1]
				}
				files["index.html"] = docPage(first, docs, siteTitle, nil, next, 0)
			}
		} else {
			files["index.html"] = []byte(`<!doctype html><meta charset="utf-8"><meta http-equiv="refresh" content="0;url=` + esc(href(first.out)) + `"><a href="` + esc(href(first.out)) + `">` + esc(first.title) + `</a>`)
		}
	}
	addBase(files, "docs.js", "slides.js")
	for _, d := range docs {
		if d.slides {
			if len(docs) == 1 {
				return "slides", nil
			}
		}
	}
	return "markdown", nil
}

func sortDocs(docs []*mdDoc) {
	sort.SliceStable(docs, func(i, j int) bool {
		a, b := docs[i], docs[j]
		da, db := path.Dir(a.out), path.Dir(b.out)
		if da != db {
			// Root pages first, then folders alphabetically.
			if da == "." {
				return true
			}
			if db == "." {
				return false
			}
			return da < db
		}
		ia, ib := strings.HasSuffix(a.out, "index.html"), strings.HasSuffix(b.out, "index.html")
		if ia != ib {
			return ia
		}
		if a.hasOrd != b.hasOrd {
			return a.hasOrd
		}
		if a.hasOrd && a.order != b.order {
			return a.order < b.order
		}
		return strings.ToLower(a.src) < strings.ToLower(b.src)
	})
}

func (d *mdDoc) render(outOf map[string]string) {
	if d.slides {
		d.title = firstNonEmpty(d.fm["title"], niceName(d.src))
		return
	}
	if !d.bud.allow(d.body) {
		d.html = "<pre>" + esc(string(d.body)) + "</pre>"
		d.title = firstNonEmpty(d.fm["title"], niceName(d.src))
		return
	}
	reader := text.NewReader(d.body)
	doc := md.Parser().Parse(reader)
	dir := path.Dir(d.src)
	outDir := path.Dir(d.out)
	_ = ast.Walk(doc, func(n ast.Node, entering bool) (ast.WalkStatus, error) {
		if !entering {
			return ast.WalkContinue, nil
		}
		switch v := n.(type) {
		case *ast.Heading:
			t := nodeText(v, d.body)
			id, _ := v.AttributeString("id")
			ids, _ := id.([]byte)
			if v.Level == 1 && !d.hasH1 {
				d.hasH1 = true
				if d.title == "" {
					d.title = t
				}
			}
			if v.Level == 2 || v.Level == 3 {
				d.heads = append(d.heads, heading{Level: v.Level, ID: string(ids), Text: t})
			}
		case *ast.Link:
			v.Destination = fixLink(v.Destination, dir, outDir, outOf)
		}
		return ast.WalkContinue, nil
	})
	var b bytes.Buffer
	_ = md.Renderer().Render(&b, d.body, doc)
	d.html = addAnchors(b.String())
	if t := d.fm["title"]; t != "" {
		d.title = t
	}
	if d.title == "" {
		d.title = niceName(d.src)
		if strings.HasSuffix(d.out, "index.html") {
			if dir == "." {
				d.title = "Home"
			} else {
				d.title = niceName(dir)
			}
		}
	}
}

var h23Re = regexp.MustCompile(`(?s)<h([23]) id="([^"]+)">(.*?)</h[23]>`)

// addAnchors adds a "#" link after each h2/h3 (after the text, so it
// doesn't push the heading in while it's hidden).
func addAnchors(h string) string {
	return h23Re.ReplaceAllStringFunc(h, func(m string) string {
		sm := h23Re.FindStringSubmatch(m)
		return `<h` + sm[1] + ` id="` + sm[2] + `">` + sm[3] + ` <a class="anchor" href="#` + sm[2] + `" aria-hidden="true">#</a></h` + sm[1] + `>`
	})
}

// fixLink rewrites links between markdown files to the generated pages.
func fixLink(dest []byte, dir, outDir string, outOf map[string]string) []byte {
	s := string(dest)
	if s == "" || strings.HasPrefix(s, "#") || strings.Contains(s, "://") || strings.HasPrefix(s, "mailto:") || strings.HasPrefix(s, "/") {
		return dest
	}
	target, frag, _ := strings.Cut(s, "#")
	ext := strings.ToLower(path.Ext(target))
	if ext != ".md" && ext != ".markdown" {
		return dest
	}
	full := path.Clean(path.Join(dir, target))
	out, ok := outOf[full]
	if !ok {
		out = mdOut(full)
	}
	rel := relPath(outDir, out)
	if frag != "" {
		rel += "#" + frag
	}
	return []byte(rel)
}

// relPath returns the relative URL from directory fromDir to file to.
func relPath(fromDir, to string) string {
	if fromDir == "." {
		fromDir = ""
	}
	fa := strings.Split(fromDir, "/")
	if fromDir == "" {
		fa = nil
	}
	ta := strings.Split(to, "/")
	i := 0
	for i < len(fa) && i < len(ta)-1 && fa[i] == ta[i] {
		i++
	}
	return strings.Repeat("../", len(fa)-i) + strings.Join(ta[i:], "/")
}

func nodeText(n ast.Node, src []byte) string {
	var b strings.Builder
	_ = ast.Walk(n, func(c ast.Node, entering bool) (ast.WalkStatus, error) {
		if !entering {
			return ast.WalkContinue, nil
		}
		switch t := c.(type) {
		case *ast.Text:
			b.Write(t.Segment.Value(src))
			if t.SoftLineBreak() {
				b.WriteByte(' ')
			}
		case *ast.String:
			b.Write(t.Value)
		}
		return ast.WalkContinue, nil
	})
	return strings.TrimSpace(b.String())
}

func docPage(d *mdDoc, docs []*mdDoc, siteTitle string, prev, next *mdDoc, depth int) []byte {
	outDir := path.Dir(d.out)
	link := func(o *mdDoc) string { return esc(href(relPath(outDir, o.out))) }
	var nav strings.Builder
	nav.WriteString(`<nav class="side" id="side" aria-label="Pages"><a class="brand" href="` + esc(relPath(outDir, "index.html")) + `">` + esc(siteTitle) + `</a>`)
	group, open := "\x00", false
	for _, o := range docs {
		if g := path.Dir(o.out); g != group {
			if open {
				nav.WriteString(`</ul>`)
			}
			if g != "." {
				nav.WriteString(`<div class="group">` + esc(niceName(g)) + `</div>`)
			}
			nav.WriteString(`<ul>`)
			group, open = g, true
		}
		cur := ""
		if o == d {
			cur = ` aria-current="page"`
		}
		nav.WriteString(`<li><a href="` + link(o) + `"` + cur + `>` + esc(o.title) + `</a>`)
		if o == d && len(d.heads) > 0 {
			nav.WriteString(`<ul>`)
			for _, h := range d.heads {
				if h.Level == 2 {
					nav.WriteString(`<li><a href="#` + esc(h.ID) + `">` + esc(h.Text) + `</a></li>`)
				}
			}
			nav.WriteString(`</ul>`)
		}
		nav.WriteString(`</li>`)
	}
	if open {
		nav.WriteString(`</ul>`)
	}
	nav.WriteString(`</nav>`)
	navHTML := nav.String()

	var body strings.Builder
	single := len(docs) == 1
	if single && len(d.heads) <= 1 {
		body.WriteString(`<div class="docs" style="grid-template-columns:minmax(0,1fr)">`)
	} else if single {
		body.WriteString(`<div class="docs" style="grid-template-columns:minmax(0,1fr) 220px">`)
	} else {
		body.WriteString(`<button class="btn menu-btn" id="menu" aria-controls="side" aria-expanded="false">☰ Pages</button><div class="docs">`)
		body.WriteString(navHTML)
	}
	body.WriteString(`<main id="main"><article class="prose"`)
	if single {
		body.WriteString(` style="margin:0 auto"`)
	}
	body.WriteString(`>`)
	if !d.hasH1 {
		body.WriteString(`<h1>` + esc(d.title) + `</h1>`)
	}
	body.WriteString(d.html)
	body.WriteString(`</article>`)
	if prev != nil || next != nil {
		body.WriteString(`<div class="pager prose">`)
		if prev != nil {
			body.WriteString(`<a class="prev" href="` + link(prev) + `"><small>Previous</small>` + esc(prev.title) + `</a>`)
		} else {
			body.WriteString(`<span></span>`)
		}
		if next != nil {
			body.WriteString(`<a class="next" href="` + link(next) + `"><small>Next</small>` + esc(next.title) + `</a>`)
		}
		body.WriteString(`</div>`)
	}
	body.WriteString(`</main>`)
	if !single || len(d.heads) > 1 {
		body.WriteString(`<aside class="toc" aria-label="On this page">`)
		if len(d.heads) > 1 {
			body.WriteString(`<b>On this page</b>`)
			for _, h := range d.heads {
				body.WriteString(`<a class="l` + strconv.Itoa(h.Level) + `" href="#` + esc(h.ID) + `">` + esc(h.Text) + `</a>`)
			}
		}
		body.WriteString(`</aside>`)
	}
	body.WriteString(`</div>`)
	title := d.title
	if !single && title != siteTitle {
		title += " · " + siteTitle
	}
	return page{Title: title, Depth: depth, Body: body.String(), JS: []string{"docs.js"}}.render()
}

// ---- slides ----

var slideSep = regexp.MustCompile(`(?m)^---[ \t]*\r?$`)
var classDirective = regexp.MustCompile(`<!--\s*_?class:\s*([\w -]+?)\s*-->`)

func splitSlides(body []byte) []string {
	// Split on "---" lines that are outside fenced code blocks.
	var out []string
	var cur strings.Builder
	fence := ""
	for _, line := range strings.SplitAfter(string(body), "\n") {
		t := strings.TrimSpace(line)
		if fence == "" && (strings.HasPrefix(t, "```") || strings.HasPrefix(t, "~~~")) {
			fence = t[:3]
		} else if fence != "" && strings.HasPrefix(t, fence) {
			fence = ""
		}
		if fence == "" && slideSep.MatchString(strings.TrimRight(line, "\n")) {
			out = append(out, cur.String())
			cur.Reset()
			continue
		}
		cur.WriteString(line)
	}
	out = append(out, cur.String())
	var res []string
	for _, s := range out {
		if strings.TrimSpace(s) != "" {
			res = append(res, s)
		}
	}
	return res
}

func slidesPage(d *mdDoc, depth int) []byte {
	parts := splitSlides(d.body)
	var b strings.Builder
	b.WriteString(`<div class="deck" id="deck" role="region" aria-roledescription="slide deck" aria-label="` + esc(d.title) + `">`)
	for i, s := range parts {
		cls := ""
		if m := classDirective.FindStringSubmatch(s); m != nil {
			cls = " " + m[1]
		} else if i == 0 && truthy(firstNonEmpty(d.fm["lead"], "true")) && isTitleSlide(s) {
			cls = " lead"
		}
		var hb bytes.Buffer
		d.bud.convert([]byte(s), &hb)
		b.WriteString(`<section class="slide` + esc(cls) + `" aria-roledescription="slide" aria-label="` + strconv.Itoa(i+1) + ` of ` + strconv.Itoa(len(parts)) + `">`)
		b.WriteString(hb.String())
		if truthy(firstNonEmpty(d.fm["paginate"], "true")) {
			b.WriteString(`<span class="num">` + strconv.Itoa(i+1) + ` / ` + strconv.Itoa(len(parts)) + `</span>`)
		}
		b.WriteString(`</section>`)
	}
	b.WriteString(`</div><div class="deck-bar" id="bar"></div><div class="deck-ui"><button class="btn" id="prev" aria-label="Previous slide">←</button><button class="btn" id="full" aria-label="Full screen">⤢</button><button class="btn" id="next" aria-label="Next slide">→</button></div>`)
	return page{Title: d.title, Depth: depth, Body: b.String(), Class: "slides", JS: []string{"slides.js"}}.render()
}

func isTitleSlide(s string) bool {
	lines := 0
	for _, l := range strings.Split(strings.TrimSpace(s), "\n") {
		if strings.TrimSpace(l) != "" && !strings.HasPrefix(strings.TrimSpace(l), "<!--") {
			lines++
		}
	}
	return lines <= 3
}

func firstNonEmpty(v ...string) string {
	for _, s := range v {
		if strings.TrimSpace(s) != "" {
			return s
		}
	}
	return ""
}

// Goldmark's parser is quadratic on some inputs (200 KB of "[a](" takes
// ~17 s; deeply nested lists or quotes are similar), and any signed-in
// person can deploy markdown. mdBudget caps the work per deploy: hostile
// shapes and anything past the byte budget are shown as plain text.
type mdBudget struct{ left int }

const (
	mdBudgetBytes   = 4 << 20 // markdown converted per deploy
	mdMaxBrackets   = 3000    // "[" per document (links, refs, footnotes)
	mdMaxLineNested = 32      // container markers (> - * + 1.) opening one line
)

func newMDBudget() *mdBudget { return &mdBudget{left: mdBudgetBytes} }

// allow reports whether src may be converted, and charges it.
func (b *mdBudget) allow(src []byte) bool {
	if b == nil {
		b = newMDBudget()
	}
	if len(src) > b.left || mdHostile(src) {
		return false
	}
	b.left -= len(src)
	return true
}

// convert renders src as markdown, or as escaped plain text when it's
// over budget or shaped to blow up the parser.
func (b *mdBudget) convert(src []byte, w *bytes.Buffer) {
	if !b.allow(src) {
		w.WriteString("<pre>" + esc(string(src)) + "</pre>")
		return
	}
	_ = md.Convert(src, w)
}

func mdHostile(src []byte) bool {
	if bytes.Count(src, []byte("[")) > mdMaxBrackets {
		return true
	}
	for len(src) > 0 {
		line := src
		if i := bytes.IndexByte(src, '\n'); i >= 0 {
			line, src = src[:i], src[i+1:]
		} else {
			src = nil
		}
		if lineNesting(line) > mdMaxLineNested {
			return true
		}
	}
	return false
}

// lineNesting counts block container markers at the start of a line.
func lineNesting(l []byte) int {
	n := 0
	for i := 0; i < len(l); {
		c := l[i]
		switch {
		case c == ' ' || c == '\t':
			i++
		case c == '>':
			n++
			i++
		case (c == '-' || c == '*' || c == '+') && (i+1 == len(l) || l[i+1] == ' ' || l[i+1] == '\t'):
			n++
			i += 2
		case c >= '0' && c <= '9':
			j := i
			for j < len(l) && j-i < 10 && l[j] >= '0' && l[j] <= '9' {
				j++
			}
			if j < len(l) && (l[j] == '.' || l[j] == ')') && (j+1 == len(l) || l[j+1] == ' ') {
				n++
				i = j + 1
				continue
			}
			return n
		default:
			return n
		}
	}
	return n
}
