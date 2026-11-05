// Package hooks provides inbound webhooks: a secret URL that other tools
// (Power Automate, Zapier, scripts, forms) can POST JSON to, which lands as
// documents in one of a site's bunker.db collections. No code runs on the
// server; the request is written as the person who created the hook.
package hooks

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/kucukkanat/bunker/internal/auth"
	"github.com/kucukkanat/bunker/internal/core"
	"github.com/kucukkanat/bunker/internal/sites"
)

func init() {
	core.Register(core.Module{
		Name:  "hooks",
		Order: 70,
		Migrations: []string{`
CREATE TABLE hooks (
  id INTEGER PRIMARY KEY,
  site_id INTEGER NOT NULL REFERENCES sites(id) ON DELETE CASCADE,
  name TEXT NOT NULL,
  coll TEXT NOT NULL,
  token_hash TEXT UNIQUE NOT NULL,
  prefix TEXT NOT NULL,
  created_by INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  created_at INTEGER NOT NULL,
  last_used_at INTEGER,
  calls INTEGER NOT NULL DEFAULT 0
);
CREATE INDEX hooks_site ON hooks(site_id);
`},
		Init: initHooks,
	})
}

var app *core.App

const tokenPrefix = "bkh_"

var siteParam = core.Param{Name: "site", In: "path", Required: true}

func initHooks(a *core.App) error {
	app = a
	a.Public("/hooks/")
	a.Mux.HandleFunc("POST /hooks/{token}", receive)

	a.Handle(core.Op{Name: "hooks.list", Method: "GET", Path: "/api/v1/sites/{site}/hooks", Summary: "Inbound webhooks that write into this site's data",
		Params: []core.Param{siteParam},
		Fn: func(r *http.Request) (any, error) {
			s, _, err := sites.Full(r.Context(), core.MustUser(r), r.PathValue("site"), core.RoleEditor)
			if err != nil {
				return nil, err
			}
			rows, err := a.DB.QueryContext(r.Context(), `SELECT h.id,h.name,h.coll,h.prefix,h.created_at,COALESCE(h.last_used_at,0),h.calls,COALESCE(u.name,'') FROM hooks h LEFT JOIN users u ON u.id=h.created_by WHERE h.site_id=? ORDER BY h.id`, s.ID)
			if err != nil {
				return nil, err
			}
			defer rows.Close()
			out := []map[string]any{}
			for rows.Next() {
				var id, ca, lu, calls int64
				var name, coll, prefix, by string
				rows.Scan(&id, &name, &coll, &prefix, &ca, &lu, &calls, &by)
				out = append(out, map[string]any{"id": id, "name": name, "collection": coll, "prefix": prefix, "createdAt": ca, "lastUsedAt": lu, "calls": calls, "createdBy": by})
			}
			return map[string]any{"hooks": out}, nil
		}})

	a.Handle(core.Op{Name: "hooks.create", Method: "POST", Path: "/api/v1/sites/{site}/hooks", Summary: "Create a webhook URL that adds JSON it receives to a collection",
		Params: []core.Param{siteParam, {Name: "name", In: "body", Required: true, Desc: "What sends to it, e.g. 'Power Automate: new hires'"},
			{Name: "collection", In: "body", Required: true, Desc: "Collection the data goes into"}},
		Fn: func(r *http.Request) (any, error) {
			u := core.MustUser(r)
			s, _, err := sites.Full(r.Context(), u, r.PathValue("site"), core.RoleEditor)
			if err != nil {
				return nil, err
			}
			var in struct{ Name, Collection string }
			if err := core.Decode(r, &in); err != nil {
				return nil, err
			}
			in.Name, in.Collection = strings.TrimSpace(in.Name), strings.TrimSpace(in.Collection)
			if in.Name == "" || len(in.Name) > 100 || in.Collection == "" || len(in.Collection) > 64 {
				return nil, core.BadRequest("name (up to 100 characters) and collection are required")
			}
			var n int
			a.DB.QueryRowContext(r.Context(), `SELECT COUNT(*) FROM hooks WHERE site_id=?`, s.ID).Scan(&n)
			if n >= 20 {
				return nil, core.Quota("a site can have up to 20 webhooks")
			}
			tok := tokenPrefix + core.RandomToken(30)
			res, err := a.DB.ExecContext(r.Context(), `INSERT INTO hooks(site_id,name,coll,token_hash,prefix,created_by,created_at) VALUES(?,?,?,?,?,?,?)`,
				s.ID, in.Name, in.Collection, core.HashToken(tok), tok[:10], u.ID, core.Now())
			if err != nil {
				return nil, err
			}
			id, _ := res.LastInsertId()
			a.Audit(r.Context(), "hook.create", s.ID, in.Name, map[string]any{"collection": in.Collection})
			hookURL := a.Cfg.URL + "/hooks/" + tok
			return core.Created{V: map[string]any{
				"id": id, "url": hookURL,
				"note":    "Copy this URL now; it won't be shown again. Anyone who has it can add data, so keep it secret.",
				"example": "curl -X POST " + hookURL + " -H 'Content-Type: application/json' -d '{\"name\":\"Ada\",\"amount\":42}'",
			}}, nil
		}})

	a.Handle(core.Op{Name: "hooks.delete", Method: "DELETE", Path: "/api/v1/sites/{site}/hooks/{id}", Summary: "Delete a webhook (its URL stops working)", Destructive: true,
		Params: []core.Param{siteParam, {Name: "id", In: "path", Type: "integer", Required: true}},
		Fn: func(r *http.Request) (any, error) {
			s, _, err := sites.Full(r.Context(), core.MustUser(r), r.PathValue("site"), core.RoleEditor)
			if err != nil {
				return nil, err
			}
			id, _ := strconv.ParseInt(r.PathValue("id"), 10, 64)
			res, _ := a.DB.ExecContext(r.Context(), `DELETE FROM hooks WHERE id=? AND site_id=?`, id, s.ID)
			if n, _ := res.RowsAffected(); n == 0 {
				return nil, core.NotFound("webhook not found")
			}
			a.Audit(r.Context(), "hook.delete", s.ID, strconv.FormatInt(id, 10), nil)
			return map[string]any{"ok": true}, nil
		}})
	return nil
}

