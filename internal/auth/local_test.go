package auth_test

import (
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/kucukkanat/bunker/internal/testkit"
)

func postForm(t *testing.T, k *testkit.Kit, path string, v url.Values, cookies []*http.Cookie) *http.Response {
	t.Helper()
	req, _ := http.NewRequest("POST", k.Srv.URL+path, strings.NewReader(v.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Origin", k.Srv.URL)
	for _, c := range cookies {
		req.AddCookie(c)
	}
	res, err := (&http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}).Do(req)
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	return res
}

func TestBuiltInAccounts(t *testing.T) {
	k := testkit.NewProd(t)
	anon := k.As("", "")
	st := anon.Must("GET", "/api/v1/setup", nil)
	if st["configured"] != false || st["codeNeeded"] != false {
		t.Fatalf("status: %v", st)
	}
	if r := anon.Do("GET", "/api/v1/me", nil); r.Status != 401 {
		t.Fatalf("me before setup: %d", r.Status)
	}
	if r := anon.Do("POST", "/api/v1/setup", map[string]any{"provider": "local", "email": "me@home.dev", "name": "Me", "password": "short"}); r.Status != 400 {
		t.Errorf("short password accepted: %d", r.Status)
	}
	anon.Must("POST", "/api/v1/setup", map[string]any{"provider": "local", "email": "me@home.dev", "name": "Me", "password": "correct horse battery"})
	if r := anon.Do("POST", "/api/v1/setup", map[string]any{"provider": "local", "email": "x@y.z", "password": "correct horse battery"}); r.Status != 403 {
		t.Errorf("setup re-run allowed: %d", r.Status)
	}
	// Wrong password → back to the login page, no session.
	res := postForm(t, k, "/auth/password", url.Values{"email": {"me@home.dev"}, "password": {"nope nope nope"}}, nil)
	if len(res.Cookies()) != 0 || res.StatusCode != 200 {
		t.Errorf("bad password: %d %v", res.StatusCode, res.Cookies())
	}
	res = postForm(t, k, "/auth/password", url.Values{"email": {"ME@home.dev"}, "password": {"correct horse battery"}, "next": {"/sites/x"}}, nil)
	if res.StatusCode != 303 || res.Header.Get("Location") != "/sites/x" || len(res.Cookies()) == 0 {
		t.Fatalf("login: %d %s", res.StatusCode, res.Header.Get("Location"))
	}
	me := k.WithCookies(res.Cookies())
	who := me.Must("GET", "/api/v1/me", nil)
	if who["email"] != "me@home.dev" || who["isAdmin"] != true {
		t.Fatalf("me: %v", who)
	}
	// Cross-site login posts are refused.
	req, _ := http.NewRequest("POST", k.Srv.URL+"/auth/password", strings.NewReader("email=me%40home.dev&password=correct+horse+battery"))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Origin", "https://evil.example")
	r2, _ := http.DefaultClient.Do(req)
	if r2.StatusCode != 403 {
		t.Errorf("cross-site login: %d", r2.StatusCode)
	}
	// Invite a friend.
	inv := me.Must("POST", "/api/v1/admin/invites", map[string]any{"email": "friend@home.dev", "groups": []string{"Family"}})
	link := inv["link"].(string)
	tok := link[strings.Index(link, "token=")+6:]
	res = postForm(t, k, "/auth/invite", url.Values{"token": {tok}, "name": {"Friend"}, "password": {"another long password"}}, nil)
	if res.StatusCode != 303 || len(res.Cookies()) == 0 {
		t.Fatalf("invite accept: %d", res.StatusCode)
	}
	friend := k.WithCookies(res.Cookies())
	fw := friend.Must("GET", "/api/v1/me", nil)
	if fw["isAdmin"] != false || !strings.Contains(string(friend.Do("GET", "/api/v1/me", nil).Body), "Family") {
		t.Errorf("friend: %v", fw)
	}
	// Invite links work once.
	if res := postForm(t, k, "/auth/invite", url.Values{"token": {tok}, "password": {"another long password"}}, nil); res.StatusCode != 403 {
		t.Errorf("invite reused: %d", res.StatusCode)
	}
	// Can't turn off built-in accounts without SSO.
	if r := me.Do("PUT", "/api/v1/admin/auth/local", map[string]any{"enabled": false}); r.Status != 400 {
		t.Errorf("lockout allowed: %d", r.Status)
	}
	// Change password.
	me.Must("POST", "/api/v1/me/password", map[string]any{"current": "correct horse battery", "new": "an even better passphrase"})
	if res := postForm(t, k, "/auth/password", url.Values{"email": {"me@home.dev"}, "password": {"an even better passphrase"}}, nil); res.StatusCode != 303 {
		t.Errorf("new password login: %d", res.StatusCode)
	}
	// Login page renders.
	lp := anon.Do("GET", "/auth/login", nil)
	if lp.Status != 200 || !strings.Contains(string(lp.Body), `action="/auth/password"`) {
		t.Errorf("login page: %d", lp.Status)
	}
}
