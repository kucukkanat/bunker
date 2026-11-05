package auth

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"database/sql"
	"encoding/base64"
	"fmt"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"

	"golang.org/x/crypto/argon2"

	"github.com/kucukkanat/bunker/internal/core"
)

// Built-in accounts: email + password with invite links. They work out of
// the box (personal use, small teams, trying Bunker). Company sign-in (SSO)
// is optional and can be switched on later; admins can then turn built-in
// accounts off.

const localMigration = `
CREATE TABLE local_accounts (
  user_id INTEGER PRIMARY KEY REFERENCES users(id) ON DELETE CASCADE,
  email TEXT UNIQUE NOT NULL,
  pw_hash TEXT NOT NULL,
  created_at INTEGER NOT NULL,
  updated_at INTEGER NOT NULL
);
CREATE TABLE invites (
  token_hash TEXT PRIMARY KEY,
  email TEXT NOT NULL,
  name TEXT NOT NULL DEFAULT '',
  is_admin INTEGER NOT NULL DEFAULT 0,
  groups_json TEXT NOT NULL DEFAULT '[]',
  kind TEXT NOT NULL DEFAULT 'invite',
  created_by INTEGER,
  created_at INTEGER NOT NULL,
  expires_at INTEGER NOT NULL,
  used_at INTEGER
);
`

// LocalEnabled reports whether built-in accounts can sign in.
func LocalEnabled() bool {
	v := app.Setting("auth.local")
	return v == "" || v == "on" || v == "true"
}

// SSOConfigured reports whether a company identity provider is set up.
func SSOConfigured() bool { return Provider().Validate() == nil }

func localReady() bool { return LocalEnabled() && app.Setting("auth.local_ready") == "1" }

// --- password hashing (argon2id, OWASP parameters) ---

func hashPassword(pw string) string {
	salt := make([]byte, 16)
	rand.Read(salt)
	h := argon2.IDKey([]byte(pw), salt, 2, 19*1024, 1, 32)
	return fmt.Sprintf("argon2id$2$19456$1$%s$%s", base64.RawStdEncoding.EncodeToString(salt), base64.RawStdEncoding.EncodeToString(h))
}

func checkPassword(pw, enc string) bool {
	var t, m uint32
	var p uint8
	parts := strings.Split(enc, "$")
	if len(parts) != 6 || parts[0] != "argon2id" {
		return false
	}
	fmt.Sscan(parts[1], &t)
	fmt.Sscan(parts[2], &m)
	fmt.Sscan(parts[3], &p)
	salt, err1 := base64.RawStdEncoding.DecodeString(parts[4])
	want, err2 := base64.RawStdEncoding.DecodeString(parts[5])
	if err1 != nil || err2 != nil || t == 0 || m == 0 || p == 0 {
		return false
	}
	got := argon2.IDKey([]byte(pw), salt, t, m, p, uint32(len(want)))
	return subtle.ConstantTimeCompare(got, want) == 1
}

// dummy hash so unknown emails take as long as real ones.
var dummyHash = hashPasswordOnce()

func hashPasswordOnce() string {
	salt := make([]byte, 16)
	h := argon2.IDKey([]byte("x"), salt, 2, 19*1024, 1, 32)
	return fmt.Sprintf("argon2id$2$19456$1$%s$%s", base64.RawStdEncoding.EncodeToString(salt), base64.RawStdEncoding.EncodeToString(h))
}

func validPassword(pw string) error {
	if len(pw) < 10 {
		return core.BadRequest("use at least 10 characters for your password")
	}
	if len(pw) > 200 {
		return core.BadRequest("that password is too long")
	}
	return nil
}

func validEmail(e string) bool {
	e = strings.TrimSpace(e)
	at := strings.LastIndex(e, "@")
	return at > 0 && at < len(e)-1 && !strings.ContainsAny(e, " <>\"'")
}