// per-hook rate limit: 120 requests per minute.
var (
	rlMu sync.Mutex
	rl   = map[int64][]time.Time{}
)

func allow(id int64) bool {
	rlMu.Lock()
	defer rlMu.Unlock()
	now := time.Now()
	var keep []time.Time
	for _, t := range rl[id] {
		if now.Sub(t) < time.Minute {
			keep = append(keep, t)
		}
	}
	if len(keep) >= 120 {
		rl[id] = keep
		return false
	}
	rl[id] = append(keep, now)
	if len(rl) > 10000 {
		rl = map[int64][]time.Time{}
	}
	return true
}

func receive(w http.ResponseWriter, r *http.Request) {
	tok := r.PathValue("token")
	if !strings.HasPrefix(tok, tokenPrefix) {
		core.Err(w, core.NotFound("unknown webhook"))
		return
	}
	var id, siteID, creator int64
	var coll string
	if app.DB.QueryRowContext(r.Context(), `SELECT id,site_id,created_by,coll FROM hooks WHERE token_hash=?`, core.HashToken(tok)).Scan(&id, &siteID, &creator, &coll) != nil {
		core.Err(w, core.NotFound("unknown webhook"))
		return
	}
	if !allow(id) {
		w.Header().Set("Retry-After", "30")
		core.Err(w, core.Quota("too many requests to this webhook"))
		return
	}
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 1<<20))
	if err != nil {
		core.Err(w, core.TooLarge("send at most 1 MB per request"))
		return
	}
	// Accept JSON objects, arrays of objects, or form posts.
	var docs []map[string]any
	ct := r.Header.Get("Content-Type")
	switch {
	case strings.HasPrefix(ct, "application/x-www-form-urlencoded"):
		v, _ := url.ParseQuery(string(body))
		d := map[string]any{}
		for k, vs := range v {
			if len(vs) == 1 {
				d[k] = vs[0]
			} else {
				d[k] = vs
			}
		}
		docs = []map[string]any{d}
	default:
		trim := bytes.TrimSpace(body)
		if len(trim) > 0 && trim[0] == '[' {
			if err := json.Unmarshal(trim, &docs); err != nil {
				core.Err(w, core.BadRequest("send a JSON object or an array of objects"))
				return
			}
		} else {
			var d map[string]any
			if err := json.Unmarshal(trim, &d); err != nil {
				core.Err(w, core.BadRequest("send a JSON object or an array of objects"))
				return
			}
			docs = []map[string]any{d}
		}
	}
	if len(docs) == 0 || len(docs) > 100 {
		core.Err(w, core.BadRequest("send 1 to 100 records per request"))
		return
	}
	s, err := sites.GetByID(r.Context(), siteID, false)
	if err != nil {
		core.Err(w, core.NotFound("unknown webhook"))
		return
	}
	u, err := auth.LoadUser(r.Context(), creator)
	if err != nil || u == nil || sites.RoleOf(r.Context(), u, s) < core.RoleEditor {
		// The creator left or lost edit access: the hook stops working.
		core.Err(w, core.Forbidden("this webhook's owner no longer has edit access"))
		return
	}
	ctx := core.WithIP(core.WithUser(context.WithoutCancel(r.Context()), u), core.IPFrom(r.Context()))
	ids := []any{}
	for _, d := range docs {
		for k := range d {
			if strings.HasPrefix(k, "_") {
				delete(d, k) // system fields are Bunker's
			}
		}
		b, _ := json.Marshal(d)
		req := httptest.NewRequest("POST", "/api/v1/sites/"+s.Slug+"/db/"+url.PathEscape(coll), bytes.NewReader(b)).WithContext(ctx)
		req.Header.Set("Content-Type", "application/json")
		rec := httptest.NewRecorder()
		app.Mux.ServeHTTP(rec, req)
		if rec.Code >= 300 {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(rec.Code)
			w.Write(rec.Body.Bytes())
			return
		}
		var out map[string]any
		json.Unmarshal(rec.Body.Bytes(), &out)
		ids = append(ids, out["_id"])
	}
	app.DB.ExecContext(ctx, `UPDATE hooks SET calls=calls+?, last_used_at=? WHERE id=?`, len(docs), core.Now(), id)
	core.JSON(w, 201, map[string]any{"ok": true, "added": len(ids), "ids": ids})
}
