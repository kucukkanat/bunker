package mcp

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"regexp"
	"strings"
	"testing"

	"github.com/kucukkanat/bunker/internal/core"
	"github.com/kucukkanat/bunker/internal/server"

	_ "github.com/kucukkanat/bunker/internal/auth"
	_ "github.com/kucukkanat/bunker/internal/sites"
	_ "github.com/kucukkanat/bunker/internal/web"
)

func newServer(t *testing.T) *httptest.Server {
	t.Helper()
	t.Setenv("BUNKER_DEV", "1")
	cfg := &core.Config{Dev: true, DataDir: t.TempDir(), Listen: "127.0.0.1:0", TLS: "off"}
	a, err := server.New(cfg, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatal(err)
	}
	ts := httptest.NewServer(server.Handler(a))
	t.Cleanup(func() { ts.Close(); a.DB.Close() })
	return ts
}

var noRedirect = &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}

func postJSON(t *testing.T, u string, hdr map[string]string, body string) (*http.Response, map[string]any) {
	t.Helper()
	req, _ := http.NewRequest("POST", u, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	for k, v := range hdr {
		req.Header.Set(k, v)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var m map[string]any
	json.NewDecoder(resp.Body).Decode(&m)
	return resp, m
}

func TestOAuthFlowAndRemoteMCP(t *testing.T) {
	ts := newServer(t)
	base := ts.URL

	// Discovery.
	resp, err := http.Get(base + "/.well-known/oauth-protected-resource/mcp")
	if err != nil || resp.StatusCode != 200 {
		t.Fatalf("PRM: %v %v", err, resp)
	}
	var prm map[string]any
	json.NewDecoder(resp.Body).Decode(&prm)
	resp.Body.Close()
	if prm["resource"] != base+"/mcp" {
		t.Fatalf("PRM resource: %v", prm)
	}
	resp, _ = http.Get(base + "/.well-known/oauth-authorization-server")
	var asm map[string]any
	json.NewDecoder(resp.Body).Decode(&asm)
	resp.Body.Close()
	if asm["issuer"] != base || asm["registration_endpoint"] != base+"/oauth/register" {
		t.Fatalf("AS metadata: %v", asm)
	}

	// /mcp without a token → 401 with discovery hint.
	resp, _ = postJSON(t, base+"/mcp", nil, `{"jsonrpc":"2.0","id":1,"method":"ping"}`)
	if resp.StatusCode != 401 || !strings.Contains(resp.Header.Get("WWW-Authenticate"), `resource_metadata="`+base+`/.well-known/oauth-protected-resource`) {
		t.Fatalf("401 challenge: %d %q", resp.StatusCode, resp.Header.Get("WWW-Authenticate"))
	}

	// Dynamic client registration.
	resp, reg := postJSON(t, base+"/oauth/register", nil, `{"client_name":"Test Agent","redirect_uris":["http://127.0.0.1:9999/callback"]}`)
	if resp.StatusCode != 201 {
		t.Fatalf("register: %d %v", resp.StatusCode, reg)
	}
	clientID := reg["client_id"].(string)
	if r, _ := postJSON(t, base+"/oauth/register", nil, `{"client_name":"Bad","redirect_uris":["http://evil.example/cb"]}`); r.StatusCode != 400 {
		t.Fatal("non-loopback http redirect must be rejected")
	}

	// Authorize (dev mode signs the browser in as the admin).
	verifier := strings.Repeat("v", 20) + core.RandomToken(32)
	sum := sha256.Sum256([]byte(verifier))
	challenge := base64.RawURLEncoding.EncodeToString(sum[:])
	q := url.Values{"response_type": {"code"}, "client_id": {clientID}, "redirect_uri": {"http://127.0.0.1:9999/callback"},
		"code_challenge": {challenge}, "code_challenge_method": {"S256"}, "state": {"xyz"}, "scope": {"bunker"}, "resource": {base + "/mcp"}}
	resp, err = noRedirect.Get(base + "/oauth/authorize?" + q.Encode())
	if err != nil || resp.StatusCode != 200 {
		t.Fatalf("consent page: %v %d", err, resp.StatusCode)
	}
	page, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if !strings.Contains(string(page), "Test Agent") || !strings.Contains(resp.Header.Get("Content-Security-Policy"), "frame-ancestors 'none'") || strings.Contains(string(page), "<script") {
		t.Fatalf("consent page content/CSP")
	}
	csrf := regexp.MustCompile(`name="csrf" value="([^"]+)"`).FindStringSubmatch(string(page))
	if csrf == nil {
		t.Fatal("no csrf field")
	}
	form := url.Values{}
	for k, v := range q {
		form[k] = v
	}
	form.Set("action", "allow")
	// A forged form without the CSRF token must fail.
	resp, _ = noRedirect.PostForm(base+"/oauth/authorize", form)
	if resp.StatusCode != 403 {
		t.Fatalf("missing csrf should be 403, got %d", resp.StatusCode)
	}
	form.Set("csrf", csrf[1])
	resp, err = noRedirect.PostForm(base+"/oauth/authorize", form)
	if err != nil || resp.StatusCode != 302 {
		t.Fatalf("approve: %v %d", err, resp.StatusCode)
	}
	loc, _ := url.Parse(resp.Header.Get("Location"))
	if loc.Query().Get("state") != "xyz" || loc.Query().Get("iss") != base || loc.Query().Get("code") == "" {
		t.Fatalf("redirect: %s", loc)
	}
	code := loc.Query().Get("code")

	// Wrong PKCE verifier fails (and burns the code).
	tok := func(v url.Values) (int, map[string]any) {
		r, err := http.PostForm(base+"/oauth/token", v)
		if err != nil {
			t.Fatal(err)
		}
		defer r.Body.Close()
		var m map[string]any
		json.NewDecoder(r.Body).Decode(&m)
		return r.StatusCode, m
	}
	exchange := url.Values{"grant_type": {"authorization_code"}, "code": {code}, "client_id": {clientID}, "redirect_uri": {"http://127.0.0.1:9999/callback"}, "code_verifier": {verifier}, "resource": {base + "/mcp"}}
	st, out := tok(exchange)
	if st != 200 {
		t.Fatalf("token: %d %v", st, out)
	}
	access := out["access_token"].(string)
	refresh := out["refresh_token"].(string)
	if !strings.HasPrefix(access, "bk_") || out["token_type"] != "Bearer" {
		t.Fatalf("token response: %v", out)
	}
	if st, _ := tok(exchange); st != 400 {
		t.Fatal("codes are single-use")
	}

	// The OAuth token is a personal token listed under Settings → Tokens.
	auth := map[string]string{"Authorization": "Bearer " + access}
	req, _ := http.NewRequest("GET", base+"/api/v1/tokens", nil)
	req.Header.Set("Authorization", "Bearer "+access)
	r2, _ := http.DefaultClient.Do(req)
	b2, _ := io.ReadAll(r2.Body)
	r2.Body.Close()
	if !strings.Contains(string(b2), `"client":"mcp:Test Agent"`) {
		t.Fatalf("token should be listed with client mcp:<name>: %s", b2)
	}

	// Remote MCP with the token.
	resp, init := postJSON(t, base+"/mcp", auth, `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18","capabilities":{},"clientInfo":{"name":"t","version":"1"}}}`)
	if resp.StatusCode != 200 || resp.Header.Get("Mcp-Session-Id") == "" || init["result"].(map[string]any)["protocolVersion"] != "2025-06-18" {
		t.Fatalf("initialize: %d %v", resp.StatusCode, init)
	}
	sid := map[string]string{"Authorization": "Bearer " + access, "Mcp-Session-Id": resp.Header.Get("Mcp-Session-Id"), "MCP-Protocol-Version": "2025-06-18"}
	if r, _ := postJSON(t, base+"/mcp", sid, `{"jsonrpc":"2.0","method":"notifications/initialized"}`); r.StatusCode != 202 {
		t.Fatalf("notification should be 202, got %d", r.StatusCode)
	}
	_, list := postJSON(t, base+"/mcp", sid, `{"jsonrpc":"2.0","id":2,"method":"tools/list"}`)
	names := map[string]bool{}
	for _, x := range list["result"].(map[string]any)["tools"].([]any) {
		names[x.(map[string]any)["name"].(string)] = true
	}
	for _, n := range []string{"sites_list", "sharing_add", "deploy_files", "create_site_from_html", "versions_restore"} {
		if !names[n] {
			t.Errorf("remote tools/list missing %s", n)
		}
	}
	_, call := postJSON(t, base+"/mcp", sid, `{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"create_site_from_html","arguments":{"title":"Remote Made","html":"<!doctype html><title>Remote Made</title><h1>hi</h1>"}}}`)
	res := call["result"].(map[string]any)
	if res["isError"] == true {
		t.Fatalf("create_site_from_html failed: %v", res)
	}
	if sc := res["structuredContent"].(map[string]any); sc["slug"] != "remote-made" {
		t.Fatalf("created site: %v", sc)
	}
	_, call = postJSON(t, base+"/mcp", sid, `{"jsonrpc":"2.0","id":4,"method":"tools/call","params":{"name":"deploy_files","arguments":{"site":"remote-made","files":{"index.html":"<h1>v2</h1>","app.js":"console.log(1)"},"message":"second"}}}`)
	if sc := call["result"].(map[string]any)["structuredContent"].(map[string]any); sc["version"].(float64) != 2 {
		t.Fatalf("deploy_files: %v", call)
	}
	_, call = postJSON(t, base+"/mcp", sid, `{"jsonrpc":"2.0","id":5,"method":"tools/call","params":{"name":"edit_file","arguments":{"site":"remote-made","path":"index.html","find":"v2","replace":"v3","publish":true}}}`)
	if call["result"].(map[string]any)["isError"] == true {
		t.Fatalf("edit_file: %v", call)
	}
	_, call = postJSON(t, base+"/mcp", sid, `{"jsonrpc":"2.0","id":6,"method":"resources/read","params":{"uri":"bunker://sites/remote-made/files/index.html"}}`)
	if c := call["result"].(map[string]any)["contents"].([]any)[0].(map[string]any); c["text"] != "<h1>v3</h1>" {
		t.Fatalf("resource read after edit: %v", call)
	}
	_, call = postJSON(t, base+"/mcp", sid, `{"jsonrpc":"2.0","id":7,"method":"tools/call","params":{"name":"versions_list","arguments":{"site":"remote-made"}}}`)
	if sc := call["result"].(map[string]any)["structuredContent"].(map[string]any); sc["live"].(float64) != 3 {
		t.Fatalf("versions_list: %v", call)
	}

	// Stateless (2026-07-28) request: headers must mirror the body.
	stateless := `{"jsonrpc":"2.0","id":8,"method":"tools/call","params":{"name":"sites_list","arguments":{},"_meta":{"io.modelcontextprotocol/protocolVersion":"2026-07-28"}}}`
	if r, m := postJSON(t, base+"/mcp", map[string]string{"Authorization": "Bearer " + access, "MCP-Protocol-Version": "2026-07-28", "Mcp-Method": "tools/call", "Mcp-Name": "wrong"}, stateless); r.StatusCode != 400 || m["error"].(map[string]any)["code"].(float64) != CodeHeaderMismatch {
		t.Fatalf("header mismatch: %d %v", r.StatusCode, m)
	}
	if r, m := postJSON(t, base+"/mcp", map[string]string{"Authorization": "Bearer " + access, "MCP-Protocol-Version": "2026-07-28", "Mcp-Method": "tools/call", "Mcp-Name": "sites_list"}, stateless); r.StatusCode != 200 || m["result"] == nil {
		t.Fatalf("stateless call: %d %v", r.StatusCode, m)
	}
	// Foreign Origin is refused (DNS rebinding guard).
	if r, _ := postJSON(t, base+"/mcp", map[string]string{"Authorization": "Bearer " + access, "Origin": "https://evil.example"}, `{"jsonrpc":"2.0","id":1,"method":"ping"}`); r.StatusCode != 403 {
		t.Fatalf("foreign origin should be 403, got %d", r.StatusCode)
	}

	// Refresh rotation + reuse detection.
	st, out = tok(url.Values{"grant_type": {"refresh_token"}, "refresh_token": {refresh}, "client_id": {clientID}})
	if st != 200 || out["refresh_token"] == refresh {
		t.Fatalf("refresh: %d %v", st, out)
	}
	access2, refresh2 := out["access_token"].(string), out["refresh_token"].(string)
	if r, _ := postJSON(t, base+"/mcp", auth, `{"jsonrpc":"2.0","id":1,"method":"ping"}`); r.StatusCode != 401 {
		t.Fatalf("old access token should be revoked after refresh, got %d", r.StatusCode)
	}
	if r, _ := postJSON(t, base+"/mcp", map[string]string{"Authorization": "Bearer " + access2}, `{"jsonrpc":"2.0","id":1,"method":"ping"}`); r.StatusCode != 200 {
		t.Fatalf("new access token should work, got %d", r.StatusCode)
	}
	if st, _ := tok(url.Values{"grant_type": {"refresh_token"}, "refresh_token": {refresh}, "client_id": {clientID}}); st != 400 {
		t.Fatal("reusing a rotated refresh token must fail")
	}
	if st, _ := tok(url.Values{"grant_type": {"refresh_token"}, "refresh_token": {refresh2}, "client_id": {clientID}}); st != 400 {
		t.Fatal("reuse must revoke the whole family")
	}
	if r, _ := postJSON(t, base+"/mcp", map[string]string{"Authorization": "Bearer " + access2}, `{"jsonrpc":"2.0","id":1,"method":"ping"}`); r.StatusCode != 401 {
		t.Fatalf("family revocation should kill the access token, got %d", r.StatusCode)
	}
}

func TestAuthorizeErrors(t *testing.T) {
	ts := newServer(t)
	_, reg := postJSON(t, ts.URL+"/oauth/register", nil, `{"client_name":"X","redirect_uris":["https://app.example/cb"]}`)
	id := reg["client_id"].(string)
	// Unregistered redirect: error page, never a redirect.
	resp, _ := noRedirect.Get(ts.URL + "/oauth/authorize?" + url.Values{"response_type": {"code"}, "client_id": {id}, "redirect_uri": {"https://evil.example/cb"}, "code_challenge": {"x"}, "code_challenge_method": {"S256"}}.Encode())
	if resp.StatusCode != 400 {
		t.Fatalf("bad redirect: %d", resp.StatusCode)
	}
	// Missing PKCE: redirect back with an error.
	resp, _ = noRedirect.Get(ts.URL + "/oauth/authorize?" + url.Values{"response_type": {"code"}, "client_id": {id}, "redirect_uri": {"https://app.example/cb"}, "state": {"s"}}.Encode())
	loc, _ := url.Parse(resp.Header.Get("Location"))
	if resp.StatusCode != 302 || loc.Query().Get("error") != "invalid_request" || loc.Query().Get("state") != "s" {
		t.Fatalf("missing pkce: %d %s", resp.StatusCode, loc)
	}
	// Unknown client.
	resp, _ = noRedirect.Get(ts.URL + "/oauth/authorize?client_id=nope&redirect_uri=https://a/cb")
	if resp.StatusCode != 400 {
		t.Fatalf("unknown client: %d", resp.StatusCode)
	}
}

func TestLLMSTxt(t *testing.T) {
	ts := newServer(t)
	for _, p := range []string{"/llms.txt", "/llms-full.txt", "/api/v1/agent-guide"} {
		resp, err := http.Get(ts.URL + p)
		if err != nil || resp.StatusCode != 200 {
			t.Fatalf("%s: %v %v", p, err, resp)
		}
		b, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		if !strings.Contains(string(b), "sites.create") || !strings.Contains(string(b), "bunker.db") {
			t.Errorf("%s lacks ops or SDK docs", p)
		}
	}
}
