package functions

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"regexp"
	"strconv"
	"strings"

	"github.com/kucukkanat/bunker/internal/core"
	"github.com/kucukkanat/bunker/internal/sites"
)

var siteParam = core.Param{Name: "site", In: "path", Required: true, Desc: "App address (slug)"}

var secretRe = regexp.MustCompile(`^[A-Z][A-Z0-9_]{0,63}$`)

const maxSecret = 16 << 10

func limitsOf(st Settings) map[string]any {
	return map[string]any{"memoryMb": st.MemoryMB, "timeoutMs": st.TimeoutMs, "triggerTimeoutMs": st.TriggerTimeoutMs,
		"siteConcurrency": st.SiteConcurrency, "inputBytes": 1 << 20, "outputBytes": 1 << 20, "logBytes": 64 << 10, "hostCalls": 1000}
}

func registerOps(a *core.App) {
	a.Handle(core.Op{Name: "fn.list", Method: "GET", Path: "/api/v1/sites/{site}/functions",
		Summary: "Server functions in this app (from api/), with their triggers and whether they can run",
		Params:  []core.Param{siteParam, {Name: "draft", In: "query", Type: "boolean", Desc: "List the draft's functions instead of the live version's"}},
		Fn: func(r *http.Request) (any, error) {
			s, _, err := sites.Full(r.Context(), core.MustUser(r), r.PathValue("site"), core.RoleEditor)
			if err != nil {
				return nil, err
			}
			var m sites.Manifest
			if r.URL.Query().Get("draft") == "1" || r.URL.Query().Get("draft") == "true" {
				m, _, _ = sites.WorkingManifest(r.Context(), s)
			} else if s.Version > 0 {
				m, _ = sites.LoadManifest(r.Context(), s.ID, s.Version)
			}
			cat := emptyCatalog
			if m != nil {
				cat = CatalogFor(r.Context(), m)
			}
			fns := []map[string]any{}
			for _, n := range cat.Names() {
				d := cat.Fns[n]
				f := map[string]any{"name": d.Name, "path": d.Path, "elevated": d.Config.Elevated, "schedule": d.Config.Schedule,
					"onWrite": append([]string{}, d.Config.OnWrite...), "timeoutMs": d.Config.TimeoutMs}
				if d.Err != "" {
					f["error"] = d.Err
				}
				fns = append(fns, f)
			}
			st := current()
			b := BlockFor(r.Context(), &s.Site)
			return map[string]any{"enabled": b == nil, "blocked": b, "functions": fns, "limits": limitsOf(st)}, nil
		}})

	a.Handle(core.Op{Name: "fn.call", Method: "POST", Path: "/api/v1/sites/{site}/functions/{name...}", PageSafe: true,
		Summary: "Run a server function with the caller's rights (or the owner's, for elevated functions)",
		Params: []core.Param{siteParam, {Name: "name", In: "path", Required: true, Desc: "Function name: api/orders.js is \"orders\""},
			{Name: "input", In: "body", Type: "object", Desc: "Any JSON value, passed as the function's first argument"},
			{Name: "draft", In: "body", Type: "boolean", Desc: "Run the draft's code (editors only)"}},
		Handler: func(w http.ResponseWriter, r *http.Request) {
			u := core.MustUser(r)
			s, role, err := sites.Full(r.Context(), u, r.PathValue("site"), core.RoleViewer)
			if err != nil {
				core.Err(w, err)
				return
			}
			var in struct {
				Input json.RawMessage `json:"input"`
				Draft bool            `json:"draft"`
			}
			r.Body = http.MaxBytesReader(w, r.Body, 1<<20+1024)
			if r.ContentLength != 0 {
				if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
					var mbe *http.MaxBytesError
					if errors.As(err, &mbe) {
						core.Err(w, core.TooLarge("send at most 1 MB to a server function"))
					} else {
						core.Err(w, core.BadRequest("send JSON like {\"input\": {...}}"))
					}
					return
				}
			}
			if in.Draft && role < core.RoleEditor {
				in.Draft = false
			}
			name := strings.Trim(r.PathValue("name"), "/")
			if name == "_runs" {
				core.Err(w, core.NotFound("use GET for run history"))
				return
			}
			res, err := run(r.Context(), Call{Site: s, Name: name, Input: in.Input, As: u, Draft: in.Draft})
			if err != nil {
				var he *core.HTTPError
				if !errors.As(err, &he) {
					he = core.Internal("internal error")
				}
				e := map[string]any{"code": he.Code, "message": he.Message}
				if res != nil && res.Stack != "" && role >= core.RoleEditor {
					e["stack"] = res.Stack
				}
				core.JSON(w, he.Status, map[string]any{"error": e})
				return
			}
			out := res.Output
			if len(out) == 0 {
				out = json.RawMessage("null")
			}
			core.JSON(w, 200, map[string]any{"output": out, "durationMs": res.Duration.Milliseconds()})
		}})

	a.Handle(core.Op{Name: "fn.runs", Method: "GET", Path: "/api/v1/sites/{site}/functions/_runs",
		Summary: "Recent server function runs with their logs (last 200 per app)",
		Params: []core.Param{siteParam, {Name: "name", In: "query", Desc: "Only this function"},
			{Name: "limit", In: "query", Type: "integer", Desc: "Up to 200 (default 50)"}},
		Fn: func(r *http.Request) (any, error) {
			s, _, err := sites.Full(r.Context(), core.MustUser(r), r.PathValue("site"), core.RoleEditor)
			if err != nil {
				return nil, err
			}
			limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
			if limit <= 0 || limit > keepRuns {
				limit = 50
			}
			q := `SELECT f.id,f.name,f.trigger,COALESCE(u.name,''),COALESCE(u.email,''),f.status,f.duration_ms,f.error,f.logs,f.at FROM fn_runs f LEFT JOIN users u ON u.id=f.user_id WHERE f.site_id=?`
			args := []any{s.ID}
			if n := r.URL.Query().Get("name"); n != "" {
				q += ` AND f.name=?`
				args = append(args, n)
			}
			q += ` ORDER BY f.id DESC LIMIT ?`
			args = append(args, limit)
			rows, err := a.DB.QueryContext(r.Context(), q, args...)
			if err != nil {
				return nil, err
			}
			defer rows.Close()
			items := []map[string]any{}
			for rows.Next() {
				var id, dur, at int64
				var name, trig, uname, email, status, e, logs string
				rows.Scan(&id, &name, &trig, &uname, &email, &status, &dur, &e, &logs, &at)
				items = append(items, map[string]any{"id": id, "name": name, "trigger": trig, "user": map[string]any{"name": uname, "email": email},
					"status": status, "durationMs": dur, "error": e, "logs": logs, "at": at})
			}
			return map[string]any{"items": items}, nil
		}})

	// ---- secrets ----
	a.Handle(core.Op{Name: "secrets.list", Method: "GET", Path: "/api/v1/sites/{site}/secrets",
		Summary: "Names of this app's secrets (values are never shown)",
		Params:  []core.Param{siteParam},
		Fn: func(r *http.Request) (any, error) {
			s, _, err := sites.Full(r.Context(), core.MustUser(r), r.PathValue("site"), core.RoleEditor)
			if err != nil {
				return nil, err
			}
			rows, err := a.DB.QueryContext(r.Context(), `SELECT s.name,s.updated_at,COALESCE(u.name,'') FROM fn_secrets s LEFT JOIN users u ON u.id=s.updated_by WHERE s.site_id=? ORDER BY s.name`, s.ID)
			if err != nil {
				return nil, err
			}
			defer rows.Close()
			items := []map[string]any{}
			for rows.Next() {
				var n, by string
				var at int64
				rows.Scan(&n, &at, &by)
				items = append(items, map[string]any{"name": n, "updatedAt": at, "updatedBy": by})
			}
			return map[string]any{"items": items}, nil
		}})

	a.Handle(core.Op{Name: "secrets.set", Method: "PUT", Path: "/api/v1/sites/{site}/secrets/{name}",
		Summary: "Add or replace a secret that only this app's server functions can read (bunker.secrets.get)",
		Params: []core.Param{siteParam, {Name: "name", In: "path", Required: true, Desc: "Like STRIPE_KEY: capital letters, numbers and _"},
			{Name: "value", In: "body", Required: true, Desc: "Up to 16 KB. Stored encrypted; never shown again"}},
		Fn: func(r *http.Request) (any, error) {
			u := core.MustUser(r)
			s, _, err := sites.Full(r.Context(), u, r.PathValue("site"), core.RoleEditor)
			if err != nil {
				return nil, err
			}
			name := r.PathValue("name")
			if !secretRe.MatchString(name) {
				return nil, core.BadRequest("secret names use capital letters, numbers and underscores and start with a letter, like STRIPE_KEY")
			}
			var in struct {
				Value *string `json:"value"`
			}
			if err := core.Decode(r, &in); err != nil {
				return nil, err
			}
			if in.Value == nil {
				return nil, core.BadRequest("send {\"value\": \"...\"}")
			}
			if len(*in.Value) > maxSecret {
				return nil, core.TooLarge("secrets can be up to 16 KB")
			}
			var n int
			a.DB.QueryRowContext(r.Context(), `SELECT COUNT(*) FROM fn_secrets WHERE site_id=? AND name<>?`, s.ID, name).Scan(&n)
			if n >= 100 {
				return nil, core.Quota("an app can have up to 100 secrets")
			}
			enc, err := core.Encrypt(*in.Value)
			if err != nil {
				return nil, err
			}
			if _, err := a.DB.ExecContext(r.Context(), `INSERT INTO fn_secrets(site_id,name,value,updated_at,updated_by) VALUES(?,?,?,?,?)
ON CONFLICT(site_id,name) DO UPDATE SET value=excluded.value, updated_at=excluded.updated_at, updated_by=excluded.updated_by`, s.ID, name, enc, core.Now(), u.ID); err != nil {
				return nil, err
			}
			a.Audit(r.Context(), "functions.secret.set", s.ID, name, nil)
			return map[string]any{"ok": true, "name": name}, nil
		}})

	a.Handle(core.Op{Name: "secrets.delete", Method: "DELETE", Path: "/api/v1/sites/{site}/secrets/{name}", Destructive: true,
		Summary: "Delete one of this app's secrets",
		Params:  []core.Param{siteParam, {Name: "name", In: "path", Required: true}},
		Fn: func(r *http.Request) (any, error) {
			s, _, err := sites.Full(r.Context(), core.MustUser(r), r.PathValue("site"), core.RoleEditor)
			if err != nil {
				return nil, err
			}
			res, err := a.DB.ExecContext(r.Context(), `DELETE FROM fn_secrets WHERE site_id=? AND name=?`, s.ID, r.PathValue("name"))
			if err != nil {
				return nil, err
			}
			if n, _ := res.RowsAffected(); n == 0 {
				return nil, core.NotFound("there's no secret called " + r.PathValue("name"))
			}
			a.Audit(r.Context(), "functions.secret.delete", s.ID, r.PathValue("name"), nil)
			return nil, nil
		}})

	// ---- status for the signed-in person (dashboard banner, badges) ----
	a.Handle(core.Op{Name: "functions.status", Method: "GET", Path: "/api/v1/functions/status",
		Summary: "Whether server functions are on for you and which of your apps have them turned off",
		Fn: func(r *http.Request) (any, error) {
			u := core.MustUser(r)
			st := current()
			out := map[string]any{"enabled": st.Enabled, "user": nil, "sites": map[string]any{}}
			if !st.Enabled {
				return out, nil
			}
			ctx := r.Context()
			ub := blockOf(ctx, "user", u.ID)
			if ub != nil {
				out["user"] = map[string]any{"blocked": true, "reason": ub.Reason, "by": ub.By, "at": ub.At}
			}
			q := `SELECT s.slug, b.reason FROM sites s JOIN fn_blocks b ON b.kind='site' AND b.target=s.id WHERE s.owner_id=? AND s.deleted_at IS NULL`
			if ub != nil {
				q = `SELECT s.slug, '' FROM sites s WHERE s.owner_id=? AND s.deleted_at IS NULL`
			}
			rows, err := a.DB.QueryContext(ctx, q+` LIMIT 2000`, u.ID)
			if err != nil {
				return nil, err
			}
			defer rows.Close()
			ss := map[string]any{}
			for rows.Next() {
				var slug, reason string
				rows.Scan(&slug, &reason)
				if ub != nil {
					ss[slug] = map[string]any{"scope": "user", "reason": ub.Reason}
				} else {
					ss[slug] = map[string]any{"scope": "site", "reason": reason}
				}
			}
			out["sites"] = ss
			return out, nil
		}})

	// ---- admin ----
	a.Handle(core.Op{Name: "admin.functions.get", Method: "GET", Path: "/api/v1/admin/functions", Access: core.AccessAdmin,
		Summary: "Server function settings: on/off, limits and what they may reach on the internet",
		Fn: func(r *http.Request) (any, error) {
			return current(), nil
		}})

	a.Handle(core.Op{Name: "admin.functions.update", Method: "PUT", Path: "/api/v1/admin/functions", Access: core.AccessAdmin,
		Summary: "Change server function settings (send only the fields to change)",
		Params: []core.Param{{Name: "enabled", In: "body", Type: "boolean"}, {Name: "memoryMb", In: "body", Type: "integer", Desc: "16-1024"},
			{Name: "timeoutMs", In: "body", Type: "integer"}, {Name: "triggerTimeoutMs", In: "body", Type: "integer"},
			{Name: "siteConcurrency", In: "body", Type: "integer"}, {Name: "globalConcurrency", In: "body", Type: "integer"},
			{Name: "outbound", In: "body", Enum: []string{"internet", "allowlist", "off"}, Desc: "internet: any public host; allowlist: only listed hosts; off: only admin connections"},
			{Name: "allowlist", In: "body", Type: "array", Desc: "Hosts like api.stripe.com or *.example.com"}},
		Fn: func(r *http.Request) (any, error) {
			var in struct {
				Enabled           *bool     `json:"enabled"`
				MemoryMB          *int      `json:"memoryMb"`
				TimeoutMs         *int      `json:"timeoutMs"`
				TriggerTimeoutMs  *int      `json:"triggerTimeoutMs"`
				SiteConcurrency   *int      `json:"siteConcurrency"`
				GlobalConcurrency *int      `json:"globalConcurrency"`
				Outbound          *string   `json:"outbound"`
				Allowlist         *[]string `json:"allowlist"`
			}
			if err := core.Decode(r, &in); err != nil {
				return nil, err
			}
			ctx := r.Context()
			changed := map[string]any{}
			set := func(key string, v string, field string, raw any) error {
				if a.SettingFromEnv(key) {
					return core.Conflict(field + " is set by the server's environment (" + strings.ToUpper("BUNKER_"+strings.ReplaceAll(key, ".", "_")) + ") and can't be changed here")
				}
				changed[field] = raw
				return a.SetSetting(ctx, key, v)
			}
			ints := []struct {
				p      *int
				key, f string
				lo, hi int
			}{
				{in.MemoryMB, "functions.memory_mb", "memoryMb", 16, 1024},
				{in.TimeoutMs, "functions.timeout_ms", "timeoutMs", 1000, 300000},
				{in.TriggerTimeoutMs, "functions.trigger_timeout_ms", "triggerTimeoutMs", 1000, 900000},
				{in.SiteConcurrency, "functions.site_concurrency", "siteConcurrency", 1, 256},
				{in.GlobalConcurrency, "functions.global_concurrency", "globalConcurrency", 1, 1024},
			}
			for _, x := range ints {
				if x.p == nil {
					continue
				}
				if *x.p < x.lo || *x.p > x.hi {
					return nil, core.Errorf("%s must be between %d and %d", x.f, x.lo, x.hi)
				}
				if err := set(x.key, strconv.Itoa(*x.p), x.f, *x.p); err != nil {
					return nil, err
				}
			}
			if in.Enabled != nil {
				if err := set("functions.enabled", strconv.FormatBool(*in.Enabled), "enabled", *in.Enabled); err != nil {
					return nil, err
				}
			}
			if in.Outbound != nil {
				switch *in.Outbound {
				case "internet", "allowlist", "off":
				default:
					return nil, core.BadRequest("outbound is internet, allowlist or off")
				}
				if err := set("functions.outbound", *in.Outbound, "outbound", *in.Outbound); err != nil {
					return nil, err
				}
			}
			if in.Allowlist != nil {
				var hs []string
				for _, h := range *in.Allowlist {
					h = strings.ToLower(strings.TrimSpace(h))
					if h == "" {
						continue
					}
					if !hostRe.MatchString(h) {
						return nil, core.BadRequest(h + " isn't a host name. Use names like api.stripe.com or *.example.com")
					}
					hs = append(hs, h)
				}
				if len(hs) > 500 {
					return nil, core.BadRequest("the allowlist can have up to 500 hosts")
				}
				if err := set("functions.allowlist", strings.Join(hs, ","), "allowlist", hs); err != nil {
					return nil, err
				}
			}
			a.Audit(ctx, "functions.settings", 0, "", changed)
			return current(), nil
		}})

	a.Handle(core.Op{Name: "admin.functions.blocks", Method: "GET", Path: "/api/v1/admin/functions/blocks", Access: core.AccessAdmin,
		Summary: "People and apps with server functions turned off",
		Fn: func(r *http.Request) (any, error) {
			rows, err := a.DB.QueryContext(r.Context(), `SELECT b.kind,b.target,b.reason,COALESCE(by.name,''),b.at,
  CASE b.kind WHEN 'user' THEN COALESCE(u.name,'') ELSE COALESCE(s.title,'') END,
  CASE b.kind WHEN 'user' THEN COALESCE(u.email,'') ELSE COALESCE(s.slug,'') END
FROM fn_blocks b LEFT JOIN users by ON by.id=b.by_id
LEFT JOIN users u ON b.kind='user' AND u.id=b.target
LEFT JOIN sites s ON b.kind='site' AND s.id=b.target
ORDER BY b.at DESC`)
			if err != nil {
				return nil, err
			}
			defer rows.Close()
			items := []map[string]any{}
			for rows.Next() {
				var kind, reason, by, label, sub string
				var target, at int64
				rows.Scan(&kind, &target, &reason, &by, &at, &label, &sub)
				it := map[string]any{"kind": kind, "reason": reason, "by": by, "at": at, "label": label}
				if kind == "user" {
					it["id"], it["email"] = strconv.FormatInt(target, 10), sub
				} else {
					it["id"], it["slug"] = sub, sub
				}
				items = append(items, it)
			}
			return map[string]any{"items": items}, nil
		}})

	blockParams := []core.Param{{Name: "kind", In: "path", Required: true, Enum: []string{"user", "site"}},
		{Name: "id", In: "path", Required: true, Desc: "A person's id, or an app's address"}}
	a.Handle(core.Op{Name: "admin.functions.block", Method: "PUT", Path: "/api/v1/admin/functions/blocks/{kind}/{id}", Access: core.AccessAdmin,
		Summary: "Turn off server functions for a person (all apps they own) or one app",
		Params:  append(blockParams, core.Param{Name: "reason", In: "body", Desc: "Shown to the person on their dashboard"}),
		Fn: func(r *http.Request) (any, error) {
			kind, target, label, err := blockTarget(r.Context(), r.PathValue("kind"), r.PathValue("id"))
			if err != nil {
				return nil, err
			}
			var in struct {
				Reason string `json:"reason"`
			}
			if r.ContentLength != 0 {
				if err := core.Decode(r, &in); err != nil {
					return nil, err
				}
			}
			in.Reason = strings.TrimSpace(in.Reason)
			if len(in.Reason) > 500 {
				return nil, core.BadRequest("keep the reason under 500 characters")
			}
			u := core.MustUser(r)
			if _, err := a.DB.ExecContext(r.Context(), `INSERT INTO fn_blocks(kind,target,reason,by_id,at) VALUES(?,?,?,?,?)
ON CONFLICT(kind,target) DO UPDATE SET reason=excluded.reason, by_id=excluded.by_id, at=excluded.at`, kind, target, in.Reason, u.ID, core.Now()); err != nil {
				return nil, err
			}
			var sid int64
			if kind == "site" {
				sid = target
			}
			a.Audit(r.Context(), "functions.block", sid, label, map[string]any{"kind": kind, "reason": in.Reason})
			return map[string]any{"ok": true, "kind": kind, "id": r.PathValue("id"), "reason": in.Reason}, nil
		}})

	a.Handle(core.Op{Name: "admin.functions.unblock", Method: "DELETE", Path: "/api/v1/admin/functions/blocks/{kind}/{id}", Access: core.AccessAdmin,
		Summary: "Turn server functions back on for a person or an app",
		Params:  blockParams,
		Fn: func(r *http.Request) (any, error) {
			kind, target, label, err := blockTarget(r.Context(), r.PathValue("kind"), r.PathValue("id"))
			if err != nil {
				return nil, err
			}
			res, err := a.DB.ExecContext(r.Context(), `DELETE FROM fn_blocks WHERE kind=? AND target=?`, kind, target)
			if err != nil {
				return nil, err
			}
			if n, _ := res.RowsAffected(); n == 0 {
				return nil, core.NotFound("server functions aren't turned off for " + label)
			}
			var sid int64
			if kind == "site" {
				sid = target
			}
			a.Audit(r.Context(), "functions.unblock", sid, label, map[string]any{"kind": kind})
			return nil, nil
		}})
}

var hostRe = regexp.MustCompile(`^(\*\.)?[a-z0-9]([a-z0-9-]*[a-z0-9])?(\.[a-z0-9]([a-z0-9-]*[a-z0-9])?)*$`)

func blockTarget(ctx context.Context, kind, id string) (string, int64, string, error) {
	switch kind {
	case "user":
		n, err := strconv.ParseInt(id, 10, 64)
		if err != nil {
			return "", 0, "", core.BadRequest("use the person's numeric id")
		}
		var email string
		if app.DB.QueryRowContext(ctx, `SELECT email FROM users WHERE id=?`, n).Scan(&email) != nil {
			return "", 0, "", core.NotFound("no such person")
		}
		return kind, n, email, nil
	case "site":
		s, err := sites.GetBySlug(ctx, id)
		if err != nil {
			return "", 0, "", core.NotFound("no app at " + id)
		}
		return kind, s.ID, s.Slug, nil
	}
	return "", 0, "", core.BadRequest("kind is user or site")
}
