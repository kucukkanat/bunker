// Package entra registers Bunker in a Microsoft Entra ID tenant
// automatically. An administrator signs in once with a device code (using
// Microsoft's public "Microsoft Graph Command Line Tools" client), and
// Bunker then creates its own single-tenant app registration, a client
// secret, the service principal and tenant-wide consent for the delegated
// permissions it needs, and saves the result as its sign-in settings. The
// administrator's Graph token is used for those few calls only and is
// never stored.
package entra

import (
	"bytes"
	"context"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/kucukkanat/bunker/internal/auth"
	"github.com/kucukkanat/bunker/internal/core"
)

// Endpoints (overridable in tests).
var (
	LoginBase = "https://login.microsoftonline.com"
	GraphAPI  = "https://graph.microsoft.com/v1.0"
	// RetryDelay is the pause between retries while a new service
	// principal replicates across Entra.
	RetryDelay = 3 * time.Second
)

const (
	graphCLIClientID = "14d82eec-204b-4c2f-b7e8-296a70dab67e" // Microsoft Graph Command Line Tools
	graphAppID       = "00000003-0000-0000-c000-000000000000" // Microsoft Graph
	deviceScopes     = "https://graph.microsoft.com/Application.ReadWrite.All https://graph.microsoft.com/DelegatedPermissionGrant.ReadWrite.All https://graph.microsoft.com/User.Read offline_access"
)

// Delegated Graph permissions Bunker asks for, with their well-known IDs.
// IDs are resolved at runtime from the Graph service principal; these are
// the fallback (and are checked against what Graph returns).
var Permissions = []struct{ Name, ID string }{
	{"openid", "37f7f235-527c-4136-accd-4a02d197296e"},
	{"profile", "14dad69e-099b-42c9-810b-d002981feec1"},
	{"email", "64a6cdd6-aab1-4aaf-94b8-3cc8405e90d0"},
	{"offline_access", "7427e0e9-2fba-42fe-b0c0-848c9e6a8182"},
	{"User.Read", "e1fe6dd8-ba31-4d61-89e7-88639da4683d"},
	{"User.ReadBasic.All", "b340eb25-3456-403f-be2f-af7a0d370277"},
	{"GroupMember.Read.All", "bc024368-1153-4739-b217-4326f2e966d0"},
}

var httpc = &http.Client{Timeout: 30 * time.Second}

func init() {
	core.Register(core.Module{Name: "entra-setup", Order: 15, Init: initEntra})
}

var app *core.App

type flow struct {
	deviceCode string
	interval   time.Duration
	expires    time.Time
	nextPoll   time.Time
	busy       bool
}

var (
	flowMu sync.Mutex
	flows  = map[string]*flow{}
)

func loopback(r *http.Request) bool { return auth.IsLocalRequest(r) }

// authorize: admins always; before setup, anyone with the setup code (or on
// the server itself).
func authorize(r *http.Request, code string) error {
	u := core.UserFrom(r.Context())
	if u != nil && u.IsAdmin {
		return nil
	}
	if auth.Configured() {
		return core.Forbidden("Bunker is already set up; sign in as an admin to connect Microsoft Entra ID")
	}
	if loopback(r) {
		return nil
	}
	if subtle.ConstantTimeCompare([]byte(strings.ToUpper(strings.TrimSpace(code))), []byte(strings.ToUpper(auth.SetupCode()))) != 1 {
		app.Audit(r.Context(), "setup.denied", 0, "entra.auto", nil)
		return core.Forbidden("that setup code isn't right: check the server log")
	}
	return nil
}

