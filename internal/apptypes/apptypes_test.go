package apptypes

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/json"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/evanw/esbuild/pkg/api"
)

func load(t *testing.T, names ...string) map[string][]byte {
	t.Helper()
	files := map[string][]byte{}
	for _, n := range names {
		b, err := os.ReadFile(filepath.Join("testdata", n))
		if err != nil {
			t.Fatal(err)
		}
		files[filepath.Base(n)] = b
	}
	return files
}

var tagRe = regexp.MustCompile(`<(/?)([a-zA-Z][a-zA-Z0-9-]*)[^>]*?(/?)>`)
var voidTags = map[string]bool{"area": true, "base": true, "br": true, "col": true, "embed": true, "hr": true, "img": true, "input": true, "link": true, "meta": true, "source": true, "track": true, "wbr": true, "!doctype": true}

// checkHTML verifies the document is well-formed enough: doctype, a
// closing </html>, and balanced non-void tags outside <script>/<style>.
func checkHTML(t *testing.T, name string, b []byte) {
	t.Helper()
	s := string(b)
	if !strings.HasPrefix(strings.ToLower(s), "<!doctype html>") {
		t.Errorf("%s: missing doctype", name)
	}
	// Remove script/style bodies.
	s = regexp.MustCompile(`(?is)<script\b[^>]*>.*?</script>`).ReplaceAllString(s, "")
	s = regexp.MustCompile(`(?is)<style\b[^>]*>.*?</style>`).ReplaceAllString(s, "")
	var stack []string
	for _, m := range tagRe.FindAllStringSubmatch(s, -1) {
		tag := strings.ToLower(m[2])
		if voidTags[tag] || m[3] == "/" {
			continue
		}
		if m[1] == "" {
			stack = append(stack, tag)
			continue
		}
		if len(stack) == 0 || stack[len(stack)-1] != tag {
			t.Fatalf("%s: unexpected </%s>, open: %v", name, tag, stack)
		}
		stack = stack[:len(stack)-1]
	}
	if len(stack) > 0 {
		t.Fatalf("%s: unclosed tags %v", name, stack)
	}
}

func mustApply(t *testing.T, files map[string][]byte, want string) {
	t.Helper()
	kind, err := Apply(context.Background(), files)
	if err != nil {
		t.Fatal(err)
	}
	if kind != want {
		t.Fatalf("kind = %q, want %q", kind, want)
	}
	if files["index.html"] == nil {
		t.Fatal("no index.html")
	}
	for p, b := range files {
		if strings.HasSuffix(p, ".html") {
			checkHTML(t, p, b)
		}
	}
}

func TestMarkdownDocs(t *testing.T) {
	files := map[string][]byte{}
	filepath.WalkDir("testdata/docs", func(p string, d fs.DirEntry, err error) error {
		if !d.IsDir() {
			b, _ := os.ReadFile(p)
			rel, _ := filepath.Rel("testdata/docs", p)
			files[filepath.ToSlash(rel)] = b
		}
		return nil
	})
	mustApply(t, files, "markdown")
	idx := string(files["index.html"])
	for _, want := range []string{"<title>Team Handbook</title>", `href="guide/setup.html#install"`, `<table>`, `type="checkbox"`, `class="tok-k">func</span>`, `class="tok-c">// greet</span>`, `_bunker/app.css`, `aria-current="page"`, `FAQ`} {
		if !strings.Contains(idx, want) {
			t.Errorf("index.html missing %q", want)
		}
	}
	setup := string(files["guide/setup.html"])
	if !strings.Contains(setup, `href="../index.html"`) || !strings.Contains(setup, `../_bunker/app.css`) || !strings.Contains(setup, `id="install"`) {
		t.Errorf("setup page links wrong: %s", setup)
	}
	// FAQ (order: 2) comes after setup? setup has no order, FAQ has order 2 → FAQ first within the folder.
	if strings.Index(idx, "FAQ") > strings.Index(idx, ">Setup<") {
		t.Errorf("ordered page should come first in nav")
	}
	if files["_bunker/docs.js"] == nil {
		t.Error("docs.js not added")
	}
}

