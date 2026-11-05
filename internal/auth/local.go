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
	"github.com/kucukkanat/bunker/internal/mail"
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
		// Evict stale entries, then per-IP keys (cheap to spoof), but
		// never per-email lockouts: a flood of other keys must not reset them.
		for k, ts := range attempts {
			if len(ts) == 0 || now.Sub(ts[len(ts)-1]) >= 15*time.Minute || strings.HasPrefix(k, "ip:") {
				delete(attempts, k)
			}
		}
		if len(attempts) > 200000 { // hard memory cap
			attempts = map[string][]time.Time{}
		}
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

// refundAttempt removes the newest recorded attempt for key: a successful
// sign-in must not count toward a shared address's lockout (an office
// behind one NAT would otherwise be locked out after ten sign-ins).
func refundAttempt(key string) {
	attemptMu.Lock()
	defer attemptMu.Unlock()
	if ts := attempts[key]; len(ts) > 0 {
		attempts[key] = ts[:len(ts)-1]
	}
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
	if app.Cfg.PublicURL() != "" && strings.EqualFold(strings.TrimRight(app.Cfg.PublicURL(), "/"), o) {
		return true
	}
	return strings.EqualFold(o, "http://"+r.Host) || strings.EqualFold(o, "https://"+r.Host)
}

func isLoopback(r *http.Request) bool {
	return IsLocalRequest(r)
}

// proxyHeaders are added by reverse proxies; their presence means the
// loopback peer is a proxy relaying someone else's request.
var proxyHeaders = []string{"X-Forwarded-For", "X-Forwarded-Host", "X-Forwarded-Proto", "X-Real-Ip", "Forwarded", "Via",
	"Cf-Connecting-Ip", "True-Client-Ip", "X-Client-Ip", "Fly-Client-Ip", "X-Original-Forwarded-For"}

