package apptypes

import (
	"fmt"
	"path"
	"regexp"
	"strings"

	"github.com/evanw/esbuild/pkg/api"

	"github.com/kucukkanat/bunker/internal/core"
)

// AI-generated single-file apps very often use in-browser Babel:
//
//	<script src="https://unpkg.com/@babel/standalone/babel.min.js"></script>
//	<script type="text/babel"> ... JSX ... </script>
//
// Pages can't load Babel (no internet), so at deploy time we transpile
// those scripts with esbuild (classic JSX → React.createElement), make
// them plain scripts, and drop the Babel tag. This runs before the
// vendorlib transformer (priority 90), which then rewrites the React CDN
// tags to the bundled copies.

var babelScriptRe = regexp.MustCompile(`(?is)<script\b([^>]*)>(.*?)</script>`)
var typeAttrRe = regexp.MustCompile(`(?i)\s(?:type|data-type)\s*=\s*["']?([^"'\s>]+)["']?`)
var srcAttrRe = regexp.MustCompile(`(?i)\ssrc\s*=\s*["']([^"']+)["']`)
var presetsRe = regexp.MustCompile(`(?i)\sdata-presets\s*=\s*["']([^"']*)["']`)

func isBabelType(t string) bool {
	t = strings.ToLower(t)
	return t == "text/babel" || t == "text/jsx" || t == "text/tsx" || t == "application/babel"
}

// transpileBabelPages rewrites every HTML file that uses text/babel scripts.
func transpileBabelPages(files map[string][]byte) (bool, error) {
	changed := false
	for p, b := range files {
		lp := strings.ToLower(p)
		if !strings.HasSuffix(lp, ".html") && !strings.HasSuffix(lp, ".htm") {
			continue
		}
		if !strings.Contains(strings.ToLower(string(b)), "text/babel") && !strings.Contains(strings.ToLower(string(b)), "text/jsx") {
			continue
		}
		out, ok, err := transpileBabelHTML(files, p, b)
		if err != nil {
			return false, err
		}
		if ok {
			files[p] = out
			changed = true
		}
	}
	return changed, nil
}

func transpileBabelHTML(files map[string][]byte, name string, b []byte) ([]byte, bool, error) {
	var firstErr error
	changed := false
	out := babelScriptRe.ReplaceAllFunc(b, func(m []byte) []byte {
		sm := babelScriptRe.FindSubmatch(m)
		attrs, body := string(sm[1]), string(sm[2])
		if src := srcAttrRe.FindStringSubmatch(attrs); src != nil && strings.Contains(strings.ToLower(src[1]), "babel") && !hasBabelType(attrs) {
			changed = true
			return nil // drop @babel/standalone
		}
		if !hasBabelType(attrs) {
			return m
		}
		changed = true
		module := false
		for _, tm := range typeAttrRe.FindAllStringSubmatch(attrs, -1) {
			if strings.EqualFold(tm[1], "module") {
				module = true
			}
		}
		loader := api.LoaderJSX
		if pm := presetsRe.FindStringSubmatch(attrs); pm != nil && strings.Contains(strings.ToLower(pm[1]), "typescript") {
			loader = api.LoaderTSX
		}
		code := body
		srcName := name
		if src := srcAttrRe.FindStringSubmatch(attrs); src != nil {
			// <script type="text/babel" src="app.jsx">: inline the transpiled file.
			ref := strings.TrimPrefix(strings.SplitN(src[1], "?", 2)[0], "./")
			ref = path.Clean(path.Join(path.Dir(name), ref))
			if strings.HasPrefix(src[1], "/") {
				ref = strings.TrimPrefix(src[1], "/")
			}
			data, ok := files[ref]
			if !ok {
				if firstErr == nil {
					firstErr = core.NewErr(422, "build_failed", fmt.Sprintf("%s loads %s, which isn't in the upload", name, src[1]))
				}
				return m
			}
			code, srcName = string(data), ref
			if strings.HasSuffix(strings.ToLower(ref), ".tsx") || strings.HasSuffix(strings.ToLower(ref), ".ts") {
				loader = api.LoaderTSX
			}
		}
		res := api.Transform(code, api.TransformOptions{
			Loader:      loader,
			JSX:         api.JSXTransform, // classic: React.createElement, like Babel's default
			Target:      api.ES2020,
			Sourcefile:  srcName,
			LogLevel:    api.LogLevelSilent,
			Charset:     api.CharsetUTF8,
			TsconfigRaw: `{"compilerOptions":{"preserveValueImports":true,"verbatimModuleSyntax":false}}`,
		})
		if len(res.Errors) > 0 {
			if firstErr == nil {
				e := res.Errors[0]
				msg := e.Text
				if e.Location != nil {
					msg = fmt.Sprintf("%s line %d: %s", srcName, e.Location.Line, msg)
				}
				firstErr = core.NewErr(422, "build_failed", "Bunker couldn't convert the JSX in "+msg)
			}
			return m
		}
		// Keep the other attributes (id, defer...), minus type/src/presets.
		rest := typeAttrRe.ReplaceAllString(attrs, "")
		rest = srcAttrRe.ReplaceAllString(rest, "")
		rest = presetsRe.ReplaceAllString(rest, "")
		tag := "<script" + rest
		if module {
			tag += ` type="module"`
		}
		js := strings.ReplaceAll(string(res.Code), "</script", `<\/script`)
		return []byte(tag + ">\n" + js + "</script>")
	})
	if firstErr != nil {
		return nil, false, firstErr
	}
	return out, changed, nil
}

func hasBabelType(attrs string) bool {
	for _, tm := range typeAttrRe.FindAllStringSubmatch(attrs, -1) {
		if isBabelType(tm[1]) {
			return true
		}
	}
	return false
}
