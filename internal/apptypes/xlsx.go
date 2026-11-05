package apptypes

import (
	"archive/zip"
	"bytes"
	"encoding/xml"
	"errors"
	"io"
	"math"
	"path"
	"strconv"
	"strings"
	"time"
)

// A small, dependency-free .xlsx reader: values only (shared strings,
// inline strings, numbers, booleans, dates by number format). Formulas
// use their cached values. Enough to turn a spreadsheet into an app.

type xlsxSheet struct {
	Name string
	Rows [][]string // cell text; dates as YYYY-MM-DD[ HH:MM]
}

const maxXLSXCells = 2_000_000

func readXLSX(data []byte) ([]xlsxSheet, error) {
	zr, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return nil, errors.New("this doesn't look like an Excel file")
	}
	byName := map[string]*zip.File{}
	for _, f := range zr.File {
		byName[f.Name] = f
	}
	read := func(name string) ([]byte, error) {
		f := byName[name]
		if f == nil {
			return nil, errors.New("missing " + name)
		}
		if f.UncompressedSize64 > 256<<20 {
			return nil, errors.New("spreadsheet is too large")
		}
		rc, err := f.Open()
		if err != nil {
			return nil, err
		}
		defer rc.Close()
		return io.ReadAll(io.LimitReader(rc, 256<<20))
	}
	wb, err := read("xl/workbook.xml")
	if err != nil {
		return nil, errors.New("this Excel file has no workbook")
	}
	var w struct {
		Pr struct {
			Date1904 string `xml:"date1904,attr"`
		} `xml:"workbookPr"`
		Sheets []struct {
			Name  string `xml:"name,attr"`
			RID   string `xml:"http://schemas.openxmlformats.org/officeDocument/2006/relationships id,attr"`
			State string `xml:"state,attr"`
		} `xml:"sheets>sheet"`
	}
	if err := xml.Unmarshal(wb, &w); err != nil {
		return nil, errors.New("couldn't read the workbook")
	}
	rels := map[string]string{}
	if rb, err := read("xl/_rels/workbook.xml.rels"); err == nil {
		var r struct {
			Rel []struct {
				ID     string `xml:"Id,attr"`
				Target string `xml:"Target,attr"`
			} `xml:"Relationship"`
		}
		xml.Unmarshal(rb, &r)
		for _, x := range r.Rel {
			t := x.Target
			if strings.HasPrefix(t, "/") {
				t = strings.TrimPrefix(t, "/")
			} else {
				t = path.Clean("xl/" + t)
			}
			rels[x.ID] = t
		}
	}
	var shared []string
	if sb, err := read("xl/sharedStrings.xml"); err == nil {
		shared = parseShared(sb)
	}
	dateStyles := map[int]bool{}
	if sb, err := read("xl/styles.xml"); err == nil {
		dateStyles = parseDateStyles(sb)
	}
	d1904 := w.Pr.Date1904 == "1" || w.Pr.Date1904 == "true"
	var out []xlsxSheet
	cells := 0
	for i, s := range w.Sheets {
		if s.State == "hidden" || s.State == "veryHidden" {
			continue
		}
		target := rels[s.RID]
		if target == "" {
			target = "xl/worksheets/sheet" + strconv.Itoa(i+1) + ".xml"
		}
		sb, err := read(target)
		if err != nil {
			continue
		}
		rows, n, err := parseSheet(sb, shared, dateStyles, d1904, maxXLSXCells-cells)
		if err != nil {
			return nil, err
		}
		cells += n
		out = append(out, xlsxSheet{Name: s.Name, Rows: rows})
	}
	if len(out) == 0 {
		return nil, errors.New("this Excel file has no visible sheets")
	}
	return out, nil
}

func parseShared(b []byte) []string {
	var out []string
	dec := xml.NewDecoder(bytes.NewReader(b))
	var cur strings.Builder
	inSI, inT, inRPh := false, false, false
	for {
		tok, err := dec.Token()
		if err != nil {
			break
		}
		switch t := tok.(type) {
		case xml.StartElement:
			switch t.Name.Local {
			case "si":
				inSI = true
				cur.Reset()
			case "t":
				inT = true
			case "rPh": // phonetic hints: skip
				inRPh = true
			}
		case xml.EndElement:
			switch t.Name.Local {
			case "si":
				inSI = false
				out = append(out, cur.String())
			case "t":
				inT = false
			case "rPh":
				inRPh = false
			}
		case xml.CharData:
			if inSI && inT && !inRPh {
				cur.Write(t)
			}
		}
	}
	return out
}

func parseDateStyles(b []byte) map[int]bool {
	var s struct {
		NumFmts []struct {
			ID   int    `xml:"numFmtId,attr"`
			Code string `xml:"formatCode,attr"`
		} `xml:"numFmts>numFmt"`
		Xfs []struct {
			NumFmtID int `xml:"numFmtId,attr"`
		} `xml:"cellXfs>xf"`
	}
	xml.Unmarshal(b, &s)
	custom := map[int]bool{}
	for _, f := range s.NumFmts {
		custom[f.ID] = isDateFormat(f.Code)
	}
	out := map[int]bool{}
	for i, x := range s.Xfs {
		id := x.NumFmtID
		if (id >= 14 && id <= 22) || (id >= 45 && id <= 47) || custom[id] {
			out[i] = true
		}
	}
	return out
}

