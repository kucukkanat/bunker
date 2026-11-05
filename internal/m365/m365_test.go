package m365

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/kucukkanat/bunker/internal/auth"
	"github.com/kucukkanat/bunker/internal/core"
	"github.com/kucukkanat/bunker/internal/testkit"
)

type env struct {
	k     *testkit.Kit
	page  *testkit.Client
	admin *testkit.Client
	slug  string
	hits  atomic.Int64
	paths []string
}

func setup(t *testing.T, tokenScopes string) *env {
	e := &env{}
	graph := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		e.hits.Add(1)
		e.paths = append(e.paths, r.URL.RequestURI())
		if r.Header.Get("Authorization") != "Bearer tok-123" {
			w.WriteHeader(401)
			return
		}
		p := r.URL.EscapedPath()
		w.Header().Set("Content-Type", "application/json")
		switch {
		case p == "/me":
			w.Write([]byte(`{"displayName":"You","mail":"you@corp.test"}`))
		case p == "/sites/contoso.sharepoint.com:/sites/Sales":
			w.Write([]byte(`{"id":"contoso.sharepoint.com,s1,w1"}`))
		case strings.HasPrefix(p, "/sites/contoso.sharepoint.com%2Cs1%2Cw1/lists/Deals/items") || strings.HasPrefix(p, "/sites/contoso.sharepoint.com,s1,w1/lists/Deals/items"):
			if r.URL.Query().Get("page") == "2" {
				w.Write([]byte(`{"value":[{"id":"3","fields":{"Title":"Gamma","Amount":30}}]}`))
				return
			}
			w.Write([]byte(`{"value":[{"id":"1","fields":{"Title":"Alpha","Amount":10,"@odata.etag":"x"}},{"id":"2","fields":{"Title":"Beta","Amount":20}}],"@odata.nextLink":"https://graph.microsoft.com/v1.0/sites/contoso.sharepoint.com,s1,w1/lists/Deals/items?page=2"}`))
		case strings.HasPrefix(p, "/shares/u!"):
			w.Write([]byte(`{"id":"item1","parentReference":{"driveId":"drive1"}}`))
		case strings.HasPrefix(p, "/drives/drive1/items/item1/workbook/worksheets"):
			if !strings.Contains(r.URL.Path, "range(address='A1:B2')") {
				t.Errorf("unexpected excel path %s", r.URL.Path)
			}
			w.Write([]byte(`{"address":"Sheet1!A1:B2","values":[["Name","Qty"],["Pens",4]],"text":[["Name","Qty"],["Pens","4"]]}`))
		case strings.HasPrefix(p, "/me/drive/root/children"):
			w.Write([]byte(`{"value":[{"id":"f1","name":"Plan.docx","size":1200,"file":{"mimeType":"application/msword"}},{"id":"f2","name":"Photos","folder":{"childCount":3}}]}`))
		case p == "/me/events":
			w.WriteHeader(429)
			w.Header().Set("Retry-After", "7")
			w.Write([]byte(`{"error":{"code":"TooManyRequests","message":"slow down"}}`))
		default:
			w.WriteHeader(404)
			w.Write([]byte(`{"error":{"code":"itemNotFound","message":"nope"}}`))
		}
	}))
	t.Cleanup(graph.Close)
	old := auth.GraphBase
	auth.GraphBase = graph.URL
	t.Cleanup(func() { auth.GraphBase = old })
	cacheMu.Lock()
	cache = map[string]cacheEntry{}
	cacheMu.Unlock()

	e.k = testkit.New(t)
	a := e.k.App
	ctx := context.Background()
	a.SetSetting(ctx, "auth.provider", "entra")
	a.SetSetting(ctx, "auth.tenant_id", "11111111-2222-3333-4444-555555555555")
	e.admin = e.k.As("", "")
	me := e.admin.Must("GET", "/api/v1/me", nil)
	uid := int64(me["id"].(float64))
	acc, _ := core.Encrypt("tok-123")
	if _, err := a.DB.Exec(`INSERT INTO idp_tokens(user_id,refresh_enc,access_enc,access_expires_at,scopes,updated_at) VALUES(?,?,?,?,?,?)`, uid, "", acc, core.Now()+3600, tokenScopes, core.Now()); err != nil {
		t.Fatal(err)
	}
	site := e.admin.Must("POST", "/api/v1/sites", map[string]any{"title": "Sales app"})
	e.slug = site["slug"].(string)
	e.page = e.k.As("", "")
	e.page.Page = true
	return e
}

