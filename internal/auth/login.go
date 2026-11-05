package auth

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/coreos/go-oidc/v3/oidc"
	"golang.org/x/oauth2"

	"github.com/kucukkanat/bunker/internal/core"
)

// ProviderConfig is the identity provider configuration.
type ProviderConfig struct {
	Kind           string // entra | google | oidc
	TenantID       string
	Issuer         string
	ClientID       string
	ClientSecret   string
	AllowedDomains []string
	AllowedGroups  []string
	AllowGuests    bool
}

// Provider returns the active provider config (env overrides settings).
func Provider() ProviderConfig {
	p := ProviderConfig{
		Kind:         app.Setting("auth.provider"),
		TenantID:     firstNonEmpty(core.Env("ENTRA_TENANT_ID", ""), app.Setting("auth.tenant_id")),
		Issuer:       app.Setting("auth.issuer"),
		ClientID:     firstNonEmpty(core.Env("ENTRA_CLIENT_ID", ""), app.Setting("auth.client_id")),
		ClientSecret: firstNonEmpty(core.Env("ENTRA_CLIENT_SECRET", ""), app.SecretSetting("auth.client_secret")),
		AllowGuests:  app.Setting("auth.allow_guests") == "true" || core.EnvBool("ALLOW_GUESTS", false),
	}
	if p.Kind == "" && p.TenantID != "" {
		p.Kind = "entra"
	}
	p.AllowedDomains = splitList(app.Setting("auth.allowed_domains"))
	p.AllowedGroups = splitList(firstNonEmpty(core.Env("ALLOWED_GROUPS", ""), app.Setting("auth.allowed_groups")))
	switch p.Kind {
	case "entra":
		p.Issuer = "https://login.microsoftonline.com/" + p.TenantID + "/v2.0"
	case "google":
		p.Issuer = "https://accounts.google.com"
	}
	return p
}

// Validate rejects unsafe configurations (e.g. multi-tenant Entra, or
// Google without a Workspace domain restriction).
func (p ProviderConfig) Validate() error {
	if p.ClientID == "" || p.ClientSecret == "" {
		return errors.New("client ID and secret are required")
	}
	switch p.Kind {
	case "entra":
		t := strings.ToLower(p.TenantID)
		if t == "" {
			return errors.New("tenant ID is required")
		}
		if t == "common" || t == "organizations" || t == "consumers" {
			return errors.New("multi-tenant sign-in is not allowed: use your own tenant ID")
		}
	case "google":
		if len(p.AllowedDomains) == 0 {
			return errors.New("Google sign-in needs your Workspace domain (e.g. corp.com) so personal Gmail accounts can't sign in")
		}
		for _, d := range p.AllowedDomains {
			if publicDomain(d) {
				return fmt.Errorf("%s is a public email domain", d)
			}
		}
	case "oidc":
		if !strings.HasPrefix(p.Issuer, "https://") {
			return errors.New("issuer must be an https URL")
		}
		for _, d := range p.AllowedDomains {
			if publicDomain(d) {
				return fmt.Errorf("%s is a public email domain", d)
			}
		}
	default:
		return errors.New("choose a provider: entra, google or oidc")
	}
	return nil
}

func publicDomain(d string) bool {
	switch strings.ToLower(strings.TrimSpace(d)) {
	case "gmail.com", "googlemail.com", "outlook.com", "hotmail.com", "live.com", "yahoo.com", "icloud.com", "me.com", "aol.com", "proton.me", "protonmail.com", "gmx.com", "yandex.com", "mail.com":
		return true
	}
	return false
}

// Configured reports whether sign-in is ready.
func Configured() bool {
	if app.Cfg.Dev {
		return true
	}
	return SSOConfigured() || localReady()
}

type oidcClient struct {
	cfg      ProviderConfig
	provider *oidc.Provider
	verifier *oidc.IDTokenVerifier
	oauth    *oauth2.Config
}

var (
	clientMu  sync.Mutex
	clientKey string
	client    *oidcClient
)

