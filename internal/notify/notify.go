// Package notify provides in-app notifications, access requests ("Ask
// for access" on sites you can't open) and per-site view statistics.
package notify

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"github.com/kucukkanat/bunker/internal/core"
	"github.com/kucukkanat/bunker/internal/sites"
)

func init() {
	core.Register(core.Module{
		Name:  "notify",
		Order: 60,
		Migrations: []string{`
CREATE TABLE notifications (
  id INTEGER PRIMARY KEY,
  user_email TEXT NOT NULL,
  kind TEXT NOT NULL,
  title TEXT NOT NULL,
  body TEXT NOT NULL DEFAULT '',
  link TEXT NOT NULL DEFAULT '',
  site_id INTEGER,
  data TEXT,
  created_at INTEGER NOT NULL,
  read_at INTEGER
);
CREATE INDEX notifications_user ON notifications(user_email, created_at);
CREATE TABLE access_requests (
  id INTEGER PRIMARY KEY,
  site_id INTEGER NOT NULL REFERENCES sites(id) ON DELETE CASCADE,
  user_id INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  message TEXT NOT NULL DEFAULT '',
  role TEXT NOT NULL DEFAULT 'viewer',
  status TEXT NOT NULL DEFAULT 'pending',
  created_at INTEGER NOT NULL,
  decided_at INTEGER,
  decided_by INTEGER,
  UNIQUE(site_id, user_id, status)
);
`},
		Init: initNotify,
	})
}

var app *core.App

// Send stores a notification for a person (by email) and pushes it live.
func Send(ctx context.Context, email, kind, title, body, link string, siteID int64) {
	email = strings.ToLower(strings.TrimSpace(email))
	if email == "" {
		return
	}
	var sid any
	if siteID != 0 {
		sid = siteID
	}
	res, err := app.DB.ExecContext(context.WithoutCancel(ctx), `INSERT INTO notifications(user_email,kind,title,body,link,site_id,created_at) VALUES(?,?,?,?,?,?,?)`, email, kind, title, body, link, sid, core.Now())
	if err != nil {
		return
	}
	id, _ := res.LastInsertId()
	b, _ := json.Marshal(map[string]any{"type": "notification", "id": id, "kind": kind, "title": title, "body": body, "link": link})
	app.Bus.Publish("user:"+email, b)
}

