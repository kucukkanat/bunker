// Package vendor holds the small set of browser libraries embedded in the
// Bunker binary. Site pages can't reach the internet (CSP), so bare
// imports in dropped React/JSX/TS files are resolved against this set at
// deploy time and bundled into the site.
//
// Supported bare imports (React apps run on Preact's compat layer, ~25 KB):
//
//	react, react-dom, react-dom/client, react/jsx-runtime, react/jsx-dev-runtime
//	preact, preact/hooks, preact/compat, preact/jsx-runtime
//	htm, htm/preact
//
// pdf.js (legacy build) is also embedded, gzip-compressed, for the PDF
// viewer generated for dropped .pdf files.
package vendor

import (
	"bytes"
	"compress/gzip"
	"embed"
	"io"
	"sort"
	"sync"
)

//go:embed files
var files embed.FS

// Versions of the embedded libraries.
const (
	PreactVersion = "10.26.9"
	HTMVersion    = "3.1.1"
	PDFJSVersion  = "5.4.296"
)

// modules maps a bare import specifier to an embedded file.
var modules = map[string]string{
	"preact":                 "preact.js",
	"preact/hooks":           "preact-hooks.js",
	"preact/compat":          "preact-compat.js",
	"preact/compat/client":   "preact-compat-client.js",
	"preact/jsx-runtime":     "preact-jsx-runtime.js",
	"preact/jsx-dev-runtime": "preact-jsx-runtime.js",
	"htm":                    "htm.js",
	"htm/preact":             "htm-preact.js",
}

// aliases map React specifiers onto Preact's compat layer.
var aliases = map[string]string{
	"react":                     "preact/compat",
	"react-dom":                 "preact/compat",
	"react-dom/client":          "preact/compat/client",
	"react/jsx-runtime":         "preact/jsx-runtime",
	"react/jsx-dev-runtime":     "preact/jsx-runtime",
	"preact/compat/jsx-runtime": "preact/jsx-runtime",
}

// Resolve maps an import specifier to a canonical vendored module name.
func Resolve(spec string) (string, bool) {
	if a, ok := aliases[spec]; ok {
		spec = a
	}
	_, ok := modules[spec]
	return spec, ok
}

// Source returns the JavaScript source of a canonical module name.
func Source(name string) ([]byte, bool) {
	f, ok := modules[name]
	if !ok {
		return nil, false
	}
	b, err := files.ReadFile("files/" + f)
	if err != nil {
		return nil, false
	}
	// Drop sourceMappingURL comments: the maps aren't shipped.
	if i := bytes.LastIndex(b, []byte("//# sourceMappingURL=")); i >= 0 {
		b = b[:i]
	}
	return b, true
}

// Supported lists every bare import a page may use.
func Supported() []string {
	var out []string
	for k := range modules {
		out = append(out, k)
	}
	for k := range aliases {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

var (
	pdfOnce   sync.Once
	pdfLib    []byte
	pdfWorker []byte
)

// PDFJS returns the pdf.js library and worker modules.
func PDFJS() (lib, worker []byte) {
	pdfOnce.Do(func() {
		pdfLib = gunzip("files/pdfjs/pdf.min.mjs.gz")
		pdfWorker = gunzip("files/pdfjs/pdf.worker.min.mjs.gz")
	})
	return pdfLib, pdfWorker
}

func gunzip(p string) []byte {
	b, err := files.ReadFile(p)
	if err != nil {
		return nil
	}
	zr, err := gzip.NewReader(bytes.NewReader(b))
	if err != nil {
		return nil
	}
	out, _ := io.ReadAll(zr)
	return out
}
