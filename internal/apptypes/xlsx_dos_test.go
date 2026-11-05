package apptypes

import (
	"archive/zip"
	"bytes"
	"runtime"
	"strings"
	"testing"
	"time"
)

func xlsxWithShared(mb int) []byte {
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	w, _ := zw.Create("xl/workbook.xml")
	w.Write([]byte(`<workbook><sheets><sheet name="a" r:id="r1" xmlns:r="http://schemas.openxmlformats.org/officeDocument/2006/relationships"/></sheets></workbook>`))
	w, _ = zw.Create("xl/sharedStrings.xml")
	w.Write([]byte("<sst>"))
	chunk := []byte(strings.Repeat("<si><t>a</t></si>", 10000))
	for i := 0; i < mb<<20/len(chunk); i++ {
		w.Write(chunk)
	}
	w.Write([]byte("</sst>"))
	w, _ = zw.Create("xl/worksheets/sheet1.xml")
	w.Write([]byte(`<worksheet><sheetData><row><c t="s"><v>0</v></c></row></sheetData></worksheet>`))
	zw.Close()
	return buf.Bytes()
}

// A 600 KB .xlsx that unpacks to 250 MB of shared strings took 20 s and
// 1.6 GB of memory per upload.
func TestXLSXExpansionBounded(t *testing.T) {
	for _, mb := range []int{250, 60} {
		data := xlsxWithShared(mb)
		var m0, m1 runtime.MemStats
		runtime.GC()
		runtime.ReadMemStats(&m0)
		st := time.Now()
		readXLSX(data)
		runtime.ReadMemStats(&m1)
		d, alloc := time.Since(st), (m1.TotalAlloc-m0.TotalAlloc)>>20
		t.Logf("%d MB: %v, %d MB allocated", mb, d, alloc)
		if d > 8*time.Second*raceSlowdown || alloc > 2500 {
			t.Errorf("%d MB of shared strings: %v, %d MB allocated", mb, d, alloc)
		}
	}
}
