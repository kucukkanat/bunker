package sites

import (
	"fmt"
	"io"
	"strconv"
	"strings"
	"sync"
)

// The shell page is rendered from shellTmpl's own output: the template is
// executed once with a marker in place of every field, which yields the
// static segments, and each request writes those segments with the field
// values escaped exactly as html/template escapes them in that position.
// That skips html/template's reflection-driven escaper pipeline (~40% of
// the CPU of a shell request); TestShellMatchesTemplate checks the output
// is byte-identical.

type shellData struct {
	Title, Slug, Src, Base, Role, Icon, User string
	Draft, Badge, Guest                      bool
	Version, Live, SiteID                    int64
}

func (d shellData) values() map[string]any {
	return map[string]any{
		"Title": d.Title, "Slug": d.Slug, "Src": d.Src, "Base": d.Base, "Role": d.Role,
		"Draft": d.Draft, "Version": d.Version, "Live": d.Live, "Icon": d.Icon,
		"Badge": d.Badge, "User": d.User, "SiteID": d.SiteID, "Sandbox": SandboxFlags, "Guest": d.Guest,
	}
}

// Field kinds, by how html/template escapes them where they appear.
const (
	escText = iota // element text or a plain quoted attribute
	escURL         // a URL attribute (src, data-icon): filter + normalize + attribute escape
)

type shellPart struct {
	static string
	field  string // "" for the trailing static segment
	kind   int
}

var (
	shellOnce  sync.Once
	shellParts []shellPart // nil: fall back to the template
)

func compileShell() {
	fields := []string{"Title", "Slug", "Src", "Base", "Role", "Icon", "User", "Draft", "Badge", "Version", "Live", "SiteID", "Guest"}
	vals := map[string]any{"Sandbox": SandboxFlags}
	for i, f := range fields {
		vals[f] = "zqx" + strconv.Itoa(i) + "qxz"
	}
	var b strings.Builder
	if shellTmpl.Execute(&b, vals) != nil {
		return
	}
	out := b.String()
	var parts []shellPart
	for {
		i := strings.Index(out, "zqx")
		if i < 0 {
			break
		}
		j := strings.Index(out[i:], "qxz")
		if j < 0 {
			return
		}
		n, err := strconv.Atoi(out[i+3 : i+j])
		if err != nil || n < 0 || n >= len(fields) {
			return
		}
		kind := escText
		attr := out[:i]
		if strings.HasSuffix(attr, ` src="`) || strings.HasSuffix(attr, ` data-icon="`) {
			kind = escURL
		} else if !strings.HasSuffix(attr, `="`) && !strings.HasSuffix(attr, "<title>") && !strings.HasSuffix(attr, `<h1 class="bk-sr">`) {
			return // a position this renderer doesn't know: use the template
		}
		parts = append(parts, shellPart{static: out[:i], field: fields[n], kind: kind})
		out = out[i+j+3:]
	}
	shellParts = append(parts, shellPart{static: out})
}

func renderShell(w io.Writer, d shellData) {
	shellOnce.Do(compileShell)
	if shellParts == nil {
		shellTmpl.Execute(w, d.values())
		return
	}
	var b strings.Builder
	b.Grow(1400 + len(d.Src) + 2*len(d.Title))
	for _, p := range shellParts {
		b.WriteString(p.static)
		if p.field == "" {
			continue
		}
		var v string
		switch p.field {
		case "Title":
			v = d.Title
		case "Slug":
			v = d.Slug
		case "Src":
			v = d.Src
		case "Base":
			v = d.Base
		case "Role":
			v = d.Role
		case "Icon":
			v = d.Icon
		case "User":
			v = d.User
		case "Draft":
			v = strconv.FormatBool(d.Draft)
		case "Guest":
			v = strconv.FormatBool(d.Guest)
		case "Badge":
			v = strconv.FormatBool(d.Badge)
		case "Version":
			v = strconv.FormatInt(d.Version, 10)
		case "Live":
			v = strconv.FormatInt(d.Live, 10)
		case "SiteID":
			v = strconv.FormatInt(d.SiteID, 10)
		}
		if p.kind == escURL {
			v = normalizeURL(v)
		}
		writeAttrEscaped(&b, v)
	}
	io.WriteString(w, b.String())
}

// writeAttrEscaped is html/template's attribute and RCDATA escaper
// (htmlReplacementTable): NUL, quotes, &, + and angle brackets.
func writeAttrEscaped(b *strings.Builder, s string) {
	last := 0
	for i := 0; i < len(s); i++ {
		var r string
		switch s[i] {
		case 0:
			r = "�"
		case '"':
			r = "&#34;"
		case '&':
			r = "&amp;"
		case '\'':
			r = "&#39;"
		case '+':
			r = "&#43;"
		case '<':
			r = "&lt;"
		case '>':
			r = "&gt;"
		default:
			continue
		}
		b.WriteString(s[last:i])
		b.WriteString(r)
		last = i + 1
	}
	b.WriteString(s[last:])
}

// normalizeURL is html/template's urlFilter followed by urlNormalizer.
func normalizeURL(s string) string {
	if protocol, _, ok := strings.Cut(s, ":"); ok && !strings.Contains(protocol, "/") {
		if !strings.EqualFold(protocol, "http") && !strings.EqualFold(protocol, "https") && !strings.EqualFold(protocol, "mailto") {
			return "#ZgotmplZ"
		}
	}
	var b strings.Builder
	written := 0
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case strings.IndexByte("!#$&*+,/:;=?@[]-._~", c) >= 0,
			'a' <= c && c <= 'z', 'A' <= c && c <= 'Z', '0' <= c && c <= '9':
			continue
		case c == '%' && i+2 < len(s) && isHexByte(s[i+1]) && isHexByte(s[i+2]):
			continue
		}
		if written == 0 {
			b.Grow(len(s) + 16)
		}
		b.WriteString(s[written:i])
		fmt.Fprintf(&b, "%%%02x", c)
		written = i + 1
	}
	if written == 0 {
		return s
	}
	b.WriteString(s[written:])
	return b.String()
}

func isHexByte(c byte) bool {
	return '0' <= c && c <= '9' || 'a' <= c && c <= 'f' || 'A' <= c && c <= 'F'
}
