package apptypes

import (
	"bytes"
	"encoding/csv"
	"fmt"
	"io"
	"path"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/kucukkanat/bunker/internal/core"
)

// tableSheet is the data model the table app reads.
type tableSheet struct {
	Name    string     `json:"name"`
	Source  string     `json:"source"`
	Columns []tableCol `json:"columns"`
	Rows    [][]any    `json:"rows"`
	Total   int        `json:"total"` // rows before truncation
}

type tableCol struct {
	Name   string `json:"name"`
	Type   string `json:"type"` // number | date | bool | text
	Prefix string `json:"prefix,omitempty"`
	Suffix string `json:"suffix,omitempty"`
	Dec    int    `json:"dec,omitempty"` // decimals seen
}

const maxTableRows = 100_000

func renderTable(files map[string][]byte, paths []string) (string, error) {
	var sheets []tableSheet
	for _, p := range paths {
		ext := strings.ToLower(path.Ext(p))
		switch ext {
		case ".xlsx", ".xlsm":
			xs, err := readXLSX(files[p])
			if err != nil {
				return "", core.BadRequest(path.Base(p) + ": " + err.Error())
			}
			for _, x := range xs {
				name := x.Name
				if len(paths) > 1 {
					name = niceName(p) + " · " + x.Name
				}
				if s, ok := buildSheet(name, p, x.Rows); ok {
					sheets = append(sheets, s)
				}
			}
		default:
			rows, err := readDelimited(files[p], ext == ".tsv")
			if err != nil {
				return "", core.BadRequest(path.Base(p) + ": " + err.Error())
			}
			if s, ok := buildSheet(niceName(p), p, rows); ok {
				sheets = append(sheets, s)
			}
		}
	}
	if len(sheets) == 0 {
		return "", nil
	}
	title := niceName(paths[0])
	if len(paths) > 1 {
		title = "Data"
	}
	var b strings.Builder
	b.WriteString(topBar(title, "", `<button class="btn" id="exp-csv" type="button">Export CSV</button><button class="btn" id="exp-json" type="button">JSON</button>`))
	if len(sheets) > 1 {
		b.WriteString(`<div class="tabs" role="tablist" id="tabs"></div>`)
	}
	b.WriteString(`<div class="tbl-wrap"><div class="stats" id="stats"></div><div class="chart-card" id="chart" hidden></div>
<div class="toolbar"><input class="input grow" id="q" type="search" placeholder="Search all columns…" aria-label="Search"><span class="muted" id="count" aria-live="polite"></span>
<button class="btn" id="tog-filters" type="button" aria-pressed="false">Filters</button><button class="btn" id="tog-chart" type="button" aria-pressed="false">Chart</button><button class="btn" id="reset" type="button">Reset</button></div>
<div class="grid-scroll"><table class="grid" id="grid"><thead></thead><tbody></tbody></table></div><div class="more" id="more"></div></div>`)
	b.WriteString(dataScript("bk-data", map[string]any{"title": title, "sheets": sheets}))
	files["index.html"] = page{Title: title, Body: b.String(), JS: []string{"table.js"}}.render()
	addBase(files, "table.js")
	return "table", nil
}

func readDelimited(data []byte, tsv bool) ([][]string, error) {
	data = bytes.TrimPrefix(data, []byte("\xef\xbb\xbf"))
	if !utf8.Valid(data) {
		// Most likely Windows-1252 from Excel "Save as CSV": map bytes 1:1 (Latin-1).
		r := make([]rune, len(data))
		for i, c := range data {
			r[i] = rune(c)
		}
		data = []byte(string(r))
	}
	sep := ','
	if tsv {
		sep = '\t'
	} else {
		first := data
		if i := bytes.IndexByte(data, '\n'); i >= 0 {
			first = data[:i]
		}
		c, s, t := bytes.Count(first, []byte(",")), bytes.Count(first, []byte(";")), bytes.Count(first, []byte("\t"))
		if t > c && t >= s {
			sep = '\t'
		} else if s > c {
			sep = ';'
		}
	}
	r := csv.NewReader(bytes.NewReader(data))
	r.Comma = sep
	r.LazyQuotes = true
	r.FieldsPerRecord = -1
	r.ReuseRecord = false
	var rows [][]string
	for {
		rec, err := r.Read()
		if err != nil {
			if err == io.EOF {
				break
			}
			if pe, ok := err.(*csv.ParseError); ok {
				return nil, fmt.Errorf("couldn't read line %d: %v", pe.Line, pe.Err)
			}
			break
		}
		rows = append(rows, rec)
		if len(rows) > maxTableRows+1 {
			break
		}
	}
	return rows, nil
}