func TestSingleMarkdown(t *testing.T) {
	files := map[string][]byte{"notes.md": []byte("Just a paragraph with no heading.\n")}
	mustApply(t, files, "markdown")
	if !strings.Contains(string(files["index.html"]), "<h1>Notes</h1>") {
		t.Error("expected a generated title")
	}
}

func TestSlides(t *testing.T) {
	files := load(t, "slides.md")
	mustApply(t, files, "slides")
	h := string(files["index.html"])
	if n := strings.Count(h, `<section class="slide`); n != 3 {
		t.Fatalf("got %d slides, want 3", n)
	}
	if !strings.Contains(h, `class="slide lead"`) || !strings.Contains(h, "slides.js") {
		t.Error("lead class or script missing")
	}
	// Slides mode via slides: true front matter.
	files = map[string][]byte{"talk.md": []byte("---\nslides: true\n---\n# A\n---\n# B\n")}
	mustApply(t, files, "slides")
	// Without the flag, --- stays a horizontal rule.
	files = map[string][]byte{"doc.md": []byte("# A\n\n---\n\n# B\n")}
	mustApply(t, files, "markdown")
}

func TestCSVTable(t *testing.T) {
	files := load(t, "sales.csv")
	mustApply(t, files, "table")
	data := extractData(t, files["index.html"])
	var d struct {
		Sheets []tableSheet `json:"sheets"`
	}
	if err := json.Unmarshal(data, &d); err != nil {
		t.Fatal(err)
	}
	s := d.Sheets[0]
	types := []string{}
	for _, c := range s.Columns {
		types = append(types, c.Type)
	}
	if strings.Join(types, ",") != "text,text,number,date,bool" {
		t.Fatalf("types = %v", types)
	}
	if s.Columns[2].Prefix != "$" || s.Rows[0][2].(float64) != 1200.5 || s.Rows[0][4] != true {
		t.Fatalf("bad conversion: %+v %v", s.Columns[2], s.Rows[0])
	}
	if files["_bunker/table.js"] == nil {
		t.Fatal("table.js missing")
	}
}

func TestDelimiterSniffing(t *testing.T) {
	rows, err := readDelimited([]byte("a;b\n1;2\n"), false)
	if err != nil || len(rows[0]) != 2 {
		t.Fatalf("semicolon: %v %v", rows, err)
	}
	if _, _, _, _, ok := parseNum("00123"); ok {
		t.Error("leading-zero IDs must stay text")
	}
	if f, _, suf, _, ok := parseNum("(1,000)"); !ok || f != -1000 || suf != "" {
		t.Error("accounting negatives")
	}
	if f, _, suf, _, _ := parseNum("12.5%"); f != 12.5 || suf != "%" {
		t.Error("percent")
	}
}

