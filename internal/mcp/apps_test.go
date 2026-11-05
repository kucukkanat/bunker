package mcp

import (
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"

	"github.com/kucukkanat/bunker/internal/core"
	"github.com/kucukkanat/bunker/internal/server"

	_ "github.com/kucukkanat/bunker/internal/data"
)

func appsServer(t *testing.T) (*Server, *httptest.Server) {
	t.Helper()
	t.Setenv("BUNKER_DEV", "1")
	cfg := &core.Config{Dev: true, DataDir: t.TempDir(), Listen: "127.0.0.1:0", TLS: "off"}
	a, err := server.New(cfg, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatal(err)
	}
	ts := httptest.NewServer(server.Handler(a))
	cfg.URL = ts.URL
	t.Cleanup(func() { ts.Close(); a.DB.Close() })
	// Dev mode: the first request creates the admin user.
	req, _ := http.NewRequest("POST", ts.URL+"/api/v1/drop?title=Expenses", strings.NewReader(`{"files":{"index.html":"<!doctype html><title>Expenses</title><h1>Expenses</h1>"}}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Bunker", "1")
	resp, err := http.DefaultClient.Do(req)
	if err != nil || resp.StatusCode >= 300 {
		b, _ := io.ReadAll(resp.Body)
		t.Fatalf("drop: %v %d %s", err, resp.StatusCode, b)
	}
	resp.Body.Close()
	u := &core.User{}
	if err := a.DB.QueryRow(`SELECT id,email FROM users ORDER BY id LIMIT 1`).Scan(&u.ID, &u.Email); err != nil {
		t.Fatal(err)
	}
	u.IsAdmin = true
	return &Server{Backend: &inproc{a: a, u: u, ip: "127.0.0.1", base: ts.URL}}, ts
}

func TestMCPAppsToolsList(t *testing.T) {
	s := &Server{Backend: &fakeBackend{ops: testOps}}
	m := rpc(t, s, `{"jsonrpc":"2.0","id":1,"method":"tools/list"}`)
	var open, api map[string]any
	for _, x := range m["result"].(map[string]any)["tools"].([]any) {
		tl := x.(map[string]any)
		switch tl["name"] {
		case "open_site":
			open = tl
		case "site_api":
			api = tl
		}
	}
	if open == nil || api == nil {
		t.Fatal("open_site and site_api must be listed")
	}
	ui := open["_meta"].(map[string]any)["ui"].(map[string]any)
	if ui["resourceUri"] != "ui://bunker/_open" {
		t.Fatalf("open_site _meta.ui: %v", ui)
	}
	a := open["annotations"].(map[string]any)
	if a["readOnlyHint"] != true || a["destructiveHint"] != false {
		t.Fatalf("open_site annotations: %v", a)
	}
	vis := api["_meta"].(map[string]any)["ui"].(map[string]any)["visibility"].([]any)
	if len(vis) != 1 || vis[0] != "app" {
		t.Fatalf("site_api must be app-only: %v", vis)
	}
	init := rpc(t, s, `{"jsonrpc":"2.0","id":2,"method":"initialize","params":{"protocolVersion":"2025-11-25","capabilities":{}}}`)
	ext := init["result"].(map[string]any)["capabilities"].(map[string]any)["extensions"].(map[string]any)
	if ext[UIExtension] == nil {
		t.Fatalf("missing MCP Apps extension capability: %v", ext)
	}
}

func TestMCPAppsOpenAndRead(t *testing.T) {
	s, ts := appsServer(t)
	m := rpc(t, s, `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"open_site","arguments":{"site":"expenses"}}}`)
	res := m["result"].(map[string]any)
	if res["isError"] == true {
		t.Fatalf("open_site: %v", res)
	}
	if res["_meta"].(map[string]any)["ui"].(map[string]any)["resourceUri"] != "ui://bunker/expenses" {
		t.Fatalf("result _meta: %v", res["_meta"])
	}
	sc := res["structuredContent"].(map[string]any)
	embed := sc["url"].(string)
	if !strings.HasPrefix(embed, ts.URL+"/s/expenses/~e/") {
		t.Fatalf("embed url: %s", embed)
	}

	// The embed URL serves the page to a frame without cookies and
	// without frame-ancestors (MCP hosts frame it from an opaque origin).
	req, _ := http.NewRequest("GET", embed, nil)
	req.Header.Set("Sec-Fetch-Dest", "iframe")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != 200 || !strings.Contains(string(body), "<h1>Expenses</h1>") || !strings.Contains(string(body), "/_/bunker.js") {
		t.Fatalf("embed page: %d %s", resp.StatusCode, body)
	}
	if csp := resp.Header.Get("Content-Security-Policy"); strings.Contains(csp, "frame-ancestors") || !strings.Contains(csp, "sandbox") {
		t.Fatalf("embed CSP: %s", csp)
	}
	// A normal content ticket is not an embed ticket, and vice versa.
	tk := regexp.MustCompile(`/~e/([^/]+)/`).FindStringSubmatch(embed)[1]
	for _, u := range []string{ts.URL + "/s/expenses/~r/" + tk + "/", strings.Replace(embed, "~e/", "~e/x", 1)} {
		resp, _ := http.Get(u)
		resp.Body.Close()
		if resp.StatusCode != 403 {
			t.Errorf("%s should be refused, got %d", u, resp.StatusCode)
		}
	}

	// resources/read returns the MCP Apps wrapper with CSP metadata.
	m = rpc(t, s, `{"jsonrpc":"2.0","id":2,"method":"resources/read","params":{"uri":"ui://bunker/expenses"}}`)
	c := m["result"].(map[string]any)["contents"].([]any)[0].(map[string]any)
	if c["mimeType"] != "text/html;profile=mcp-app" {
		t.Fatalf("mime: %v", c["mimeType"])
	}
	html := c["text"].(string)
	if !strings.Contains(html, "ui/initialize") || !strings.Contains(html, "/s/expenses/~e/") || !strings.Contains(html, "site_api") {
		t.Fatalf("wrapper html: %s", html)
	}
	csp := c["_meta"].(map[string]any)["ui"].(map[string]any)["csp"].(map[string]any)
	for _, k := range []string{"connectDomains", "resourceDomains", "frameDomains"} {
		l := csp[k].([]any)
		if len(l) == 0 || l[0] != ts.URL {
			t.Errorf("csp %s = %v", k, l)
		}
	}
	// The generic viewer works without a site.
	m = rpc(t, s, `{"jsonrpc":"2.0","id":3,"method":"resources/read","params":{"uri":"ui://bunker/_open"}}`)
	if m["error"] != nil {
		t.Fatalf("_open: %v", m)
	}
	// Unknown site → resource error.
	if m = rpc(t, s, `{"jsonrpc":"2.0","id":4,"method":"resources/read","params":{"uri":"ui://bunker/nope"}}`); m["error"] == nil {
		t.Fatal("unknown site should fail")
	}
	// resources/list offers the app.
	m = rpc(t, s, `{"jsonrpc":"2.0","id":5,"method":"resources/list"}`)
	b, _ := json.Marshal(m)
	if !strings.Contains(string(b), `"uri":"ui://bunker/expenses"`) {
		t.Fatalf("resources/list: %s", b)
	}
}

func TestMCPAppsSiteAPIPageSafe(t *testing.T) {
	s, _ := appsServer(t)
	call := func(args string) map[string]any {
		m := rpc(t, s, `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"site_api","arguments":`+args+`}}`)
		return m["result"].(map[string]any)
	}
	if r := call(`{"site":"expenses","path":"/me"}`); r["isError"] == true || r["structuredContent"].(map[string]any)["ok"] != true {
		t.Fatalf("page-safe /me: %v", r)
	}
	if r := call(`{"site":"expenses","method":"POST","path":"/db/orders","body":{"item":"pen"}}`); r["isError"] == true {
		t.Fatalf("db add: %v", r)
	}
	// sites.get (GET /api/v1/sites/{site}) is not page-safe.
	if r := call(`{"site":"expenses","path":"/"}`); r["isError"] != true || r["structuredContent"].(map[string]any)["status"].(float64) != 403 {
		t.Fatalf("non-page-safe op must be refused: %v", r)
	}
	for _, p := range []string{"/../../admin/users", "/%2e%2e/%2e%2e/admin/users"} {
		if r := call(`{"site":"expenses","path":"` + p + `"}`); r["isError"] != true {
			t.Fatalf("%s must be refused: %v", p, r)
		}
	}
}