func initNotify(a *core.App) error {
	app = a
	// Tell people when something is shared with them.
	a.On("site.shared", func(ctx context.Context, p any) {
		m, _ := p.(map[string]any)
		if m == nil || m["type"] != "user" {
			return
		}
		sid, _ := m["siteId"].(int64)
		s, err := sites.GetByID(ctx, sid, false)
		if err != nil {
			return
		}
		Send(ctx, fmt.Sprint(m["id"]), "share", fmt.Sprintf("%v shared “%s” with you", m["by"], s.Title), "You can "+map[string]string{"viewer": "view", "editor": "edit"}[fmt.Sprint(m["role"])]+" it now.", "/s/"+s.Slug+"/", s.ID)
	})

	// Idle archival (sites module): warn the owner a week ahead, then tell them.
	a.On("site.archive_warning", func(ctx context.Context, p any) {
		m, _ := p.(map[string]any)
		s, email := siteOwner(ctx, m)
		if s == nil {
			return
		}
		left := (toInt64(m["archiveAt"]) - core.Now() + 43200) / 86400
		when := "soon"
		if left >= 1 {
			when = fmt.Sprintf("in %d day%s", left, map[bool]string{true: "", false: "s"}[left == 1])
		}
		Send(ctx, email, "archive", fmt.Sprintf("“%s” will be archived %s", s.Title, when),
			fmt.Sprintf("Nobody has opened or changed it for a while (sites are archived after %d days without use). Open it to keep it. Archived sites can be restored any time.", toInt64(m["days"])), "/s/"+s.Slug+"/", s.ID)
	})
	a.On("site.archived", func(ctx context.Context, p any) {
		m, _ := p.(map[string]any)
		if toInt64(m["days"]) == 0 {
			return // archived by hand: no news for the owner
		}
		s, email := siteOwner(ctx, m)
		if s == nil {
			return
		}
		Send(ctx, email, "archive", fmt.Sprintf("“%s” was archived", s.Title),
			fmt.Sprintf("Nobody used it for %d days. Its files and data are kept: open it and press Restore to bring it back.", toInt64(m["days"])), "/s/"+s.Slug+"/", s.ID)
	})

	a.Handle(core.Op{Name: "notifications.list", Method: "GET", Path: "/api/v1/notifications", Summary: "Your notifications",
		Params: []core.Param{{Name: "unread", In: "query", Type: "boolean"}},
		Fn: func(r *http.Request) (any, error) {
			u := core.MustUser(r)
			q := `SELECT id,kind,title,body,link,COALESCE(site_id,0),created_at,read_at FROM notifications WHERE user_email=?`
			if r.URL.Query().Get("unread") == "true" {
				q += ` AND read_at IS NULL`
			}
			rows, err := a.DB.QueryContext(r.Context(), q+` ORDER BY id DESC LIMIT 100`, strings.ToLower(u.Email))
			if err != nil {
				return nil, err
			}
			defer rows.Close()
			type n struct {
				ID        int64  `json:"id"`
				Kind      string `json:"kind"`
				Title     string `json:"title"`
				Body      string `json:"body"`
				Link      string `json:"link"`
				SiteID    int64  `json:"siteId,omitempty"`
				CreatedAt int64  `json:"createdAt"`
				Read      bool   `json:"read"`
			}
			out := []n{}
			unread := 0
			for rows.Next() {
				var x n
				var ra sql.NullInt64
				rows.Scan(&x.ID, &x.Kind, &x.Title, &x.Body, &x.Link, &x.SiteID, &x.CreatedAt, &ra)
				x.Read = ra.Valid
				if !x.Read {
					unread++
				}
				out = append(out, x)
			}
			return map[string]any{"notifications": out, "unread": unread}, nil
		}})
	a.Handle(core.Op{Name: "notifications.read", Method: "POST", Path: "/api/v1/notifications/read", Summary: "Mark notifications as read",
		Params: []core.Param{{Name: "ids", In: "body", Type: "array", Desc: "IDs; empty = all"}},
		Fn: func(r *http.Request) (any, error) {
			u := core.MustUser(r)
			var in struct{ IDs []int64 }
			_ = core.Decode(r, &in)
			if len(in.IDs) == 0 {
				a.DB.ExecContext(r.Context(), `UPDATE notifications SET read_at=? WHERE user_email=? AND read_at IS NULL`, core.Now(), strings.ToLower(u.Email))
			} else {
				for _, id := range in.IDs {
					a.DB.ExecContext(r.Context(), `UPDATE notifications SET read_at=? WHERE id=? AND user_email=?`, core.Now(), id, strings.ToLower(u.Email))
				}
			}
			return map[string]any{"ok": true}, nil
		}})
	a.Handle(core.Op{Name: "notifications.stream", Method: "GET", Path: "/api/v1/notifications/events", Summary: "Live notifications (server-sent events)", Stream: true, Hidden: true,
		Handler: func(w http.ResponseWriter, r *http.Request) {
			u := core.MustUser(r)
			ch, cancel := a.Bus.Subscribe("user:" + strings.ToLower(u.Email))
			defer cancel()
			streamSSE(w, r, ch)
		}})

	registerAccess(a)
	registerStats(a)
	registerPageNotify(a)
	return nil
}