func makeXLSX(t *testing.T) []byte {
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	add := func(name, body string) {
		w, _ := zw.Create(name)
		w.Write([]byte(body))
	}
	add("xl/workbook.xml", `<workbook xmlns="http://schemas.openxmlformats.org/spreadsheetml/2006/main" xmlns:r="http://schemas.openxmlformats.org/officeDocument/2006/relationships"><sheets><sheet name="Budget" sheetId="1" r:id="rId1"/><sheet name="Hidden" sheetId="2" state="hidden" r:id="rId2"/><sheet name="People" sheetId="3" r:id="rId3"/></sheets></workbook>`)
	add("xl/_rels/workbook.xml.rels", `<Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships"><Relationship Id="rId1" Target="worksheets/sheet1.xml"/><Relationship Id="rId2" Target="worksheets/sheet2.xml"/><Relationship Id="rId3" Target="/xl/worksheets/sheet3.xml"/></Relationships>`)
	add("xl/sharedStrings.xml", `<sst><si><t>Item</t></si><si><t>Cost</t></si><si><t>When</t></si><si><r><t>Lap</t></r><r><t>top</t></r></si><si><t>Name</t></si><si><t>Ana</t></si></sst>`)
	add("xl/styles.xml", `<styleSheet><numFmts><numFmt numFmtId="164" formatCode="dd/mm/yyyy"/></numFmts><cellXfs><xf numFmtId="0"/><xf numFmtId="164"/></cellXfs></styleSheet>`)
	add("xl/worksheets/sheet1.xml", `<worksheet><sheetData><row r="1"><c r="A1" t="s"><v>0</v></c><c r="B1" t="s"><v>1</v></c><c r="C1" t="s"><v>2</v></c></row><row r="2"><c r="A2" t="s"><v>3</v></c><c r="B2"><v>1299.99</v></c><c r="C2" s="1"><v>45292</v></c></row><row r="3"><c r="A3" t="inlineStr"><is><t>Desk</t></is></c><c r="C3" s="1"><v>45293</v></c><c r="B3"><f>B2/2</f><v>650</v></c></row></sheetData></worksheet>`)
	add("xl/worksheets/sheet2.xml", `<worksheet><sheetData/></worksheet>`)
	add("xl/worksheets/sheet3.xml", `<worksheet><sheetData><row r="1"><c r="A1" t="s"><v>4</v></c><c r="B1" t="str"><v>Active</v></c></row><row r="2"><c r="A2" t="s"><v>5</v></c><c r="B2" t="b"><v>1</v></c></row></sheetData></worksheet>`)
	zw.Close()
	return buf.Bytes()
}

func TestXLSX(t *testing.T) {
	sheets, err := readXLSX(makeXLSX(t))
	if err != nil {
		t.Fatal(err)
	}
	if len(sheets) != 2 || sheets[0].Name != "Budget" || sheets[1].Name != "People" {
		t.Fatalf("sheets: %+v", sheets)
	}
	r := sheets[0].Rows
	if r[1][0] != "Laptop" || r[1][1] != "1299.99" || r[1][2] != "2024-01-01" || r[2][0] != "Desk" || r[2][1] != "650" {
		t.Fatalf("rows: %q", r)
	}
	files := map[string][]byte{"budget.xlsx": makeXLSX(t)}
	mustApply(t, files, "table")
	h := string(files["index.html"])
	if !strings.Contains(h, `id="tabs"`) || !strings.Contains(h, `"name":"People"`) {
		t.Error("multi-sheet tabs missing")
	}
}

func extractData(t *testing.T, h []byte) []byte {
	t.Helper()
	m := regexp.MustCompile(`(?s)<script type="application/json" id="bk-data">(.*?)</script>`).FindSubmatch(h)
	if m == nil {
		t.Fatal("no embedded data")
	}
	return m[1]
}

func TestJSONViewer(t *testing.T) {
	files := load(t, "data.json")
	mustApply(t, files, "json")
	h := files["index.html"]
	if bytes.Contains(extractData(t, h), []byte("</script>")) {
		t.Fatal("embedded JSON can break out of its script tag")
	}
	var v map[string]any
	if err := json.Unmarshal(extractData(t, h), &v); err != nil || v["name"] != "Bunker" {
		t.Fatalf("data not round-tripped: %v", err)
	}
	// Invalid JSON falls back to text.
	files = map[string][]byte{"bad.json": []byte("{nope")}
	mustApply(t, files, "text")
}

func TestGalleryAndMedia(t *testing.T) {
	px := load(t, "pixel.png")["pixel.png"]
	files := map[string][]byte{"photos/a.png": px, "photos/b b.png": px, "photos/clip.mp4": []byte("x")}
	mustApply(t, files, "gallery")
	h := string(files["index.html"])
	if !strings.Contains(h, `src="photos/b%20b.png"`) || !strings.Contains(h, `data-kind="video"`) || !strings.Contains(h, "<title>Photos</title>") {
		t.Errorf("gallery html: %s", h)
	}
	files = map[string][]byte{"a.mp3": []byte("x"), "b.mp3": []byte("y")}
	mustApply(t, files, "media")
	if strings.Count(string(files["index.html"]), "data-kind=\"audio\"") != 2 {
		t.Error("playlist missing")
	}
}

