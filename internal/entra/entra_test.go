package entra

import (
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/kucukkanat/bunker/internal/auth"
	"github.com/kucukkanat/bunker/internal/testkit"
)

func fakeJWT(claims map[string]any) string {
	b, _ := json.Marshal(claims)
	return "eyJhbGciOiJub25lIn0." + base64.RawURLEncoding.EncodeToString(b) + ".sig"
}

type fake struct {
	mu        sync.Mutex
	polls     int
	pwFails   int
	denyApps  bool
	appBody   map[string]any
	grantBody map[string]any
	badAuth   bool
	tok       string
}

func (f *fake) servers(t *testing.T) {
	f.tok = fakeJWT(map[string]any{"tid": "aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee", "upn": "Admin@Contoso.com"})
	login := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		r.ParseForm()
		w.Header().Set("Content-Type", "application/json")
		if r.Form.Get("client_id") != graphCLIClientID {
			t.Errorf("client_id = %s", r.Form.Get("client_id"))
		}
		switch r.URL.Path {
		case "/organizations/oauth2/v2.0/devicecode":
			if !strings.Contains(r.Form.Get("scope"), "Application.ReadWrite.All") || !strings.Contains(r.Form.Get("scope"), "DelegatedPermissionGrant.ReadWrite.All") {
				t.Errorf("scope = %s", r.Form.Get("scope"))
			}
			w.Write([]byte(`{"device_code":"dc-1","user_code":"ABCD-EFGH","verification_uri":"https://microsoft.com/devicelogin","expires_in":900,"interval":1,"message":"To sign in, use a web browser to open https://microsoft.com/devicelogin and enter the code ABCD-EFGH."}`))
		case "/organizations/oauth2/v2.0/token":
			if r.Form.Get("device_code") != "dc-1" || r.Form.Get("grant_type") != "urn:ietf:params:oauth:grant-type:device_code" {
				t.Errorf("token form: %v", r.Form)
			}
			f.mu.Lock()
			f.polls++
			n := f.polls
			f.mu.Unlock()
			if n == 1 {
				w.WriteHeader(400)
				w.Write([]byte(`{"error":"authorization_pending","error_description":"AADSTS70016: pending"}`))
				return
			}
			json.NewEncoder(w).Encode(map[string]any{"access_token": f.tok, "token_type": "Bearer", "expires_in": 3600})
		default:
			w.WriteHeader(404)
		}
	}))
	graph := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer "+f.tok {
			f.badAuth = true
			w.WriteHeader(401)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		var body map[string]any
		json.NewDecoder(r.Body).Decode(&body)
		p := r.URL.Path
		switch {
		case r.Method == "GET" && strings.HasPrefix(p, "/servicePrincipals(appId='00000003-0000-0000-c000-000000000000')"):
			w.Write([]byte(`{"id":"graph-sp","oauth2PermissionScopes":[{"id":"e1fe6dd8-ba31-4d61-89e7-88639da4683d","value":"User.Read"},{"id":"b340eb25-3456-403f-be2f-af7a0d370277","value":"User.ReadBasic.All"},{"id":"bc024368-1153-4739-b217-4326f2e966d0","value":"GroupMember.Read.All"},{"id":"37f7f235-527c-4136-accd-4a02d197296e","value":"openid"}]}`))
		case r.Method == "POST" && p == "/applications":
			if f.denyApps {
				w.WriteHeader(403)
				w.Write([]byte(`{"error":{"code":"Authorization_RequestDenied","message":"Insufficient privileges to complete the operation."}}`))
				return
			}
			f.appBody = body
			w.WriteHeader(201)
			w.Write([]byte(`{"id":"obj-1","appId":"client-1"}`))
		case r.Method == "POST" && p == "/applications/obj-1/addPassword":
			f.mu.Lock()
			f.pwFails++
			n := f.pwFails
			f.mu.Unlock()
			if n == 1 { // replication delay
				w.WriteHeader(404)
				w.Write([]byte(`{"error":{"code":"Request_ResourceNotFound","message":"Resource 'obj-1' does not exist"}}`))
				return
			}
			w.Write([]byte(`{"secretText":"s3cr3t~value","endDateTime":"2028-11-05T00:00:00Z"}`))
		case r.Method == "POST" && p == "/servicePrincipals":
			if body["appId"] != "client-1" {
				t.Errorf("sp body: %v", body)
			}
			w.WriteHeader(201)
			w.Write([]byte(`{"id":"sp-1"}`))
		case r.Method == "POST" && p == "/oauth2PermissionGrants":
			f.grantBody = body
			w.WriteHeader(201)
			w.Write([]byte(`{"id":"grant-1"}`))
		default:
			t.Errorf("unexpected graph call %s %s", r.Method, p)
			w.WriteHeader(404)
		}
	}))
	t.Cleanup(login.Close)
	t.Cleanup(graph.Close)
	ol, og, od := LoginBase, GraphAPI, RetryDelay
	LoginBase, GraphAPI, RetryDelay = login.URL, graph.URL, 10*time.Millisecond
	t.Cleanup(func() { LoginBase, GraphAPI, RetryDelay = ol, og, od })
}