func registerAccess(a *core.App) {
	// Browser form on the "nothing here" page. Same-site form POSTs carry
	// the SameSite=Lax session; a MAC bound to user+site guards against CSRF.
	a.Public("/access-request")
	a.Mux.HandleFunc("POST /access-request", func(w http.ResponseWriter, r *http.Request) {
		u := core.UserFrom(r.Context())
		if u == nil {
			http.Redirect(w, r, "/auth/login", http.StatusSeeOther)
			return
		}
		slug := r.FormValue("site")
		if !core.MACEqual(r.FormValue("t"), AccessToken(u.ID, slug)) {
			http.Error(w, "expired form, go back and try again", http.StatusForbidden)
			return
		}
		requestAccess(r.Context(), u, slug, r.FormValue("message"))
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Header().Set("Content-Security-Policy", "default-src 'none'; style-src 'self' 'unsafe-inline'; img-src 'self'")
		fmt.Fprint(w, `<!doctype html><meta charset=utf-8><meta name=viewport content="width=device-width,initial-scale=1"><title>Request sent</title><link rel=stylesheet href="/_/kit.css">
<main class="bk-center"><div class="bk-card"><img src="/_/icon.svg" width=40 height=40 alt=""><h1>Request sent</h1><p>We've asked the owner to give you access. You'll get a notification in Bunker when they do.</p><p><a class="bk-btn" href="/">Go to Bunker</a></p></div></main>`)
	})
	a.Handle(core.Op{Name: "access.request", Method: "POST", Path: "/api/v1/sites/{site}/access-requests", Summary: "Ask the owner for access to a site",
		Params: []core.Param{{Name: "site", In: "path", Required: true}, {Name: "message", In: "body"}, {Name: "role", In: "body", Enum: []string{"viewer", "editor"}}},
		Fn: func(r *http.Request) (any, error) {
			var in struct{ Message, Role string }
			_ = core.Decode(r, &in)
			requestAccess(r.Context(), core.MustUser(r), r.PathValue("site"), in.Message)
			// Same answer whether or not the site exists: no enumeration.
			return map[string]any{"ok": true}, nil
		}})
	a.Handle(core.Op{Name: "access.list", Method: "GET", Path: "/api/v1/sites/{site}/access-requests", Summary: "Pending access requests for a site",
		Params: []core.Param{{Name: "site", In: "path", Required: true}},
		Fn: func(r *http.Request) (any, error) {
			s, _, err := sites.Full(r.Context(), core.MustUser(r), r.PathValue("site"), core.RoleEditor)
			if err != nil {
				return nil, err
			}
			rows, err := a.DB.QueryContext(r.Context(), `SELECT ar.id, u.name, u.email, ar.message, ar.role, ar.created_at FROM access_requests ar JOIN users u ON u.id=ar.user_id WHERE ar.site_id=? AND ar.status='pending' ORDER BY ar.id DESC`, s.ID)
			if err != nil {
				return nil, err
			}
			defer rows.Close()
			out := []map[string]any{}
			for rows.Next() {
				var id, at int64
				var name, email, msg, role string
				rows.Scan(&id, &name, &email, &msg, &role, &at)
				out = append(out, map[string]any{"id": id, "name": name, "email": email, "message": msg, "role": role, "createdAt": at})
			}
			return map[string]any{"requests": out}, nil
		}})
	a.Handle(core.Op{Name: "access.decide", Method: "POST", Path: "/api/v1/sites/{site}/access-requests/{id}", Summary: "Approve or deny an access request",
		Params: []core.Param{{Name: "site", In: "path", Required: true}, {Name: "id", In: "path", Type: "integer", Required: true}, {Name: "approve", In: "body", Type: "boolean", Required: true}, {Name: "role", In: "body", Enum: []string{"viewer", "editor"}}},
		Fn: func(r *http.Request) (any, error) {
			u := core.MustUser(r)
			s, _, err := sites.Full(r.Context(), u, r.PathValue("site"), core.RoleEditor)
			if err != nil {
				return nil, err
			}
			id, _ := strconv.ParseInt(r.PathValue("id"), 10, 64)
			var in struct {
				Approve bool
				Role    string
			}
			if err := core.Decode(r, &in); err != nil {
				return nil, err
			}
			var uid int64
			var name, email, role string
			if err := a.DB.QueryRowContext(r.Context(), `SELECT u.id,u.name,u.email,ar.role FROM access_requests ar JOIN users u ON u.id=ar.user_id WHERE ar.id=? AND ar.site_id=? AND ar.status='pending'`, id, s.ID).Scan(&uid, &name, &email, &role); err != nil {
				return nil, core.NotFound("request not found")
			}
			if in.Role != "" {
				role = in.Role
			}
			status := "denied"
			if in.Approve {
				status = "approved"
				nr := core.ParseRole(role)
				if nr != core.RoleEditor {
					nr = core.RoleViewer
				}
				a.DB.ExecContext(r.Context(), `INSERT INTO site_members(site_id,ptype,pid,pname,role,added_by,added_at) VALUES(?,?,?,?,?,?,?) ON CONFLICT(site_id,ptype,pid) DO UPDATE SET role=MAX(role,excluded.role)`,
					s.ID, "user", strings.ToLower(email), name, int(nr), u.ID, core.Now())
				sites.Invalidate(s.Slug)
				a.Audit(r.Context(), "site.share", s.ID, "user:"+email, map[string]any{"role": nr.String(), "via": "access_request"})
				Send(r.Context(), email, "access", fmt.Sprintf("You can now open “%s”", s.Title), u.Name+" approved your request.", "/s/"+s.Slug+"/", s.ID)
			} else {
				Send(r.Context(), email, "access", fmt.Sprintf("Your request for “%s” was declined", s.Title), "", "", s.ID)
			}
			a.DB.ExecContext(r.Context(), `DELETE FROM access_requests WHERE site_id=? AND user_id=? AND status=?`, s.ID, uid, status)
			a.DB.ExecContext(r.Context(), `UPDATE access_requests SET status=?, decided_at=?, decided_by=? WHERE id=?`, status, core.Now(), u.ID, id)
			return map[string]any{"ok": true, "status": status}, nil
		}})
}

