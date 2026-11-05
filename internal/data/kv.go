package data

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/kucukkanat/bunker/internal/core"
)

// Per-viewer key/value storage (bunker.storage and the localStorage shim).

func kvMaxValue() int   { return core.EnvInt("KV_MAX_VALUE_KB", 64) << 10 }
func kvMaxKeys() int    { return core.EnvInt("KV_MAX_KEYS", 1000) }
func kvMaxTotal() int64 { return int64(core.EnvInt("KV_MAX_TOTAL_MB", 5)) << 20 }

const kvMaxKeyLen = 256

func checkKey(k string) error {
	if k == "" || len(k) > kvMaxKeyLen || !utf8.ValidString(k) || strings.ContainsAny(k, "\x00") {
		return core.BadRequest("storage keys are 1–256 characters")
	}
	return nil
}

func registerKV(a *core.App) {
	keyParam := core.Param{Name: "key", In: "path", Required: true}
	a.Handle(core.Op{Name: "kv.list", Method: "GET", Path: "/api/v1/sites/{site}/kv", Summary: "List your stored keys for this site (optionally with values)", PageSafe: true,
		Params: []core.Param{siteParam, {Name: "prefix", In: "query"}, {Name: "values", In: "query", Type: "boolean", Desc: "Include values"}},
		Fn: func(r *http.Request) (any, error) {
			s, _, u, err := siteFor(r, core.RoleViewer)
			if err != nil {
				return nil, err
			}
			prefix := r.URL.Query().Get("prefix")
			withValues := r.URL.Query().Get("values") == "1" || r.URL.Query().Get("values") == "true"
			cols := "key"
			if withValues {
				cols = "key, value"
			}
			// Prefix match via a range scan on the primary key.
			q := `SELECT ` + cols + ` FROM kv WHERE site_id=? AND user_id=?`
			args := []any{s.ID, u.ID}
			if prefix != "" {
				q += ` AND key >= ? AND key < ?`
				args = append(args, prefix, prefix+"\U0010FFFF")
			}
			q += ` ORDER BY key LIMIT ?`
			args = append(args, kvMaxKeys())
			rows, err := a.DB.QueryContext(r.Context(), q, args...)
			if err != nil {
				return nil, err
			}
			defer rows.Close()
			keys := []string{}
			items := map[string]json.RawMessage{}
			for rows.Next() {
				var k, v string
				if withValues {
					rows.Scan(&k, &v)
					items[k] = json.RawMessage(v)
				} else {
					rows.Scan(&k)
				}
				keys = append(keys, k)
			}
			if withValues {
				return map[string]any{"keys": keys, "items": items}, nil
			}
			return map[string]any{"keys": keys}, nil
		}})

	a.Handle(core.Op{Name: "kv.get", Method: "GET", Path: "/api/v1/sites/{site}/kv/{key}", Summary: "Read one of your stored values", PageSafe: true,
		Params: []core.Param{siteParam, keyParam},
		Fn: func(r *http.Request) (any, error) {
			s, _, u, err := siteFor(r, core.RoleViewer)
			if err != nil {
				return nil, err
			}
			k := r.PathValue("key")
			if err := checkKey(k); err != nil {
				return nil, err
			}
			var v string
			if a.DB.QueryRowContext(r.Context(), `SELECT value FROM kv WHERE site_id=? AND user_id=? AND key=?`, s.ID, u.ID, k).Scan(&v) != nil {
				return map[string]any{"key": k, "value": nil, "exists": false}, nil
			}
			return map[string]any{"key": k, "value": json.RawMessage(v), "exists": true}, nil
		}})

	a.Handle(core.Op{Name: "kv.set", Method: "PUT", Path: "/api/v1/sites/{site}/kv/{key}", Summary: "Store a value for yourself (64 KB at most)", PageSafe: true,
		Params: []core.Param{siteParam, keyParam, {Name: "value", In: "body", Required: true, Desc: "Any JSON value"}},
		Fn: func(r *http.Request) (any, error) {
			s, _, u, err := siteFor(r, core.RoleViewer)
			if err != nil {
				return nil, err
			}
			k := r.PathValue("key")
			if err := checkKey(k); err != nil {
				return nil, err
			}
			raw, err := io.ReadAll(io.LimitReader(r.Body, int64(kvMaxValue())+1024))
			if err != nil {
				return nil, core.BadRequest("couldn't read the request")
			}
			if len(raw) > kvMaxValue()+512 {
				return nil, core.TooLarge("stored values can be " + strconv.Itoa(kvMaxValue()>>10) + " KB at most. Use bunker.db or bunker.files for bigger data.")
			}
			var in struct {
				Value json.RawMessage `json:"value"`
			}
			if err := json.Unmarshal(raw, &in); err != nil {
				return nil, core.BadRequest(`send JSON like {"value": ...}`)
			}
			var buf bytes.Buffer
			if len(in.Value) == 0 {
				buf.WriteString("null")
			} else if err := json.Compact(&buf, in.Value); err != nil {
				return nil, core.BadRequest("value must be JSON")
			}
			if buf.Len() > kvMaxValue() {
				return nil, core.TooLarge("stored values can be " + strconv.Itoa(kvMaxValue()>>10) + " KB at most. Use bunker.db or bunker.files for bigger data.")
			}
			ctx := r.Context()
			tx, err := a.DB.BeginTx(ctx, nil)
			if err != nil {
				return nil, err
			}
			defer tx.Rollback()
			var n int
			var total, old int64
			tx.QueryRowContext(ctx, `SELECT COUNT(*), COALESCE(SUM(size),0) FROM kv WHERE site_id=? AND user_id=?`, s.ID, u.ID).Scan(&n, &total)
			exists := tx.QueryRowContext(ctx, `SELECT size FROM kv WHERE site_id=? AND user_id=? AND key=?`, s.ID, u.ID, k).Scan(&old) == nil
			if !exists && n >= kvMaxKeys() {
				return nil, core.Quota("you've stored " + strconv.Itoa(n) + " values on this site, the most allowed. Remove some first.")
			}
			if total-old+int64(buf.Len()) > kvMaxTotal() {
				return nil, core.Quota("your storage on this site is full. Remove some values first.")
			}
			if _, err := tx.ExecContext(ctx, `INSERT INTO kv(site_id,user_id,key,value,size,updated_at) VALUES(?,?,?,?,?,?)
ON CONFLICT(site_id,user_id,key) DO UPDATE SET value=excluded.value, size=excluded.size, updated_at=excluded.updated_at`,
				s.ID, u.ID, k, buf.String(), buf.Len(), nowMS()); err != nil {
				return nil, err
			}
			if err := tx.Commit(); err != nil {
				return nil, err
			}
			return map[string]any{"ok": true}, nil
		}})

	a.Handle(core.Op{Name: "kv.delete", Method: "DELETE", Path: "/api/v1/sites/{site}/kv/{key}", Summary: "Remove one of your stored values", PageSafe: true,
		Params: []core.Param{siteParam, keyParam},
		Fn: func(r *http.Request) (any, error) {
			s, _, u, err := siteFor(r, core.RoleViewer)
			if err != nil {
				return nil, err
			}
			k := r.PathValue("key")
			if err := checkKey(k); err != nil {
				return nil, err
			}
			_, err = a.DB.ExecContext(r.Context(), `DELETE FROM kv WHERE site_id=? AND user_id=? AND key=?`, s.ID, u.ID, k)
			return nil, err
		}})

	a.Handle(core.Op{Name: "kv.clear", Method: "DELETE", Path: "/api/v1/sites/{site}/kv", Summary: "Remove all your stored values for this site (optionally only a prefix)", PageSafe: true, Destructive: true,
		Params: []core.Param{siteParam, {Name: "prefix", In: "query"}},
		Fn: func(r *http.Request) (any, error) {
			s, _, u, err := siteFor(r, core.RoleViewer)
			if err != nil {
				return nil, err
			}
			q := `DELETE FROM kv WHERE site_id=? AND user_id=?`
			args := []any{s.ID, u.ID}
			if p := r.URL.Query().Get("prefix"); p != "" {
				q += ` AND key >= ? AND key < ?`
				args = append(args, p, p+"\U0010FFFF")
			}
			res, err := a.DB.ExecContext(r.Context(), q, args...)
			if err != nil {
				return nil, err
			}
			n, _ := res.RowsAffected()
			return map[string]any{"deleted": n}, nil
		}})
}