func initEntra(a *core.App) error {
	app = a
	a.Handle(core.Op{Name: "setup.entra.auto.start", Method: "POST", Path: "/api/v1/setup/entra/auto/start", Access: core.AccessPublic,
		Summary: "Start automatic Microsoft Entra ID setup (an admin signs in with a code)",
		Params:  []core.Param{{Name: "code", In: "body", Desc: "Setup code (not needed for admins)"}},
		Fn: func(r *http.Request) (any, error) {
			var in struct{ Code string }
			if r.ContentLength != 0 {
				core.Decode(r, &in)
			}
			if err := authorize(r, in.Code); err != nil {
				return nil, err
			}
			if a.Cfg.URL == "" {
				return nil, core.BadRequest("Bunker doesn't know its own address yet. Set BUNKER_URL (e.g. https://bunker.corp.com) and restart, then try again.")
			}
			return startFlow(r.Context())
		}})
	a.Handle(core.Op{Name: "setup.entra.auto.poll", Method: "POST", Path: "/api/v1/setup/entra/auto/poll", Access: core.AccessPublic,
		Summary: "Check whether the admin finished signing in; when they have, Bunker registers itself",
		Params:  []core.Param{{Name: "flowId", In: "body", Required: true}, {Name: "code", In: "body"}},
		Fn: func(r *http.Request) (any, error) {
			var in struct{ FlowID, Code string }
			if err := core.Decode(r, &in); err != nil {
				return nil, err
			}
			if err := authorize(r, in.Code); err != nil {
				return nil, err
			}
			return poll(r, in.FlowID)
		}})
	a.Handle(core.Op{Name: "setup.entra.script", Method: "GET", Path: "/api/v1/setup/entra/script", Access: core.AccessPublic,
		Summary: "Azure Cloud Shell script that registers Bunker (fallback when automatic setup is blocked)",
		Fn: func(r *http.Request) (any, error) {
			base := a.Cfg.URL
			if base == "" {
				base = "https://bunker.example.com"
			}
			return map[string]any{"script": CloudShellScript(base), "portalUrl": "https://entra.microsoft.com/#view/Microsoft_AAD_RegisteredApps/CreateApplicationBlade",
				"redirectUri": base + "/auth/callback", "permissions": permNames()}, nil
		}})
	go func() {
		for range time.Tick(time.Minute) {
			flowMu.Lock()
			for k, f := range flows {
				if time.Now().After(f.expires) {
					delete(flows, k)
				}
			}
			flowMu.Unlock()
		}
	}()
	return nil
}

func permNames() []string {
	var out []string
	for _, p := range Permissions {
		out = append(out, p.Name)
	}
	return out
}

// ---- device code flow ----

type oauthErr struct {
	Error       string `json:"error"`
	Description string `json:"error_description"`
	Codes       []int  `json:"error_codes"`
}