func TestAutoSetup(t *testing.T) {
	f := &fake{}
	f.servers(t)
	k := testkit.NewProd(t)
	c := k.As("", "")
	remote := []string{"X-Forwarded-For", "203.0.113.9"} // not on the server itself
	// Needs the setup code.
	if r := c.Do("POST", "/api/v1/setup/entra/auto/start", map[string]any{"code": "WRONG"}, remote...); r.Status != 403 {
		t.Fatalf("wrong code: %d %s", r.Status, r.Body)
	}
	r := c.Do("POST", "/api/v1/setup/entra/auto/start", map[string]any{"code": auth.SetupCode()}, remote...)
	if r.Status != 200 {
		t.Fatalf("start: %d %s", r.Status, r.Body)
	}
	st := r.JSON()
	if st["userCode"] != "ABCD-EFGH" || st["verificationUri"] != "https://microsoft.com/devicelogin" || st["flowId"] == "" {
		t.Fatalf("start: %v", st)
	}
	pollBody := map[string]any{"flowId": st["flowId"], "code": auth.SetupCode()}
	p := c.Do("POST", "/api/v1/setup/entra/auto/poll", pollBody, remote...).JSON()
	if p["pending"] != true {
		t.Fatalf("first poll: %v", p)
	}
	// Polling faster than the interval doesn't hit Microsoft.
	c.Do("POST", "/api/v1/setup/entra/auto/poll", pollBody, remote...)
	if f.polls != 1 {
		t.Fatalf("polled Microsoft %d times", f.polls)
	}
	time.Sleep(1100 * time.Millisecond)
	res := c.Do("POST", "/api/v1/setup/entra/auto/poll", pollBody, remote...)
	if res.Status != 200 || res.JSON()["done"] != true {
		t.Fatalf("final poll: %d %s", res.Status, res.Body)
	}
	if f.badAuth {
		t.Fatal("Graph was called without the admin token")
	}
	// The app registration Bunker asked for.
	if f.appBody["signInAudience"] != "AzureADMyOrg" {
		t.Errorf("audience: %v", f.appBody["signInAudience"])
	}
	web := f.appBody["web"].(map[string]any)
	if uris := web["redirectUris"].([]any); len(uris) != 1 || uris[0] != k.Srv.URL+"/auth/callback" {
		t.Errorf("redirect: %v", uris)
	}
	rra := f.appBody["requiredResourceAccess"].([]any)[0].(map[string]any)
	if rra["resourceAppId"] != graphAppID || len(rra["resourceAccess"].([]any)) != len(Permissions) {
		t.Errorf("resource access: %v", rra)
	}
	if f.grantBody["consentType"] != "AllPrincipals" || f.grantBody["clientId"] != "sp-1" || f.grantBody["resourceId"] != "graph-sp" || !strings.Contains(f.grantBody["scope"].(string), "GroupMember.Read.All") {
		t.Errorf("grant: %v", f.grantBody)
	}
	// Saved like the manual setup.
	a := k.App
	if a.Setting("auth.provider") != "entra" || a.Setting("auth.tenant_id") != "aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee" || a.Setting("auth.client_id") != "client-1" {
		t.Fatalf("settings not saved")
	}
	if a.SecretSetting("auth.client_secret") != "s3cr3t~value" || strings.Contains(a.Setting("auth.client_secret"), "s3cr3t") {
		t.Fatal("secret not stored encrypted")
	}
	if a.Setting("admins") != "admin@contoso.com" || a.Setting("setup.code") != "" {
		t.Fatalf("admins=%q code=%q", a.Setting("admins"), a.Setting("setup.code"))
	}
	if p := auth.Provider(); p.Kind != "entra" || p.ClientID != "client-1" || p.Validate() != nil {
		t.Fatalf("provider: %+v", p)
	}
	var n int
	a.DB.QueryRow(`SELECT COUNT(*) FROM audit WHERE event='admin.config' AND meta LIKE '%"auto":true%'`).Scan(&n)
	if n != 1 {
		t.Fatal("not audited")
	}
	// The token is gone with the flow; the flow can't be replayed.
	if r := c.Do("POST", "/api/v1/setup/entra/auto/poll", pollBody, remote...); r.Status != 403 && r.Status != 410 {
		t.Fatalf("replay: %d", r.Status)
	}
}

func TestAutoSetupDenied(t *testing.T) {
	f := &fake{denyApps: true}
	f.servers(t)
	k := testkit.NewProd(t)
	c := k.As("", "")
	st := c.Must("POST", "/api/v1/setup/entra/auto/start", map[string]any{})
	f.polls = 1 // skip the pending step
	time.Sleep(10 * time.Millisecond)
	r := c.Do("POST", "/api/v1/setup/entra/auto/poll", map[string]any{"flowId": st["flowId"]})
	if r.Status != 403 || !strings.Contains(string(r.Body), "Application Administrator") {
		t.Fatalf("want a plain-English role error, got %d %s", r.Status, r.Body)
	}
	if k.App.Setting("auth.provider") == "entra" {
		t.Fatal("must not save anything on failure")
	}
}

func TestScriptAndClaims(t *testing.T) {
	s := CloudShellScript("https://bunker.corp.com")
	for _, want := range []string{"az ad app create", "AzureADMyOrg", "https://bunker.corp.com/auth/callback", "admin-consent", "e1fe6dd8-ba31-4d61-89e7-88639da4683d"} {
		if !strings.Contains(s, want) {
			t.Errorf("script missing %q", want)
		}
	}
	if c := jwtClaims(fakeJWT(map[string]any{"tid": "x"})); c["tid"] != "x" {
		t.Error("claims")
	}
	if c := jwtClaims("garbage"); len(c) != 0 {
		t.Error("garbage claims")
	}
}