func TestPDF(t *testing.T) {
	files := load(t, "hello.pdf")
	mustApply(t, files, "pdf")
	if len(files["_bunker/pdfjs/pdf.min.mjs"]) < 100000 || len(files["_bunker/pdfjs/pdf.worker.min.mjs"]) < 100000 {
		t.Fatal("pdf.js not bundled")
	}
	h := string(files["index.html"])
	if !strings.Contains(h, `data-src="hello.pdf"`) || !strings.Contains(h, `id="fallback"`) {
		t.Error("viewer markup")
	}
}

func TestNotebook(t *testing.T) {
	files := load(t, "notebook.ipynb")
	mustApply(t, files, "notebook")
	h := string(files["index.html"])
	for _, want := range []string{"<title>Sales analysis</title>", "<strong>bold</strong>", "[1]:", "hello\n", "data:image/png;base64,iVBOR", `sandbox="allow-scripts"`, "ValueError: bad", `class="tok-k">import</span>`} {
		if !strings.Contains(h, want) {
			t.Errorf("notebook missing %q", want)
		}
	}
	if strings.Contains(h, "\x1b[") {
		t.Error("ANSI codes not stripped")
	}
}

func TestText(t *testing.T) {
	files := load(t, "notes.txt")
	mustApply(t, files, "text")
	if !strings.Contains(string(files["index.html"]), "- one &lt;b&gt;") {
		t.Error("text not escaped")
	}
}

func TestReactBundle(t *testing.T) {
	files := load(t, "App.jsx", "style.css")
	mustApply(t, files, "react")
	js := string(files["_bunker/bundle.js"])
	if js == "" || files["_bunker/bundle.css"] == nil {
		t.Fatal("bundle missing")
	}
	if strings.Contains(js, `from"react"`) || strings.Contains(js, `from "react"`) {
		t.Error("bare import left in bundle")
	}
	if !strings.Contains(string(files["index.html"]), `<div id="root">`) {
		t.Error("root missing")
	}
	// Unknown packages fail with a plain message.
	files = map[string][]byte{"App.tsx": []byte("import x from 'left-pad'\nexport default () => <div>{x}</div>\n")}
	_, err := Apply(context.Background(), files)
	if err == nil || !strings.Contains(err.Error(), `"left-pad" isn't available`) {
		t.Fatalf("err = %v", err)
	}
	// Built-in UMD libraries become globals + a /_/lib script tag.
	files = map[string][]byte{"Chart.jsx": []byte("import { Chart } from 'chart.js/auto';\nimport _ from 'lodash';\nexport default function C(){ return <p>{_.capitalize('x')}{String(!!Chart)}</p> }\n")}
	mustApply(t, files, "react")
	if h := string(files["index.html"]); !strings.Contains(h, `src="/_/lib/chart.js/`) || !strings.Contains(h, `src="/_/lib/lodash/`) {
		t.Fatalf("lib tags missing: %s", h)
	}
	// TypeScript, htm and a local module.
	files = map[string][]byte{
		"main.ts": []byte("import { html, render } from 'htm/preact';\nimport { greet } from './lib';\nconst n: number = 2;\nrender(html`<p>${greet(n)}</p>`, document.body);\n"),
		"lib.ts":  []byte("export const greet = (n: number): string => 'hi ' + n;\n"),
	}
	mustApply(t, files, "react")
	if node, err := exec.LookPath("node"); err == nil {
		// The bundle must at least parse as a module.
		tmp := filepath.Join(t.TempDir(), "b.mjs")
		os.WriteFile(tmp, files["_bunker/bundle.js"], 0o600)
		if out, err := exec.Command(node, "--check", tmp).CombinedOutput(); err != nil {
			t.Fatalf("bundle doesn't parse: %v %s", err, out)
		}
	}
}