// createLocalUser makes a built-in account.
func createLocalUser(ctx context.Context, email, name, pw string, admin bool, groups []core.Group) (*core.User, error) {
	email = strings.ToLower(strings.TrimSpace(email))
	if !validEmail(email) {
		return nil, core.BadRequest("enter a valid email address")
	}
	if err := validPassword(pw); err != nil {
		return nil, err
	}
	var n int
	app.DB.QueryRowContext(ctx, `SELECT COUNT(*) FROM local_accounts WHERE email=?`, email).Scan(&n)
	if n > 0 {
		return nil, core.Conflict("there's already an account for " + email)
	}
	if strings.TrimSpace(name) == "" {
		name = strings.Split(email, "@")[0]
	}
	u, err := upsertUser(ctx, "local:"+email, email, strings.TrimSpace(name), groups)
	if err != nil {
		return nil, err
	}
	if admin {
		app.DB.ExecContext(ctx, `UPDATE users SET is_admin=1 WHERE id=?`, u.ID)
		u.IsAdmin = true
	}
	if _, err := app.DB.ExecContext(ctx, `INSERT INTO local_accounts(user_id,email,pw_hash,created_at,updated_at) VALUES(?,?,?,?,?)`, u.ID, email, hashPassword(pw), core.Now(), core.Now()); err != nil {
		return nil, err
	}
	app.SetSetting(ctx, "auth.local_ready", "1")
	return u, nil
}

// --- login rate limiting (per IP and per email) ---

var (
	attemptMu sync.Mutex
	attempts  = map[string][]time.Time{}
)

func tooMany(keys ...string) bool {
	attemptMu.Lock()
	defer attemptMu.Unlock()
	now := time.Now()
	if len(attempts) > 100000 {
		attempts = map[string][]time.Time{}
	}
	limited := false
	for _, k := range keys {
		var keep []time.Time
		for _, t := range attempts[k] {
			if now.Sub(t) < 15*time.Minute {
				keep = append(keep, t)
			}
		}
		attempts[k] = append(keep, now)
		if len(keep) >= 10 {
			limited = true
		}
	}
	return limited
}

func clearAttempts(keys ...string) {
	attemptMu.Lock()
	defer attemptMu.Unlock()
	for _, k := range keys {
		delete(attempts, k)
	}
}

// sameOrigin rejects cross-site form posts (login CSRF).
func sameOrigin(r *http.Request) bool {
	o := r.Header.Get("Origin")
	if o == "" || o == "null" {
		return r.Header.Get("Sec-Fetch-Site") == "" || r.Header.Get("Sec-Fetch-Site") == "same-origin" || r.Header.Get("Sec-Fetch-Site") == "none"
	}
	if app.Cfg.URL != "" && strings.HasPrefix(app.Cfg.URL, o) {
		return true
	}
	return strings.HasSuffix(o, "://"+r.Host)
}