func postForm(ctx context.Context, u string, v url.Values, out any) (int, error) {
	req, _ := http.NewRequestWithContext(ctx, "POST", u, strings.NewReader(v.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	res, err := httpc.Do(req)
	if err != nil {
		return 0, core.Unavailable("Bunker couldn't reach Microsoft (login.microsoftonline.com). Check the server's internet access.")
	}
	defer res.Body.Close()
	b, _ := io.ReadAll(io.LimitReader(res.Body, 1<<20))
	return res.StatusCode, json.Unmarshal(b, out)
}

func startFlow(ctx context.Context) (any, error) {
	var dc struct {
		DeviceCode      string `json:"device_code"`
		UserCode        string `json:"user_code"`
		VerificationURI string `json:"verification_uri"`
		ExpiresIn       int    `json:"expires_in"`
		Interval        int    `json:"interval"`
		Message         string `json:"message"`
		oauthErr
	}
	status, err := postForm(ctx, LoginBase+"/organizations/oauth2/v2.0/devicecode", url.Values{"client_id": {graphCLIClientID}, "scope": {deviceScopes}}, &dc)
	if err != nil {
		return nil, err
	}
	if status != 200 || dc.DeviceCode == "" {
		return nil, friendlyOAuth(dc.oauthErr)
	}
	if dc.Interval <= 0 {
		dc.Interval = 5
	}
	if dc.ExpiresIn <= 0 {
		dc.ExpiresIn = 900
	}
	id := core.RandomToken(18)
	flowMu.Lock()
	if len(flows) > 100 {
		flows = map[string]*flow{}
	}
	flows[id] = &flow{deviceCode: dc.DeviceCode, interval: time.Duration(dc.Interval) * time.Second, expires: time.Now().Add(time.Duration(dc.ExpiresIn) * time.Second)}
	flowMu.Unlock()
	app.Audit(ctx, "setup.entra.start", 0, "entra.auto", nil)
	return map[string]any{"flowId": id, "userCode": dc.UserCode, "verificationUri": dc.VerificationURI, "message": dc.Message,
		"expiresIn": dc.ExpiresIn, "interval": dc.Interval,
		"hint": "Sign in as a Global Administrator, Application Administrator or Cloud Application Administrator."}, nil
}

func friendlyOAuth(e oauthErr) error {
	d := e.Description
	if i := strings.Index(d, "\r\n"); i > 0 {
		d = d[:i]
	}
	has := func(code string) bool { return strings.Contains(e.Description, code) }
	switch {
	case e.Error == "expired_token":
		return core.NewErr(410, "expired", "The sign-in code expired. Start again and enter the new code within 15 minutes.")
	case e.Error == "authorization_declined" || e.Error == "access_denied":
		return core.NewErr(403, "declined", "Sign-in was cancelled or permission wasn't granted. Start again and accept the request.")
	case has("AADSTS7000112") || has("AADSTS700016") || has("AADSTS65002") || has("AADSTS50105"):
		return core.NewErr(403, "blocked", "Your organization blocks the Microsoft Graph Command Line Tools app that automatic setup uses. Use the Azure Cloud Shell script instead (it only needs the same admin role).")
	case has("AADSTS53003") || has("AADSTS50076") || has("AADSTS50079"):
		return core.NewErr(403, "conditional_access", "A Conditional Access policy blocked this sign-in. Try from a compliant device, or use the Azure Cloud Shell script.")
	case has("AADSTS65001"):
		return core.NewErr(403, "consent", "The permissions weren't approved. Sign in as an administrator who can consent for the organization.")
	case has("AADSTS50020") || has("AADSTS90072"):
		return core.NewErr(403, "wrong_tenant", "That account is a guest in this organization. Sign in with an account from the organization itself.")
	}
	if d == "" {
		d = e.Error
	}
	return core.NewErr(502, "entra_error", "Microsoft sign-in failed: "+d)
}

func poll(r *http.Request, id string) (any, error) {
	flowMu.Lock()
	f := flows[id]
	if f == nil {
		flowMu.Unlock()
		return nil, core.NewErr(410, "expired", "That sign-in has expired or finished. Start again.")
	}
	if f.busy {
		flowMu.Unlock()
		return map[string]any{"pending": true, "status": "registering", "interval": int(f.interval.Seconds())}, nil
	}
	if time.Now().Before(f.nextPoll) {
		flowMu.Unlock()
		return map[string]any{"pending": true, "status": "waiting", "interval": int(f.interval.Seconds())}, nil
	}
	f.nextPoll = time.Now().Add(f.interval)
	dc := f.deviceCode
	flowMu.Unlock()

	var tok struct {
		AccessToken string `json:"access_token"`
		IDToken     string `json:"id_token"`
		oauthErr
	}
	ctx := r.Context()
	_, err := postForm(ctx, LoginBase+"/organizations/oauth2/v2.0/token", url.Values{
		"grant_type": {"urn:ietf:params:oauth:grant-type:device_code"}, "client_id": {graphCLIClientID}, "device_code": {dc}}, &tok)
	if err != nil {
		return nil, err
	}
	if tok.AccessToken == "" {
		switch tok.Error {
		case "authorization_pending":
			return map[string]any{"pending": true, "status": "waiting", "interval": int(f.interval.Seconds())}, nil
		case "slow_down":
			flowMu.Lock()
			f.interval += 5 * time.Second
			flowMu.Unlock()
			return map[string]any{"pending": true, "status": "waiting", "interval": int(f.interval.Seconds())}, nil
		}
		removeFlow(id)
		return nil, friendlyOAuth(tok.oauthErr)
	}
	flowMu.Lock()
	f.busy = true
	flowMu.Unlock()
	defer removeFlow(id)
	claims := jwtClaims(tok.AccessToken)
	idc := jwtClaims(tok.IDToken)
	tid, _ := claims["tid"].(string)
	if tid == "" {
		tid, _ = idc["tid"].(string)
	}
	if tid == "" {
		return nil, core.NewErr(502, "entra_error", "Microsoft didn't say which organization you signed in to. Try again.")
	}
	if tid == "9188040d-6c67-4c5b-b112-36a304b66dad" {
		return nil, core.NewErr(403, "personal_account", "That's a personal Microsoft account. Sign in with your work account.")
	}
	adminEmail := firstString(claims, "upn", "unique_name", "preferred_username", "email")
	if adminEmail == "" {
		adminEmail = firstString(idc, "preferred_username", "email", "upn")
	}
	res, err := register(context.WithoutCancel(ctx), tok.AccessToken, tid)
	tok.AccessToken, tok.IDToken = "", "" // the admin's token is never kept
	if err != nil {
		app.Audit(ctx, "setup.entra.failed", 0, "entra.auto", map[string]any{"tenant": tid, "error": err.Error()})
		return nil, err
	}
	// Save exactly what the manual setup saves (see auth/setup.go).
	a := app
	sctx := context.WithoutCancel(ctx)
	a.SetSetting(sctx, "auth.provider", "entra")
	a.SetSetting(sctx, "auth.tenant_id", tid)
	a.SetSetting(sctx, "auth.issuer", "https://login.microsoftonline.com/"+tid+"/v2.0")
	a.SetSetting(sctx, "auth.client_id", res.ClientID)
	a.SetSecretSetting("auth.client_secret", res.Secret)
	a.SetSetting(sctx, "auth.client_secret_expires", res.SecretExpires.Format(time.RFC3339))
	a.SetSetting(sctx, "auth.entra_app_object_id", res.AppObjectID)
	if a.Setting("admins") == "" && adminEmail != "" && strings.Contains(adminEmail, "@") {
		a.SetSetting(sctx, "admins", strings.ToLower(adminEmail))
	}
	a.SetSetting(sctx, "setup.code", "")
	os.Remove(filepath.Join(a.Cfg.DataDir, "setup-code.txt"))
	a.Audit(sctx, "admin.config", 0, "auth", map[string]any{"provider": "entra", "auto": true, "tenant": tid, "clientId": res.ClientID, "by": adminEmail, "warnings": res.Warnings})
	return map[string]any{"done": true, "tenantId": tid, "clientId": res.ClientID, "secretExpires": res.SecretExpires.Format("2006-01-02"),
		"loginUrl": "/auth/login", "warnings": res.Warnings}, nil
}

func removeFlow(id string) {
	flowMu.Lock()
	delete(flows, id)
	flowMu.Unlock()
}

func firstString(m map[string]any, keys ...string) string {
	for _, k := range keys {
		if s, ok := m[k].(string); ok && s != "" {
			return s
		}
	}
	return ""
}

// jwtClaims decodes a JWT payload without verifying it. Safe here: the
// token came straight from Microsoft over TLS in answer to our request,
// and it is only read for the tenant ID and the admin's name.
func jwtClaims(t string) map[string]any {
	parts := strings.Split(t, ".")
	if len(parts) < 2 {
		return map[string]any{}
	}
	b, err := base64.RawURLEncoding.DecodeString(strings.TrimRight(parts[1], "="))
	if err != nil {
		return map[string]any{}
	}
	var m map[string]any
	json.Unmarshal(b, &m)
	if m == nil {
		m = map[string]any{}
	}
	return m
}

// ---- Graph provisioning ----

type graphError struct {
	Status  int
	Code    string
	Message string
}

func (e *graphError) Error() string {
	return fmt.Sprintf("graph %d %s: %s", e.Status, e.Code, e.Message)
}

func gcall(ctx context.Context, tok, method, p string, body any, out any) error {
	var rd io.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		rd = bytes.NewReader(b)
	}
	req, _ := http.NewRequestWithContext(ctx, method, GraphAPI+p, rd)
	req.Header.Set("Authorization", "Bearer "+tok)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	res, err := httpc.Do(req)
	if err != nil {
		return core.Unavailable("Bunker couldn't reach Microsoft Graph (graph.microsoft.com). Check the server's internet access.")
	}
	defer res.Body.Close()
	b, _ := io.ReadAll(io.LimitReader(res.Body, 4<<20))
	if res.StatusCode >= 300 {
		var ge struct {
			Error struct{ Code, Message string } `json:"error"`
		}
		json.Unmarshal(b, &ge)
		return &graphError{Status: res.StatusCode, Code: ge.Error.Code, Message: ge.Error.Message}
	}
	if out != nil && len(b) > 0 {
		return json.Unmarshal(b, out)
	}
	return nil
}

// friendlyGraph turns Graph failures into next steps for a non-expert.
func friendlyGraph(step string, err error) error {
	var ge *graphError
	if !errors.As(err, &ge) {
		return err
	}
	switch {
	case ge.Status == 401:
		return core.NewErr(403, "entra_denied", "Microsoft rejected the sign-in while "+step+". Start again.")
	case ge.Status == 403 || ge.Code == "Authorization_RequestDenied":
		return core.NewErr(403, "entra_denied", "Your account can't "+step+". Ask a Global Administrator, Application Administrator or Cloud Application Administrator to run this setup, or to give you that role.")
	case ge.Code == "Request_BadRequest" && strings.Contains(ge.Message, "redirect"):
		return core.NewErr(400, "entra_error", "Microsoft didn't accept Bunker's address as a sign-in redirect ("+ge.Message+"). Check BUNKER_URL: it must be https:// (or http://localhost).")
	case ge.Status == 429:
		return core.NewErr(429, "throttled", "Microsoft is busy. Wait a minute and try again.")
	}
	return core.NewErr(502, "entra_error", "Microsoft Graph refused to "+step+": "+ge.Message)
}

type registered struct {
	ClientID      string
	AppObjectID   string
	Secret        string
	SecretExpires time.Time
	Warnings      []string
}

// retry repeats fn while Entra is still replicating a new object.
func retry(ctx context.Context, fn func() error) error {
	var err error
	for i := 0; i < 10; i++ {
		if err = fn(); err == nil {
			return nil
		}
		var ge *graphError
		if !errors.As(err, &ge) || (ge.Status != 400 && ge.Status != 404 && ge.Status != 409 && ge.Status < 500) {
			return err
		}
		if ge.Status == 400 && !strings.Contains(strings.ToLower(ge.Message), "does not exist") && !strings.Contains(strings.ToLower(ge.Message), "not found") && !strings.Contains(strings.ToLower(ge.Message), "replica") && ge.Code != "Request_ResourceNotFound" {
			return err
		}
		select {
		case <-time.After(RetryDelay):
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	return err
}

func register(ctx context.Context, tok, tid string) (*registered, error) {
	out := &registered{}
	// 1. Microsoft Graph's service principal: its object ID (for consent)
	//    and the permission IDs (verified against the well-known ones).
	var gsp struct {
		ID     string `json:"id"`
		Scopes []struct {
			ID    string `json:"id"`
			Value string `json:"value"`
		} `json:"oauth2PermissionScopes"`
	}
	if err := gcall(ctx, tok, "GET", "/servicePrincipals(appId='"+graphAppID+"')?$select=id,oauth2PermissionScopes", nil, &gsp); err != nil {
		return nil, friendlyGraph("read Microsoft Graph's permissions", err)
	}
	ids := map[string]string{}
	for _, s := range gsp.Scopes {
		ids[s.Value] = s.ID
	}
	var access []map[string]string
	for _, p := range Permissions {
		id := p.ID
		if got, ok := ids[p.Name]; ok && got != "" {
			if !strings.EqualFold(got, p.ID) {
				out.Warnings = append(out.Warnings, fmt.Sprintf("%s has ID %s in this tenant (expected %s); using the tenant's", p.Name, got, p.ID))
			}
			id = got
		}
		access = append(access, map[string]string{"id": id, "type": "Scope"})
	}
	// 2. The app registration (single tenant).
	name := "Bunker"
	if u, err := url.Parse(app.Cfg.URL); err == nil && u.Host != "" {
		name = "Bunker (" + u.Host + ")"
	}
	appBody := map[string]any{
		"displayName":    name,
		"signInAudience": "AzureADMyOrg",
		"web": map[string]any{
			"redirectUris":          []string{app.Cfg.URL + "/auth/callback"},
			"homePageUrl":           app.Cfg.URL,
			"logoutUrl":             app.Cfg.URL + "/auth/logout",
			"implicitGrantSettings": map[string]bool{"enableIdTokenIssuance": false, "enableAccessTokenIssuance": false},
		},
		"groupMembershipClaims": "SecurityGroup",
		"optionalClaims": map[string]any{"idToken": []map[string]any{
			{"name": "email", "essential": false}, {"name": "preferred_username", "essential": false}, {"name": "groups", "essential": false},
		}},
		"requiredResourceAccess": []map[string]any{{"resourceAppId": graphAppID, "resourceAccess": access}},
		"notes":                  "Created by Bunker automatic setup. Bunker signs people in with this app and reads Microsoft 365 data with each viewer's own permissions.",
	}
	var created struct {
		ID    string `json:"id"`
		AppID string `json:"appId"`
	}
	if err := gcall(ctx, tok, "POST", "/applications", appBody, &created); err != nil {
		return nil, friendlyGraph("create apps", err)
	}
	if created.ID == "" || created.AppID == "" {
		return nil, core.NewErr(502, "entra_error", "Microsoft didn't return the new app's ID. Check App registrations for “"+name+"”.")
	}
	out.ClientID, out.AppObjectID = created.AppID, created.ID
	// 3. A client secret valid for 24 months.
	out.SecretExpires = time.Now().UTC().AddDate(2, 0, 0).Truncate(time.Second)
	var pw struct {
		SecretText string `json:"secretText"`
		EndDate    string `json:"endDateTime"`
	}
	err := retry(ctx, func() error {
		return gcall(ctx, tok, "POST", "/applications/"+url.PathEscape(created.ID)+"/addPassword",
			map[string]any{"passwordCredential": map[string]any{"displayName": "bunker-server", "endDateTime": out.SecretExpires.Format(time.RFC3339)}}, &pw)
	})
	if err != nil {
		return nil, friendlyGraph("add a client secret to the new app", err)
	}
	if pw.SecretText == "" {
		return nil, core.NewErr(502, "entra_error", "Microsoft didn't return the new client secret.")
	}
	out.Secret = pw.SecretText
	if t, err := time.Parse(time.RFC3339, pw.EndDate); err == nil {
		out.SecretExpires = t
	}
	// 4. The service principal (the app's identity in this tenant).
	var sp struct {
		ID string `json:"id"`
	}
	err = retry(ctx, func() error {
		return gcall(ctx, tok, "POST", "/servicePrincipals", map[string]any{"appId": created.AppID, "appRoleAssignmentRequired": false, "tags": []string{"WindowsAzureActiveDirectoryIntegratedApp"}}, &sp)
	})
	if err != nil {
		var ge *graphError
		if errors.As(err, &ge) && ge.Status == 409 {
			// Already exists (e.g. a retry): look it up.
			var l struct {
				Value []struct {
					ID string `json:"id"`
				} `json:"value"`
			}
			if gcall(ctx, tok, "GET", "/servicePrincipals?$filter="+url.QueryEscape("appId eq '"+created.AppID+"'")+"&$select=id", nil, &l) == nil && len(l.Value) > 0 {
				sp.ID = l.Value[0].ID
				err = nil
			}
		}
		if err != nil {
			return nil, friendlyGraph("create the app's service principal", err)
		}
	}
	// 5. Tenant-wide consent for the delegated permissions, so nobody sees
	//    a consent prompt on first sign-in.
	err = retry(ctx, func() error {
		return gcall(ctx, tok, "POST", "/oauth2PermissionGrants", map[string]any{
			"clientId": sp.ID, "consentType": "AllPrincipals", "resourceId": gsp.ID, "scope": strings.Join(permNames(), " "),
		}, nil)
	})
	if err != nil {
		var ge *graphError
		if errors.As(err, &ge) && (ge.Status == 403 || ge.Code == "Authorization_RequestDenied") {
			out.Warnings = append(out.Warnings, "Admin consent couldn't be granted with your role; people will be asked to consent on first sign-in. A Global Administrator can grant it under Enterprise applications → "+name+" → Permissions.")
		} else {
			return nil, friendlyGraph("grant consent for Bunker's permissions", err)
		}
	}
	return out, nil
}

// CloudShellScript is the fallback for tenants that block the Graph CLI
// app: paste into Azure Cloud Shell (Bash) as an admin.
func CloudShellScript(base string) string {
	var perms []string
	for _, p := range Permissions {
		perms = append(perms, p.ID+"=Scope")
	}
	return `# Bunker: register the sign-in app (paste into Azure Cloud Shell, Bash)
set -euo pipefail
NAME="Bunker"
REDIRECT="` + base + `/auth/callback"
APP_ID=$(az ad app create --display-name "$NAME" --sign-in-audience AzureADMyOrg \
  --web-redirect-uris "$REDIRECT" --enable-id-token-issuance false \
  --required-resource-accesses '[{"resourceAppId":"` + graphAppID + `","resourceAccess":[` + resourceAccessJSON() + `]}]' \
  --query appId -o tsv)
az ad app update --id "$APP_ID" --set groupMembershipClaims=SecurityGroup
SECRET=$(az ad app credential reset --id "$APP_ID" --display-name bunker-server --years 2 --query password -o tsv)
az ad sp create --id "$APP_ID" >/dev/null
sleep 20  # let Entra replicate the new app
az ad app permission admin-consent --id "$APP_ID"
TENANT=$(az account show --query tenantId -o tsv)
echo
echo "Paste these into Bunker's setup page:"
echo "  Tenant ID:     $TENANT"
echo "  Client ID:     $APP_ID"
echo "  Client secret: $SECRET"
# permissions: ` + strings.Join(perms, " ") + `
`
}

func resourceAccessJSON() string {
	var parts []string
	for _, p := range Permissions {
		parts = append(parts, `{"id":"`+p.ID+`","type":"Scope"}`)
	}
	return strings.Join(parts, ",")
}