// IsLocalRequest reports whether r was made on this machine (not relayed
// by a reverse proxy running on it): a loopback peer, no proxy headers,
// and a localhost Host name. A proxy on the same host makes every request
// come from 127.0.0.1, so the peer address alone proves nothing.
func IsLocalRequest(r *http.Request) bool {
	for _, h := range proxyHeaders {
		if r.Header.Get(h) != "" {
			return false
		}
	}
	host, _, _ := net.SplitHostPort(r.RemoteAddr)
	ip := net.ParseIP(host)
	if ip == nil || !ip.IsLoopback() {
		return false
	}
	h := r.Host
	if hh, _, err := net.SplitHostPort(h); err == nil {
		h = hh
	}
	h = strings.Trim(strings.ToLower(h), "[]")
	if h == "localhost" || strings.HasSuffix(h, ".localhost") {
		return true
	}
	hip := net.ParseIP(h)
	return hip != nil && hip.IsLoopback()
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
			by := core.MustUser(r).Name
			emailed := mail.Send(mail.Message{To: strings.ToLower(strings.TrimSpace(in.Email)), Subject: by + " invited you to Bunker",
				Text: by + " invited you to Bunker, where your team builds and shares small apps.\n\nThe link works once and expires in " + inviteDays(in.Days) + ".", ButtonText: "Join Bunker", ButtonURL: link})
			note := "Send this link to them. It works once."
			if emailed {
				note = "We emailed them the link. You can also send it yourself; it works once."
			}
			return core.Created{V: map[string]any{"link": link, "email": strings.ToLower(in.Email), "emailed": emailed, "note": note}}, nil
		}})
	a.Handle(core.Op{Name: "invites.list", Method: "GET", Path: "/api/v1/admin/invites", Summary: "Pending invites", Access: core.AccessAdmin,
		Fn: func(r *http.Request) (any, error) {
			rows, err := a.DB.QueryContext(r.Context(), `SELECT email,name,is_admin,kind,created_at,expires_at,groups_json FROM invites WHERE used_at IS NULL AND expires_at>? ORDER BY created_at DESC`, core.Now())
			if err != nil {
				return nil, err
			}
			defer rows.Close()
			out := []map[string]any{}
			for rows.Next() {
				var e, n, k, gj string
				var adm int
				var c, x int64
				rows.Scan(&e, &n, &adm, &k, &c, &x, &gj)
				var gs []core.Group
				jsonUnmarshal([]byte(gj), &gs)
				names := []string{}
				for _, g := range gs {
					names = append(names, g.Name)
				}
				out = append(out, map[string]any{"email": e, "name": n, "isAdmin": adm == 1, "kind": k, "createdAt": c, "expiresAt": x, "groups": names})
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
			emailed := mail.Send(mail.Message{To: email, Subject: "Choose a new Bunker password",
				Text: "Your Bunker admin made a link for you to choose a new password.\n\nIt works once and expires in 2 days. If you didn't ask for this, you can ignore this email.", ButtonText: "Choose a new password", ButtonURL: link})
			return map[string]any{"link": link, "emailed": emailed}, nil
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

// clampDays is an invite link's lifetime: 1-30 days, 7 when unset.
func clampDays(days int) int {
	if days <= 0 || days > 30 {
		return 7
	}
	return days
}

// inviteDays words an invite lifetime for the email ("1 day", "14 days").
func inviteDays(days int) string {
	if days = clampDays(days); days == 1 {
		return "1 day"
	}
	return fmt.Sprintf("%d days", days)
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
	days = clampDays(days)
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
	return app.Cfg.PublicURL() + "/auth/invite?token=" + tok, nil
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
	refundAttempt("ip:" + ip)
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
		inviteGone(w, r, tok)
		return
	}
	renderInvite(w, http.StatusOK, tok, iv, iv.name, "")
}

// renderInvite shows the "create your account" (or "choose a new password")
// form for an invite link, with an optional error above it.
func renderInvite(w http.ResponseWriter, status int, tok string, iv *inviteRow, name, errMsg string) {
	title, lead, cta := "Welcome to Bunker", "Choose a password to finish setting up your account for <b>%s</b>.", "Create my account"
	if iv.kind == "reset" {
		title, lead, cta = "Choose a new password", "This sets a new password for <b>%s</b>. You'll be signed in right after.", "Save password"
	}
	pageHeadStatus(w, status, title)
	fmt.Fprintf(w, `<main class="card"><form method="post" action="/auth/invite"><img src="/_/icon.svg" width=44 height=44 alt=""><h1>%s</h1>
<p class="muted">`+lead+`</p>`, htmlEsc(title), htmlEsc(iv.email))
	desc, invalid := "pw-hint", ""
	if errMsg != "" {
		fmt.Fprintf(w, `<p class="err" id="form-err" role="alert">%s</p>`, htmlEsc(errMsg))
		desc, invalid = "form-err pw-hint", ` aria-invalid="true"`
	}
	fmt.Fprintf(w, `<input type="hidden" name="token" value="%s">`, htmlEsc(tok))
	if iv.kind != "reset" {
		fmt.Fprintf(w, `<label>Your name<input name="name" value="%s" autocomplete="name" required></label>`, htmlEsc(name))
	}
	af := ""
	if iv.kind == "reset" || name != "" {
		af = " autofocus"
	}
	if errMsg != "" {
		af = " autofocus"
	}
	fmt.Fprintf(w, `<label>Password<input name="password" type="password" minlength="10" autocomplete="new-password" aria-describedby="%s" required%s%s></label>
<p class="hint" id="pw-hint">At least 10 characters. A short sentence works well, like “blue kettle on the stairs”.</p><button type="submit">%s</button></form></main>`, desc, invalid, af, htmlEsc(cta))
	pageFoot(w)
}

// inviteGone explains why an invite or reset link no longer works.
func inviteGone(w http.ResponseWriter, r *http.Request, tok string) {
	var email, kind string
	var exp int64
	var used sql.NullInt64
	if tok == "" || app.DB.QueryRowContext(r.Context(), `SELECT email,kind,expires_at,used_at FROM invites WHERE token_hash=?`, core.HashToken(tok)).Scan(&email, &kind, &exp, &used) != nil {
		authPage(w, http.StatusNotFound, "This link doesn't work", "Check that you copied the whole link. If it still doesn't work, ask your Bunker admin for a new one.", "/auth/login", "Go to sign in")
		return
	}
	login := "/auth/login?login_hint=" + urlQuery(email)
	if used.Valid {
		if kind == "reset" {
			authPage(w, http.StatusGone, "This link was already used", "Your new password is set. Sign in with it, or ask your Bunker admin for another reset link.", login, "Sign in")
		} else {
			authPage(w, http.StatusGone, "You already joined", "Your account for "+email+" is ready. Sign in with the password you chose.", login, "Sign in")
		}
		return
	}
	authPage(w, http.StatusGone, "This link has expired", "Links like this only work for a few days, to keep your account safe. Ask your Bunker admin to send you a new one.", "/auth/login", "Go to sign in")
}

func handleInviteAccept(w http.ResponseWriter, r *http.Request) {
	if !sameOrigin(r) {
		authError(w, r, "Blocked", "Open the invite link again.")
		return
	}
	tok := r.FormValue("token")
	iv, ok := lookupInvite(r.Context(), tok)
	if !ok {
		inviteGone(w, r, tok)
		return
	}
	pw := r.FormValue("password")
	if err := validPassword(pw); err != nil {
		msg := "Choose a longer password."
		if he, ok := err.(*core.HTTPError); ok {
			msg = sentence(he.Message)
		}
		renderInvite(w, http.StatusUnprocessableEntity, tok, iv, r.FormValue("name"), msg)
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
		authError(w, r, "Couldn't finish", sentence(msg))
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
	fmt.Fprint(w, `<main class="card"><img src="/_/icon.svg" width=44 height=44 alt=""><h1>Sign in to Bunker</h1>`)
	errAttr := ""
	if errMsg != "" {
		fmt.Fprintf(w, `<p class="err" id="form-err" role="alert">%s</p>`, htmlEsc(errMsg))
		errAttr = ` aria-describedby="form-err" aria-invalid="true"`
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
<label>Email<input name="email" type="email" value="%s" autocomplete="username" required%s></label>
<label>Password<input name="password" type="password" autocomplete="current-password" required%s%s></label>
<button type="submit">Sign in</button></form><p class="hint">Forgot your password? Ask your Bunker admin for a reset link.</p>`, htmlEsc(next), htmlEsc(email), af(email == ""), af(email != ""), errAttr)
	}
	fmt.Fprint(w, `</main>`)
	pageFoot(w)
}

func pageHead(w http.ResponseWriter, title string) { pageHeadStatus(w, http.StatusOK, title) }

func pageHeadStatus(w http.ResponseWriter, status int, title string) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Content-Security-Policy", "default-src 'none'; style-src 'unsafe-inline'; img-src 'self'; font-src 'self'; form-action 'self'; frame-ancestors 'none'; base-uri 'none'")
	w.Header().Set("X-Frame-Options", "DENY")
	w.WriteHeader(status)
	fmt.Fprintf(w, `<!doctype html><html lang="en"><meta charset=utf-8><meta name=viewport content="width=device-width,initial-scale=1"><meta name="color-scheme" content="light dark"><title>%s</title><link rel=icon href="/_/icon.svg">
<style>
@font-face{font-family:Fraunces;src:url(/_/fonts/fraunces.woff2) format("woff2");font-weight:100 900;font-display:swap}
@font-face{font-family:Inter;src:url(/_/fonts/inter.woff2) format("woff2");font-weight:100 900;font-display:swap}
:root{--forest:#1f3a2e;--paper:#f7f4ec;--card:#fffdf8;--ink:#1c211e;--slate:#5a635d;--line:#ddd6c4;--err:#9a4423;--errBg:rgba(194,104,63,.12);--btn:#1f3a2e;--btnText:#f7f4ec;--link:#2f5543}
@media(prefers-color-scheme:dark){:root{--paper:#121714;--card:#1a211d;--ink:#e8ebe6;--slate:#a4ada7;--line:#2e3832;--forest:#e8ebe6;--err:#f0a27c;--errBg:rgba(224,138,95,.14);--btn:#8fbf9f;--btnText:#121714;--link:#8fbf9f}}
*{box-sizing:border-box}body{margin:0;min-height:100vh;display:grid;place-items:center;padding:16px;background:var(--paper);color:var(--ink);font:16px/1.5 Inter,system-ui,-apple-system,"Segoe UI",sans-serif}
.card{width:100%%;max-width:400px;background:var(--card);border:1px solid var(--line);border-radius:18px;padding:32px;box-shadow:0 20px 50px -30px rgba(31,58,46,.4)}
@media(max-width:440px){.card{padding:24px 20px}}
h1{font:560 28px/1.2 Fraunces,Georgia,serif;letter-spacing:-.01em;color:var(--forest);margin:14px 0 12px}label{display:block;font-weight:600;font-size:14px;margin:0 0 14px}
p{margin:0 0 16px}b{font-weight:600}
input{display:block;width:100%%;margin-top:6px;padding:11px 12px;border:1px solid var(--line);border-radius:10px;background:transparent;color:inherit;font:inherit}
input:focus{outline:2px solid var(--btn);outline-offset:1px}input[aria-invalid=true]{border-color:var(--err)}
button,.sso,.cta{display:block;width:100%%;text-align:center;padding:12px;border:0;border-radius:10px;background:var(--btn);color:var(--btnText);font:600 16px Inter,system-ui,sans-serif;cursor:pointer;text-decoration:none}
button:focus-visible,.sso:focus-visible,.cta:focus-visible{outline:2px solid var(--btn);outline-offset:2px}
.sso{background:transparent;color:var(--ink);border:1px solid var(--line)}.sso:hover{border-color:var(--slate)}
.or{display:flex;align-items:center;gap:12px;color:var(--slate);font-size:13px;margin:18px 0}.or:before,.or:after{content:"";flex:1;height:1px;background:var(--line)}
.hint,.muted{color:var(--slate);font-size:14px}label+.hint{margin:-6px 0 16px}form+.hint{margin:16px 0 0}.err{background:var(--errBg);color:var(--err);padding:10px 12px;border-radius:10px;font-weight:500}
a{color:var(--link)}
</style><body>`, htmlEsc(title))
}

// authPage is a small branded message page with one button.
func authPage(w http.ResponseWriter, status int, title, detail, href, label string) {
	pageHeadStatus(w, status, title)
	fmt.Fprintf(w, `<main class="card"><img src="/_/icon.svg" width=44 height=44 alt=""><h1>%s</h1><p>%s</p><a class="cta" href="%s">%s</a></main>`,
		htmlEsc(title), htmlEsc(detail), htmlEsc(href), htmlEsc(label))
	pageFoot(w)
}

// af returns an autofocus attribute when on is true.
func af(on bool) string {
	if on {
		return " autofocus"
	}
	return ""
}

// sentence capitalizes a server error message and ends it with a period.
func sentence(s string) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return s
	}
	s = strings.ToUpper(s[:1]) + s[1:]
	if !strings.HasSuffix(s, ".") && !strings.HasSuffix(s, "!") && !strings.HasSuffix(s, "?") {
		s += "."
	}
	return s
}

func pageFoot(w http.ResponseWriter) { fmt.Fprint(w, `</body></html>`) }

func urlQuery(s string) string {
	return strings.NewReplacer("%", "%25", "&", "%26", "?", "%3F", "#", "%23", " ", "%20", "+", "%2B").Replace(s)
}
