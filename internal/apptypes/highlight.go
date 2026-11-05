package apptypes

import (
	"strings"
)

// A deliberately tiny syntax highlighter: comments, strings, numbers and
// keywords for the languages people paste most often. Colors come from
// CSS classes (tok-k, tok-s, tok-c, tok-n), so it follows light/dark.

var keywords = map[string]bool{}

func init() {
	for _, k := range strings.Fields(`
abstract and as assert async await break case catch class const continue debugger def default defer del delete do elif else enum except export extends false final finally fn for from func function go goto if impl implements import in instanceof interface is lambda let loop match mod module mut namespace new nil none None not null of or package pass private protected pub public raise return select self static struct super switch this throw throws trait true True False try type typeof union unsafe use using var void while with yield
SELECT FROM WHERE INSERT INTO VALUES UPDATE SET DELETE CREATE TABLE INDEX JOIN LEFT RIGHT INNER OUTER ON GROUP BY ORDER HAVING LIMIT AS AND OR NOT NULL PRIMARY KEY DISTINCT UNION ALL CASE WHEN THEN END
select from where insert into values update set delete create table join left inner on group by order having limit distinct
echo fi then esac done elif local export readonly`) {
		keywords[k] = true
	}
}

func hashComments(lang string) bool {
	switch lang {
	case "py", "python", "sh", "bash", "shell", "zsh", "console", "yaml", "yml", "toml", "ruby", "rb", "r", "ps1", "powershell", "perl", "make", "makefile", "dockerfile", "conf", "ini", "nginx", "elixir":
		return true
	}
	return false
}

func dashComments(lang string) bool {
	switch lang {
	case "sql", "lua", "haskell", "hs", "elm":
		return true
	}
	return false
}

func noHighlight(lang string) bool {
	switch lang {
	case "", "text", "txt", "plain", "plaintext", "md", "markdown", "csv", "tsv", "diff", "mermaid", "output", "log":
		return true
	}
	return false
}

// highlight returns HTML for code.
func highlight(code, lang string) string {
	lang = strings.ToLower(strings.TrimSpace(lang))
	if noHighlight(lang) {
		return esc(code)
	}
	var b strings.Builder
	b.Grow(len(code) * 2)
	hash, dash := hashComments(lang), dashComments(lang)
	slash := !hash || lang == "php"
	span := func(cls, s string) {
		b.WriteString(`<span class="tok-` + cls + `">` + esc(s) + `</span>`)
	}
	n := len(code)
	for i := 0; i < n; {
		c := code[i]
		switch {
		case slash && c == '/' && i+1 < n && code[i+1] == '/', hash && c == '#', dash && c == '-' && i+1 < n && code[i+1] == '-':
			j := strings.IndexByte(code[i:], '\n')
			if j < 0 {
				j = n - i
			}
			span("c", code[i:i+j])
			i += j
		case c == '/' && i+1 < n && code[i+1] == '*':
			j := strings.Index(code[i+2:], "*/")
			if j < 0 {
				j = n - i
			} else {
				j += 4
			}
			span("c", code[i:i+j])
			i += j
		case c == '"' || c == '\'' || c == '`':
			j := i + 1
			for j < n && code[j] != c {
				if code[j] == '\\' {
					j += 2
					continue
				}
				if code[j] == '\n' && c != '`' {
					break
				}
				j++
			}
			if j < n && code[j] == c {
				j++
			}
			if j > n {
				j = n
			}
			span("s", code[i:j])
			i = j
		case c >= '0' && c <= '9' && (i == 0 || !isIdent(code[i-1])):
			j := i
			for j < n && (isIdent(code[j]) || code[j] == '.') {
				j++
			}
			span("n", code[i:j])
			i = j
		case isIdent(c):
			j := i
			for j < n && isIdent(code[j]) {
				j++
			}
			w := code[i:j]
			if keywords[w] {
				span("k", w)
			} else {
				b.WriteString(esc(w))
			}
			i = j
		default:
			b.WriteString(esc(string(c)))
			i++
		}
	}
	return b.String()
}

func isIdent(c byte) bool {
	return c == '_' || c == '$' || (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9') || c >= 0x80
}
