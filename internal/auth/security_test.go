package auth_test

import (
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/kucukkanat/bunker/internal/auth"
	"github.com/kucukkanat/bunker/internal/testkit"
)

// Browsers drop tabs and newlines inside URLs and treat "\" like "/", so
// "/\t/evil.example" in a Location header lands on //evil.example.
func TestSafeNextRejectsBrowserNormalizedRedirects(t *testing.T) {
	for _, bad := range []string{
		"/\t/evil.example", "/\n/evil.example", "/\r/evil.example", "/\t\\evil.example",
		"/x\\..\\\\evil.example", "/\x00/evil.example", "/\x7f/evil.example",
	} {
		if got := auth.SafeNext(bad); got != "/" {
			t.Errorf("SafeNext(%q) = %q, want /", bad, got)
		}
	}
	for _, ok := range []string{"/", "/sites/x", "/oauth/authorize?client_id=a&redirect_uri=https%3A%2F%2Fa.example%2Fcb", "/s/x/?a=1#frag"} {
		if got := auth.SafeNext(ok); got != ok {
			t.Errorf("SafeNext(%q) = %q", ok, got)
		}
	}
}

func TestLoginRedirectNotOpen(t *testing.T) {
	k := testkit.NewProd(t)
	anon := k.As("", "")
	anon.Must("POST", "/api/v1/setup", map[string]any{"provider": "local", "email": "me@home.dev", "name": "Me", "password": "correct horse battery"})
	res := postForm(t, k, "/auth/password", url.Values{"email": {"me@home.dev"}, "password": {"correct horse battery"}, "next": {"/\t/evil.example"}}, nil)
	loc := res.Header.Get("Location")
	if res.StatusCode != 303 || loc != "/" {
		t.Fatalf("login redirect: %d %q", res.StatusCode, loc)
	}
}

// Behind a reverse proxy on the same machine every request comes from
// 127.0.0.1. A proxy that doesn't add X-Forwarded-For (or only sets
// X-Real-IP / Forwarded) must not unlock setup without the code.
func TestSetupLoopbackNeedsLocalHost(t *testing.T) {
	cases := []struct {
		name string
		hdr  []string
	}{
		{"public host", []string{"Host", "bunker.example.com"}},
		{"x-real-ip", []string{"X-Real-Ip", "203.0.113.9"}},
		{"forwarded", []string{"Forwarded", "for=203.0.113.9;proto=https"}},
		{"x-forwarded-host", []string{"X-Forwarded-Host", "bunker.example.com"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			k := testkit.NewProd(t)
			anon := k.As("", "")
			if st := anon.Do("GET", "/api/v1/setup", nil, c.hdr...).JSON(); st["codeNeeded"] != true {
				t.Errorf("codeNeeded = %v", st["codeNeeded"])
			}
			r := anon.Do("POST", "/api/v1/setup", map[string]any{"provider": "local", "email": "evil@x.dev", "password": "correct horse battery"}, c.hdr...)
			if r.Status != 403 {
				t.Fatalf("setup without code through a proxy: %d %s", r.Status, r.Body)
			}
		})
	}
	// Directly on the machine it still works without the code.
	k := testkit.NewProd(t)
	k.As("", "").Must("POST", "/api/v1/setup", map[string]any{"provider": "local", "email": "me@home.dev", "password": "correct horse battery"})
}

// A login/invite form post from a look-alike origin whose name is a prefix
// of ours (https://bunker.example.co vs https://bunker.example.com) is cross-site.
func TestLoginOriginPrefixIsCrossSite(t *testing.T) {
	k := testkit.NewProd(t)
	anon := k.As("", "")
	anon.Must("POST", "/api/v1/setup", map[string]any{"provider": "local", "email": "me@home.dev", "password": "correct horse battery"})
	// The test server's URL is http://127.0.0.1:PORT; a prefix of it is another origin.
	short := k.Srv.URL[:len(k.Srv.URL)-1]
	req, _ := http.NewRequest("POST", k.Srv.URL+"/auth/password", strings.NewReader("email=me%40home.dev&password=correct+horse+battery"))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Origin", short)
	res, err := (&http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}).Do(req)
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	if res.StatusCode == 303 || len(res.Cookies()) > 0 {
		t.Fatalf("login accepted from %s: %d", short, res.StatusCode)
	}
}