func TestViteProject(t *testing.T) {
	files := map[string][]byte{
		"index.html":   []byte(`<!doctype html><html><head><title>X</title></head><body><div id="root"></div><script type="module" src="/src/main.tsx"></script></body></html>`),
		"package.json": []byte(`{"name":"x"}`),
		"src/main.tsx": []byte("import { createRoot } from 'react-dom/client';\nimport App from './App';\ncreateRoot(document.getElementById('root')!).render(<App />);\n"),
		"src/App.tsx":  []byte("export default function App(){ return <h1>Hi</h1> }\n"),
	}
	kind, err := Apply(context.Background(), files)
	if err != nil || kind != "react" {
		t.Fatalf("kind=%q err=%v", kind, err)
	}
	if !strings.Contains(string(files["index.html"]), `src="_bunker/bundle.js"`) {
		t.Error("script tag not rewritten")
	}
}

func TestLeavesHTMLAlone(t *testing.T) {
	files := map[string][]byte{"page.html": []byte("<p>hi"), "notes.md": []byte("# x")}
	kind, err := Apply(context.Background(), files)
	if err != nil || kind != "" || files["index.html"] != nil {
		t.Fatalf("should not transform: %q %v", kind, err)
	}
	files = map[string][]byte{"setup.exe.zip.unknown": []byte("x")}
	if kind, _ := Apply(context.Background(), files); kind != "" {
		t.Fatal("unknown types go to the file index")
	}
}

// Every shipped JS asset must parse.
func TestAssetsParse(t *testing.T) {
	entries, _ := assets.ReadDir("assets")
	for _, e := range entries {
		if !strings.HasSuffix(e.Name(), ".js") {
			continue
		}
		b, _ := assets.ReadFile("assets/" + e.Name())
		format := api.FormatIIFE
		if e.Name() == "pdf-viewer.js" {
			format = api.FormatESModule
		}
		res := api.Transform(string(b), api.TransformOptions{Loader: api.LoaderJS, Format: format})
		if len(res.Errors) > 0 {
			t.Errorf("%s: %v", e.Name(), res.Errors[0].Text)
		}
	}
}

func TestHighlight(t *testing.T) {
	h := highlight(`x = "a\"b" # c`+"\n", "python")
	if !strings.Contains(h, `<span class="tok-s">&#34;a\&#34;b&#34;</span>`) || !strings.Contains(h, `<span class="tok-c"># c</span>`) {
		t.Fatalf("got %s", h)
	}
	// Unterminated string at EOF must not panic.
	highlight(`"abc\`, "js")
}

func TestBabelStandalone(t *testing.T) {
	page := `<!doctype html><html><head><script src="https://unpkg.com/react@18/umd/react.production.min.js"></script>
<script src="https://unpkg.com/@babel/standalone/babel.min.js"></script></head><body><div id="root"></div>
<script type="text/babel" id="app">
function App(){ const [n,setN]=React.useState(0); return <button onClick={()=>setN(n+1)}>{n}</button> }
ReactDOM.createRoot(document.getElementById('root')).render(<App/>);
</script>
<script type="text/babel" data-presets="typescript,react" src="extra.tsx"></script></body></html>`
	files := map[string][]byte{"index.html": []byte(page), "extra.tsx": []byte("const x: number = 1; console.log(<b>{x}</b>);")}
	kind, err := Apply(context.Background(), files)
	if err != nil || kind != "react" {
		t.Fatalf("kind=%q err=%v", kind, err)
	}
	h := string(files["index.html"])
	if strings.Contains(h, "babel.min.js") || strings.Contains(h, "text/babel") {
		t.Fatalf("babel left behind: %s", h)
	}
	if !strings.Contains(h, "React.createElement(\"button\"") || !strings.Contains(h, `<script id="app">`) || !strings.Contains(h, "react.production.min.js") {
		t.Fatalf("not transpiled: %s", h)
	}
	if !strings.Contains(h, `React.createElement("b", null, x)`) {
		t.Fatalf("external tsx not inlined: %s", h)
	}
	files = map[string][]byte{"index.html": []byte(`<script type="text/babel">const a = <div>;</script>`)}
	if _, err := Apply(context.Background(), files); err == nil || !strings.Contains(err.Error(), "line 1") {
		t.Fatalf("want a build error, got %v", err)
	}
}
