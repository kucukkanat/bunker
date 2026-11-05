// Package auth signs people in (Microsoft Entra ID, any OIDC provider, or
// local dev mode), manages sessions and personal access tokens, and runs
// the first-run setup wizard backend.
package auth

import (
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/kucukkanat/bunker/internal/core"
)

const cookieName = "bunker_session"

func init() {
	core.Register(core.Module{
		Name:  "auth",
		Order: 10,
		Migrations: []string{`
CREATE TABLE sessions (
  id_hash TEXT PRIMARY KEY,
  user_id INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  created_at INTEGER NOT NULL,
  expires_at INTEGER NOT NULL,
  last_seen_at INTEGER NOT NULL,
  ip TEXT, ua TEXT
);
CREATE INDEX sessions_user ON sessions(user_id);
CREATE TABLE tokens (
  id INTEGER PRIMARY KEY,
  user_id INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  name TEXT NOT NULL,
  hash TEXT UNIQUE NOT NULL,
  prefix TEXT NOT NULL,
  scopes TEXT NOT NULL,
  client TEXT NOT NULL DEFAULT 'manual',
  created_at INTEGER NOT NULL,
  last_used_at INTEGER,
  expires_at INTEGER
);
CREATE INDEX tokens_user ON tokens(user_id);
CREATE TABLE device_codes (
  device_hash TEXT PRIMARY KEY,
  user_code TEXT UNIQUE NOT NULL,
  client TEXT NOT NULL,
  scopes TEXT NOT NULL,
  created_at INTEGER NOT NULL,
  expires_at INTEGER NOT NULL,
  user_id INTEGER,
  token TEXT,
  denied INTEGER NOT NULL DEFAULT 0
);
CREATE TABLE idp_tokens (
  user_id INTEGER PRIMARY KEY REFERENCES users(id) ON DELETE CASCADE,
  refresh_enc TEXT,
  access_enc TEXT,
  access_expires_at INTEGER,
  scopes TEXT NOT NULL DEFAULT '',
  updated_at INTEGER NOT NULL
);
`, localMigration},
		Init: initAuth,
	})
}

var app *core.App

func initAuth(a *core.App) error {
	app = a
	cacheMu.Lock()
	cache = map[string]cached{}
	cacheMu.Unlock()
	a.Authn = authenticate
	a.Public("/auth/")
	a.Public("/setup")
	a.Public("/device")
	initSetup(a)
	registerLogin(a)
	registerLocal(a)
	registerTokens(a)
	registerUsers(a)
	registerDirectory(a)
	go sweep(a)
	return nil
}

// sessionCache avoids a DB round-trip on every request.
type cached struct {
	u   *core.User
	exp time.Time
}

var (
	cacheMu sync.Mutex
	cache   = map[string]cached{}
)

func cacheGet(k string) *core.User {
	cacheMu.Lock()
	defer cacheMu.Unlock()
	c, ok := cache[k]
	if !ok || time.Now().After(c.exp) {
		delete(cache, k)
		return nil
	}
	return c.u
}

func cachePut(k string, u *core.User) {
	cacheMu.Lock()
	defer cacheMu.Unlock()
	if len(cache) > 50000 {
		cache = map[string]cached{}
	}
	cache[k] = cached{u: u, exp: time.Now().Add(30 * time.Second)}
}

// InvalidateUser drops cached sessions (after role/group changes or sign-out).
func InvalidateUser(id int64) {
	cacheMu.Lock()
	defer cacheMu.Unlock()
	for k, c := range cache {
		if c.u.ID == id {
			delete(cache, k)
		}
	}
}

func authenticate(r *http.Request) *core.User {
	if h := r.Header.Get("Authorization"); strings.HasPrefix(h, "Bearer ") {
		return tokenUser(r.Context(), strings.TrimSpace(h[7:]))
	}
	c, err := r.Cookie(cookieName)
	if err != nil || c.Value == "" {
		if app.Cfg.Dev {
			return devUser(r.Context())
		}
		return nil
	}
	hash := core.HashToken(c.Value)
	if u := cacheGet("s:" + hash); u != nil {
		return u
	}
	var uid, exp, last int64
	err = app.DB.QueryRowContext(r.Context(), `SELECT user_id, expires_at, last_seen_at FROM sessions WHERE id_hash=?`, hash).Scan(&uid, &exp, &last)
	if err != nil || exp < core.Now() {
		if app.Cfg.Dev {
			return devUser(r.Context())
		}
		return nil
	}
	u, err := loadUser(r.Context(), uid)
	if err != nil || u == nil {
		return nil
	}
	u.SessionID = hash
	now := core.Now()
	if now-last > 300 {
		// Sliding expiry, written at most every 5 minutes.
		days := int64(core.EnvInt("SESSION_DAYS", 14))
		app.DB.ExecContext(r.Context(), `UPDATE sessions SET last_seen_at=?, expires_at=? WHERE id_hash=?`, now, now+days*86400, hash)
		app.DB.ExecContext(r.Context(), `UPDATE users SET last_seen_at=? WHERE id=?`, now, uid)
	}
	cachePut("s:"+hash, u)
	return u
}

