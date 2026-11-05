package apptypes

import (
	"bytes"
	"encoding/json"
	"path"
	"regexp"
	"strconv"
	"strings"

	"github.com/kucukkanat/bunker/internal/core"
)

// Jupyter notebook (nbformat 4) → static HTML.

type nbText []string

// UnmarshalJSON accepts a string or a list of strings.
func (t *nbText) UnmarshalJSON(b []byte) error {
	var s string
	if json.Unmarshal(b, &s) == nil {
		*t = []string{s}
		return nil
	}
	var l []string
	if err := json.Unmarshal(b, &l); err != nil {
		return nil // tolerate odd values
	}
	*t = l
	return nil
}

func (t nbText) String() string { return strings.Join(t, "") }

type nbOutput struct {
	OutputType string            `json:"output_type"`
	Name       string            `json:"name"`
	Text       nbText            `json:"text"`
	Data       map[string]nbText `json:"data"`
	ExecCount  *int              `json:"execution_count"`
	Ename      string            `json:"ename"`
	Evalue     string            `json:"evalue"`
	Traceback  []string          `json:"traceback"`
}

type nbCell struct {
	CellType  string     `json:"cell_type"`
	Source    nbText     `json:"source"`
	Outputs   []nbOutput `json:"outputs"`
	ExecCount *int       `json:"execution_count"`
}

type notebook struct {
	Cells    []nbCell `json:"cells"`
	Metadata struct {
		Kernel struct {
			Language    string `json:"language"`
			DisplayName string `json:"display_name"`
		} `json:"kernelspec"`
		LangInfo struct {
			Name string `json:"name"`
		} `json:"language_info"`
		Title string `json:"title"`
	} `json:"metadata"`
	NBFormat int `json:"nbformat"`
}

func renderNotebooks(files map[string][]byte, ps []string) (string, error) {
	bud := newMDBudget()
	if len(ps) == 1 {
		h, err := notebookPage(ps[0], files[ps[0]], 0, bud)
		if err != nil {
			return "", err
		}
		files["index.html"] = h
		addBase(files, "notebook.js")
		return "notebook", nil
	}
	var b strings.Builder
	b.WriteString(topBar("Notebooks", strconv.Itoa(len(ps))+" notebooks", ""))
	b.WriteString(`<main class="cards">`)
	for _, p := range ps {
		out := strings.TrimSuffix(p, path.Ext(p)) + ".html"
		h, err := notebookPage(p, files[p], strings.Count(out, "/"), bud)
		if err != nil {
			return "", err
		}
		files[out] = h
		b.WriteString(`<a href="` + esc(href(out)) + `"><b>` + esc(niceName(p)) + `</b><br><small>` + esc(p) + `</small></a>`)
	}
	b.WriteString(`</main>`)
	files["index.html"] = page{Title: "Notebooks", Body: b.String()}.render()
	addBase(files, "notebook.js")
	return "notebook", nil
}

var nbH1 = regexp.MustCompile(`(?m)^#\s+(.+)$`)
var ansiRe = regexp.MustCompile(`\x1b\[[0-9;]*[A-Za-z]`)

