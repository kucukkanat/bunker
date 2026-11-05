package sites_test

import (
	"archive/zip"
	"bytes"
	"strings"
	"testing"

	"github.com/kucukkanat/bunker/internal/sites"
	"github.com/kucukkanat/bunker/internal/testkit"
)

func drop(t *testing.T, c *testkit.Client, files map[string]string) map[string]any {
	t.Helper()
	return c.Must("POST", "/api/v1/drop", map[string]any{"files": files})
}

func TestDropServeAndIsolation(t *testing.T) {
	k := testkit.New(t)
	ada := k.As("ada@corp.com", "Finance")
	res := drop(t, ada, map[string]string{
		"my-site/index.html":     `<html><head><title>Budget Tool</title><link href="/assets/a.css" rel="stylesheet"></head><body><img src="/assets/x.png" srcset="/assets/x.png 1x, /assets/x2.png 2x"><a href="/">home</a></body></html>`,
		"my-site/assets/a.css":   `body{background:url(/assets/bg.png)}`,
		"my-site/.DS_Store":      "junk",
		"my-site/node_modules/x": "junk",
	})
	if res["slug"] != "budget-tool" {
		t.Fatalf("slug = %v", res["slug"])
	}
	// Shell page
	r := ada.Do("GET", "/s/budget-tool/", nil)
	if r.Status != 200 || !strings.Contains(string(r.Body), `sandbox="allow-scripts allow-forms allow-modals allow-downloads"`) {
		t.Fatalf("shell: %d %s", r.Status, r.Body)
	}
	body := string(r.Body)
	i := strings.Index(body, `src="/s/budget-tool/~r/`)
	src := body[i+5 : i+5+strings.Index(body[i+5:], `"`)]
	src = strings.ReplaceAll(src, "&#43;", "+")
	raw := ada.Do("GET", src, nil)
	if raw.Status != 200 {
		t.Fatalf("raw: %d %s", raw.Status, raw.Body)
	}
	csp := raw.Header.Get("Content-Security-Policy")
	for _, want := range []string{"sandbox allow-scripts", "connect-src 'self'", "form-action 'none'", "frame-ancestors 'self'"} {
		if !strings.Contains(csp, want) {
			t.Errorf("CSP missing %q: %s", want, csp)
		}
	}
	if raw.Header.Get("Connection-Allowlist") == "" || raw.Header.Get("Access-Control-Allow-Origin") != "*" {
		t.Errorf("missing isolation headers: %v", raw.Header)
	}
	html := string(raw.Body)
	if !strings.Contains(html, `<script src="/_/bunker.js"></script>`) {
		t.Error("SDK not injected")
	}
	prefix := strings.TrimSuffix(src, "")
	if !strings.Contains(html, `href="`+prefix+`assets/a.css"`) || !strings.Contains(html, `srcset="`+prefix+`assets/x.png 1x, `+prefix+`assets/x2.png 2x"`) {
		t.Errorf("root paths not rewritten: %s", html)
	}
	css := ada.Do("GET", src+"assets/a.css", nil)
	if !strings.Contains(string(css.Body), "url("+prefix+"assets/bg.png)") {
		t.Errorf("css not rewritten: %s", css.Body)
	}
	// Junk was skipped
	files := ada.Must("GET", "/api/v1/sites/budget-tool/files", nil)["files"].([]any)
	if len(files) != 2 {
		t.Errorf("files = %v", files)
	}
	// A tampered ticket is rejected
	bad := strings.Replace(src, "~r/", "~r/9", 1)
	if r := ada.Do("GET", bad, nil); r.Status != 403 {
		t.Errorf("tampered ticket: %d", r.Status)
	}
	// Someone else can't see it, and can't use Ada's ticket once access is gone.
	bob := k.As("bob@corp.com", "")
	if r := bob.Do("GET", "/api/v1/sites/budget-tool", nil); r.Status != 404 {
		t.Errorf("bob sees private site: %d", r.Status)
	}
	if r := bob.Do("GET", "/s/budget-tool/", nil); r.Status != 404 {
		t.Errorf("bob shell: %d", r.Status)
	}
	// Share with Finance group → bob (not in Finance) still out, carol in.
	ada.Must("POST", "/api/v1/sites/budget-tool/members", map[string]any{"type": "group", "id": "Finance", "role": "viewer"})
	carol := k.As("carol@corp.com", "Finance")
	if r := carol.Do("GET", "/api/v1/sites/budget-tool", nil); r.Status != 200 {
		t.Errorf("carol (Finance) can't view: %d", r.Status)
	}
	if r := carol.Do("POST", "/api/v1/sites/budget-tool/deploy", map[string]any{"files": map[string]string{"index.html": "x"}}); r.Status != 403 {
		t.Errorf("viewer could deploy: %d", r.Status)
	}
	if r := bob.Do("GET", "/api/v1/sites/budget-tool", nil); r.Status != 404 {
		t.Errorf("bob sees group site: %d", r.Status)
	}
	// Editor via user share
	ada.Must("POST", "/api/v1/sites/budget-tool/members", map[string]any{"type": "user", "id": "Bob@Corp.com", "role": "editor"})
	sites.Invalidate("budget-tool")
	bob.Must("POST", "/api/v1/sites/budget-tool/deploy", map[string]any{"files": map[string]string{"index.html": "<h1>v2</h1>"}, "message": "bob edit"})
	// Editors can't change who can view org-wide or delete.
	if r := bob.Do("PATCH", "/api/v1/sites/budget-tool", map[string]any{"orgView": true}); r.Status != 403 {
		t.Errorf("editor changed orgView: %d", r.Status)
	}
	if r := bob.Do("DELETE", "/api/v1/sites/budget-tool", nil); r.Status != 403 {
		t.Errorf("editor deleted: %d", r.Status)
	}
	vers := ada.Must("GET", "/api/v1/sites/budget-tool/versions", nil)
	if vers["live"].(float64) != 2 {
		t.Errorf("live = %v", vers["live"])
	}
	ada.Must("POST", "/api/v1/sites/budget-tool/versions/1/restore", nil)
	vers = ada.Must("GET", "/api/v1/sites/budget-tool/versions", nil)
	if vers["live"].(float64) != 3 {
		t.Errorf("after restore live = %v", vers["live"])
	}
}

