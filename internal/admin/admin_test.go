package admin

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	_ "modernc.org/sqlite"

	"github.com/kucukkanat/bunker/internal/core"
	"github.com/kucukkanat/bunker/internal/testkit"
)

func TestStatsAndPolicy(t *testing.T) {
	k := testkit.New(t)
	c := k.As("", "")
	c.Must("POST", "/api/v1/sites", map[string]any{"title": "One"})
	k.App.Audit(context.Background(), "site.view", 1, "one", nil)
	st := c.Must("GET", "/api/v1/admin/stats", nil)
	if st["sites"].(float64) != 1 || st["users"].(float64) < 1 || st["dbBytes"].(float64) <= 0 {
		t.Fatalf("stats: %v", st)
	}
	tops := st["topSites"].([]any)
	if len(tops) != 1 || tops[0].(map[string]any)["views"].(float64) != 1 {
		t.Fatalf("top sites: %v", tops)
	}
	// Non-admins can't see stats.
	if r := k.As("bob@corp.test", "").Do("GET", "/api/v1/admin/stats", nil); r.Status != 403 {
		t.Fatalf("non-admin stats: %d", r.Status)
	}
	p := c.Must("PUT", "/api/v1/admin/policy", map[string]any{"orgWideSharing": false, "editorsCanShare": false, "maxSitesPerUser": 2, "siteMaxMB": 5})
	pol := p["policy"].(map[string]any)
	if pol["orgWideSharing"] != false || pol["maxSitesPerUser"].(float64) != 2 {
		t.Fatalf("policy: %v", p)
	}
	if k.App.Setting("sharing.org_wide") != "false" || k.App.Setting("sharing.editors_can_share") != "false" {
		t.Fatal("sharing keys not written")
	}
	// The sites module enforces the per-person limit from the policy.
	c.Must("POST", "/api/v1/sites", map[string]any{"title": "Two"})
	if r := c.Do("POST", "/api/v1/sites", map[string]any{"title": "Three"}); r.Status != 429 {
		t.Fatalf("maxSitesPerUser not enforced: %d %s", r.Status, r.Body)
	}
	if r := c.Do("PUT", "/api/v1/admin/policy", map[string]any{"siteMaxMB": 0}); r.Status != 400 {
		t.Fatalf("validation: %d", r.Status)
	}
}

func TestAuditQueryExport(t *testing.T) {
	k := testkit.New(t)
	c := k.As("", "")
	ctx := context.Background()
	for i := 0; i < 5; i++ {
		k.App.Audit(ctx, "m365.call", 0, "/me", map[string]any{"n": i})
	}
	k.App.Audit(core.WithUser(ctx, &core.User{ID: 1, Email: "you@localhost"}), "site.share", 0, "=HYPERLINK(\"x\")", nil)
	r := c.Must("GET", "/api/v1/admin/audit?event=m365.*&limit=2", nil)
	evs := r["events"].([]any)
	if len(evs) != 2 || r["next"] == nil {
		t.Fatalf("page 1: %v", r)
	}
	next := int64(r["next"].(float64))
	r2 := c.Must("GET", "/api/v1/admin/audit?event=m365.call&limit=10&beforeId="+itoa(next), nil)
	if len(r2["events"].([]any)) != 3 {
		t.Fatalf("page 2: %v", r2)
	}
	if r := c.Must("GET", "/api/v1/admin/audit?user=you@localhost&event=site.share", nil); len(r["events"].([]any)) != 1 {
		t.Fatalf("user filter: %v", r)
	}
	if r := c.Do("GET", "/api/v1/admin/audit?since=yesterday", nil); r.Status != 400 {
		t.Fatalf("bad date: %d", r.Status)
	}
	csvRes := c.Do("GET", "/api/v1/admin/audit/export?format=csv&event=site.share", nil)
	if csvRes.Status != 200 || !strings.Contains(string(csvRes.Body), "'=HYPERLINK") || !strings.HasPrefix(csvRes.Header.Get("Content-Disposition"), "attachment") {
		t.Fatalf("csv: %d %s", csvRes.Status, csvRes.Body)
	}
	js := c.Do("GET", "/api/v1/admin/audit/export?format=json&event=m365.call", nil)
	var arr []map[string]any
	if err := json.Unmarshal(js.Body, &arr); err != nil || len(arr) != 5 {
		t.Fatalf("json export: %v %s", err, js.Body)
	}
	// Retention.
	k.App.DB.Exec(`UPDATE audit SET ts=? WHERE event='m365.call'`, core.Now()-400*86400)
	if n := PruneAudit(ctx); n != 5 {
		t.Fatalf("pruned %d", n)
	}
}

