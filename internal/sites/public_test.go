package sites_test

import (
	"io"
	"net/http"
	"net/url"
	"strings"
	"testing"

	_ "github.com/kucukkanat/bunker/internal/admin"
	"github.com/kucukkanat/bunker/internal/testkit"
)

func TestPublicLinks(t *testing.T) {
	k := testkit.NewProd(t)
	anon := k.As("", "")
	anon.Must("POST", "/api/v1/setup", map[string]any{"provider": "local", "email": "me@home.dev", "name": "Me", "password": "correct horse battery"})
	req, _ := http.NewRequest("POST", k.Srv.URL+"/auth/password", strings.NewReader(url.Values{"email": {"me@home.dev"}, "password": {"correct horse battery"}}.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Origin", k.Srv.URL)
	res, err := (&http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}).Do(req)
	if err != nil {
		t.Fatal(err)
	}
	me := k.WithCookies(res.Cookies())
	me.Must("POST", "/api/v1/drop", map[string]any{"title": "Portfolio", "files": map[string]string{"index.html": "<h1>Hi world</h1>"}})

	// Off by default.
	if r := me.Do("PATCH", "/api/v1/sites/portfolio", map[string]any{"public": true}); r.Status != 403 {
		t.Fatalf("public allowed without policy: %d", r.Status)
	}
	if r := anon.Do("GET", "/s/portfolio/", nil); r.Status != 302 {
		t.Fatalf("anonymous got %d before publishing", r.Status)
	}
	me.Must("PUT", "/api/v1/admin/policy", map[string]any{"publicLinks": true})
	me.Must("PATCH", "/api/v1/sites/portfolio", map[string]any{"public": true})

	shell := anon.Do("GET", "/s/portfolio/", nil)
	if shell.Status != 200 {
		t.Fatalf("public shell: %d", shell.Status)
	}
	body := string(shell.Body)
	i := strings.Index(body, `src="/s/portfolio/~r/`)
	src := body[i+5 : i+5+strings.Index(body[i+5:], `"`)]
	rr, _ := http.Get(k.Srv.URL + src)
	b, _ := io.ReadAll(rr.Body)
	if rr.StatusCode != 200 || !strings.Contains(string(b), "Hi world") {
		t.Fatalf("public content: %d %s", rr.StatusCode, b)
	}
	// Guests can't use the API, and the site isn't listed anywhere for them.
	if r := anon.Do("GET", "/api/v1/sites/portfolio", nil); r.Status != 401 {
		t.Errorf("guest API: %d", r.Status)
	}
	// Turning the policy off closes the link (also the guest ticket).
	me.Must("PUT", "/api/v1/admin/policy", map[string]any{"publicLinks": false})
	if r := anon.Do("GET", "/s/portfolio/", nil); r.Status != 302 {
		t.Errorf("still public after policy off: %d", r.Status)
	}
}
