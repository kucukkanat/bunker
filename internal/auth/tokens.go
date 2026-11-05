package auth

import (
	"context"
	"crypto/rand"
	"database/sql"
	"net/http"
	"strconv"
	"strings"

	"github.com/kucukkanat/bunker/internal/core"
)

const tokenPrefix = "bk_"

func tokenUser(ctx context.Context, tok string) *core.User {
	if !strings.HasPrefix(tok, tokenPrefix) {
		return nil
	}
	h := core.HashToken(tok)
	if u := cacheGet("t:" + h); u != nil {
		return u
	}
	var id, uid int64
	var scopes string
	var exp sql.NullInt64
	if err := app.DB.QueryRowContext(ctx, `SELECT id,user_id,scopes,expires_at FROM tokens WHERE hash=?`, h).Scan(&id, &uid, &scopes, &exp); err != nil {
		return nil
	}
	if exp.Valid && exp.Int64 < core.Now() {
		return nil
	}
	u, err := loadUser(ctx, uid)
	if err != nil || u == nil {
		return nil
	}
	u.ViaToken, u.TokenID, u.Scopes = true, id, strings.Split(scopes, ",")
	app.DB.ExecContext(ctx, `UPDATE tokens SET last_used_at=? WHERE id=?`, core.Now(), id)
	cachePut("t:"+h, u)
	return u
}

// CreateToken issues a personal access token for uid.
func CreateToken(ctx context.Context, uid int64, name, client string, scopes []string, days int) (string, int64, error) {
	if len(scopes) == 0 {
		scopes = []string{core.ScopeRead, core.ScopeWrite}
	}
	for _, s := range scopes {
		if s != core.ScopeRead && s != core.ScopeWrite && s != core.ScopeAdmin {
			return "", 0, core.BadRequest("unknown scope " + s)
		}
	}
	maxDays := core.EnvInt("TOKEN_MAX_DAYS", 90)
	if days <= 0 || days > maxDays {
		days = maxDays
	}
	tok := tokenPrefix + core.RandomToken(30)
	res, err := app.DB.ExecContext(ctx, `INSERT INTO tokens(user_id,name,hash,prefix,scopes,client,created_at,expires_at) VALUES(?,?,?,?,?,?,?,?)`,
		uid, name, core.HashToken(tok), tok[:9], strings.Join(scopes, ","), client, core.Now(), core.Now()+int64(days)*86400)
	if err != nil {
		return "", 0, err
	}
	id, _ := res.LastInsertId()
	return tok, id, nil
}

type tokenInfo struct {
	ID        int64    `json:"id"`
	Name      string   `json:"name"`
	Prefix    string   `json:"prefix"`
	Scopes    []string `json:"scopes"`
	Client    string   `json:"client"`
	CreatedAt int64    `json:"createdAt"`
	LastUsed  *int64   `json:"lastUsedAt"`
	ExpiresAt *int64   `json:"expiresAt"`
}

// userCode returns codes like "BKRT-WXQP" from an unambiguous alphabet.
func userCode() string {
	const alpha = "BCDFGHJKLMNPQRSTVWXZ"
	b := make([]byte, 8)
	rand.Read(b)
	out := make([]byte, 0, 9)
	for i, c := range b {
		if i == 4 {
			out = append(out, '-')
		}
		out = append(out, alpha[int(c)%len(alpha)])
	}
	return string(out)
}

