package mcp

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"html/template"
	"io"
	"net"
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/kucukkanat/bunker/internal/auth"
	"github.com/kucukkanat/bunker/internal/core"
)

// OAuth 2.1 authorization server for MCP clients (Claude, ChatGPT, Cursor,
// VS Code...). Access tokens are ordinary Bunker personal tokens (client
// "mcp:<name>"), so people see and revoke them in Settings → Tokens.

const oauthSchema = `
CREATE TABLE oauth_clients (
  id TEXT PRIMARY KEY,
  name TEXT NOT NULL,
  redirect_uris TEXT NOT NULL,
  created_at INTEGER NOT NULL,
  last_used_at INTEGER,
  ip TEXT
);
CREATE TABLE oauth_codes (
  hash TEXT PRIMARY KEY,
  client_id TEXT NOT NULL,
  client_name TEXT NOT NULL,
  user_id INTEGER NOT NULL,
  redirect_uri TEXT NOT NULL,
  challenge TEXT NOT NULL,
  scopes TEXT NOT NULL,
  resource TEXT NOT NULL DEFAULT '',
  expires_at INTEGER NOT NULL
);
CREATE TABLE oauth_refresh (
  hash TEXT PRIMARY KEY,
  family TEXT NOT NULL,
  client_id TEXT NOT NULL,
  client_name TEXT NOT NULL,
  user_id INTEGER NOT NULL,
  token_id INTEGER NOT NULL,
  scopes TEXT NOT NULL,
  resource TEXT NOT NULL DEFAULT '',
  used INTEGER NOT NULL DEFAULT 0,
  created_at INTEGER NOT NULL,
  expires_at INTEGER NOT NULL
);
CREATE INDEX oauth_refresh_family ON oauth_refresh(family);
CREATE INDEX oauth_refresh_token ON oauth_refresh(token_id);
`

const refreshPrefix = "bkr_"

func registerOAuth(a *core.App) {
	a.Public("/.well-known/")
	a.Public("/oauth/")
	prm := func(w http.ResponseWriter, r *http.Request) {
		base := BaseURL(r)
		cors(w)
		core.JSON(w, 200, map[string]any{
			"resource":                 base + "/mcp",
			"authorization_servers":    []string{base},
			"scopes_supported":         scopesSupported,
			"bearer_methods_supported": []string{"header"},
			"resource_name":            "Bunker",
			"resource_documentation":   base + "/llms.txt",
		})
	}
	a.Mux.HandleFunc("GET /.well-known/oauth-protected-resource", prm)
	a.Mux.HandleFunc("GET /.well-known/oauth-protected-resource/mcp", prm)
	asm := func(w http.ResponseWriter, r *http.Request) {
		base := BaseURL(r)
		cors(w)
		core.JSON(w, 200, map[string]any{
			"issuer":                                         base,
			"authorization_endpoint":                         base + "/oauth/authorize",
			"token_endpoint":                                 base + "/oauth/token",
			"registration_endpoint":                          base + "/oauth/register",
			"revocation_endpoint":                            base + "/oauth/revoke",
			"response_types_supported":                       []string{"code"},
			"response_modes_supported":                       []string{"query"},
			"grant_types_supported":                          []string{"authorization_code", "refresh_token"},
			"code_challenge_methods_supported":               []string{"S256"},
			"token_endpoint_auth_methods_supported":          []string{"none"},
			"revocation_endpoint_auth_methods_supported":     []string{"none"},
			"scopes_supported":                               scopesSupported,
			"client_id_metadata_document_supported":          true,
			"authorization_response_iss_parameter_supported": true,
			"service_documentation":                          base + "/llms.txt",
		})
	}
	a.Mux.HandleFunc("GET /.well-known/oauth-authorization-server", asm)
	a.Mux.HandleFunc("GET /.well-known/oauth-authorization-server/mcp", asm)
	a.Mux.HandleFunc("GET /.well-known/openid-configuration", asm)
	for _, p := range []string{"/.well-known/oauth-protected-resource", "/.well-known/oauth-protected-resource/mcp", "/.well-known/oauth-authorization-server", "/.well-known/openid-configuration", "/oauth/token", "/oauth/register", "/oauth/revoke"} {
		a.Mux.HandleFunc("OPTIONS "+p, preflight)
	}
	a.Mux.HandleFunc("POST /oauth/register", handleRegister)
	a.Mux.HandleFunc("GET /oauth/authorize", handleAuthorize)
	a.Mux.HandleFunc("POST /oauth/authorize", handleAuthorize)
	a.Mux.HandleFunc("POST /oauth/token", handleToken)
	a.Mux.HandleFunc("POST /oauth/revoke", handleRevoke)
}

var scopesSupported = []string{"bunker", "read", "write", "admin"}