func TestPageSafeAndCSRF(t *testing.T) {
	k := testkit.New(t)
	ada := k.As("ada@corp.com", "")
	drop(t, ada, map[string]string{"index.html": "<title>P</title>hi"})
	// Pages can't call deploy/sharing through the bridge.
	page := k.As("ada@corp.com", "")
	page.Page = true
	if r := page.Do("POST", "/api/v1/sites/p/members", map[string]any{"type": "user", "id": "x@y.z", "role": "editor"}); r.Status != 403 {
		t.Errorf("page could share: %d %s", r.Status, r.Body)
	}
	// Cookie requests without X-Bunker are refused.
	r := ada.Do("DELETE", "/api/v1/sites/p", nil, "X-Bunker", "")
	if r.Status != 403 {
		t.Errorf("CSRF header not enforced: %d", r.Status)
	}
}

func TestSecretsAndExecutablesBlocked(t *testing.T) {
	k := testkit.New(t)
	ada := k.As("ada@corp.com", "")
	r := ada.Do("POST", "/api/v1/drop", map[string]any{"files": map[string]string{"index.html": `<script>const k="sk-ant-` + strings.Repeat("a", 40) + `"</script>`}})
	if r.Status != 422 || !strings.Contains(string(r.Body), "index.html (line 1) looks like an Anthropic API key") {
		t.Errorf("secret not blocked: %d %s", r.Status, r.Body)
	}
	r = ada.Do("POST", "/api/v1/drop", map[string]any{"files": map[string]string{"index.html": "ok", "setup.exe": "MZ"}})
	if r.Status != 415 {
		t.Errorf("exe not blocked: %d", r.Status)
	}
	// Failed drops leave no empty site behind.
	list := ada.Must("GET", "/api/v1/sites?filter=mine", nil)["sites"].([]any)
	if len(list) != 0 {
		t.Errorf("leftover sites: %v", list)
	}
}

