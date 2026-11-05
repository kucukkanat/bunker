package connections

import (
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/kucukkanat/bunker/internal/testkit"
)

func tlsServer(t *testing.T, h http.HandlerFunc) *httptest.Server {
	srv := httptest.NewTLSServer(h)
	t.Cleanup(srv.Close)
	return srv
}

func trust(t *testing.T, srvs ...*httptest.Server) {
	pool := x509.NewCertPool()
	for _, s := range srvs {
		pool.AddCert(s.Certificate())
	}
	old := TLSConfig
	TLSConfig = &tls.Config{RootCAs: pool}
	t.Cleanup(func() { TLSConfig = old })
	clientMu.Lock()
	clients = map[string]*http.Client{}
	clientMu.Unlock()
}

func TestConnections(t *testing.T) {
	var gotKey, gotAuth, gotQuery, gotBody, gotPath string
	other := tlsServer(t, func(w http.ResponseWriter, r *http.Request) {
		t.Error("redirect to another host must not be followed")
	})
	api := tlsServer(t, func(w http.ResponseWriter, r *http.Request) {
		gotKey, gotAuth, gotQuery, gotPath = r.Header.Get("X-Api-Key"), r.Header.Get("Authorization"), r.URL.RawQuery, r.URL.Path
		b := make([]byte, 1024)
		n, _ := r.Body.Read(b)
		gotBody = string(b[:n])
		switch r.URL.Path {
		case "/v1/forecast":
			w.Header().Set("Set-Cookie", "session=leak")
			w.Header().Set("X-Internal-Secret", "nope")
			w.Header().Set("Content-Type", "application/json")
			w.Write([]byte(`{"temp":21}`))
		case "/v1/big":
			w.Header().Set("Content-Type", "text/plain")
			w.Write([]byte(strings.Repeat("x", MaxResponse+10)))
		case "/v1/bin":
			w.Header().Set("Content-Type", "image/png")
			w.Write([]byte{0x89, 'P', 'N', 'G', 0xff})
		case "/v1/away":
			http.Redirect(w, r, other.URL+"/steal", http.StatusFound)
		case "/v1/here":
			http.Redirect(w, r, "/v1/forecast", http.StatusFound)
		default:
			w.WriteHeader(404)
		}
	})
	trust(t, api, other)
	k := testkit.New(t)
	admin := k.As("", "")
	site := admin.Must("POST", "/api/v1/sites", map[string]any{"title": "Weather board"})
	slug := site["slug"].(string)
	page := k.As("", "")
	page.Page = true
	fetch := func(name string, body map[string]any) testkit.Res {
		return page.Do("POST", "/api/v1/sites/"+slug+"/connections/"+name+"/fetch", body)
	}

	// Validation.
	if r := admin.Do("POST", "/api/v1/admin/connections", map[string]any{"name": "w", "baseUrl": "http://api.example.com"}); r.Status != 400 {
		t.Fatalf("http base must be rejected: %d", r.Status)
	}
	// Pages can't manage connections.
	if r := page.Do("POST", "/api/v1/admin/connections", map[string]any{"name": "w", "baseUrl": api.URL}); r.Status != 403 {
		t.Fatalf("page admin call: %d", r.Status)
	}
	admin.Must("POST", "/api/v1/admin/connections", map[string]any{
		"name": "weather", "baseUrl": api.URL + "/v1", "headers": map[string]string{"X-Api-Key": "s3cret", "Authorization": "Bearer top"},
		"allowedMethods": []string{"GET", "POST"}, "allowedPathPrefixes": []string{"/forecast", "/big", "/bin", "/away", "/here"}, "sites": []string{slug}, "rateLimitPerMinute": 9,
	})
	// Secrets never come back.
	got := admin.Must("GET", "/api/v1/admin/connections/weather", nil)
	if strings.Contains(string(mustJSON(got)), "s3cret") {
		t.Fatal("secret leaked in admin read")
	}
	// SSRF: 127.0.0.1 is blocked unless allowPrivate.
	r := fetch("weather", map[string]any{"path": "/forecast"})
	if r.Status != 403 || !strings.Contains(string(r.Body), "private network") {
		t.Fatalf("loopback must be blocked: %d %s", r.Status, r.Body)
	}
	admin.Must("PUT", "/api/v1/admin/connections/weather", map[string]any{"allowPrivate": true, "headers": map[string]any{"X-Api-Key": "••••••"}})
	r = fetch("weather", map[string]any{"path": "/forecast", "query": map[string]any{"city": "Oslo", "days": []any{1, 2}}, "headers": map[string]string{"Accept": "application/json"}})
	if r.Status != 200 {
		t.Fatalf("fetch: %d %s", r.Status, r.Body)
	}
	m := r.JSON()
	if m["status"].(float64) != 200 || m["json"].(map[string]any)["temp"].(float64) != 21 {
		t.Fatalf("response: %s", r.Body)
	}
	if gotKey != "s3cret" || gotAuth != "Bearer top" || gotPath != "/v1/forecast" || !strings.Contains(gotQuery, "city=Oslo") || !strings.Contains(gotQuery, "days=2") {
		t.Fatalf("upstream saw key=%q auth=%q path=%q query=%q", gotKey, gotAuth, gotPath, gotQuery)
	}
	if strings.Contains(string(r.Body), "leak") || strings.Contains(string(r.Body), "X-Internal") || strings.Contains(strings.ToLower(string(r.Body)), "set-cookie") {
		t.Fatalf("unsafe headers passed through: %s", r.Body)
	}
	// POST with a JSON body.
	fetch("weather", map[string]any{"method": "POST", "path": "/forecast", "body": map[string]any{"a": 1}})
	if gotBody != `{"a":1}` {
		t.Errorf("body = %q", gotBody)
	}
	// Disallowed method, path, header.
	if r := fetch("weather", map[string]any{"method": "DELETE", "path": "/forecast"}); r.Status != 403 {
		t.Errorf("DELETE: %d", r.Status)
	}
	for _, p := range []string{"/admin", "/forecast/../admin", "/forecast/%2e%2e/admin", "//evil.com/x", "https://evil.com", "/forecast?x=1"} {
		if r := fetch("weather", map[string]any{"path": p}); r.Status != 403 && r.Status != 400 {
			t.Errorf("path %q: %d", p, r.Status)
		}
	}
	if r := fetch("weather", map[string]any{"path": "/forecast", "headers": map[string]string{"Authorization": "Bearer mine"}}); r.Status != 400 {
		t.Errorf("auth header from page: %d", r.Status)
	}
	// Size cap, binary bodies, redirects.
	if r := fetch("weather", map[string]any{"path": "/big"}); r.Status != 413 {
		t.Errorf("big: %d", r.Status)
	}
	if r := fetch("weather", map[string]any{"path": "/bin"}); r.JSON()["encoding"] != "base64" {
		t.Errorf("binary: %s", r.Body)
	}
	if r := fetch("weather", map[string]any{"path": "/away"}); r.Status != 502 || !strings.Contains(string(r.Body), "redirect") {
		t.Errorf("cross-host redirect: %d %s", r.Status, r.Body)
	}
	if r := fetch("weather", map[string]any{"path": "/here"}); r.Status != 200 {
		t.Errorf("same-host redirect: %d %s", r.Status, r.Body)
	}
	// Rate limit (9/min; about 8 used so far).
	limited := false
	for i := 0; i < 3 && !limited; i++ {
		limited = fetch("weather", map[string]any{"path": "/forecast"}).Status == 429
	}
	if !limited {
		t.Error("rate limit never kicked in")
	}
	// Audit trail.
	var n int
	k.App.DB.QueryRow(`SELECT COUNT(*) FROM audit WHERE event='connection.call'`).Scan(&n)
	if n < 8 {
		t.Errorf("audit rows = %d", n)
	}
	// Site allowlist.
	other2 := admin.Must("POST", "/api/v1/sites", map[string]any{"title": "Other"})
	if r := page.Do("POST", "/api/v1/sites/"+other2["slug"].(string)+"/connections/weather/fetch", map[string]any{"path": "/forecast"}); r.Status != 403 {
		t.Errorf("site allowlist: %d", r.Status)
	}
	lst := page.Must("GET", "/api/v1/sites/"+slug+"/connections", nil)
	if len(lst["connections"].([]any)) != 1 {
		t.Errorf("site connection list: %v", lst)
	}
	// DNS names resolving to private IPs are blocked too.
	admin.Must("POST", "/api/v1/admin/connections", map[string]any{"name": "local", "baseUrl": strings.Replace(api.URL, "127.0.0.1", "localhost", 1), "sites": []string{"*"}})
	if r := fetch("local", map[string]any{"path": "/v1/forecast"}); r.Status != 403 {
		t.Errorf("localhost: %d %s", r.Status, r.Body)
	}
	admin.Must("DELETE", "/api/v1/admin/connections/local", nil)
}

func mustJSON(v any) []byte { b, _ := json.Marshal(v); return b }

func TestBlockedIPs(t *testing.T) {
	for _, s := range []string{"127.0.0.1", "10.1.2.3", "172.16.0.1", "192.168.1.1", "169.254.169.254", "100.64.0.1", "0.0.0.0", "::1", "fe80::1", "fd00::1", "::ffff:127.0.0.1", "::ffff:169.254.169.254", "224.0.0.1"} {
		if !blockedIP(net.ParseIP(s)) {
			t.Errorf("%s should be blocked", s)
		}
	}
	for _, s := range []string{"8.8.8.8", "1.1.1.1", "2606:4700:4700::1111", "20.190.128.1"} {
		if blockedIP(net.ParseIP(s)) {
			t.Errorf("%s should be allowed", s)
		}
	}
}