func cors(w http.ResponseWriter) {
	h := w.Header()
	h.Set("Access-Control-Allow-Origin", "*")
	h.Set("Access-Control-Allow-Methods", "GET, POST, OPTIONS")
	h.Set("Access-Control-Allow-Headers", "Authorization, Content-Type, MCP-Protocol-Version")
	h.Set("Access-Control-Max-Age", "86400")
}

func preflight(w http.ResponseWriter, r *http.Request) {
	cors(w)
	w.WriteHeader(http.StatusNoContent)
}

func oauthErr(w http.ResponseWriter, status int, code, desc string) {
	w.Header().Set("Cache-Control", "no-store")
	core.JSON(w, status, map[string]string{"error": code, "error_description": desc})
}

// ---- clients ----

type client struct {
	ID           string   `json:"client_id"`
	Name         string   `json:"client_name"`
	RedirectURIs []string `json:"redirect_uris"`
	ClientURI    string   `json:"client_uri,omitempty"`
}

// ValidRedirectURI accepts https URLs, http loopback URLs (RFC 8252 §7.3)
// and private-use custom schemes for native apps (cursor://, vscode://).
func ValidRedirectURI(s string) error {
	u, err := url.Parse(s)
	if err != nil || u.Scheme == "" {
		return errors.New("redirect URI must be an absolute URL")
	}
	if u.Fragment != "" || strings.Contains(s, "#") {
		return errors.New("redirect URI must not contain a fragment")
	}
	switch strings.ToLower(u.Scheme) {
	case "https":
		if u.Host == "" {
			return errors.New("redirect URI needs a host")
		}
		return nil
	case "http":
		if isLoopback(u.Hostname()) {
			return nil
		}
		return errors.New("http redirect URIs are only allowed for localhost / 127.0.0.1")
	case "javascript", "data", "file", "vbscript", "about", "blob", "ftp", "ws", "wss", "mailto", "tel", "filesystem":
		return errors.New("redirect URI scheme " + u.Scheme + " is not allowed")
	}
	return nil // custom scheme for a native app
}

func isLoopback(h string) bool {
	if h == "localhost" {
		return true
	}
	ip := net.ParseIP(h)
	return ip != nil && ip.IsLoopback()
}

// redirectMatches compares exactly, except that loopback redirects may use
// any port (native apps pick a free port at runtime).
func redirectMatches(registered []string, given string) bool {
	for _, r := range registered {
		if r == given {
			return true
		}
		ru, e1 := url.Parse(r)
		gu, e2 := url.Parse(given)
		if e1 != nil || e2 != nil || ru.Scheme != "http" || gu.Scheme != "http" || !isLoopback(ru.Hostname()) {
			continue
		}
		if ru.Hostname() == gu.Hostname() && ru.Path == gu.Path && ru.RawQuery == gu.RawQuery {
			return true
		}
	}
	return false
}

var (
	regMu   sync.Mutex
	regSeen = map[string][]int64{}
)

func allowRegistration(ip string) bool {
	regMu.Lock()
	defer regMu.Unlock()
	now := core.Now()
	if len(regSeen) > 10000 {
		regSeen = map[string][]int64{}
	}
	var keep []int64
	for _, t := range regSeen[ip] {
		if now-t < 3600 {
			keep = append(keep, t)
		}
	}
	if len(keep) >= 30 {
		regSeen[ip] = keep
		return false
	}
	regSeen[ip] = append(keep, now)
	return true
}