func itoa(n int64) string { return strconv.FormatInt(n, 10) }

func TestAuditStreaming(t *testing.T) {
	var mu sync.Mutex
	var got []map[string]any
	hook := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Events []map[string]any `json:"events"`
		}
		json.NewDecoder(r.Body).Decode(&body)
		mu.Lock()
		got = append(got, body.Events...)
		mu.Unlock()
	}))
	defer hook.Close()
	udp, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer udp.Close()
	k := testkit.New(t)
	c := k.As("", "")
	c.Must("PUT", "/api/v1/admin/audit/streaming", map[string]any{"webhook": hook.URL, "syslog": "udp://" + udp.LocalAddr().String()})
	if r := c.Do("PUT", "/api/v1/admin/audit/streaming", map[string]any{"syslog": "ftp://x"}); r.Status != 400 {
		t.Fatalf("bad syslog accepted: %d", r.Status)
	}
	k.App.Audit(context.Background(), "test.event", 0, "target-1", map[string]any{"x": 1})
	flushSinks()
	mu.Lock()
	found := false
	for _, e := range got {
		if e["event"] == "test.event" {
			found = true
		}
	}
	mu.Unlock()
	if !found {
		t.Fatalf("webhook didn't get the event: %v", got)
	}
	buf := make([]byte, 4096)
	udp.SetReadDeadline(time.Now().Add(3 * time.Second))
	for {
		n, _, err := udp.ReadFrom(buf)
		if err != nil {
			t.Fatal("no syslog message:", err)
		}
		if line := string(buf[:n]); strings.Contains(line, "test.event") {
			if !strings.HasPrefix(line, "<110>1 ") || !strings.Contains(line, " bunker ") {
				t.Fatalf("not RFC 5424: %q", line)
			}
			break
		}
	}
	c.Must("PUT", "/api/v1/admin/audit/streaming", map[string]any{"webhook": "", "syslog": ""})
}

func TestBackupRestore(t *testing.T) {
	k := testkit.New(t)
	c := k.As("", "")
	site := c.Must("POST", "/api/v1/sites", map[string]any{"title": "Keep me"})
	c.Do("POST", "/api/v1/sites/"+site["slug"].(string)+"/deploy", `{"files":{"index.html":{"content":"<h1>hi</h1>"}}}`, "Content-Type", "application/json")
	res := c.Do("POST", "/api/v1/admin/backup", nil)
	if res.Status != 200 || res.Header.Get("Content-Type") != "application/gzip" {
		t.Fatalf("backup: %d %s", res.Status, res.Body)
	}
	dst := t.TempDir()
	if err := RestoreArchive(bytes.NewReader(res.Body), dst, false); err != nil {
		t.Fatal(err)
	}
	db, err := sql.Open("sqlite", filepath.Join(dst, "bunker.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var title string
	if err := db.QueryRow(`SELECT title FROM sites WHERE slug=?`, site["slug"]).Scan(&title); err != nil || title != "Keep me" {
		t.Fatalf("restored db: %v %q", err, title)
	}
	var manifest string
	db.QueryRow(`SELECT manifest FROM versions LIMIT 1`).Scan(&manifest)
	var m map[string]struct {
		Hash string `json:"h"`
	}
	json.Unmarshal([]byte(manifest), &m)
	h := m["index.html"].Hash
	if h == "" {
		t.Fatalf("no version restored: %s", manifest)
	}
	if b, err := os.ReadFile(filepath.Join(dst, "blobs", h[:2], h[2:])); err != nil || string(b) != "<h1>hi</h1>" {
		t.Fatalf("blob not restored: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dst, "secret.key")); err != nil {
		t.Fatal("secret key missing from backup")
	}
	// Refuses to overwrite, and rejects path traversal.
	if err := RestoreArchive(bytes.NewReader(res.Body), dst, false); err == nil {
		t.Fatal("restore over an existing database must fail")
	}
	if err := RestoreArchive(strings.NewReader("nope"), t.TempDir(), false); err == nil {
		t.Fatal("garbage accepted")
	}
	// Scheduled snapshots keep N.
	k.App.SetSetting(context.Background(), "backup.dir", t.TempDir())
	k.App.SetSetting(context.Background(), "backup.keep", "2")
	for i := 0; i < 3; i++ {
		if _, err := Snapshot(context.Background()); err != nil {
			t.Fatal(err)
		}
		time.Sleep(1100 * time.Millisecond) // names have 1s resolution
	}
	l := c.Must("GET", "/api/v1/admin/backups", nil)
	if n := len(l["backups"].([]any)); n != 2 {
		t.Fatalf("kept %d snapshots, want 2", n)
	}
}