func isLoopback(r *http.Request) bool {
	if r.Header.Get("X-Forwarded-For") != "" {
		return false
	}
	host, _, _ := net.SplitHostPort(r.RemoteAddr)
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

func registerLocal(a *core.App) {
	a.Mux.HandleFunc("POST /auth/password", handlePasswordLogin)
	a.Mux.HandleFunc("GET /auth/invite", handleInvitePage)
	a.Mux.HandleFunc("POST /auth/invite", handleInviteAccept)

	a.Handle(core.Op{Name: "me.password", Method: "POST", Path: "/api/v1/me/password", Summary: "Change your Bunker password (built-in accounts)",
		Params: []core.Param{{Name: "current", In: "body", Required: true}, {Name: "new", In: "body", Required: true}},
		Fn: func(r *http.Request) (any, error) {
			u := core.MustUser(r)
			if u.ViaToken {
				return nil, core.Forbidden("change your password in the browser")
			}
			var in struct{ Current, New string }
			if err := core.Decode(r, &in); err != nil {
				return nil, err
			}
			var h string
			if a.DB.QueryRowContext(r.Context(), `SELECT pw_hash FROM local_accounts WHERE user_id=?`, u.ID).Scan(&h) != nil {
				return nil, core.BadRequest("your account signs in with your company account, not a Bunker password")
			}
			if !checkPassword(in.Current, h) {
				return nil, core.Forbidden("your current password isn't right")
			}
			if err := validPassword(in.New); err != nil {
				return nil, err
			}
			a.DB.ExecContext(r.Context(), `UPDATE local_accounts SET pw_hash=?, updated_at=? WHERE user_id=?`, hashPassword(in.New), core.Now(), u.ID)
			// Sign out other sessions.
			a.DB.ExecContext(r.Context(), `DELETE FROM sessions WHERE user_id=? AND id_hash<>?`, u.ID, u.SessionID)
			InvalidateUser(u.ID)
			a.Audit(r.Context(), "auth.password_change", 0, "", nil)
			return map[string]any{"ok": true}, nil
		}})

	a.Handle(core.Op{Name: "invites.create", Method: "POST", Path: "/api/v1/admin/invites", Summary: "Invite someone with a built-in account (returns a link to send them)", Access: core.AccessAdmin,
		Params: []core.Param{{Name: "email", In: "body", Required: true}, {Name: "name", In: "body"}, {Name: "isAdmin", In: "body", Type: "boolean"},
			{Name: "groups", In: "body", Type: "array", Desc: "Group names for sharing"}, {Name: "days", In: "body", Type: "integer", Desc: "Link lifetime (default 7)"}},
		Fn: func(r *http.Request) (any, error) {
			var in struct {
				Email, Name string
				IsAdmin     bool
				Groups      []string
				Days        int
			}
			if err := core.Decode(r, &in); err != nil {
				return nil, err
			}
			if !LocalEnabled() {
				return nil, core.BadRequest("built-in accounts are turned off; people sign in with your company account")
			}
			link, err := makeInvite(r.Context(), core.MustUser(r), in.Email, in.Name, in.IsAdmin, in.Groups, in.Days, "invite")
			if err != nil {
				return nil, err
			}
			a.Audit(r.Context(), "auth.invite", 0, in.Email, map[string]any{"admin": in.IsAdmin})
			return core.Created{V: map[string]any{"link": link, "email": strings.ToLower(in.Email), "note": "Send this link to them. It works once."}}, nil
		}})
	a.Handle(core.Op{Name: "invites.list", Method: "GET", Path: "/api/v1/admin/invites", Summary: "Pending invites", Access: core.AccessAdmin,
		Fn: func(r *http.Request) (any, error) {
			rows, err := a.DB.QueryContext(r.Context(), `SELECT email,name,is_admin,kind,created_at,expires_at FROM invites WHERE used_at IS NULL AND expires_at>? ORDER BY created_at DESC`, core.Now())
			if err != nil {
				return nil, err
			}
			defer rows.Close()
			out := []map[string]any{}
			for rows.Next() {
				var e, n, k string
				var adm int
				var c, x int64
				rows.Scan(&e, &n, &adm, &k, &c, &x)
				out = append(out, map[string]any{"email": e, "name": n, "isAdmin": adm == 1, "kind": k, "createdAt": c, "expiresAt": x})
			}
			return map[string]any{"invites": out}, nil
		}})
	a.Handle(core.Op{Name: "users.reset_password", Method: "POST", Path: "/api/v1/admin/users/{id}/reset-password", Summary: "Make a password-reset link for a built-in account", Access: core.AccessAdmin,
		Params: []core.Param{{Name: "id", In: "path", Type: "integer", Required: true}},
		Fn: func(r *http.Request) (any, error) {
			var email, name string
			if a.DB.QueryRowContext(r.Context(), `SELECT l.email,u.name FROM local_accounts l JOIN users u ON u.id=l.user_id WHERE l.user_id=?`, r.PathValue("id")).Scan(&email, &name) != nil {
				return nil, core.NotFound("that person doesn't have a built-in account")
			}
			link, err := makeInvite(r.Context(), core.MustUser(r), email, name, false, nil, 2, "reset")
			if err != nil {
				return nil, err
			}
			a.Audit(r.Context(), "auth.reset_link", 0, email, nil)
			return map[string]any{"link": link}, nil
		}})
	a.Handle(core.Op{Name: "auth.local.update", Method: "PUT", Path: "/api/v1/admin/auth/local", Summary: "Turn built-in accounts (email + password) on or off", Access: core.AccessAdmin,
		Params: []core.Param{{Name: "enabled", In: "body", Type: "boolean", Required: true}},
		Fn: func(r *http.Request) (any, error) {
			var in struct{ Enabled bool }
			if err := core.Decode(r, &in); err != nil {
				return nil, err
			}
			if !in.Enabled && !SSOConfigured() {
				return nil, core.BadRequest("set up company sign-in first, or nobody could sign in")
			}
			v := "off"
			if in.Enabled {
				v = "on"
			}
			a.SetSetting(r.Context(), "auth.local", v)
			a.Audit(r.Context(), "admin.config", 0, "auth.local", map[string]any{"enabled": in.Enabled})
			return map[string]any{"ok": true, "enabled": in.Enabled}, nil
		}})
	a.Handle(core.Op{Name: "auth.methods", Method: "GET", Path: "/api/v1/auth/methods", Summary: "Which sign-in methods are on", Access: core.AccessPublic, Hidden: true,
		Fn: func(r *http.Request) (any, error) {
			return map[string]any{"local": LocalEnabled(), "localReady": localReady(), "sso": SSOConfigured(), "ssoProvider": Provider().Kind}, nil
		}})
}

func makeInvite(ctx context.Context, by *core.User, email, name string, admin bool, groups []string, days int, kind string) (string, error) {
	email = strings.ToLower(strings.TrimSpace(email))
	if !validEmail(email) {
		return "", core.BadRequest("enter a valid email address")
	}
	if kind == "invite" {
		var n int
		app.DB.QueryRowContext(ctx, `SELECT COUNT(*) FROM local_accounts WHERE email=?`, email).Scan(&n)
		if n > 0 {
			return "", core.Conflict(email + " already has an account")
		}
	}
	if days <= 0 || days > 30 {
		days = 7
	}
	var gs []core.Group
	for _, g := range groups {
		if g = strings.TrimSpace(g); g != "" {
			gs = append(gs, core.Group{ID: g, Name: g})
		}
	}
	gj := "[]"
	if len(gs) > 0 {
		b, _ := jsonMarshal(gs)
		gj = string(b)
	}
	tok := core.RandomToken(24)
	app.DB.ExecContext(ctx, `DELETE FROM invites WHERE email=? AND kind=? AND used_at IS NULL`, email, kind)
	_, err := app.DB.ExecContext(ctx, `INSERT INTO invites(token_hash,email,name,is_admin,groups_json,kind,created_by,created_at,expires_at) VALUES(?,?,?,?,?,?,?,?,?)`,
		core.HashToken(tok), email, strings.TrimSpace(name), boolInt(admin), gj, kind, by.ID, core.Now(), core.Now()+int64(days)*86400)
	if err != nil {
		return "", err
	}
	return app.Cfg.URL + "/auth/invite?token=" + tok, nil
}

type inviteRow struct {
	email, name, groups, kind string
	admin                     bool
}

func lookupInvite(ctx context.Context, tok string) (*inviteRow, bool) {
	var iv inviteRow
	var adm int
	var exp int64
	var used sql.NullInt64
	if app.DB.QueryRowContext(ctx, `SELECT email,name,is_admin,groups_json,kind,expires_at,used_at FROM invites WHERE token_hash=?`, core.HashToken(tok)).Scan(&iv.email, &iv.name, &adm, &iv.groups, &iv.kind, &exp, &used) != nil {
		return nil, false
	}
	if used.Valid || exp < core.Now() {
		return nil, false
	}
	iv.admin = adm == 1
	return &iv, true
}

func handlePasswordLogin(w http.ResponseWriter, r *http.Request) {
	if !LocalEnabled() {
		http.NotFound(w, r)
		return
	}
	if !sameOrigin(r) {
		authError(w, r, "Sign-in blocked", "That request didn't come from this Bunker. Open Bunker and try again.")
		return
	}
	email := strings.ToLower(strings.TrimSpace(r.FormValue("email")))
	pw := r.FormValue("password")
	next := SafeNext(r.FormValue("next"))
	ip := core.IPFrom(r.Context())
	if tooMany("ip:"+ip, "email:"+email) {
		app.Audit(r.Context(), "auth.denied", 0, email, map[string]any{"reason": "rate limited"})
		renderLogin(w, r, next, email, "Too many attempts. Wait a few minutes and try again.")
		return
	}
	var uid int64
	h := dummyHash
	if app.DB.QueryRowContext(r.Context(), `SELECT user_id,pw_hash FROM local_accounts WHERE email=?`, email).Scan(&uid, &h) != nil {
		uid = 0
	}
	ok := checkPassword(pw, h)
	if !ok || uid == 0 {
		app.Audit(r.Context(), "auth.denied", 0, email, map[string]any{"reason": "bad password"})
		renderLogin(w, r, next, email, "That email and password don't match.")
		return
	}
	u, err := loadUser(r.Context(), uid)
	if err != nil || u == nil {
		renderLogin(w, r, next, email, "This account is disabled. Ask your admin.")
		return
	}
	clearAttempts("email:" + email)
	if err := startSession(w, r, u); err != nil {
		authError(w, r, "Sign-in failed", "Could not start a session.")
		return
	}
	app.DB.ExecContext(r.Context(), `UPDATE users SET last_seen_at=? WHERE id=?`, core.Now(), uid)
	app.Audit(core.WithUser(r.Context(), u), "auth.login", 0, "", map[string]any{"provider": "local"})
	http.Redirect(w, r, next, http.StatusSeeOther)
}

func handleInvitePage(w http.ResponseWriter, r *http.Request) {
	tok := r.URL.Query().Get("token")
	iv, ok := lookupInvite(r.Context(), tok)
	if !ok {
		authError(w, r, "This link has expired", "Ask your Bunker admin for a new one.")
		return
	}
	title, cta := "Join Bunker", "Create account"
	if iv.kind == "reset" {
		title, cta = "Choose a new password", "Save password"
	}
	pageHead(w, title)
	fmt.Fprintf(w, `<form class="card" method="post" action="/auth/invite"><img src="/_/icon.svg" width=44 height=44 alt=""><h1>%s</h1>
<p class="muted">%s</p><input type="hidden" name="token" value="%s">`, htmlEsc(title), htmlEsc(iv.email), htmlEsc(tok))
	if iv.kind != "reset" {
		fmt.Fprintf(w, `<label>Your name<input name="name" value="%s" autocomplete="name" required></label>`, htmlEsc(iv.name))
	}
	fmt.Fprintf(w, `<label>Password<input name="password" type="password" minlength="10" autocomplete="new-password" required></label>
<p class="hint">At least 10 characters. A short sentence works well.</p><button type="submit">%s</button></form>`, htmlEsc(cta))
	pageFoot(w)
}

func handleInviteAccept(w http.ResponseWriter, r *http.Request) {
	if !sameOrigin(r) {
		authError(w, r, "Blocked", "Open the invite link again.")
		return
	}
	tok := r.FormValue("token")
	iv, ok := lookupInvite(r.Context(), tok)
	if !ok {
		authError(w, r, "This link has expired", "Ask your Bunker admin for a new one.")
		return
	}
	pw := r.FormValue("password")
	if err := validPassword(pw); err != nil {
		authError(w, r, "Try a longer password", err.(*core.HTTPError).Message)
		return
	}
	ctx := r.Context()
	var u *core.User
	var err error
	if iv.kind == "reset" {
		var uid int64
		if app.DB.QueryRowContext(ctx, `SELECT user_id FROM local_accounts WHERE email=?`, iv.email).Scan(&uid) != nil {
			authError(w, r, "Account not found", "Ask your admin.")
			return
		}
		app.DB.ExecContext(ctx, `UPDATE local_accounts SET pw_hash=?, updated_at=? WHERE user_id=?`, hashPassword(pw), core.Now(), uid)
		app.DB.ExecContext(ctx, `DELETE FROM sessions WHERE user_id=?`, uid)
		InvalidateUser(uid)
		u, err = loadUser(ctx, uid)
	} else {
		var groups []core.Group
		jsonUnmarshal([]byte(iv.groups), &groups)
		u, err = createLocalUser(ctx, iv.email, firstNonEmpty(r.FormValue("name"), iv.name), pw, iv.admin, groups)
	}
	if err != nil || u == nil {
		msg := "Something went wrong."
		if he, ok := err.(*core.HTTPError); ok {
			msg = he.Message
		}
		authError(w, r, "Couldn't finish", msg)
		return
	}
	app.DB.ExecContext(ctx, `UPDATE invites SET used_at=? WHERE token_hash=?`, core.Now(), core.HashToken(tok))
	startSession(w, r, u)
	app.Audit(core.WithUser(ctx, u), "auth.invite_accepted", 0, iv.kind, nil)
	http.Redirect(w, r, "/", http.StatusSeeOther)
}

// renderLogin shows the sign-in page: email + password and, if company
// sign-in is configured, a "Continue with ..." button.
func renderLogin(w http.ResponseWriter, r *http.Request, next, email, errMsg string) {
	pageHead(w, "Sign in to Bunker")
	fmt.Fprint(w, `<div class="card"><img src="/_/icon.svg" width=44 height=44 alt=""><h1>Sign in</h1>`)
	if errMsg != "" {
		fmt.Fprintf(w, `<p class="err" role="alert">%s</p>`, htmlEsc(errMsg))
	}
	if SSOConfigured() {
		label := map[string]string{"entra": "Continue with Microsoft", "google": "Continue with Google"}[Provider().Kind]
		if label == "" {
			label = "Continue with company sign-in"
		}
		fmt.Fprintf(w, `<a class="sso" href="/auth/login?sso=1&amp;next=%s">%s</a>`, htmlEsc(urlQuery(next)), htmlEsc(label))
		if localReady() {
			fmt.Fprint(w, `<div class="or"><span>or</span></div>`)
		}
	}
	if localReady() {
		fmt.Fprintf(w, `<form method="post" action="/auth/password"><input type="hidden" name="next" value="%s">
<label>Email<input name="email" type="email" value="%s" autocomplete="username" required autofocus></label>
<label>Password<input name="password" type="password" autocomplete="current-password" required></label>
<button type="submit">Sign in</button></form><p class="hint">Forgot your password? Ask your Bunker admin for a reset link.</p>`, htmlEsc(next), htmlEsc(email))
	}
	fmt.Fprint(w, `</div>`)
	pageFoot(w)
}

func pageHead(w http.ResponseWriter, title string) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Content-Security-Policy", "default-src 'none'; style-src 'unsafe-inline'; img-src 'self'; form-action 'self'; frame-ancestors 'none'; base-uri 'none'")
	w.Header().Set("X-Frame-Options", "DENY")
	fmt.Fprintf(w, `<!doctype html><html lang="en"><meta charset=utf-8><meta name=viewport content="width=device-width,initial-scale=1"><title>%s</title><link rel=icon href="/_/icon.svg">
<style>
:root{--forest:#1f3a2e;--paper:#f7f4ec;--card:#fffdf8;--ink:#1c211e;--slate:#5a635d;--line:#ddd6c4;--clay:#c2683f;--btn:#1f3a2e;--btnText:#f7f4ec}
@media(prefers-color-scheme:dark){:root{--paper:#121714;--card:#1a211d;--ink:#e8ebe6;--slate:#a4ada7;--line:#2e3832;--forest:#e8ebe6;--btn:#8fbf9f;--btnText:#121714}}
*{box-sizing:border-box}body{margin:0;min-height:100vh;display:grid;place-items:center;padding:16px;background:var(--paper);color:var(--ink);font:16px/1.5 system-ui,-apple-system,"Segoe UI",sans-serif}
.card{width:100%%;max-width:400px;background:var(--card);border:1px solid var(--line);border-radius:18px;padding:32px;box-shadow:0 20px 50px -30px rgba(31,58,46,.4)}
h1{font:600 28px/1.2 Georgia,serif;color:var(--forest);margin:14px 0 18px}label{display:block;font-weight:600;font-size:14px;margin:0 0 14px}
input{display:block;width:100%%;margin-top:6px;padding:11px 12px;border:1px solid var(--line);border-radius:10px;background:transparent;color:inherit;font:inherit}
input:focus{outline:2px solid var(--btn);outline-offset:1px}
button,.sso{display:block;width:100%%;text-align:center;padding:12px;border:0;border-radius:10px;background:var(--btn);color:var(--btnText);font:600 16px system-ui,sans-serif;cursor:pointer;text-decoration:none}
.sso{background:transparent;color:var(--ink);border:1px solid var(--line)}.sso:hover{border-color:var(--slate)}
.or{display:flex;align-items:center;gap:12px;color:var(--slate);font-size:13px;margin:18px 0}.or:before,.or:after{content:"";flex:1;height:1px;background:var(--line)}
.hint,.muted{color:var(--slate);font-size:14px}.err{background:rgba(194,104,63,.12);color:var(--clay);padding:10px 12px;border-radius:10px;font-weight:500}
</style><body>`, htmlEsc(title))
}

func pageFoot(w http.ResponseWriter) { fmt.Fprint(w, `</body></html>`) }

func urlQuery(s string) string {
	return strings.NewReplacer("%", "%25", "&", "%26", "?", "%3F", "#", "%23", " ", "%20", "+", "%2B").Replace(s)
}