func loadUser(ctx context.Context, id int64) (*core.User, error) {
	u := &core.User{ID: id}
	var gj string
	var admin, disabled int
	err := app.DB.QueryRowContext(ctx, `SELECT subject,email,name,is_admin,groups_json,disabled FROM users WHERE id=?`, id).Scan(&u.Subject, &u.Email, &u.Name, &admin, &gj, &disabled)
	if err == sql.ErrNoRows || disabled == 1 {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	_ = json.Unmarshal([]byte(gj), &u.Groups)
	u.IsAdmin = admin == 1 || isConfiguredAdmin(u)
	return u, nil
}

// LoadUser is exported for other modules.
func LoadUser(ctx context.Context, id int64) (*core.User, error) { return loadUser(ctx, id) }

func isConfiguredAdmin(u *core.User) bool {
	list := app.Setting("admins")
	for _, a := range strings.Split(list, ",") {
		a = strings.TrimSpace(a)
		if a == "" {
			continue
		}
		if strings.EqualFold(a, u.Email) || u.InGroup(a) {
			return true
		}
	}
	return false
}

// upsertUser creates or updates a user from an identity-provider login.
func upsertUser(ctx context.Context, subject, email, name string, groups []core.Group) (*core.User, error) {
	gj, _ := json.Marshal(groups)
	now := core.Now()
	var id int64
	err := app.DB.QueryRowContext(ctx, `SELECT id FROM users WHERE subject=?`, subject).Scan(&id)
	if err == sql.ErrNoRows {
		// The first person to sign in to a fresh install becomes admin.
		var n int
		app.DB.QueryRowContext(ctx, `SELECT COUNT(*) FROM users WHERE subject NOT LIKE 'dev:%' AND subject NOT LIKE 'system:%'`).Scan(&n)
		admin := 0
		if n == 0 && app.Setting("admins") == "" && !strings.HasPrefix(subject, "dev:") {
			admin = 1
		}
		res, err := app.DB.ExecContext(ctx, `INSERT INTO users(subject,email,name,is_admin,groups_json,created_at,last_seen_at) VALUES(?,?,?,?,?,?,?)`, subject, email, name, admin, string(gj), now, now)
		if err != nil {
			return nil, err
		}
		id, _ = res.LastInsertId()
	} else if err != nil {
		return nil, err
	} else {
		if _, err := app.DB.ExecContext(ctx, `UPDATE users SET email=?, name=?, groups_json=?, last_seen_at=? WHERE id=?`, email, name, string(gj), now, id); err != nil {
			return nil, err
		}
		InvalidateUser(id)
	}
	u, err := loadUser(ctx, id)
	if err != nil {
		return nil, err
	}
	if u == nil {
		return nil, core.Forbidden("your account is disabled")
	}
	return u, nil
}

// startSession creates a session and sets the cookie.
func startSession(w http.ResponseWriter, r *http.Request, u *core.User) error {
	tok := core.RandomToken(32)
	now := core.Now()
	days := int64(core.EnvInt("SESSION_DAYS", 14))
	_, err := app.DB.ExecContext(r.Context(), `INSERT INTO sessions(id_hash,user_id,created_at,expires_at,last_seen_at,ip,ua) VALUES(?,?,?,?,?,?,?)`,
		core.HashToken(tok), u.ID, now, now+days*86400, now, core.IPFrom(r.Context()), truncate(r.UserAgent(), 200))
	if err != nil {
		return err
	}
	http.SetCookie(w, &http.Cookie{
		Name: cookieName, Value: tok, Path: "/", HttpOnly: true,
		Secure:   strings.HasPrefix(app.Cfg.URL, "https://"),
		SameSite: http.SameSiteLaxMode, MaxAge: int(days * 86400),
	})
	return nil
}

func truncate(s string, n int) string {
	if len(s) > n {
		return s[:n]
	}
	return s
}

func sweep(a *core.App) {
	t := time.NewTicker(time.Hour)
	defer t.Stop()
	for range t.C {
		now := core.Now()
		a.DB.Exec(`DELETE FROM sessions WHERE expires_at < ?`, now)
		a.DB.Exec(`DELETE FROM device_codes WHERE expires_at < ?`, now-3600)
		a.DB.Exec(`DELETE FROM tokens WHERE expires_at IS NOT NULL AND expires_at < ?`, now)
	}
}

// devUser signs everyone in as a local admin in dev mode. The identity can
// be switched with the bunker_dev_as cookie (set via /auth/dev?as=email).
func devUser(ctx context.Context) *core.User {
	email := "you@localhost"
	if u := cacheGet("dev"); u != nil {
		return u
	}
	u, err := upsertUser(ctx, "dev:"+email, email, "You (dev)", []core.Group{{ID: "dev-team", Name: "Dev Team"}})
	if err != nil {
		return nil
	}
	u.IsAdmin = true
	cachePut("dev", u)
	return u
}
