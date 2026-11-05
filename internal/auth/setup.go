package auth

import (
	"context"
	"crypto/subtle"
	"fmt"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/kucukkanat/bunker/internal/core"
)

// SetupCode returns the one-time code that protects the first-run wizard.
func SetupCode() string {
	if v := core.Env("SETUP_CODE", ""); v != "" {
		return v
	}
	if v := app.Setting("setup.code"); v != "" {
		return v
	}
	code := userCode()
	app.SetSetting(context.Background(), "setup.code", code)
	return code
}

func initSetup(a *core.App) {
	if !a.Cfg.Dev && !Configured() {
		code := SetupCode()
		os.WriteFile(filepath.Join(a.Cfg.DataDir, "setup-code.txt"), []byte(code+"\n"), 0o600)
		url := a.Cfg.URL
		if url == "" {
			if strings.HasPrefix(a.Cfg.Listen, ":") {
				url = "http://localhost" + a.Cfg.Listen
			} else {
				url = "http://" + a.Cfg.Listen
			}
		}
		banner := fmt.Sprintf("\n\n  ┌──────────────────────────────────────────────┐\n  │  Bunker is ready to set up.                  │\n  │                                              │\n  │  Open:  %-37s│\n  │  Code:  %-37s│\n  └──────────────────────────────────────────────┘\n\n", url+"/setup", code)
		fmt.Fprint(os.Stderr, banner)
		a.Log.Info("setup required", "url", url+"/setup", "code", code)
	}
	a.Handle(core.Op{Name: "setup.status", Method: "GET", Path: "/api/v1/setup", Summary: "First-run setup status", Access: core.AccessPublic, Hidden: true,
		Fn: func(r *http.Request) (any, error) {
			p := Provider()
			return map[string]any{
				"configured":  Configured(),
				"sso":         SSOConfigured(),
				"local":       LocalEnabled(),
				"localReady":  localReady(),
				"codeNeeded":  !isLoopback(r),
				"dev":         a.Cfg.Dev,
				"provider":    p.Kind,
				"url":         a.Cfg.URL,
				"urlKnown":    a.Cfg.URL != "",
				"redirectUri": a.Cfg.URL + "/auth/callback",
				"version":     core.Version,
			}, nil
		}})
	a.Handle(core.Op{Name: "setup.configure", Method: "POST", Path: "/api/v1/setup", Summary: "Configure sign-in (first run with setup code, or admin)", Access: core.AccessPublic, Hidden: true,
		Params: []core.Param{
			{Name: "code", In: "body", Desc: "Setup code from the server log (not needed for admins)"},
			{Name: "provider", In: "body", Required: true, Enum: []string{"local", "entra", "google", "oidc"}, Desc: "local = built-in email + password accounts (no company sign-in needed)"},
			{Name: "email", In: "body", Desc: "local: your email"}, {Name: "name", In: "body", Desc: "local: your name"}, {Name: "password", In: "body", Desc: "local: your password (10+ characters)"},
			{Name: "tenantId", In: "body"}, {Name: "issuer", In: "body"},
			{Name: "clientId", In: "body", Required: true}, {Name: "clientSecret", In: "body", Required: true},
			{Name: "allowedDomains", In: "body"}, {Name: "admins", In: "body"},
		},
		Fn: func(r *http.Request) (any, error) {
			var in struct {
				Code, Provider, TenantID, Issuer, ClientID, ClientSecret, AllowedDomains, Admins, AllowedGroups string
				Email, Name, Password                                                                           string
				DryRun                                                                                          bool
			}
			if err := core.Decode(r, &in); err != nil {
				return nil, err
			}
			u := core.UserFrom(r.Context())
			isAdmin := u != nil && u.IsAdmin && !a.Cfg.Dev
			if Configured() && !isAdmin {
				return nil, core.Forbidden("already configured; sign in as an admin to change it")
			}
			// On the machine itself (localhost) the code isn't needed.
			if !isAdmin && !isLoopback(r) && subtle.ConstantTimeCompare([]byte(strings.ToUpper(strings.TrimSpace(in.Code))), []byte(strings.ToUpper(SetupCode()))) != 1 {
				a.Audit(r.Context(), "setup.denied", 0, "", nil)
				return nil, core.Forbidden("that setup code isn't right: check the server log")
			}
			if in.Provider == "local" {
				if isAdmin {
					return nil, core.BadRequest("built-in accounts are already available; invite people from Admin → People")
				}
				if in.DryRun {
					if err := validPassword(in.Password); err != nil {
						return nil, err
					}
					return map[string]any{"ok": true, "dryRun": true}, nil
				}
				u, err := createLocalUser(r.Context(), in.Email, in.Name, in.Password, true, nil)
				if err != nil {
					return nil, err
				}
				a.SetSetting(r.Context(), "auth.local", "on")
				a.SetSetting(r.Context(), "setup.code", "")
				RememberURL(r)
				os.Remove(filepath.Join(a.Cfg.DataDir, "setup-code.txt"))
				a.Audit(core.WithUser(r.Context(), u), "admin.config", 0, "auth", map[string]any{"provider": "local"})
				return map[string]any{"ok": true, "loginUrl": "/auth/login", "email": u.Email}, nil
			}
			if in.ClientSecret == "" && isAdmin {
				in.ClientSecret = Provider().ClientSecret
			}
			p := ProviderConfig{Kind: in.Provider, TenantID: strings.TrimSpace(in.TenantID), Issuer: strings.TrimRight(strings.TrimSpace(in.Issuer), "/"),
				ClientID: strings.TrimSpace(in.ClientID), ClientSecret: strings.TrimSpace(in.ClientSecret), AllowedDomains: splitList(in.AllowedDomains)}
			if p.Kind == "entra" {
				p.Issuer = "https://login.microsoftonline.com/" + p.TenantID + "/v2.0"
			} else if p.Kind == "google" {
				p.Issuer = "https://accounts.google.com"
			}
			if err := p.Validate(); err != nil {
				return nil, core.BadRequest(err.Error())
			}
			// Make sure the issuer is reachable before saving.
			if err := checkIssuer(r, p.Issuer); err != nil {
				return nil, core.BadRequest(err.Error())
			}
			if in.DryRun {
				return map[string]any{"ok": true, "dryRun": true}, nil
			}
			ctx := r.Context()
			a.SetSetting(ctx, "auth.provider", p.Kind)
			a.SetSetting(ctx, "auth.tenant_id", p.TenantID)
			a.SetSetting(ctx, "auth.issuer", p.Issuer)
			a.SetSetting(ctx, "auth.client_id", p.ClientID)
			a.SetSecretSetting("auth.client_secret", p.ClientSecret)
			a.SetSetting(ctx, "auth.allowed_domains", strings.Join(p.AllowedDomains, ","))
			if in.AllowedGroups != "" || isAdmin {
				a.SetSetting(ctx, "auth.allowed_groups", in.AllowedGroups)
			}
			if in.Admins != "" {
				a.SetSetting(ctx, "admins", in.Admins)
			}
			a.SetSetting(ctx, "setup.code", "")
			RememberURL(r)
			os.Remove(filepath.Join(a.Cfg.DataDir, "setup-code.txt"))
			clientMu.Lock()
			client = nil
			clientMu.Unlock()
			a.Audit(ctx, "admin.config", 0, "auth", map[string]any{"provider": p.Kind})
			return map[string]any{"ok": true, "loginUrl": "/auth/login"}, nil
		}})
}

