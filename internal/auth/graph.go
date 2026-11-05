package auth

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"golang.org/x/oauth2"

	"github.com/kucukkanat/bunker/internal/core"
)

// GraphBase is the Microsoft Graph endpoint (overridable in tests).
var GraphBase = "https://graph.microsoft.com/v1.0"

var httpClient = &http.Client{Timeout: 20 * time.Second}

func saveIDPToken(ctx context.Context, uid int64, tok *oauth2.Token, scopes string) {
	var ref, acc string
	if tok.RefreshToken != "" {
		ref, _ = core.Encrypt(tok.RefreshToken)
	}
	acc, _ = core.Encrypt(tok.AccessToken)
	app.DB.ExecContext(ctx, `INSERT INTO idp_tokens(user_id,refresh_enc,access_enc,access_expires_at,scopes,updated_at) VALUES(?,?,?,?,?,?)
ON CONFLICT(user_id) DO UPDATE SET refresh_enc=COALESCE(NULLIF(excluded.refresh_enc,''),idp_tokens.refresh_enc), access_enc=excluded.access_enc, access_expires_at=excluded.access_expires_at, scopes=excluded.scopes, updated_at=excluded.updated_at`,
		uid, ref, acc, tok.Expiry.Unix(), scopes, core.Now())
}

// AccessToken returns a delegated Microsoft Graph access token for the
// user, refreshing it if needed. extraScopes, if given, must already have
// been consented (see ConsentURL). Returns core error "consent_required"
// if the user must sign in again.
func AccessToken(ctx context.Context, uid int64, extraScopes ...string) (string, error) {
	if Provider().Kind != "entra" {
		return "", core.Unavailable("Microsoft 365 requires Microsoft Entra ID sign-in")
	}
	var refEnc, accEnc, scopes string
	var exp int64
	err := app.DB.QueryRowContext(ctx, `SELECT COALESCE(refresh_enc,''),COALESCE(access_enc,''),COALESCE(access_expires_at,0),scopes FROM idp_tokens WHERE user_id=?`, uid).Scan(&refEnc, &accEnc, &exp, &scopes)
	if err != nil {
		return "", core.NewErr(403, "consent_required", "sign in again to allow Microsoft 365 access")
	}
	for _, s := range extraScopes {
		if !strings.Contains(" "+scopes+" ", " "+s+" ") {
			return "", core.NewErr(403, "consent_required", "additional consent needed: "+s)
		}
	}
	if exp > core.Now()+60 && accEnc != "" {
		if t, err := core.Decrypt(accEnc); err == nil {
			return t, nil
		}
	}
	if refEnc == "" {
		return "", core.NewErr(403, "consent_required", "sign in again to allow Microsoft 365 access")
	}
	ref, err := core.Decrypt(refEnc)
	if err != nil {
		return "", core.NewErr(403, "consent_required", "sign in again")
	}
	c, err := getClient(ctx)
	if err != nil {
		return "", core.Unavailable(err.Error())
	}
	cfg := *c.oauth
	cfg.Scopes = strings.Fields(scopes)
	tok, err := cfg.TokenSource(ctx, &oauth2.Token{RefreshToken: ref}).Token()
	if err != nil {
		return "", core.NewErr(403, "consent_required", "sign in again to allow Microsoft 365 access")
	}
	saveIDPToken(ctx, uid, tok, scopes)
	return tok.AccessToken, nil
}

// ConsentURL is where to send a user to grant extra delegated scopes.
func ConsentURL(next string, scopes ...string) string {
	return "/auth/login?next=" + url.QueryEscape(SafeNext(next)) + "&scopes=" + url.QueryEscape(strings.Join(scopes, " "))
}

// Graph performs a GET against Microsoft Graph with the user's token.
func Graph(ctx context.Context, uid int64, path string, out any) error {
	tok, err := AccessToken(ctx, uid)
	if err != nil {
		return err
	}
	return graphGet(ctx, tok, path, out)
}

func graphGet(ctx context.Context, tok, path string, out any) error {
	req, _ := http.NewRequestWithContext(ctx, "GET", GraphBase+path, nil)
	req.Header.Set("Authorization", "Bearer "+tok)
	req.Header.Set("ConsistencyLevel", "eventual")
	res, err := httpClient.Do(req)
	if err != nil {
		return core.Unavailable("Microsoft Graph unreachable")
	}
	defer res.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(res.Body, 16<<20))
	if res.StatusCode >= 300 {
		var ge struct {
			Error struct{ Code, Message string } `json:"error"`
		}
		json.Unmarshal(body, &ge)
		if res.StatusCode == 401 || res.StatusCode == 403 {
			return core.NewErr(403, "forbidden", "Microsoft 365: "+ge.Error.Message)
		}
		if res.StatusCode == 404 {
			return core.NotFound("Microsoft 365: " + ge.Error.Message)
		}
		return core.NewErr(502, "upstream", fmt.Sprintf("Microsoft 365 error %d: %s", res.StatusCode, ge.Error.Message))
	}
	if out != nil {
		return json.Unmarshal(body, out)
	}
	return nil
}