func notebookPage(p string, data []byte, depth int, bud *mdBudget) ([]byte, error) {
	var nb notebook
	if err := json.Unmarshal(data, &nb); err != nil {
		return nil, core.BadRequest(path.Base(p) + " isn't a valid Jupyter notebook")
	}
	if nb.NBFormat != 0 && nb.NBFormat < 4 {
		return nil, core.BadRequest(path.Base(p) + " uses an old notebook format; save it again with Jupyter 4 or newer")
	}
	lang := firstNonEmpty(nb.Metadata.LangInfo.Name, nb.Metadata.Kernel.Language, "python")
	title := nb.Metadata.Title
	var b strings.Builder
	for _, c := range nb.Cells {
		src := c.Source.String()
		switch c.CellType {
		case "markdown":
			var hb bytes.Buffer
			bud.convert([]byte(src), &hb)
			if title == "" {
				if m := nbH1.FindStringSubmatch(src); m != nil {
					title = strings.TrimSpace(m[1])
				}
			}
			b.WriteString(`<section class="cell markdown prose">` + hb.String() + `</section>`)
		case "code":
			n := ""
			if c.ExecCount != nil {
				n = strconv.Itoa(*c.ExecCount)
			}
			b.WriteString(`<section class="cell code"><div class="in"><div class="prompt">[` + n + `]:</div><pre class="src"><code>` + highlight(src, lang) + `</code></pre></div>`)
			if len(c.Outputs) > 0 {
				b.WriteString(`<div class="outs"><div class="prompt out"></div><div>`)
				for _, o := range c.Outputs {
					b.WriteString(renderOutput(o, bud))
				}
				b.WriteString(`</div></div>`)
			}
			b.WriteString(`</section>`)
		default: // raw
			if strings.TrimSpace(src) != "" {
				b.WriteString(`<section class="cell"><pre class="src">` + esc(src) + `</pre></section>`)
			}
		}
	}
	if title == "" {
		title = niceName(p)
	}
	meta := firstNonEmpty(nb.Metadata.Kernel.DisplayName, lang)
	body := topBar(title, meta, `<a class="btn" href="`+esc(href(path.Base(p)))+`" download>Download .ipynb</a>`) + `<main class="nb">` + b.String() + `</main>`
	return page{Title: title, Depth: depth, Body: body, JS: []string{"notebook.js"}}.render(), nil
}

func renderOutput(o nbOutput, bud *mdBudget) string {
	switch o.OutputType {
	case "stream":
		cls := ""
		if o.Name == "stderr" {
			cls = ` class="err"`
		}
		return `<div class="output"><pre` + cls + `>` + esc(ansiRe.ReplaceAllString(o.Text.String(), "")) + `</pre></div>`
	case "error":
		tb := ansiRe.ReplaceAllString(strings.Join(o.Traceback, "\n"), "")
		if tb == "" {
			tb = o.Ename + ": " + o.Evalue
		}
		return `<div class="output"><pre class="err">` + esc(tb) + `</pre></div>`
	case "execute_result", "display_data":
		d := o.Data
		for _, mt := range []string{"image/png", "image/jpeg", "image/gif", "image/webp"} {
			if v, ok := d[mt]; ok {
				b64 := strings.Map(func(r rune) rune {
					if r == '\n' || r == '\r' || r == ' ' {
						return -1
					}
					return r
				}, v.String())
				return `<div class="output"><img alt="Output image" src="data:` + mt + `;base64,` + esc(b64) + `"></div>`
			}
		}
		if v, ok := d["image/svg+xml"]; ok {
			return `<div class="output"><img alt="Output image" src="data:image/svg+xml;charset=utf-8,` + esc(urlEncodeSVG(v.String())) + `"></div>`
		}
		if v, ok := d["text/html"]; ok {
			// Sandboxed: HTML output can't touch the rest of the page.
			// The tiny script inside reports its height so the frame fits.
			doc := `<!doctype html><meta charset="utf-8"><style>body{margin:0;padding:4px 0;display:flow-root;font:13px system-ui,sans-serif;color:#1C211E;background:#fff}table{border-collapse:collapse}th,td{border:1px solid #ddd;padding:4px 8px;text-align:right}th{background:#f4f1e8}</style>` +
				v.String() + `<script>new ResizeObserver(function(){parent.postMessage({nbh:Math.ceil(document.body.getBoundingClientRect().bottom)},'*')}).observe(document.body)</script>`
			return `<div class="output"><iframe sandbox="allow-scripts" title="Output" srcdoc="` + esc(doc) + `"></iframe></div>`
		}
		if v, ok := d["text/markdown"]; ok {
			var hb bytes.Buffer
			bud.convert([]byte(v.String()), &hb)
			return `<div class="output prose">` + hb.String() + `</div>`
		}
		if v, ok := d["text/latex"]; ok {
			return `<div class="output"><pre>` + esc(v.String()) + `</pre></div>`
		}
		if v, ok := d["application/json"]; ok {
			return `<div class="output"><pre>` + esc(v.String()) + `</pre></div>`
		}
		if v, ok := d["text/plain"]; ok {
			return `<div class="output"><pre>` + esc(ansiRe.ReplaceAllString(v.String(), "")) + `</pre></div>`
		}
	}
	return ""
}

func urlEncodeSVG(s string) string {
	return strings.NewReplacer("%", "%25", "#", "%23", "\n", " ", "\r", " ", "\"", "'").Replace(s)
}