// AccessToken is the anti-CSRF value for the request-access form.
func AccessToken(uid int64, slug string) string {
	return core.MAC(fmt.Sprintf("access:%d:%s", uid, slug))
}

func requestAccess(ctx context.Context, u *core.User, slug, msg string) {
	s, err := sites.GetBySlug(ctx, slug)
	if err != nil || sites.RoleOf(ctx, u, s) != core.RoleNone {
		return
	}
	if len(msg) > 500 {
		msg = msg[:500]
	}
	res, err := app.DB.ExecContext(ctx, `INSERT OR IGNORE INTO access_requests(site_id,user_id,message,created_at) VALUES(?,?,?,?)`, s.ID, u.ID, msg, core.Now())
	if err != nil {
		return
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return // already pending
	}
	var ownerEmail string
	app.DB.QueryRowContext(ctx, `SELECT email FROM users WHERE id=?`, s.OwnerID).Scan(&ownerEmail)
	body := ""
	if msg != "" {
		body = "“" + msg + "”"
	}
	Send(ctx, ownerEmail, "access_request", fmt.Sprintf("%s wants to open “%s”", u.Name, s.Title), body, "/sites/"+s.Slug+"?share=1", s.ID)
	app.Audit(ctx, "access.request", s.ID, u.Email, nil)
}

func registerStats(a *core.App) {
	a.Handle(core.Op{Name: "sites.stats", Method: "GET", Path: "/api/v1/sites/{site}/stats", Summary: "Views and visitors for a site",
		Params: []core.Param{{Name: "site", In: "path", Required: true}, {Name: "days", In: "query", Type: "integer", Desc: "Window (default 30)"}},
		Fn: func(r *http.Request) (any, error) {
			s, _, err := sites.Full(r.Context(), core.MustUser(r), r.PathValue("site"), core.RoleEditor)
			if err != nil {
				return nil, err
			}
			days, _ := strconv.Atoi(r.URL.Query().Get("days"))
			if days <= 0 || days > 365 {
				days = 30
			}
			since := core.Now() - int64(days)*86400
			var views, visitors int
			a.DB.QueryRowContext(r.Context(), `SELECT COUNT(*), COUNT(DISTINCT actor_id) FROM audit WHERE site_id=? AND event='site.view' AND ts>=?`, s.ID, since).Scan(&views, &visitors)
			rows, err := a.DB.QueryContext(r.Context(), `SELECT (ts/86400)*86400 d, COUNT(*), COUNT(DISTINCT actor_id) FROM audit WHERE site_id=? AND event='site.view' AND ts>=? GROUP BY d ORDER BY d`, s.ID, since)
			if err != nil {
				return nil, err
			}
			defer rows.Close()
			daily := []map[string]any{}
			for rows.Next() {
				var d int64
				var v, u int
				rows.Scan(&d, &v, &u)
				daily = append(daily, map[string]any{"day": d, "views": v, "visitors": u})
			}
			top := []map[string]any{}
			trows, err := a.DB.QueryContext(r.Context(), `SELECT actor_email, COUNT(*) c, MAX(ts) FROM audit WHERE site_id=? AND event='site.view' AND ts>=? GROUP BY actor_email ORDER BY c DESC LIMIT 10`, s.ID, since)
			if err == nil {
				defer trows.Close()
				for trows.Next() {
					var e string
					var c int
					var last int64
					trows.Scan(&e, &c, &last)
					top = append(top, map[string]any{"email": e, "views": c, "lastSeen": last})
				}
			}
			return map[string]any{"days": days, "views": views, "visitors": visitors, "daily": daily, "topVisitors": top}, nil
		}})
}

func streamSSE(w http.ResponseWriter, r *http.Request, ch <-chan []byte) {
	rc := http.NewResponseController(w)
	h := w.Header()
	h.Set("Content-Type", "text/event-stream")
	h.Set("Cache-Control", "no-cache")
	h.Set("X-Accel-Buffering", "no")
	w.WriteHeader(200)
	fmt.Fprint(w, ": ok\n\n")
	rc.Flush()
	t := newTicker()
	defer t.Stop()
	for {
		select {
		case <-r.Context().Done():
			return
		case m, ok := <-ch:
			if !ok {
				return
			}
			fmt.Fprintf(w, "data: %s\n\n", m)
		case <-t.C:
			fmt.Fprint(w, ": ping\n\n")
		}
		if rc.Flush() != nil {
			return
		}
	}
}

var (
	pnMu   = make(chan struct{}, 1)
	pnSeen = map[string][]int64{}
)