func isDateFormat(code string) bool {
	// Strip quoted text and escapes, then look for date/time tokens.
	var b strings.Builder
	inQ := false
	for i := 0; i < len(code); i++ {
		c := code[i]
		switch {
		case c == '"':
			inQ = !inQ
		case inQ:
		case c == '\\' || c == '_' || c == '*':
			i++
		case c == '[':
			j := strings.IndexByte(code[i:], ']')
			if j < 0 {
				return false
			}
			i += j
		default:
			b.WriteByte(c)
		}
	}
	s := strings.ToLower(b.String())
	return strings.ContainsAny(s, "dy") || (strings.Contains(s, "m") && strings.ContainsAny(s, "hs:"))
}

func colIndex(ref string) int {
	n := 0
	for i := 0; i < len(ref); i++ {
		c := ref[i]
		if c >= 'A' && c <= 'Z' {
			n = n*26 + int(c-'A'+1)
		} else if c >= 'a' && c <= 'z' {
			n = n*26 + int(c-'a'+1)
		} else {
			break
		}
	}
	return n - 1
}

func parseSheet(b []byte, shared []string, dates map[int]bool, d1904 bool, budget int) ([][]string, int, error) {
	dec := xml.NewDecoder(bytes.NewReader(b))
	var rows [][]string
	var row []string
	var cellType, cellRef string
	var cellStyle int
	var val strings.Builder
	inV, inIS, inT := false, false, false
	cells := 0
	rowNum := 0
	for {
		tok, err := dec.Token()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, 0, errors.New("couldn't read a sheet in this Excel file")
		}
		switch t := tok.(type) {
		case xml.StartElement:
			switch t.Name.Local {
			case "row":
				row = nil
				r := rowNum + 1
				for _, a := range t.Attr {
					if a.Name.Local == "r" {
						if n, err := strconv.Atoi(a.Value); err == nil {
							r = n
						}
					}
				}
				// Keep blank rows in between so row positions match Excel.
				for rowNum+1 < r && len(rows) > 0 {
					rows = append(rows, nil)
					rowNum++
				}
				rowNum = r
			case "c":
				cellType, cellRef, cellStyle = "", "", 0
				for _, a := range t.Attr {
					switch a.Name.Local {
					case "t":
						cellType = a.Value
					case "r":
						cellRef = a.Value
					case "s":
						cellStyle, _ = strconv.Atoi(a.Value)
					}
				}
				val.Reset()
			case "v":
				inV = true
			case "is":
				inIS = true
			case "t":
				inT = true
			}
		case xml.EndElement:
			switch t.Name.Local {
			case "v":
				inV = false
			case "is":
				inIS = false
			case "t":
				inT = false
			case "c":
				cells++
				if cells > budget {
					return nil, 0, errors.New("this spreadsheet has too many cells to show (limit 2 million)")
				}
				v := cellValue(cellType, val.String(), cellStyle, shared, dates, d1904)
				idx := len(row)
				if cellRef != "" {
					idx = colIndex(cellRef)
				}
				if idx < 0 || idx > 16384 {
					continue
				}
				for len(row) < idx {
					row = append(row, "")
				}
				if idx == len(row) {
					row = append(row, v)
				} else {
					row[idx] = v
				}
			case "row":
				rows = append(rows, row)
			}
		case xml.CharData:
			if inV || (inIS && inT) {
				val.Write(t)
			}
		}
	}
	return rows, cells, nil
}

func cellValue(typ, raw string, style int, shared []string, dates map[int]bool, d1904 bool) string {
	switch typ {
	case "s":
		i, err := strconv.Atoi(strings.TrimSpace(raw))
		if err != nil || i < 0 || i >= len(shared) {
			return ""
		}
		return shared[i]
	case "b":
		if raw == "1" {
			return "TRUE"
		}
		return "FALSE"
	case "str", "inlineStr", "e":
		return raw
	}
	if raw == "" {
		return ""
	}
	f, err := strconv.ParseFloat(raw, 64)
	if err != nil {
		return raw
	}
	if dates[style] {
		return excelDate(f, d1904)
	}
	// Trim float noise like 0.30000000000000004.
	if f == math.Trunc(f) && math.Abs(f) < 1e15 {
		return strconv.FormatInt(int64(f), 10)
	}
	return strconv.FormatFloat(f, 'g', 15, 64)
}

func excelDate(f float64, d1904 bool) string {
	base := time.Date(1899, 12, 30, 0, 0, 0, 0, time.UTC)
	if d1904 {
		base = time.Date(1904, 1, 1, 0, 0, 0, 0, time.UTC)
	}
	days := math.Floor(f)
	secs := math.Round((f - days) * 86400)
	t := base.AddDate(0, 0, int(days)).Add(time.Duration(secs) * time.Second)
	if days == 0 && !d1904 { // time only
		return t.Format("15:04")
	}
	if secs == 0 {
		return t.Format("2006-01-02")
	}
	return t.Format("2006-01-02 15:04")
}
