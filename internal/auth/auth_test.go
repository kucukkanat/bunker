package auth_test

import (
	"strings"
	"testing"

	"github.com/kucukkanat/bunker/internal/auth"
	"github.com/kucukkanat/bunker/internal/testkit"
)

func TestTokensAndScopes(t *testing.T) {
	k := testkit.New(t)
	ada := k.As("ada@corp.com", "")
	res := ada.Must("POST", "/api/v1/tokens", map[string]any{"name": "ci", "scopes": []string{"read"}})
	tok := res["token"].(string)
	if !strings.HasPrefix(tok, "bk_") {
		t.Fatal(tok)
	}
	if res := ada.Do("POST", "/api/v1/tokens", map[string]any{"name": "x", "scopes": []string{"admin"}}); res.Status != 403 {
		t.Errorf("non-admin minted admin token: %d", res.Status)
	}
	ro := &testkit.Client{}
	*ro = *k.As("", "")
	ro.Token = tok
	me := ro.Must("GET", "/api/v1/me", nil)
	if me["email"] != "ada@corp.com" || me["viaToken"] != true {
		t.Fatalf("token me: %v", me)
	}
	if r := ro.Do("POST", "/api/v1/sites", map[string]any{"title": "x"}); r.Status != 403 {
		t.Errorf("read token could write: %d", r.Status)
	}
	// Bad token → anonymous (dev mode falls back to the dev user, so check identity).
	bad := *ro
	bad.Token = "bk_nope"
	if r := bad.Do("GET", "/api/v1/me", nil); strings.Contains(string(r.Body), "ada@corp.com") {
		t.Error("bad token authenticated as ada")
	}
	// Revoke
	list := ada.Must("GET", "/api/v1/tokens", nil)["tokens"].([]any)
	id := list[0].(map[string]any)["id"].(float64)
	ada.Must("DELETE", "/api/v1/tokens/"+strings.TrimSuffix(strings.TrimSuffix(ftoa(id), ".0"), ".000000"), nil)
	if r := ro.Do("GET", "/api/v1/me", nil); strings.Contains(string(r.Body), "ada@corp.com") {
		t.Error("revoked token still works")
	}
}

func ftoa(f float64) string {
	n := int(f)
	s := ""
	if n == 0 {
		return "0"
	}
	for n > 0 {
		s = string(rune('0'+n%10)) + s
		n /= 10
	}
	return s
}

func TestDeviceFlow(t *testing.T) {
	k := testkit.New(t)
	anon := k.As("", "")
	start := anon.Must("POST", "/api/v1/auth/device", map[string]any{"client": "bunker-cli"})
	code, dev := start["userCode"].(string), start["deviceCode"].(string)
	if r := anon.Do("POST", "/api/v1/auth/device/token", map[string]any{"deviceCode": dev}); !strings.Contains(string(r.Body), "authorization_pending") {
		t.Fatalf("expected pending: %s", r.Body)
	}
	ada := k.As("ada@corp.com", "")
	info := ada.Must("GET", "/api/v1/auth/device/"+code, nil)
	if info["client"] != "bunker-cli" {
		t.Fatal(info)
	}
	ada.Must("POST", "/api/v1/auth/device/"+code, map[string]any{"approve": true})
	tok := anon.Must("POST", "/api/v1/auth/device/token", map[string]any{"deviceCode": dev})["token"].(string)
	c := *anon
	c.Token = tok
	if me := c.Must("GET", "/api/v1/me", nil); me["email"] != "ada@corp.com" {
		t.Fatal(me)
	}
	// Single use
	if r := anon.Do("POST", "/api/v1/auth/device/token", map[string]any{"deviceCode": dev}); r.Status != 400 {
		t.Errorf("device code reusable: %d", r.Status)
	}
}

func TestProviderValidation(t *testing.T) {
	cases := []struct {
		p  auth.ProviderConfig
		ok bool
	}{
		{auth.ProviderConfig{Kind: "entra", TenantID: "common", ClientID: "a", ClientSecret: "b"}, false},
		{auth.ProviderConfig{Kind: "entra", TenantID: "8f1c0000-0000-0000-0000-000000000000", ClientID: "a", ClientSecret: "b"}, true},
		{auth.ProviderConfig{Kind: "google", ClientID: "a", ClientSecret: "b"}, false},
		{auth.ProviderConfig{Kind: "google", ClientID: "a", ClientSecret: "b", AllowedDomains: []string{"gmail.com"}}, false},
		{auth.ProviderConfig{Kind: "google", ClientID: "a", ClientSecret: "b", AllowedDomains: []string{"corp.com"}}, true},
		{auth.ProviderConfig{Kind: "oidc", Issuer: "http://idp", ClientID: "a", ClientSecret: "b"}, false},
	}
	for i, c := range cases {
		if err := c.p.Validate(); (err == nil) != c.ok {
			t.Errorf("case %d: err=%v want ok=%v", i, err, c.ok)
		}
	}
	if auth.SafeNext("//evil.com") != "/" || auth.SafeNext("/\\evil") != "/" || auth.SafeNext("/s/x/") != "/s/x/" || auth.SafeNext("https://evil") != "/" {
		t.Error("SafeNext allows open redirect")
	}
}