// registerPageNotify lets site pages notify people who can open the site
// (e.g. "New expense request waiting for you"). Recipients without access
// are skipped, and sending is rate limited per sender and site.
func registerPageNotify(a *core.App) {
	a.Handle(core.Op{Name: "notify.send", Method: "POST", Path: "/api/v1/sites/{site}/notify", Summary: "Notify people (or a group) who have access to this site", PageSafe: true,
		Params: []core.Param{{Name: "site", In: "path", Required: true},
			{Name: "to", In: "body", Type: "array", Required: true, Desc: "Emails and/or group names"},
			{Name: "title", In: "body", Required: true}, {Name: "body", In: "body"},
			{Name: "path", In: "body", Desc: "Page in the site to open, e.g. /requests/42"}},
		Fn: func(r *http.Request) (any, error) {
			u := core.MustUser(r)
			s, _, err := sites.Full(r.Context(), u, r.PathValue("site"), core.RoleViewer)
			if err != nil {
				return nil, err
			}
			var in struct {
				To          []string
				Title, Body string
				Path        string
			}
			if err := core.Decode(r, &in); err != nil {
				return nil, err
			}
			in.Title = strings.TrimSpace(in.Title)
			if in.Title == "" || len(in.Title) > 140 || len(in.Body) > 1000 {
				return nil, core.BadRequest("title is required (max 140 characters), body max 1000")
			}
			if len(in.To) == 0 || len(in.To) > 50 {
				return nil, core.BadRequest("send to 1–50 people or groups")
			}
			// Rate limit: 60 notifications per sender per site per hour.
			key := fmt.Sprintf("%d:%d", s.ID, u.ID)
			pnMu <- struct{}{}
			now := core.Now()
			var recent []int64
			for _, t := range pnSeen[key] {
				if now-t < 3600 {
					recent = append(recent, t)
				}
			}
			if len(recent) >= 60 {
				<-pnMu
				return nil, core.Quota("too many notifications from this site, try again later")
			}
			pnSeen[key] = append(recent, now)
			<-pnMu
			link := "/s/" + s.Slug + "/"
			if p := strings.TrimLeft(in.Path, "/"); p != "" && !strings.Contains(p, "://") {
				link += p
			}
			// Resolve recipients: emails directly, groups via known members.
			emails := map[string]bool{}
			for _, t := range in.To {
				t = strings.TrimSpace(t)
				if strings.Contains(t, "@") {
					emails[strings.ToLower(t)] = true
					continue
				}
				rows, err := a.DB.QueryContext(r.Context(), `SELECT DISTINCT u.email FROM users u, json_each(u.groups_json) g WHERE u.disabled=0 AND (lower(g.value->>'name')=lower(?) OR g.value->>'id'=?)`, t, t)
				if err == nil {
					for rows.Next() {
						var e string
						rows.Scan(&e)
						emails[strings.ToLower(e)] = true
					}
					rows.Close()
				}
			}
			sent := 0
			for e := range emails {
				ru := loadByEmail(r.Context(), e)
				if ru == nil || sites.RoleOf(r.Context(), ru, s) == core.RoleNone {
					continue
				}
				Send(r.Context(), e, "site", in.Title, in.Body, link, s.ID)
				sent++
			}
			a.Audit(r.Context(), "notify.send", s.ID, in.Title, map[string]any{"recipients": sent})
			return map[string]any{"sent": sent}, nil
		}})
}

func loadByEmail(ctx context.Context, email string) *core.User {
	var id int64
	var name, gj string
	var admin int
	if app.DB.QueryRowContext(ctx, `SELECT id,name,is_admin,groups_json FROM users WHERE lower(email)=? AND disabled=0`, email).Scan(&id, &name, &admin, &gj) != nil {
		return nil
	}
	u := &core.User{ID: id, Email: email, Name: name, IsAdmin: admin == 1}
	json.Unmarshal([]byte(gj), &u.Groups)
	return u
}

func toInt64(v any) int64 {
	switch x := v.(type) {
	case int64:
		return x
	case int:
		return int64(x)
	case float64:
		return int64(x)
	}
	return 0
}

// siteOwner loads the site named by payload["siteId"] and its owner's email.
func siteOwner(ctx context.Context, m map[string]any) (*sites.SiteRecord, string) {
	s, err := sites.GetByID(ctx, toInt64(m["siteId"]), false)
	if err != nil {
		return nil, ""
	}
	var email string
	if app.DB.QueryRowContext(ctx, `SELECT email FROM users WHERE id=? AND disabled=0`, s.OwnerID).Scan(&email) != nil {
		return nil, ""
	}
	return s, email
}