func checkIssuer(r *http.Request, issuer string) error {
	req, _ := http.NewRequestWithContext(r.Context(), "GET", issuer+"/.well-known/openid-configuration", nil)
	res, err := httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("couldn't reach %s: is the server online?", issuer)
	}
	res.Body.Close()
	if res.StatusCode != 200 {
		return fmt.Errorf("%s doesn't look like a sign-in provider (HTTP %d). Check the tenant ID or issuer URL", issuer, res.StatusCode)
	}
	return nil
}

// RememberURL records the public URL from the (authorized) first-run setup
// request when BUNKER_URL wasn't given, so company sign-in gets a full
// redirect URI. Anonymous requests never set it (Host can be anything).
func RememberURL(r *http.Request) {
	if app.Cfg.URL != "" || isLoopback(r) {
		return
	}
	host := r.Host
	h := host
	if hh, _, err := net.SplitHostPort(host); err == nil {
		h = hh
	}
	if host == "" || strings.ContainsAny(host, "/\\ @") || net.ParseIP(strings.Trim(h, "[]")) != nil {
		return
	}
	scheme := "http"
	if r.TLS != nil || (app.Cfg.TrustProxy && r.Header.Get("X-Forwarded-Proto") == "https") {
		scheme = "https"
	}
	app.Cfg.URL = scheme + "://" + host
	app.SetSetting(r.Context(), "server.url", app.Cfg.URL)
}