func (e *env) get(path string, q url.Values) testkit.Res {
	return e.page.Do("GET", "/api/v1/sites/"+e.slug+"/m365/"+path+"?"+q.Encode(), nil)
}

func errCode(r testkit.Res) string {
	m := r.JSON()
	er, _ := m["error"].(map[string]any)
	s, _ := er["code"].(string)
	return s
}

func TestGraphPassthrough(t *testing.T) {
	e := setup(t, "openid profile email offline_access User.Read Sites.Read.All")
	r := e.get("graph", url.Values{"path": {"/me"}})
	if errCode(r) != "m365_disabled" {
		t.Fatalf("want disabled, got %d %s", r.Status, r.Body)
	}
	e.admin.Must("PUT", "/api/v1/admin/m365", map[string]any{"enabled": true, "scopes": []string{"Sites.Read.All", "Files.Read"}})
	r = e.get("graph", url.Values{"path": {"/me"}})
	if r.Status != 200 || r.JSON()["displayName"] != "You" {
		t.Fatalf("me: %d %s", r.Status, r.Body)
	}
	// Cached: a second call doesn't hit Graph.
	before := e.hits.Load()
	e.get("graph", url.Values{"path": {"/me"}})
	if e.hits.Load() != before {
		t.Error("second call wasn't cached")
	}
	var n int
	e.k.App.DB.QueryRow(`SELECT COUNT(*) FROM audit WHERE event='m365.call' AND target='/me'`).Scan(&n)
	if n != 1 {
		t.Errorf("audit rows = %d", n)
	}
	// Never-allowed paths, and paths whose scope the admin didn't allow.
	if r := e.get("graph", url.Values{"path": {"/applications"}}); r.Status != 403 {
		t.Errorf("applications: %d", r.Status)
	}
	if r := e.get("graph", url.Values{"path": {"/me/messages"}}); errCode(r) != "scope_not_allowed" {
		t.Errorf("mail: %s", r.Body)
	}
	for _, bad := range []string{"me", "//evil.com/x", "/me/../applications", "/me/%2e%2e/applications", "/me\\x"} {
		if r := e.get("graph", url.Values{"path": {bad}}); r.Status != 400 && r.Status != 403 {
			t.Errorf("%q: %d", bad, r.Status)
		}
	}
	// Allowed by admin but never consented by the viewer → consent flow.
	r = e.get("graph", url.Values{"path": {"/me/drive/root"}})
	m := r.JSON()["error"].(map[string]any)
	if m["code"] != "consent_required" || !strings.Contains(m["consentUrl"].(string), "scopes=Files.Read") {
		t.Fatalf("consent: %s", r.Body)
	}
	// Throttling becomes a friendly 429.
	e.admin.Must("PUT", "/api/v1/admin/m365", map[string]any{"scopes": []string{"Sites.Read.All", "Calendars.Read"}})
	e.k.App.DB.Exec(`UPDATE idp_tokens SET scopes=scopes||' Calendars.Read'`)
	if r := e.get("graph", url.Values{"path": {"/me/events"}}); r.Status != 429 || !strings.Contains(string(r.Body), "busy") {
		t.Errorf("throttle: %d %s", r.Status, r.Body)
	}
	// Admin ops are not reachable from pages.
	if r := e.page.Do("GET", "/api/v1/admin/m365", nil); r.Status != 403 {
		t.Errorf("admin from page: %d", r.Status)
	}
}