func TestDraftsTrashAndIncrementalDeploy(t *testing.T) {
	k := testkit.New(t)
	ada := k.As("ada@corp.com", "")
	ada.Must("POST", "/api/v1/sites", map[string]any{"title": "Lunch Orders"})
	ada.Do("PUT", "/api/v1/sites/lunch-orders/files/index.html", "<h1>draft</h1>")
	got := ada.Must("GET", "/api/v1/sites/lunch-orders/files", nil)
	if got["draft"] != true {
		t.Fatal("expected draft")
	}
	if r := ada.Do("GET", "/api/v1/sites/lunch-orders/files/index.html", nil); string(r.Body) != "<h1>draft</h1>" {
		t.Fatalf("read draft: %s", r.Body)
	}
	pub := ada.Must("POST", "/api/v1/sites/lunch-orders/draft/publish", map[string]any{"message": "first"})
	if pub["version"].(float64) != 1 {
		t.Fatal(pub)
	}
	// Incremental: hash already present → nothing missing.
	files := ada.Must("GET", "/api/v1/sites/lunch-orders/files", nil)["files"].([]any)
	h := files[0].(map[string]any)["hash"].(string)
	miss := ada.Must("POST", "/api/v1/blobs/missing", map[string]any{"hashes": []string{h, strings.Repeat("0", 64)}})
	if len(miss["missing"].([]any)) != 1 {
		t.Errorf("missing = %v", miss)
	}
	ada.Must("POST", "/api/v1/sites/lunch-orders/commit", map[string]any{"files": map[string]string{"index.html": h, "copy.html": h}})
	// Trash + restore keeps the address.
	ada.Must("DELETE", "/api/v1/sites/lunch-orders", nil)
	tr := ada.Must("GET", "/api/v1/sites?filter=trash", nil)["sites"].([]any)
	if len(tr) != 1 {
		t.Fatalf("trash = %v", tr)
	}
	id := int(tr[0].(map[string]any)["id"].(float64))
	res := ada.Must("POST", "/api/v1/trash/"+itoa(id)+"/restore", nil)
	if res["slug"] != "lunch-orders" {
		t.Errorf("restored slug = %v", res["slug"])
	}
}

func itoa(i int) string { return strings.TrimSpace(strings.Repeat(" ", 0) + fmtInt(i)) }

func fmtInt(i int) string {
	if i == 0 {
		return "0"
	}
	s := ""
	for i > 0 {
		s = string(rune('0'+i%10)) + s
		i /= 10
	}
	return s
}

func TestSlugify(t *testing.T) {
	for in, want := range map[string]string{"Lunch Orders!": "lunch-orders", "Çalışan İzinleri": "calisan-izinleri", "a": "", "Über Café": "uber-cafe"} {
		got := sites.Slugify(in)
		if want == "" {
			if len(got) < 3 {
				t.Errorf("%q → %q too short", in, got)
			}
			continue
		}
		if got != want {
			t.Errorf("%q → %q want %q", in, got, want)
		}
	}
}

func TestTicketRoundTrip(t *testing.T) {
	testkit.New(t)
	for i := int64(0); i < 2000; i++ {
		tk := sites.MakeTicket(i, i%7, i*3)
		got, ok := sites.ParseTicket(tk)
		if !ok || got.Site != i || got.Ver != i%7 || got.User != i*3 {
			t.Fatalf("ticket %q failed: %+v %v", tk, got, ok)
		}
	}
}

func TestZipBombRejected(t *testing.T) {
	t.Setenv("BUNKER_SITE_MAX_MB", "1")
	k := testkit.New(t)
	ada := k.As("ada@corp.com", "")
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for i := 0; i < 3; i++ {
		w, _ := zw.Create("f" + string(rune('a'+i)) + ".txt")
		w.Write(bytes.Repeat([]byte("A"), 600<<10))
	}
	zw.Close()
	r := ada.Do("POST", "/api/v1/drop?filename=x.zip", buf.Bytes(), "Content-Type", "application/zip")
	if r.Status != 413 {
		t.Fatalf("zip bomb: %d %s", r.Status, r.Body)
	}
}