func getClient(ctx context.Context) (*oidcClient, error) {
	p := Provider()
	if err := p.Validate(); err != nil {
		return nil, err
	}
	key := p.Kind + "|" + p.Issuer + "|" + p.ClientID + "|" + p.ClientSecret
	clientMu.Lock()
	defer clientMu.Unlock()
	if client != nil && clientKey == key {
		return client, nil
	}
	dctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	prov, err := oidc.NewProvider(dctx, p.Issuer)
	if err != nil {
		return nil, fmt.Errorf("could not reach identity provider: %w", err)
	}
	scopes := []string{oidc.ScopeOpenID, "profile", "email", oidc.ScopeOfflineAccess}
	if p.Kind == "entra" {
		scopes = append(scopes, entraScopes()...)
	}
	c := &oidcClient{
		cfg:      p,
		provider: prov,
		verifier: prov.Verifier(&oidc.Config{ClientID: p.ClientID}),
		oauth: &oauth2.Config{
			ClientID: p.ClientID, ClientSecret: p.ClientSecret,
			Endpoint: prov.Endpoint(), RedirectURL: app.Cfg.PublicURL() + "/auth/callback", Scopes: scopes,
		},
	}
	client, clientKey = c, key
	return c, nil
}

// entraScopes are the delegated Graph scopes requested at sign-in.
// User.Read is always consentable; the others need admin consent and
// enable group names and the people picker.
func entraScopes() []string {
	s := firstNonEmpty(core.Env("ENTRA_SCOPES", ""), app.Setting("auth.entra_scopes"))
	if s == "" {
		s = "User.Read User.ReadBasic.All GroupMember.Read.All"
	}
	return strings.Fields(strings.ReplaceAll(s, ",", " "))
}

type loginState struct {
	State    string `json:"s"`
	Nonce    string `json:"n"`
	Verifier string `json:"v"`
	Next     string `json:"x"`
	Exp      int64  `json:"e"`
	Extra    string `json:"xs,omitempty"` // extra scopes (incremental consent)
}

func registerLogin(a *core.App) {
	a.Mux.HandleFunc("GET /auth/login", handleLogin)
	a.Mux.HandleFunc("GET /auth/callback", handleCallback)
	a.Mux.HandleFunc("POST /auth/logout", handleLogout)
	a.Mux.HandleFunc("GET /auth/dev", handleDev)
}

// SafeNext returns next if it's a same-site relative path. Browsers drop
// tabs and newlines inside URLs and read "\\" as "/", so "/\t/evil.com" or
// "/\\evil.com" would leave the site: any control character or backslash
// is refused.
func SafeNext(next string) string {
	if next == "" || !strings.HasPrefix(next, "/") || strings.HasPrefix(next, "//") || strings.ContainsRune(next, '\\') {
		return "/"
	}
	for i := 0; i < len(next); i++ {
		if next[i] < 0x20 || next[i] == 0x7f {
			return "/"
		}
	}
	return next
}

// LoginURL returns the sign-in URL that returns to next.
func LoginURL(next string) string {
	return "/auth/login?next=" + url.QueryEscape(SafeNext(next))
}

func handleLogin(w http.ResponseWriter, r *http.Request) {
	next := SafeNext(r.URL.Query().Get("next"))
	if app.Cfg.Dev {
		http.Redirect(w, r, next, http.StatusFound)
		return
	}
	if !Configured() {
		http.Redirect(w, r, "/setup", http.StatusFound)
		return
	}
	// Built-in accounts: show the sign-in page (with an SSO button when
	// company sign-in is configured too) unless SSO was explicitly chosen.
	if localReady() && (r.URL.Query().Get("sso") != "1" || !SSOConfigured()) && r.URL.Query().Get("scopes") == "" {
		renderLogin(w, r, next, r.URL.Query().Get("login_hint"), "")
		return
	}
	c, err := getClient(r.Context())
	if err != nil {
		authError(w, r, "Sign-in is misconfigured", err.Error())
		return
	}
	extra := strings.TrimSpace(r.URL.Query().Get("scopes"))
	st := loginState{State: core.RandomToken(16), Nonce: core.RandomToken(16), Verifier: oauth2.GenerateVerifier(), Next: next, Exp: core.Now() + 600, Extra: extra}
	b, _ := json.Marshal(st)
	enc, err := core.Encrypt(string(b))
	if err != nil {
		authError(w, r, "Sign-in failed", "could not create state")
		return
	}
	http.SetCookie(w, &http.Cookie{Name: "bunker_login", Value: enc, Path: "/auth/", HttpOnly: true, Secure: strings.HasPrefix(app.Cfg.PublicURL(), "https://"), SameSite: http.SameSiteLaxMode, MaxAge: 600})
	opts := []oauth2.AuthCodeOption{oidc.Nonce(st.Nonce), oauth2.S256ChallengeOption(st.Verifier)}
	if h := r.URL.Query().Get("login_hint"); h != "" {
		opts = append(opts, oauth2.SetAuthURLParam("login_hint", h))
	}
	cfg := *c.oauth
	if extra != "" {
		cfg.Scopes = append(append([]string(nil), cfg.Scopes...), strings.Fields(extra)...)
	}
	if c.cfg.Kind == "google" && len(c.cfg.AllowedDomains) == 1 {
		opts = append(opts, oauth2.SetAuthURLParam("hd", c.cfg.AllowedDomains[0]))
	}
	http.Redirect(w, r, cfg.AuthCodeURL(st.State, opts...), http.StatusFound)
}