// registerMe adds the page-safe "who is viewing" op. The platform's
// /api/v1/me is not page-safe, so the SDK bridge maps bunker.me() here.
func registerMe(a *core.App) {
	a.Handle(core.Op{Name: "me.site", Method: "GET", Path: "/api/v1/sites/{site}/me", Summary: "Who is viewing this site, and their role on it", PageSafe: true,
		Params: []core.Param{siteParam},
		Fn: func(r *http.Request) (any, error) {
			s, role, u, err := siteFor(r, core.RoleViewer)
			if err != nil {
				return nil, err
			}
			// Only reveal groups this site actually uses (sharing or data rules).
			used := map[string]bool{}
			if rows, err := a.DB.QueryContext(r.Context(), `SELECT pid FROM site_members WHERE site_id=? AND ptype='group'`, s.ID); err == nil {
				for rows.Next() {
					var g string
					rows.Scan(&g)
					used[strings.ToLower(g)] = true
				}
				rows.Close()
			}
			if rows, err := a.DB.QueryContext(r.Context(), `SELECT rule FROM collections WHERE site_id=? AND rule LIKE 'group:%'`, s.ID); err == nil {
				for rows.Next() {
					var g string
					rows.Scan(&g)
					used[strings.ToLower(strings.TrimPrefix(g, "group:"))] = true
				}
				rows.Close()
			}
			groups := []string{}
			for _, g := range u.Groups {
				if used[strings.ToLower(g.ID)] || used[strings.ToLower(g.Name)] {
					groups = append(groups, g.Name)
				}
			}
			return map[string]any{"id": u.ID, "name": u.Name, "email": u.Email, "groups": groups, "role": role.String(),
				"canEdit": role >= core.RoleEditor}, nil
		}})

	a.Handle(core.Op{Name: "me.in_group", Method: "GET", Path: "/api/v1/sites/{site}/me/groups/{group}", Summary: "Is the viewer in this group?", PageSafe: true,
		Params: []core.Param{siteParam, {Name: "group", In: "path", Required: true, Desc: "Group name or id"}},
		Fn: func(r *http.Request) (any, error) {
			_, _, u, err := siteFor(r, core.RoleViewer)
			if err != nil {
				return nil, err
			}
			g := r.PathValue("group")
			return map[string]any{"group": g, "member": u.InGroup(g)}, nil
		}})
}