func registerTokens(a *core.App) {
	a.Handle(core.Op{Name: "tokens.list", Method: "GET", Path: "/api/v1/tokens", Summary: "List your personal access tokens",
		Fn: func(r *http.Request) (any, error) {
			rows, err := a.DB.QueryContext(r.Context(), `SELECT id,name,prefix,scopes,client,created_at,last_used_at,expires_at FROM tokens WHERE user_id=? ORDER BY id DESC`, core.MustUser(r).ID)
			if err != nil {
				return nil, err
			}
			defer rows.Close()
			out := []tokenInfo{}
			for rows.Next() {
				var t tokenInfo
				var sc string
				var lu, ex sql.NullInt64
				rows.Scan(&t.ID, &t.Name, &t.Prefix, &sc, &t.Client, &t.CreatedAt, &lu, &ex)
				t.Scopes = strings.Split(sc, ",")
				if lu.Valid {
					t.LastUsed = &lu.Int64
				}
				if ex.Valid {
					t.ExpiresAt = &ex.Int64
				}
				out = append(out, t)
			}
			return map[string]any{"tokens": out}, nil
		}})
	a.Handle(core.Op{Name: "tokens.create", Method: "POST", Path: "/api/v1/tokens", Summary: "Create a personal access token for scripts and agents",
		Params: []core.Param{
			{Name: "name", In: "body", Required: true, Desc: "What it's for"},
			{Name: "scopes", In: "body", Type: "array", Desc: "read, write, admin (default read,write)"},
			{Name: "days", In: "body", Type: "integer", Desc: "Lifetime in days (default/max 90)"},
		},
		Fn: func(r *http.Request) (any, error) {
			u := core.MustUser(r)
			var in struct {
				Name   string   `json:"name"`
				Scopes []string `json:"scopes"`
				Days   int      `json:"days"`
			}
			if err := core.Decode(r, &in); err != nil {
				return nil, err
			}
			if strings.TrimSpace(in.Name) == "" {
				return nil, core.BadRequest("name is required")
			}
			for _, s := range in.Scopes {
				if s == core.ScopeAdmin && !u.IsAdmin {
					return nil, core.Forbidden("only admins can create admin tokens")
				}
			}
			if u.ViaToken {
				// A token can't mint a token with more power than itself.
				for _, s := range in.Scopes {
					if !u.HasScope(s) {
						return nil, core.Forbidden("token lacks scope " + s)
					}
				}
			}
			tok, id, err := CreateToken(r.Context(), u.ID, in.Name, "manual", in.Scopes, in.Days)
			if err != nil {
				return nil, err
			}
			a.Audit(r.Context(), "token.create", 0, in.Name, map[string]any{"tokenId": id})
			return core.Created{V: map[string]any{"id": id, "token": tok, "note": "Copy this token now. It won't be shown again."}}, nil
		}})
	a.Handle(core.Op{Name: "tokens.delete", Method: "DELETE", Path: "/api/v1/tokens/{id}", Summary: "Revoke a personal access token", Destructive: true,
		Params: []core.Param{{Name: "id", In: "path", Type: "integer", Required: true}},
		Fn: func(r *http.Request) (any, error) {
			id, _ := strconv.ParseInt(r.PathValue("id"), 10, 64)
			u := core.MustUser(r)
			res, err := a.DB.ExecContext(r.Context(), `DELETE FROM tokens WHERE id=? AND user_id=?`, id, u.ID)
			if err != nil {
				return nil, err
			}
			if n, _ := res.RowsAffected(); n == 0 {
				return nil, core.NotFound("token not found")
			}
			InvalidateUser(u.ID)
			a.Audit(r.Context(), "token.revoke", 0, strconv.FormatInt(id, 10), nil)
			return map[string]any{"ok": true}, nil
		}})

	// Device authorization flow (RFC 8628 style) for the CLI, MCP clients
	// and agents: they show a short code, the person approves it in the
	// browser, and the client receives a scoped token.
	a.Handle(core.Op{Name: "auth.device.start", Method: "POST", Path: "/api/v1/auth/device", Summary: "Start a device sign-in (CLI/agents)", Access: core.AccessPublic, Hidden: true,
		Fn: func(r *http.Request) (any, error) {
			var in struct {
				Client string   `json:"client"`
				Scopes []string `json:"scopes"`
			}
			_ = core.Decode(r, &in)
			if in.Client == "" {
				in.Client = "cli"
			}
			if len(in.Scopes) == 0 {
				in.Scopes = []string{core.ScopeRead, core.ScopeWrite}
			}
			dev := core.RandomToken(32)
			code := userCode()
			_, err := a.DB.ExecContext(r.Context(), `INSERT INTO device_codes(device_hash,user_code,client,scopes,created_at,expires_at) VALUES(?,?,?,?,?,?)`,
				core.HashToken(dev), code, truncate(in.Client, 60), strings.Join(in.Scopes, ","), core.Now(), core.Now()+600)
			if err != nil {
				return nil, err
			}
			return map[string]any{
				"deviceCode": dev, "userCode": code, "expiresIn": 600, "interval": 2,
				"verificationUri":         a.Cfg.URL + "/device",
				"verificationUriComplete": a.Cfg.URL + "/device?code=" + code,
			}, nil
		}})
	a.Handle(core.Op{Name: "auth.device.poll", Method: "POST", Path: "/api/v1/auth/device/token", Summary: "Poll for a device sign-in token", Access: core.AccessPublic, Hidden: true,
		Fn: func(r *http.Request) (any, error) {
			var in struct {
				DeviceCode string `json:"deviceCode"`
			}
			if err := core.Decode(r, &in); err != nil {
				return nil, err
			}
			h := core.HashToken(in.DeviceCode)
			var tok sql.NullString
			var exp int64
			var denied int
			err := a.DB.QueryRowContext(r.Context(), `SELECT token,expires_at,denied FROM device_codes WHERE device_hash=?`, h).Scan(&tok, &exp, &denied)
			if err != nil {
				return nil, core.NewErr(400, "expired_token", "unknown or expired code")
			}
			if denied == 1 {
				a.DB.ExecContext(r.Context(), `DELETE FROM device_codes WHERE device_hash=?`, h)
				return nil, core.NewErr(400, "access_denied", "the request was denied")
			}
			if exp < core.Now() {
				return nil, core.NewErr(400, "expired_token", "code expired")
			}
			if !tok.Valid {
				return nil, core.NewErr(400, "authorization_pending", "waiting for approval")
			}
			a.DB.ExecContext(r.Context(), `DELETE FROM device_codes WHERE device_hash=?`, h)
			t, err := core.Decrypt(tok.String)
			if err != nil {
				return nil, core.Internal("token unavailable")
			}
			return map[string]any{"token": t}, nil
		}})
	a.Handle(core.Op{Name: "auth.device.info", Method: "GET", Path: "/api/v1/auth/device/{code}", Summary: "Describe a pending device sign-in", Hidden: true,
		Fn: func(r *http.Request) (any, error) {
			var client, scopes string
			var exp int64
			err := a.DB.QueryRowContext(r.Context(), `SELECT client,scopes,expires_at FROM device_codes WHERE user_code=? AND token IS NULL AND denied=0`, strings.ToUpper(r.PathValue("code"))).Scan(&client, &scopes, &exp)
			if err != nil || exp < core.Now() {
				return nil, core.NotFound("that code is invalid or has expired")
			}
			return map[string]any{"client": client, "scopes": strings.Split(scopes, ",")}, nil
		}})
	a.Handle(core.Op{Name: "auth.device.approve", Method: "POST", Path: "/api/v1/auth/device/{code}", Summary: "Approve or deny a device sign-in", Hidden: true,
		Fn: func(r *http.Request) (any, error) {
			u := core.MustUser(r)
			if u.ViaToken {
				return nil, core.Forbidden("approve in the browser")
			}
			var in struct {
				Approve bool `json:"approve"`
			}
			if err := core.Decode(r, &in); err != nil {
				return nil, err
			}
			code := strings.ToUpper(strings.TrimSpace(r.PathValue("code")))
			var client, scopes string
			var exp int64
			if err := a.DB.QueryRowContext(r.Context(), `SELECT client,scopes,expires_at FROM device_codes WHERE user_code=? AND token IS NULL AND denied=0`, code).Scan(&client, &scopes, &exp); err != nil || exp < core.Now() {
				return nil, core.NotFound("that code is invalid or has expired")
			}
			if !in.Approve {
				a.DB.ExecContext(r.Context(), `UPDATE device_codes SET denied=1 WHERE user_code=?`, code)
				return map[string]any{"ok": true, "approved": false}, nil
			}
			sc := strings.Split(scopes, ",")
			for _, s := range sc {
				if s == core.ScopeAdmin && !u.IsAdmin {
					return nil, core.Forbidden("only admins can approve admin access")
				}
			}
			tok, id, err := CreateToken(r.Context(), u.ID, client+" sign-in", client, sc, 0)
			if err != nil {
				return nil, err
			}
			enc, _ := core.Encrypt(tok)
			a.DB.ExecContext(r.Context(), `UPDATE device_codes SET token=?, user_id=? WHERE user_code=?`, enc, u.ID, code)
			a.Audit(r.Context(), "token.create", 0, client, map[string]any{"tokenId": id, "flow": "device"})
			return map[string]any{"ok": true, "approved": true, "client": client}, nil
		}})
}