func TestListsExcelFiles(t *testing.T) {
	e := setup(t, "User.Read Sites.Read.All Files.Read.All Files.Read")
	e.admin.Must("PUT", "/api/v1/admin/m365", map[string]any{"enabled": true, "scopes": []string{"Sites.Read.All", "Files.Read.All", "Files.Read"}})
	r := e.get("lists/items", url.Values{"site": {"https://contoso.sharepoint.com/sites/Sales/Shared%20Documents"}, "list": {"Deals"}})
	if r.Status != 200 {
		t.Fatalf("lists: %d %s", r.Status, r.Body)
	}
	var out struct {
		Items []map[string]any `json:"items"`
	}
	json.Unmarshal(r.Body, &out)
	if len(out.Items) != 3 || out.Items[0]["Title"] != "Alpha" || out.Items[2]["id"] != "3" || out.Items[0]["@odata.etag"] != nil {
		t.Fatalf("items: %+v", out.Items)
	}
	r = e.get("excel/range", url.Values{"file": {"https://contoso.sharepoint.com/:x:/s/Sales/EabcDEF"}, "range": {"Sheet1!A1:B2"}})
	if r.Status != 200 || !strings.Contains(string(r.Body), `"Pens"`) {
		t.Fatalf("excel: %d %s", r.Status, r.Body)
	}
	if r := e.get("excel/range", url.Values{"file": {"https://contoso.sharepoint.com/x"}, "range": {"A1;DROP"}}); r.Status != 400 {
		t.Errorf("bad range: %d", r.Status)
	}
	r = e.get("files", nil)
	if r.Status != 200 || !strings.Contains(string(r.Body), `"Plan.docx"`) || !strings.Contains(string(r.Body), `"folder":true`) {
		t.Fatalf("files: %d %s", r.Status, r.Body)
	}
	// Restrict to one SharePoint site.
	e.admin.Must("PUT", "/api/v1/admin/m365", map[string]any{"allowedSites": []string{"https://contoso.sharepoint.com/sites/HR"}})
	if r := e.get("lists/items", url.Values{"site": {"https://contoso.sharepoint.com/sites/Sales"}, "list": {"Deals"}}); r.Status != 403 {
		t.Errorf("allowedSites: %d %s", r.Status, r.Body)
	}
	if r := e.get("graph", url.Values{"path": {"/sites/contoso.sharepoint.com,s1,w1/lists"}}); r.Status != 403 {
		t.Errorf("raw site id with allowedSites: %d", r.Status)
	}
	if r := e.get("files", nil); r.Status != 403 {
		t.Errorf("onedrive with allowedSites: %d", r.Status)
	}
	// Approval required.
	e.admin.Must("PUT", "/api/v1/admin/m365", map[string]any{"allowedSites": []string{}, "requireApproval": true})
	if r := e.get("files", nil); errCode(r) != "m365_not_approved" {
		t.Errorf("approval: %s", r.Body)
	}
	e.admin.Must("PUT", "/api/v1/admin/m365", map[string]any{"approvedSites": []string{e.slug}})
	if r := e.get("files", nil); r.Status != 200 {
		t.Errorf("approved: %d %s", r.Status, r.Body)
	}
}

func TestHelpers(t *testing.T) {
	if s := shareID("https://onedrive.live.com/redir?resid=1231244193912!12&authKey=1201919!12921!1"); s != "u!aHR0cHM6Ly9vbmVkcml2ZS5saXZlLmNvbS9yZWRpcj9yZXNpZD0xMjMxMjQ0MTkzOTEyITEyJmF1dGhLZXk9MTIwMTkxOSExMjkyMSEx" {
		t.Errorf("shareID = %s", s) // example from the Graph docs
	}
	h, p, err := spSite("https://Contoso.sharepoint.com/teams/Ops/Lists/Tasks/AllItems.aspx")
	if err != nil || h != "contoso.sharepoint.com" || p != "/teams/Ops" {
		t.Errorf("spSite: %s %s %v", h, p, err)
	}
	if _, _, err := spSite("https://evil.com/sites/x"); err == nil {
		t.Error("non-SharePoint host accepted")
	}
	if s := scopesFor("/me/drive/root:/a.xlsx:/workbook"); len(s) == 0 || s[0] != "Files.Read" {
		t.Errorf("scopesFor drive: %v", s)
	}
	if s := scopesFor("/mean"); s != nil {
		t.Errorf("prefix must match whole segments: %v", s)
	}
}