func buildSheet(name, src string, rows [][]string) (tableSheet, bool) {
	// Skip leading empty rows; the first non-empty row is the header.
	for len(rows) > 0 && emptyRow(rows[0]) {
		rows = rows[1:]
	}
	for len(rows) > 0 && emptyRow(rows[len(rows)-1]) {
		rows = rows[:len(rows)-1]
	}
	if len(rows) == 0 {
		return tableSheet{}, false
	}
	header := rows[0]
	body := rows[1:]
	width := len(header)
	for _, r := range body {
		if len(r) > width {
			width = len(r)
		}
	}
	// Drop trailing columns that are empty everywhere.
	for width > 0 {
		used := strings.TrimSpace(at(header, width-1)) != ""
		for _, r := range body {
			if strings.TrimSpace(at(r, width-1)) != "" {
				used = true
				break
			}
		}
		if used {
			break
		}
		width--
	}
	if width == 0 {
		return tableSheet{}, false
	}
	total := len(body)
	if len(body) > maxTableRows {
		body = body[:maxTableRows]
	}
	s := tableSheet{Name: name, Source: src, Total: total}
	seen := map[string]int{}
	for i := 0; i < width; i++ {
		n := strings.TrimSpace(at(header, i))
		if n == "" {
			n = "Column " + strconv.Itoa(i+1)
		}
		if seen[n] > 0 {
			seen[n]++
			n = n + " " + strconv.Itoa(seen[n])
		} else {
			seen[n] = 1
		}
		s.Columns = append(s.Columns, tableCol{Name: n})
	}
	for i := range s.Columns {
		s.Columns[i] = detectType(s.Columns[i], body, i)
	}
	s.Rows = make([][]any, 0, len(body))
	for _, r := range body {
		if emptyRow(r) {
			continue
		}
		out := make([]any, width)
		for i := 0; i < width; i++ {
			out[i] = convertCell(s.Columns[i], strings.TrimSpace(at(r, i)))
		}
		s.Rows = append(s.Rows, out)
	}
	return s, true
}

func at(r []string, i int) string {
	if i < len(r) {
		return r[i]
	}
	return ""
}

func emptyRow(r []string) bool {
	for _, c := range r {
		if strings.TrimSpace(c) != "" {
			return false
		}
	}
	return true
}

var numRe = regexp.MustCompile(`^([$€£¥₹]|USD |EUR |GBP )?\s*([-+]?\(?[\d][\d,  ]*(?:\.\d+)?\)?|[-+]?\.\d+)\s*(%|[$€£]| ?[kKmM])?$`)
var dateLayouts = []string{"2006-01-02", "2006-01-02 15:04", "2006-01-02 15:04:05", "2006-01-02T15:04:05Z07:00", "2006-01-02T15:04:05", "2006/01/02", "1/2/2006", "01/02/2006", "2.1.2006", "02.01.2006", "Jan 2, 2006", "2 Jan 2006", "January 2, 2006", "2-Jan-2006", "02-Jan-06"}

func parseNum(s string) (float64, string, string, int, bool) {
	m := numRe.FindStringSubmatch(s)
	if m == nil {
		return 0, "", "", 0, false
	}
	n := strings.NewReplacer(",", "", " ", "", " ", "").Replace(m[2])
	neg := false
	if strings.HasPrefix(n, "(") && strings.HasSuffix(n, ")") {
		neg = true
		n = n[1 : len(n)-1]
	} else if strings.ContainsAny(n, "()") {
		return 0, "", "", 0, false
	}
	f, err := strconv.ParseFloat(n, 64)
	if err != nil {
		return 0, "", "", 0, false
	}
	if neg {
		f = -f
	}
	dec := 0
	if i := strings.IndexByte(n, '.'); i >= 0 {
		dec = len(n) - i - 1
	}
	suf := strings.TrimSpace(m[3])
	switch strings.ToLower(suf) {
	case "k":
		f *= 1e3
		suf = ""
	case "m":
		f *= 1e6
		suf = ""
	}
	// Leading zeros (zip codes, IDs) stay text.
	if len(n) > 1 && n[0] == '0' && n[1] != '.' {
		return 0, "", "", 0, false
	}
	return f, strings.TrimSpace(m[1]), suf, dec, true
}

func parseDate(s string) (string, bool) {
	if len(s) < 6 || len(s) > 40 {
		return "", false
	}
	for _, l := range dateLayouts {
		if t, err := time.Parse(l, s); err == nil {
			if t.Hour() == 0 && t.Minute() == 0 && t.Second() == 0 {
				return t.Format("2006-01-02"), true
			}
			return t.Format("2006-01-02 15:04"), true
		}
	}
	return "", false
}

func detectType(c tableCol, rows [][]string, i int) tableCol {
	var nonEmpty, nums, dates, bools int
	prefixes, suffixes := map[string]int{}, map[string]int{}
	for k, r := range rows {
		if k > 5000 {
			break
		}
		v := strings.TrimSpace(at(r, i))
		if v == "" {
			continue
		}
		nonEmpty++
		if _, p, s, d, ok := parseNum(v); ok {
			nums++
			prefixes[p]++
			suffixes[s]++
			if d > c.Dec {
				c.Dec = d
			}
		} else if _, ok := parseDate(v); ok {
			dates++
		} else {
			switch strings.ToLower(v) {
			case "true", "false", "yes", "no", "y", "n", "✓", "✗", "x":
				bools++
			}
		}
	}
	if nonEmpty == 0 {
		c.Type = "text"
		return c
	}
	thr := func(n int) bool { return float64(n) >= 0.9*float64(nonEmpty) }
	switch {
	case thr(nums):
		c.Type = "number"
		c.Prefix = top(prefixes)
		c.Suffix = top(suffixes)
		if c.Dec > 4 {
			c.Dec = 4
		}
	case thr(dates):
		c.Type = "date"
	case thr(bools) && nonEmpty > 1:
		c.Type = "bool"
	default:
		c.Type = "text"
	}
	return c
}

func top(m map[string]int) string {
	best, n := "", 0
	for k, v := range m {
		if v > n {
			best, n = k, v
		}
	}
	return best
}

func convertCell(c tableCol, v string) any {
	if v == "" {
		return nil
	}
	switch c.Type {
	case "number":
		if f, _, _, _, ok := parseNum(v); ok {
			return f
		}
	case "date":
		if d, ok := parseDate(v); ok {
			return d
		}
	case "bool":
		switch strings.ToLower(v) {
		case "true", "yes", "y", "✓", "x":
			return true
		case "false", "no", "n", "✗":
			return false
		}
	}
	return v
}
