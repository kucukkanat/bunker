package server_test

import (
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/kucukkanat/bunker/internal/core"
	"github.com/kucukkanat/bunker/internal/server"

	_ "github.com/kucukkanat/bunker/internal/auth"
	_ "github.com/kucukkanat/bunker/internal/sites"
	_ "github.com/kucukkanat/bunker/internal/web"
)

// newUnconfigured starts a production-mode Bunker that doesn't know its
// public URL yet (a bare `docker run` without BUNKER_URL).
func newUnconfigured(t *testing.T) (*core.App, *httptest.Server) {
	t.Helper()
	cfg := &core.Config{DataDir: t.TempDir(), Listen: "127.0.0.1:0", TLS: "off", TrustProxy: true}
	a, err := server.New(cfg, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(server.Handler(a))
	t.Cleanup(func() { srv.Close(); a.DB.Close() })
	return a, srv
}

func get(t *testing.T, url string, hdr ...string) *http.Response {
	t.Helper()
	req, _ := http.NewRequest("GET", url, nil)
	for i := 0; i+1 < len(hdr); i += 2 {
		if hdr[i] == "Host" {
			req.Host = hdr[i+1]
			continue
		}
		req.Header.Set(hdr[i], hdr[i+1])
	}
	res, err := (&http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}).Do(req)
	if err != nil {
		t.Fatal(err)
	}
	io.Copy(io.Discard, res.Body)
	res.Body.Close()
	return res
}

// An anonymous internet request must not be able to set the public URL
// (it ends up in invite and reset links, the OIDC redirect URI, OAuth
// metadata and the Entra app registration).
func TestAnonymousRequestCantPoisonPublicURL(t *testing.T) {
	a, srv := newUnconfigured(t)
	get(t, srv.URL+"/", "Host", "evil.example")
	get(t, srv.URL+"/api/v1/setup", "Host", "evil.example", "X-Forwarded-Proto", "https")
	if a.Cfg.URL != "" || a.Setting("server.url") != "" {
		t.Fatalf("public URL learned from an anonymous request: %q / %q", a.Cfg.URL, a.Setting("server.url"))
	}
	// The first signed-in admin teaches it.
	req, _ := http.NewRequest("POST", srv.URL+"/api/v1/setup", strings.NewReader(`{"provider":"local","email":"me@home.dev","password":"correct horse battery"}`))
	req.Header.Set("Content-Type", "application/json")
	if res, err := http.DefaultClient.Do(req); err != nil || res.StatusCode != 200 {
		t.Fatalf("setup: %v %v", err, res)
	}
	req, _ = http.NewRequest("POST", srv.URL+"/auth/password", strings.NewReader("email=me%40home.dev&password=correct+horse+battery"))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	res, err := (&http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}).Do(req)
	if err != nil || len(res.Cookies()) == 0 {
		t.Fatalf("login: %v %v", err, res)
	}
	req, _ = http.NewRequest("GET", srv.URL+"/", nil)
	req.Host = "bunker.example.com"
	req.AddCookie(res.Cookies()[0])
	if res, err := http.DefaultClient.Do(req); err == nil {
		res.Body.Close()
	}
	if a.Cfg.URL != "http://bunker.example.com" {
		t.Fatalf("admin request didn't set the URL: %q", a.Cfg.URL)
	}
}

// With TrustProxy, only the address our proxy appended (the right-most
// X-Forwarded-For entry) identifies the client; the left part is whatever
// the client sent, so rotating it must not reset the per-IP rate limit.
func TestForwardedForCantDodgeRateLimit(t *testing.T) {
	_, srv := newUnconfigured(t)
	limited := false
	for i := 0; i < 60; i++ {
		req, _ := http.NewRequest("POST", srv.URL+"/api/v1/auth/device", strings.NewReader(`{}`))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("X-Forwarded-For", "10.9.8."+itoa(i)+", 198.51.100.7")
		res, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		io.Copy(io.Discard, res.Body)
		res.Body.Close()
		if res.StatusCode == 429 {
			limited = true
			break
		}
	}
	if !limited {
		t.Fatal("60 unauthenticated POSTs with rotating X-Forwarded-For were never rate limited")
	}
}

func itoa(i int) string {
	if i == 0 {
		return "0"
	}
	s := ""
	for ; i > 0; i /= 10 {
		s = string(rune('0'+i%10)) + s
	}
	return s
}

// The first-run setup (authorized by the setup code) records the URL, so
// company sign-in works without BUNKER_URL.
func TestSetupRemembersURL(t *testing.T) {
	a, srv := newUnconfigured(t)
	code := a.Setting("setup.code")
	req, _ := http.NewRequest("POST", srv.URL+"/api/v1/setup", strings.NewReader(`{"provider":"local","email":"me@home.dev","password":"correct horse battery","code":"`+code+`"}`))
	req.Host = "bunker.example.com"
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Forwarded-For", "203.0.113.5")
	req.Header.Set("X-Forwarded-Proto", "https")
	res, err := http.DefaultClient.Do(req)
	if err != nil || res.StatusCode != 200 {
		t.Fatalf("setup: %v %v", err, res)
	}
	if a.Cfg.URL != "https://bunker.example.com" {
		t.Fatalf("URL after setup: %q", a.Cfg.URL)
	}
}