func handleRegister(w http.ResponseWriter, r *http.Request) {
	cors(w)
	var in struct {
		ClientName   string   `json:"client_name"`
		RedirectURIs []string `json:"redirect_uris"`
		GrantTypes   []string `json:"grant_types"`
		Response     []string `json:"response_types"`
		AuthMethod   string   `json:"token_endpoint_auth_method"`
		ClientURI    string   `json:"client_uri"`
		Scope        string   `json:"scope"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 64<<10)).Decode(&in); err != nil {
		oauthErr(w, 400, "invalid_client_metadata", "Send JSON client metadata.")
		return
	}
	if len(in.RedirectURIs) == 0 || len(in.RedirectURIs) > 20 {
		oauthErr(w, 400, "invalid_redirect_uri", "Give 1 to 20 redirect_uris.")
		return
	}
	for _, u := range in.RedirectURIs {
		if err := ValidRedirectURI(u); err != nil {
			oauthErr(w, 400, "invalid_redirect_uri", err.Error())
			return
		}
	}
	for _, g := range in.GrantTypes {
		if g != "authorization_code" && g != "refresh_token" {
			oauthErr(w, 400, "invalid_client_metadata", "Only authorization_code and refresh_token grants are supported.")
			return
		}
	}
	ip := core.IPFrom(r.Context())
	if !allowRegistration(ip) {
		oauthErr(w, 429, "too_many_requests", "Too many client registrations from this address. Try again later.")
		return
	}
	name := strings.TrimSpace(in.ClientName)
	if name == "" {
		name = "MCP client"
	}
	name = clip(name, 80)
	id := "bkc_" + core.RandomToken(18)
	now := core.Now()
	uris, _ := json.Marshal(in.RedirectURIs)
	// Garbage-collect clients that were never used within 30 days.
	app.DB.ExecContext(r.Context(), `DELETE FROM oauth_clients WHERE (last_used_at IS NULL AND created_at < ?) OR last_used_at < ?`, now-30*86400, now-180*86400)
	if _, err := app.DB.ExecContext(r.Context(), `INSERT INTO oauth_clients(id,name,redirect_uris,created_at,ip) VALUES(?,?,?,?,?)`, id, name, string(uris), now, ip); err != nil {
		oauthErr(w, 500, "server_error", "Could not register the client.")
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	core.JSON(w, http.StatusCreated, map[string]any{
		"client_id":                  id,
		"client_id_issued_at":        now,
		"client_name":                name,
		"redirect_uris":              in.RedirectURIs,
		"grant_types":                []string{"authorization_code", "refresh_token"},
		"response_types":             []string{"code"},
		"token_endpoint_auth_method": "none",
	})
}

func lookupClient(ctx context.Context, id string) (*client, error) {
	if strings.HasPrefix(id, "https://") {
		return fetchCIMD(ctx, id)
	}
	var c client
	var uris string
	err := app.DB.QueryRowContext(ctx, `SELECT id,name,redirect_uris FROM oauth_clients WHERE id=?`, id).Scan(&c.ID, &c.Name, &uris)
	if err != nil {
		return nil, errors.New("This app isn't registered with Bunker (unknown client_id). Remove the connection in the app and add it again.")
	}
	json.Unmarshal([]byte(uris), &c.RedirectURIs)
	return &c, nil
}

// ---- Client ID Metadata Documents (client_id is an https URL) ----

type cimdEntry struct {
	c   *client
	exp time.Time
}

var (
	cimdMu    sync.Mutex
	cimdCache = map[string]cimdEntry{}
)

// AllowPrivateCIMD lets tests and internal deployments fetch metadata
// documents from private addresses.
var AllowPrivateCIMD = false

func publicIP(ip net.IP) bool {
	if ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() || ip.IsUnspecified() || ip.IsMulticast() || ip.IsInterfaceLocalMulticast() {
		return false
	}
	for _, n := range nonPublicNets {
		if n.Contains(ip) {
			return false
		}
	}
	return true
}

// nonPublicNets are special-purpose ranges the net package doesn't flag
// (CGNAT/Tailscale, "this network", benchmarking, NAT64, documentation...).
var nonPublicNets = func() []*net.IPNet {
	var out []*net.IPNet
	for _, c := range []string{"0.0.0.0/8", "100.64.0.0/10", "192.0.0.0/24", "192.0.2.0/24", "198.18.0.0/15", "198.51.100.0/24", "203.0.113.0/24", "240.0.0.0/4",
		"64:ff9b::/96", "64:ff9b:1::/48", "100::/64", "2001:db8::/32", "fec0::/10"} {
		_, n, _ := net.ParseCIDR(c)
		out = append(out, n)
	}
	return out
}()

var cimdClient = &http.Client{
	Timeout: 5 * time.Second,
	CheckRedirect: func(*http.Request, []*http.Request) error {
		return errors.New("redirects not allowed")
	},
	Transport: &http.Transport{
		Proxy: nil,
		DialContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
			host, port, err := net.SplitHostPort(addr)
			if err != nil {
				return nil, err
			}
			ips, err := net.DefaultResolver.LookupIP(ctx, "ip", host)
			if err != nil {
				return nil, err
			}
			allow := AllowPrivateCIMD || core.EnvBool("OAUTH_CIMD_ALLOW_PRIVATE", false)
			var d net.Dialer
			for _, ip := range ips {
				if !allow && !publicIP(ip) {
					continue
				}
				return d.DialContext(ctx, network, net.JoinHostPort(ip.String(), port))
			}
			return nil, errors.New("client metadata host resolves to a private address")
		},
		TLSHandshakeTimeout:   5 * time.Second,
		ResponseHeaderTimeout: 5 * time.Second,
		MaxIdleConns:          4,
	},
}

func fetchCIMD(ctx context.Context, id string) (*client, error) {
	cimdMu.Lock()
	if e, ok := cimdCache[id]; ok && time.Now().Before(e.exp) {
		cimdMu.Unlock()
		return e.c, nil
	}
	cimdMu.Unlock()
	u, err := url.Parse(id)
	if err != nil || u.Scheme != "https" || u.Host == "" || u.Fragment != "" || u.User != nil {
		return nil, errors.New("client_id must be an https URL")
	}
	req, _ := http.NewRequestWithContext(ctx, "GET", id, nil)
	req.Header.Set("Accept", "application/json")
	resp, err := cimdClient.Do(req)
	if err != nil {
		return nil, errors.New("Couldn't load the app's client metadata from " + u.Host + ".")
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("The app's client metadata at %s returned status %d.", u.Host, resp.StatusCode)
	}
	b, err := io.ReadAll(io.LimitReader(resp.Body, 64<<10+1))
	if err != nil || len(b) > 64<<10 {
		return nil, errors.New("The app's client metadata document is too large.")
	}
	var c client
	if json.Unmarshal(b, &c) != nil || c.ID != id {
		return nil, errors.New("The app's client metadata document is invalid (client_id must match its URL).")
	}
	if len(c.RedirectURIs) == 0 {
		return nil, errors.New("The app's client metadata lists no redirect_uris.")
	}
	for _, r := range c.RedirectURIs {
		if err := ValidRedirectURI(r); err != nil {
			return nil, errors.New("The app's client metadata has an invalid redirect URI: " + err.Error())
		}
	}
	if c.Name == "" {
		c.Name = u.Host
	}
	c.Name = clip(c.Name, 80)
	cimdMu.Lock()
	if len(cimdCache) > 1000 {
		cimdCache = map[string]cimdEntry{}
	}
	cimdCache[id] = cimdEntry{c: &c, exp: time.Now().Add(10 * time.Minute)}
	cimdMu.Unlock()
	return &c, nil
}

// ---- authorize ----

type authzReq struct {
	ClientID, RedirectURI, State, Challenge, Method, Scope, Resource, ResponseType string
}

func readAuthz(v url.Values) authzReq {
	return authzReq{
		ClientID: v.Get("client_id"), RedirectURI: v.Get("redirect_uri"), State: v.Get("state"),
		Challenge: v.Get("code_challenge"), Method: v.Get("code_challenge_method"), Scope: v.Get("scope"),
		Resource: v.Get("resource"), ResponseType: v.Get("response_type"),
	}
}

func (q authzReq) values() url.Values {
	v := url.Values{}
	set := func(k, s string) {
		if s != "" {
			v.Set(k, s)
		}
	}
	set("client_id", q.ClientID)
	set("redirect_uri", q.RedirectURI)
	set("state", q.State)
	set("code_challenge", q.Challenge)
	set("code_challenge_method", q.Method)
	set("scope", q.Scope)
	set("resource", q.Resource)
	set("response_type", q.ResponseType)
	return v
}

// resolveScopes maps OAuth scopes onto Bunker token scopes.
func resolveScopes(s string, admin bool) []string {
	set := map[string]bool{}
	for _, x := range strings.Fields(s) {
		switch x {
		case "bunker", "mcp":
			set[core.ScopeRead], set[core.ScopeWrite] = true, true
		case core.ScopeRead, core.ScopeWrite:
			set[x] = true
		case core.ScopeAdmin:
			if admin {
				set[x] = true
			}
		}
	}
	if len(set) == 0 {
		set[core.ScopeRead], set[core.ScopeWrite] = true, true
	}
	var out []string
	for _, k := range []string{core.ScopeRead, core.ScopeWrite, core.ScopeAdmin} {
		if set[k] {
			out = append(out, k)
		}
	}
	return out
}

func validResource(res, base string) bool {
	if res == "" {
		return true
	}
	res = strings.TrimRight(res, "/")
	return res == base+"/mcp" || res == base
}

func redirectWith(w http.ResponseWriter, r *http.Request, target string, params url.Values) {
	u, _ := url.Parse(target)
	q := u.Query()
	for k, v := range params {
		q[k] = v
	}
	u.RawQuery = q.Encode()
	http.Redirect(w, r, u.String(), http.StatusFound)
}

func csrfFor(u *core.User, q authzReq) string {
	return core.MAC(fmt.Sprintf("oauth-consent|%d|%s|%s|%s|%s", u.ID, u.SessionID, q.ClientID, q.RedirectURI, q.Challenge))
}

func handleAuthorize(w http.ResponseWriter, r *http.Request) {
	base := BaseURL(r)
	if r.Method == http.MethodPost {
		r.Body = http.MaxBytesReader(w, r.Body, 64<<10)
		if err := r.ParseForm(); err != nil {
			authPage(w, 400, "Something went wrong", "The form could not be read. Go back to your app and try connecting again.")
			return
		}
	}
	vals := r.URL.Query()
	if r.Method == http.MethodPost {
		vals = r.PostForm
	}
	q := readAuthz(vals)
	if q.ClientID == "" {
		authPage(w, 400, "Missing app details", "This sign-in link is incomplete (no client_id). Go back to your app and try connecting again.")
		return
	}
	c, err := lookupClient(r.Context(), q.ClientID)
	if err != nil {
		authPage(w, 400, "Unknown app", err.Error())
		return
	}
	if q.RedirectURI == "" && len(c.RedirectURIs) == 1 {
		q.RedirectURI = c.RedirectURIs[0]
	}
	if !redirectMatches(c.RedirectURIs, q.RedirectURI) {
		authPage(w, 400, "Unexpected return address", "This app asked to send you back to an address it didn't register. Nothing was shared.")
		return
	}
	// From here on errors go back to the app.
	fail := func(code, desc string) {
		p := url.Values{"error": {code}, "error_description": {desc}, "iss": {base}}
		if q.State != "" {
			p.Set("state", q.State)
		}
		redirectWith(w, r, q.RedirectURI, p)
	}
	if q.ResponseType != "code" {
		fail("unsupported_response_type", "response_type must be code")
		return
	}
	if q.Challenge == "" || q.Method != "S256" {
		fail("invalid_request", "PKCE with code_challenge_method=S256 is required")
		return
	}
	if !validResource(q.Resource, base) {
		fail("invalid_target", "resource must be "+base+"/mcp")
		return
	}
	u := core.UserFrom(r.Context())
	if u == nil || u.ViaToken {
		next := "/oauth/authorize?" + q.values().Encode()
		http.Redirect(w, r, auth.LoginURL(next), http.StatusFound)
		return
	}
	if r.Method == http.MethodGet {
		consentPage(w, r, c, q, u)
		return
	}
	if !core.MACEqual(vals.Get("csrf"), csrfFor(u, q)) {
		authPage(w, 403, "Please try again", "This approval form expired or didn't come from Bunker. Go back to your app and connect again.")
		return
	}
	if vals.Get("action") != "allow" {
		fail("access_denied", "The person declined access")
		return
	}
	code := core.RandomToken(32)
	scopes := resolveScopes(q.Scope, u.IsAdmin)
	_, err = app.DB.ExecContext(r.Context(), `INSERT INTO oauth_codes(hash,client_id,client_name,user_id,redirect_uri,challenge,scopes,resource,expires_at) VALUES(?,?,?,?,?,?,?,?,?)`,
		core.HashToken(code), c.ID, c.Name, u.ID, q.RedirectURI, q.Challenge, strings.Join(scopes, ","), q.Resource, core.Now()+600)
	if err != nil {
		fail("server_error", "could not issue a code")
		return
	}
	app.DB.ExecContext(r.Context(), `UPDATE oauth_clients SET last_used_at=? WHERE id=?`, core.Now(), c.ID)
	app.DB.ExecContext(r.Context(), `DELETE FROM oauth_codes WHERE expires_at < ?`, core.Now())
	app.Audit(r.Context(), "oauth.authorize", 0, c.Name, map[string]any{"clientId": c.ID, "redirect": hostOf(q.RedirectURI), "scopes": scopes})
	p := url.Values{"code": {code}, "iss": {base}}
	if q.State != "" {
		p.Set("state", q.State)
	}
	redirectWith(w, r, q.RedirectURI, p)
}

func hostOf(s string) string {
	u, err := url.Parse(s)
	if err != nil {
		return s
	}
	if u.Host == "" {
		return u.Scheme + ":"
	}
	return u.Host
}

// formActionSource lets the consent form's redirect to the app pass CSP.
func formActionSource(s string) string {
	u, err := url.Parse(s)
	if err != nil {
		return ""
	}
	if u.Scheme == "http" || u.Scheme == "https" {
		return u.Scheme + "://" + u.Host
	}
	return u.Scheme + ":"
}

var pageCSS = `body{font:16px/1.5 system-ui,-apple-system,"Segoe UI",Inter,sans-serif;background:#F7F4EC;color:#1C211E;display:grid;place-items:center;min-height:100vh;margin:0;padding:16px;box-sizing:border-box}
main{width:100%;max-width:440px;padding:32px;background:#fffdf8;border:1px solid #E9E3D3;border-radius:16px;box-shadow:0 1px 2px rgba(0,0,0,.04)}
h1{font-family:Fraunces,Georgia,serif;font-weight:600;color:#1F3A2E;margin:0 0 12px;font-size:26px;line-height:1.2}
.brand{display:flex;align-items:center;gap:8px;color:#4F7A63;font-weight:600;font-size:14px;margin-bottom:20px}.brand img{width:22px;height:22px}
.host{font-family:ui-monospace,Menlo,Consolas,monospace;background:#E9E3D3;padding:2px 6px;border-radius:6px;word-break:break-all}
ul{padding-left:20px;margin:8px 0 16px}li{margin:4px 0}.muted{color:#5A635D;font-size:14px}
.warn{background:#fbeee6;border:1px solid #e6b89d;color:#7a3a1c;padding:10px 12px;border-radius:10px;font-size:14px;margin:12px 0}
.row{display:flex;gap:12px;margin-top:20px}button{flex:1;font:inherit;font-weight:600;padding:11px 16px;border-radius:10px;border:1px solid #1F3A2E;cursor:pointer}
.allow{background:#1F3A2E;color:#F7F4EC}.allow:hover{background:#2F5543}.deny{background:transparent;color:#1F3A2E}
@media(prefers-color-scheme:dark){body{background:#121714;color:#E8EBE6}main{background:#1A211D;border-color:#2e3832}h1{color:#E8EBE6}.brand{color:#8FBF9F}.host{background:#26302a}.muted{color:#a9b2ab}
.warn{background:#3a2a20;border-color:#6b4430;color:#f0c9b0}.allow{background:#8FBF9F;color:#121714;border-color:#8FBF9F}.deny{color:#E8EBE6;border-color:#4a574f}}`

var consentTmpl = template.Must(template.New("c").Parse(`<!doctype html><html lang="en"><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1"><title>Connect {{.Client}} to Bunker</title><style>{{.CSS}}</style>
<main><div class="brand"><img src="/_/icon.svg" alt="">Bunker</div>
<h1>Allow {{.Client}} to use Bunker as you?</h1>
<p class="muted">Signed in as <strong>{{.User}}</strong></p>
<p>After you allow it, you'll be sent back to <span class="host">{{.Host}}</span></p>
{{if .Local}}<div class="warn">This address is on your own computer. Only allow it if you just started connecting an app on this device.</div>{{end}}
<p>It will be able to:</p><ul>{{range .Scopes}}<li>{{.}}</li>{{end}}</ul>
<p class="muted">It can only reach sites you can reach. You can disconnect it any time in Settings → Tokens.</p>
<form method="post" action="/oauth/authorize">{{range $k, $v := .Fields}}<input type="hidden" name="{{$k}}" value="{{$v}}">{{end}}<input type="hidden" name="csrf" value="{{.CSRF}}">
<div class="row"><button class="deny" type="submit" name="action" value="deny">Cancel</button><button class="allow" type="submit" name="action" value="allow">Allow</button></div></form></main></html>`))

var pageTmpl = template.Must(template.New("p").Parse(`<!doctype html><html lang="en"><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1"><title>{{.Title}}</title><style>{{.CSS}}</style>
<main><div class="brand"><img src="/_/icon.svg" alt="">Bunker</div><h1>{{.Title}}</h1><p>{{.Detail}}</p></main></html>`))

func secureHTML(w http.ResponseWriter, formAction string) {
	h := w.Header()
	h.Set("Content-Type", "text/html; charset=utf-8")
	h.Set("Cache-Control", "no-store")
	h.Set("X-Frame-Options", "DENY")
	fa := "'self'"
	if formAction != "" {
		fa += " " + formAction
	}
	h.Set("Content-Security-Policy", "default-src 'none'; style-src 'unsafe-inline'; img-src 'self' data:; form-action "+fa+"; frame-ancestors 'none'; base-uri 'none'")
}

func authPage(w http.ResponseWriter, status int, title, detail string) {
	secureHTML(w, "")
	w.WriteHeader(status)
	pageTmpl.Execute(w, map[string]any{"Title": title, "Detail": detail, "CSS": template.CSS(pageCSS)})
}

func consentPage(w http.ResponseWriter, r *http.Request, c *client, q authzReq, u *core.User) {
	var scopes []string
	for _, s := range resolveScopes(q.Scope, u.IsAdmin) {
		switch s {
		case core.ScopeRead:
			scopes = append(scopes, "See your sites, their files, versions and sharing")
		case core.ScopeWrite:
			scopes = append(scopes, "Create, deploy, change and share sites for you")
		case core.ScopeAdmin:
			scopes = append(scopes, "Use your Bunker admin rights")
		}
	}
	fields := map[string]string{}
	for k, v := range q.values() {
		fields[k] = v[0]
	}
	ru, _ := url.Parse(q.RedirectURI)
	who := u.Name
	if u.Email != "" {
		who += " (" + u.Email + ")"
	}
	secureHTML(w, formActionSource(q.RedirectURI))
	consentTmpl.Execute(w, map[string]any{
		"Client": c.Name, "User": who, "Host": hostOf(q.RedirectURI), "Local": ru != nil && isLoopback(ru.Hostname()),
		"Scopes": scopes, "Fields": fields, "CSRF": csrfFor(u, q), "CSS": template.CSS(pageCSS),
	})
}

// ---- token ----

func formValues(r *http.Request) url.Values {
	r.Body = http.MaxBytesReader(nil, r.Body, 64<<10)
	if strings.HasPrefix(r.Header.Get("Content-Type"), "application/json") {
		var m map[string]any
		json.NewDecoder(r.Body).Decode(&m)
		v := url.Values{}
		for k, x := range m {
			v.Set(k, Stringify(x))
		}
		return v
	}
	r.ParseForm()
	return r.PostForm
}

func pkceOK(verifier, challenge string) bool {
	if len(verifier) < 43 || len(verifier) > 128 {
		return false
	}
	sum := sha256.Sum256([]byte(verifier))
	return core.MACEqual(base64.RawURLEncoding.EncodeToString(sum[:]), challenge)
}

func accessTTL() int64 { return int64(core.EnvInt("OAUTH_ACCESS_TTL", 3600)) }

// issue creates an access token (a Bunker personal token) + refresh token.
func issue(ctx context.Context, uid int64, clientID, clientName, scopes, resource, family string) (map[string]any, error) {
	sc := strings.Split(scopes, ",")
	tok, tid, err := auth.CreateToken(ctx, uid, clientName, clip("mcp:"+clientName, 60), sc, 1)
	if err != nil {
		return nil, err
	}
	ttl := accessTTL()
	app.DB.ExecContext(ctx, `UPDATE tokens SET expires_at=? WHERE id=?`, core.Now()+ttl, tid)
	rt := refreshPrefix + core.RandomToken(32)
	if family == "" {
		family = core.RandomToken(12)
	}
	days := int64(core.EnvInt("TOKEN_MAX_DAYS", 90))
	_, err = app.DB.ExecContext(ctx, `INSERT INTO oauth_refresh(hash,family,client_id,client_name,user_id,token_id,scopes,resource,created_at,expires_at) VALUES(?,?,?,?,?,?,?,?,?,?)`,
		core.HashToken(rt), family, clientID, clientName, uid, tid, scopes, resource, core.Now(), core.Now()+days*86400)
	if err != nil {
		return nil, err
	}
	return map[string]any{
		"access_token": tok, "token_type": "Bearer", "expires_in": ttl,
		"refresh_token": rt, "scope": strings.Join(sc, " "),
	}, nil
}

// sweepOAuth removes expired refresh tokens and the expired access tokens
// no live refresh token points at. (auth.SweepExpired skips OAuth access
// tokens: a refresh must still find the expired row, since a missing row
// means the person revoked the app in Settings.)
func sweepOAuth(ctx context.Context) {
	now := core.Now()
	app.DB.ExecContext(ctx, `DELETE FROM oauth_refresh WHERE expires_at < ?`, now)
	app.DB.ExecContext(ctx, `DELETE FROM oauth_codes WHERE expires_at < ?`, now)
	app.DB.ExecContext(ctx, `DELETE FROM tokens WHERE client LIKE 'mcp:%' AND expires_at IS NOT NULL AND expires_at < ? AND id NOT IN (SELECT token_id FROM oauth_refresh)`, now)
}

func sweepOAuthLoop() {
	t := time.NewTicker(time.Hour)
	defer t.Stop()
	for range t.C {
		sweepOAuth(context.Background())
	}
}

func revokeFamily(ctx context.Context, family string, uid int64) {
	app.DB.ExecContext(ctx, `DELETE FROM tokens WHERE id IN (SELECT token_id FROM oauth_refresh WHERE family=?)`, family)
	app.DB.ExecContext(ctx, `DELETE FROM oauth_refresh WHERE family=?`, family)
	auth.InvalidateUser(uid)
}

func handleToken(w http.ResponseWriter, r *http.Request) {
	cors(w)
	v := formValues(r)
	base := BaseURL(r)
	ctx := r.Context()
	switch v.Get("grant_type") {
	case "authorization_code":
		code := v.Get("code")
		var c struct {
			clientID, clientName, redirect, challenge, scopes, resource string
			uid, exp                                                    int64
		}
		err := app.DB.QueryRowContext(ctx, `DELETE FROM oauth_codes WHERE hash=? RETURNING client_id,client_name,user_id,redirect_uri,challenge,scopes,resource,expires_at`, core.HashToken(code)).
			Scan(&c.clientID, &c.clientName, &c.uid, &c.redirect, &c.challenge, &c.scopes, &c.resource, &c.exp)
		if err != nil || c.exp < core.Now() {
			oauthErr(w, 400, "invalid_grant", "The authorization code is invalid or expired.")
			return
		}
		if cid := v.Get("client_id"); cid != "" && cid != c.clientID {
			oauthErr(w, 400, "invalid_grant", "The code was issued to another client.")
			return
		}
		if ru := v.Get("redirect_uri"); ru != "" && ru != c.redirect {
			oauthErr(w, 400, "invalid_grant", "redirect_uri doesn't match the authorization request.")
			return
		}
		if !pkceOK(v.Get("code_verifier"), c.challenge) {
			oauthErr(w, 400, "invalid_grant", "PKCE verification failed (code_verifier).")
			return
		}
		res := v.Get("resource")
		if !validResource(res, base) {
			oauthErr(w, 400, "invalid_target", "resource must be "+base+"/mcp")
			return
		}
		u, err := auth.LoadUser(ctx, c.uid)
		if err != nil || u == nil {
			oauthErr(w, 400, "invalid_grant", "This account is disabled.")
			return
		}
		out, err := issue(ctx, c.uid, c.clientID, c.clientName, c.scopes, firstNonEmpty(res, c.resource), "")
		if err != nil {
			oauthErr(w, 500, "server_error", "Could not issue a token.")
			return
		}
		app.Audit(core.WithUser(ctx, u), "token.create", 0, c.clientName, map[string]any{"flow": "oauth", "clientId": c.clientID})
		w.Header().Set("Cache-Control", "no-store")
		core.JSON(w, 200, out)
	case "refresh_token":
		rt := v.Get("refresh_token")
		var family, clientID, clientName, scopes, resource string
		var uid, tid, exp int64
		var used int
		err := app.DB.QueryRowContext(ctx, `SELECT family,client_id,client_name,user_id,token_id,scopes,resource,used,expires_at FROM oauth_refresh WHERE hash=?`, core.HashToken(rt)).
			Scan(&family, &clientID, &clientName, &uid, &tid, &scopes, &resource, &used, &exp)
		if err != nil {
			oauthErr(w, 400, "invalid_grant", "The refresh token is invalid.")
			return
		}
		if used == 1 {
			// Reuse of a rotated token: assume theft, revoke the whole chain.
			revokeFamily(ctx, family, uid)
			app.Audit(ctx, "oauth.refresh_reuse", 0, clientName, map[string]any{"clientId": clientID, "userId": uid})
			oauthErr(w, 400, "invalid_grant", "This refresh token was already used. Connect the app again.")
			return
		}
		if exp < core.Now() {
			revokeFamily(ctx, family, uid)
			oauthErr(w, 400, "invalid_grant", "The refresh token expired. Connect the app again.")
			return
		}
		if cid := v.Get("client_id"); cid != "" && cid != clientID {
			oauthErr(w, 400, "invalid_grant", "The refresh token belongs to another client.")
			return
		}
		var n int
		app.DB.QueryRowContext(ctx, `SELECT COUNT(*) FROM tokens WHERE id=?`, tid).Scan(&n)
		u, _ := auth.LoadUser(ctx, uid)
		if n == 0 || u == nil {
			// The person revoked the token in Settings (or was disabled).
			revokeFamily(ctx, family, uid)
			oauthErr(w, 400, "invalid_grant", "Access was revoked. Connect the app again.")
			return
		}
		res, err := app.DB.ExecContext(ctx, `UPDATE oauth_refresh SET used=1 WHERE hash=? AND used=0`, core.HashToken(rt))
		if k, _ := res.RowsAffected(); err != nil || k == 0 {
			oauthErr(w, 400, "invalid_grant", "The refresh token was already used.")
			return
		}
		app.DB.ExecContext(ctx, `DELETE FROM tokens WHERE id=?`, tid)
		auth.InvalidateUser(uid)
		// Drop older used links of the chain, keeping only the one just
		// used (for reuse detection).
		app.DB.ExecContext(ctx, `DELETE FROM oauth_refresh WHERE family=? AND used=1 AND hash<>?`, family, core.HashToken(rt))
		if s := v.Get("scope"); s != "" {
			// Narrowing is allowed; widening is not.
			var keep []string
			for _, x := range resolveScopes(s, u.IsAdmin) {
				if slices.Contains(strings.Split(scopes, ","), x) {
					keep = append(keep, x)
				}
			}
			if len(keep) > 0 {
				scopes = strings.Join(keep, ",")
			}
		}
		out, err := issue(ctx, uid, clientID, clientName, scopes, resource, family)
		if err != nil {
			oauthErr(w, 500, "server_error", "Could not issue a token.")
			return
		}
		w.Header().Set("Cache-Control", "no-store")
		core.JSON(w, 200, out)
	default:
		oauthErr(w, 400, "unsupported_grant_type", "Use authorization_code or refresh_token.")
	}
}

// handleRevoke implements RFC 7009 (always 200).
func handleRevoke(w http.ResponseWriter, r *http.Request) {
	cors(w)
	v := formValues(r)
	t := v.Get("token")
	ctx := r.Context()
	switch {
	case strings.HasPrefix(t, refreshPrefix):
		var family string
		var uid int64
		if app.DB.QueryRowContext(ctx, `SELECT family,user_id FROM oauth_refresh WHERE hash=?`, core.HashToken(t)).Scan(&family, &uid) == nil {
			revokeFamily(ctx, family, uid)
		}
	case strings.HasPrefix(t, "bk_"):
		var id, uid int64
		err := app.DB.QueryRowContext(ctx, `SELECT id,user_id FROM tokens WHERE hash=? AND client LIKE 'mcp:%'`, core.HashToken(t)).Scan(&id, &uid)
		if err == nil {
			var family string
			if app.DB.QueryRowContext(ctx, `SELECT family FROM oauth_refresh WHERE token_id=?`, id).Scan(&family) == nil {
				revokeFamily(ctx, family, uid)
			} else {
				app.DB.ExecContext(ctx, `DELETE FROM tokens WHERE id=?`, id)
				auth.InvalidateUser(uid)
			}
		}
	}
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(http.StatusOK)
}

func clip(s string, n int) string {
	if len(s) <= n {
		return s
	}
	for n > 0 && !utf8RuneStart(s[n]) {
		n--
	}
	return s[:n]
}

func utf8RuneStart(b byte) bool { return b&0xC0 != 0x80 }

var _ = sql.ErrNoRows
var _ = strconv.Itoa