// entraGroups resolves group names via Graph, falling back to the IDs
// from the token's groups claim.
func entraGroups(ctx context.Context, tok string, claimIDs []string) []core.Group {
	var res struct {
		Value []struct {
			ID          string `json:"id"`
			DisplayName string `json:"displayName"`
			Mail        string `json:"mail"`
		} `json:"value"`
		Next string `json:"@odata.nextLink"`
	}
	var out []core.Group
	path := "/me/transitiveMemberOf/microsoft.graph.group?$select=id,displayName,mail&$top=999"
	if err := graphGet(ctx, tok, path, &res); err == nil {
		for _, g := range res.Value {
			out = append(out, core.Group{ID: g.ID, Name: g.DisplayName})
		}
		return out
	}
	for _, id := range claimIDs {
		out = append(out, core.Group{ID: id, Name: id})
	}
	return out
}

type graphDirectory struct{}

// Search finds people and groups. With Entra it queries Microsoft Graph
// using the caller's own delegated token (so it respects directory
// visibility); otherwise it searches users who have signed in to Bunker.
func (graphDirectory) Search(ctx context.Context, u *core.User, q string) ([]core.DirEntry, error) {
	q = strings.TrimSpace(q)
	var out []core.DirEntry
	if Provider().Kind == "entra" && !app.Cfg.Dev && len(q) >= 2 {
		tok, err := AccessToken(ctx, u.ID)
		if err == nil {
			esc := strings.ReplaceAll(q, `"`, "")
			var users struct {
				Value []struct{ ID, DisplayName, Mail, UserPrincipalName string } `json:"value"`
			}
			if graphGet(ctx, tok, "/users?$top=8&$select=id,displayName,mail,userPrincipalName&$search="+url.QueryEscape(`"displayName:`+esc+`" OR "mail:`+esc+`"`), &users) == nil {
				for _, x := range users.Value {
					out = append(out, core.DirEntry{Type: "user", ID: x.ID, Name: x.DisplayName, Email: strings.ToLower(firstNonEmpty(x.Mail, x.UserPrincipalName))})
				}
			}
			var groups struct {
				Value []struct{ ID, DisplayName, Mail string } `json:"value"`
			}
			if graphGet(ctx, tok, "/groups?$top=8&$select=id,displayName,mail&$search="+url.QueryEscape(`"displayName:`+esc+`"`), &groups) == nil {
				for _, x := range groups.Value {
					out = append(out, core.DirEntry{Type: "group", ID: x.ID, Name: x.DisplayName, Email: x.Mail})
				}
			}
			if len(out) > 0 {
				return out, nil
			}
		}
	}
	like := "%" + strings.ToLower(q) + "%"
	rows, err := app.DB.QueryContext(ctx, `SELECT subject,name,email,groups_json FROM users WHERE disabled=0 AND (lower(name) LIKE ? OR lower(email) LIKE ?) ORDER BY last_seen_at DESC LIMIT 8`, like, like)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	seen := map[string]bool{}
	for rows.Next() {
		var sub, name, email, gj string
		rows.Scan(&sub, &name, &email, &gj)
		out = append(out, core.DirEntry{Type: "user", ID: strings.TrimPrefix(strings.TrimPrefix(sub, "entra:"), "dev:"), Name: name, Email: email})
	}
	// Groups we've seen on any user.
	grows, err := app.DB.QueryContext(ctx, `SELECT DISTINCT j.value->>'id', j.value->>'name' FROM users, json_each(users.groups_json) j WHERE lower(j.value->>'name') LIKE ? LIMIT 8`, like)
	if err == nil {
		defer grows.Close()
		for grows.Next() {
			var id, name string
			grows.Scan(&id, &name)
			if !seen[id] {
				seen[id] = true
				out = append(out, core.DirEntry{Type: "group", ID: id, Name: name})
			}
		}
	}
	return out, nil
}

func registerDirectory(a *core.App) {
	a.Directory = graphDirectory{}
	a.Handle(core.Op{Name: "directory.search", Method: "GET", Path: "/api/v1/directory", Summary: "Search people and groups to share with",
		Params: []core.Param{{Name: "q", In: "query", Required: true, Desc: "Name or email"}},
		Fn: func(r *http.Request) (any, error) {
			res, err := a.Directory.Search(r.Context(), core.MustUser(r), r.URL.Query().Get("q"))
			if err != nil {
				return nil, err
			}
			if res == nil {
				res = []core.DirEntry{}
			}
			return map[string]any{"results": res}, nil
		}})
}