func handleCallback(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	if e := q.Get("error"); e != "" {
		authError(w, r, "Sign-in was cancelled", q.Get("error_description"))
		return
	}
	ck, err := r.Cookie("bunker_login")
	if err != nil {
		authError(w, r, "Sign-in expired", "Please try again.")
		return
	}
	raw, err := core.Decrypt(ck.Value)
	var st loginState
	if err != nil || json.Unmarshal([]byte(raw), &st) != nil || st.Exp < core.Now() || st.State != q.Get("state") {
		authError(w, r, "Sign-in expired", "Please try again.")
		return
	}
	http.SetCookie(w, &http.Cookie{Name: "bunker_login", Value: "", Path: "/auth/", MaxAge: -1})
	c, err := getClient(r.Context())
	if err != nil {
		authError(w, r, "Sign-in is misconfigured", err.Error())
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
	defer cancel()
	cfg := *c.oauth
	if st.Extra != "" {
		cfg.Scopes = append(append([]string(nil), cfg.Scopes...), strings.Fields(st.Extra)...)
	}
	tok, err := cfg.Exchange(ctx, q.Get("code"), oauth2.VerifierOption(st.Verifier))
	if err != nil {
		authError(w, r, "Sign-in failed", "Could not exchange the authorization code. Check the client secret.")
		return
	}
	rawID, _ := tok.Extra("id_token").(string)
	idt, err := c.verifier.Verify(ctx, rawID)
	if err != nil || idt.Nonce != st.Nonce {
		authError(w, r, "Sign-in failed", "The identity token could not be verified.")
		return
	}
	var claims struct {
		Sub           string   `json:"sub"`
		OID           string   `json:"oid"`
		TID           string   `json:"tid"`
		Email         string   `json:"email"`
		EmailVerified *bool    `json:"email_verified"`
		PreferredName string   `json:"preferred_username"`
		Name          string   `json:"name"`
		HD            string   `json:"hd"`
		Groups        []string `json:"groups"`
		IDTyp         string   `json:"idtyp"`
		Acct          *int     `json:"acct"` // Entra: 1 = guest
		IDP           string   `json:"idp"`  // Entra: home identity provider when not this tenant
	}
	if err := idt.Claims(&claims); err != nil {
		authError(w, r, "Sign-in failed", "Unreadable identity token.")
		return
	}
	p := c.cfg
	subject := p.Kind + ":" + claims.Sub
	email := strings.ToLower(firstNonEmpty(claims.Email, claims.PreferredName))
	var groups []core.Group
	switch p.Kind {
	case "entra":
		if !strings.EqualFold(claims.TID, p.TenantID) && !strings.Contains(p.TenantID, ".") {
			authError(w, r, "Access denied", "Your account belongs to a different organization.")
			return
		}
		if claims.OID != "" {
			subject = "entra:" + claims.OID
		}
		if entraGuest(claims.Acct, claims.IDP, claims.TID) && !p.AllowGuests {
			authError(w, r, "Access denied", "Guest accounts can't use Bunker.")
			return
		}
		groups = entraGroups(ctx, tok.AccessToken, claims.Groups)
	case "google":
		if !domainAllowed(claims.HD, p.AllowedDomains) {
			authError(w, r, "Access denied", "Sign in with your work account.")
			return
		}
	default:
		if claims.EmailVerified != nil && !*claims.EmailVerified {
			authError(w, r, "Access denied", "Your email address isn't verified.")
			return
		}
		if len(p.AllowedDomains) > 0 && !domainAllowed(emailDomain(email), p.AllowedDomains) {
			authError(w, r, "Access denied", "Sign in with your work account.")
			return
		}
		for _, g := range claims.Groups {
			groups = append(groups, core.Group{ID: g, Name: g})
		}
	}
	if email == "" {
		authError(w, r, "Sign-in failed", "Your account has no email address.")
		return
	}
	if len(p.AllowedGroups) > 0 {
		ok := false
		tmp := &core.User{Groups: groups}
		for _, g := range p.AllowedGroups {
			if tmp.InGroup(g) {
				ok = true
			}
		}
		if !ok {
			app.Audit(r.Context(), "auth.denied", 0, email, map[string]any{"reason": "not in allowed groups"})
			authError(w, r, "Access denied", "You're not in a group that can use Bunker. Ask your IT team.")
			return
		}
	}
	name := firstNonEmpty(claims.Name, email)
	u, err := upsertUser(ctx, subject, email, name, groups)
	if err != nil {
		authError(w, r, "Sign-in failed", err.Error())
		return
	}
	if p.Kind == "entra" {
		saveIDPToken(ctx, u.ID, tok, strings.Join(cfg.Scopes, " "))
	}
	if err := startSession(w, r, u); err != nil {
		authError(w, r, "Sign-in failed", "Could not start a session.")
		return
	}
	app.Audit(core.WithUser(r.Context(), u), "auth.login", 0, "", map[string]any{"provider": p.Kind})
	http.Redirect(w, r, SafeNext(st.Next), http.StatusFound)
}

func handleLogout(w http.ResponseWriter, r *http.Request) {
	if c, err := r.Cookie(cookieName); err == nil {
		h := core.HashToken(c.Value)
		app.DB.ExecContext(r.Context(), `DELETE FROM sessions WHERE id_hash=?`, h)
		cacheMu.Lock()
		delete(cache, "s:"+h)
		cacheMu.Unlock()
	}
	http.SetCookie(w, &http.Cookie{Name: cookieName, Value: "", Path: "/", MaxAge: -1, HttpOnly: true})
	if strings.Contains(r.Header.Get("Accept"), "application/json") || r.Header.Get("X-Bunker") != "" {
		core.JSON(w, 200, map[string]any{"ok": true})
		return
	}
	http.Redirect(w, r, "/auth/signed-out", http.StatusSeeOther)
}

// handleDev switches identity in dev mode: /auth/dev?as=ada@example.com&name=Ada&groups=Finance,HR
func handleDev(w http.ResponseWriter, r *http.Request) {
	if !app.Cfg.Dev {
		http.NotFound(w, r)
		return
	}
	q := r.URL.Query()
	email := strings.ToLower(firstNonEmpty(q.Get("as"), "you@localhost"))
	name := q.Get("name")
	var groups []core.Group
	for _, g := range splitList(q.Get("groups")) {
		groups = append(groups, core.Group{ID: g, Name: g})
	}
	// Switching back to someone who already exists (e.g. a `bunker demo`
	// teammate) keeps their name and groups unless new ones are given.
	var oldName, oldGroups string
	if app.DB.QueryRowContext(r.Context(), `SELECT name, groups_json FROM users WHERE subject=?`, "dev:"+email).Scan(&oldName, &oldGroups) == nil {
		if name == "" {
			name = oldName
		}
		if !q.Has("groups") {
			json.Unmarshal([]byte(oldGroups), &groups)
		}
	}
	name = firstNonEmpty(name, strings.Split(email, "@")[0])
	u, err := upsertUser(r.Context(), "dev:"+email, email, name, groups)
	if err != nil {
		core.Err(w, err)
		return
	}
	if err := startSession(w, r, u); err != nil {
		core.Err(w, err)
		return
	}
	http.Redirect(w, r, SafeNext(q.Get("next")), http.StatusFound)
}

func authError(w http.ResponseWriter, r *http.Request, title, detail string) {
	app.Log.Warn("auth error", "title", title, "detail", detail)
	authPage(w, http.StatusForbidden, title, sentence(detail), "/auth/login", "Try again")
}

func htmlEsc(s string) string {
	return strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;", `"`, "&quot;", "'", "&#39;").Replace(s)
}

func domainAllowed(d string, allowed []string) bool {
	for _, a := range allowed {
		if strings.EqualFold(strings.TrimSpace(a), d) {
			return true
		}
	}
	return false
}

func emailDomain(e string) string {
	if i := strings.LastIndex(e, "@"); i >= 0 {
		return e[i+1:]
	}
	return ""
}

func splitList(s string) []string {
	var out []string
	for _, p := range strings.FieldsFunc(s, func(r rune) bool { return r == ',' || r == '\n' || r == ';' }) {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if v != "" {
			return v
		}
	}
	return ""
}

// entraGuest reports whether an Entra ID token belongs to a guest (B2B)
// account. acct is only sent when configured as an optional claim, so idp
// (present when the user authenticated at another tenant or provider)
// counts too.
func entraGuest(acct *int, idp, tenantID string) bool {
	if acct != nil && *acct == 1 {
		return true
	}
	return idp != "" && (tenantID == "" || !strings.Contains(strings.ToLower(idp), strings.ToLower(tenantID)))
}
