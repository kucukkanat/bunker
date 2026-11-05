package auth

import (
	"encoding/json"
	"net/http"
	"strconv"

	"github.com/kucukkanat/bunker/internal/core"
)

type userRow struct {
	ID        int64        `json:"id"`
	Email     string       `json:"email"`
	Name      string       `json:"name"`
	IsAdmin   bool         `json:"isAdmin"`
	Disabled  bool         `json:"disabled"`
	Groups    []core.Group `json:"groups"`
	CreatedAt int64        `json:"createdAt"`
	LastSeen  int64        `json:"lastSeenAt"`
}

func registerUsers(a *core.App) {
	a.Handle(core.Op{Name: "me.get", Method: "GET", Path: "/api/v1/me", Summary: "Who am I",
		Fn: func(r *http.Request) (any, error) {
			u := core.MustUser(r)
			return map[string]any{"id": u.ID, "email": u.Email, "name": u.Name, "isAdmin": u.IsAdmin, "groups": nonNil(u.Groups), "dev": a.Cfg.Dev, "viaToken": u.ViaToken}, nil
		}})
	a.Handle(core.Op{Name: "me.logout_everywhere", Method: "POST", Path: "/api/v1/me/logout-everywhere", Summary: "Sign out of every browser session", Destructive: true,
		Fn: func(r *http.Request) (any, error) {
			u := core.MustUser(r)
			a.DB.ExecContext(r.Context(), `DELETE FROM sessions WHERE user_id=?`, u.ID)
			InvalidateUser(u.ID)
			a.Audit(r.Context(), "auth.logout_everywhere", 0, "", nil)
			return map[string]any{"ok": true}, nil
		}})
	a.Handle(core.Op{Name: "users.list", Method: "GET", Path: "/api/v1/admin/users", Summary: "List everyone who has signed in", Access: core.AccessAdmin,
		Params: []core.Param{{Name: "q", In: "query", Desc: "Filter by name or email"}},
		Fn: func(r *http.Request) (any, error) {
			q := "%" + r.URL.Query().Get("q") + "%"
			rows, err := a.DB.QueryContext(r.Context(), `SELECT id,email,name,is_admin,disabled,groups_json,created_at,last_seen_at FROM users WHERE (subject NOT LIKE 'dev:%' OR ? = 1) AND subject NOT LIKE 'system:%' ORDER BY last_seen_at DESC LIMIT 500`, boolInt(a.Cfg.Dev))
			if err != nil {
				return nil, err
			}
			defer rows.Close()
			out := []userRow{}
			for rows.Next() {
				var u userRow
				var adm, dis int
				var gj string
				rows.Scan(&u.ID, &u.Email, &u.Name, &adm, &dis, &gj, &u.CreatedAt, &u.LastSeen)
				json.Unmarshal([]byte(gj), &u.Groups)
				if u.Groups == nil {
					u.Groups = []core.Group{}
				}
				u.IsAdmin, u.Disabled = adm == 1 || isConfiguredAdmin(&core.User{Email: u.Email, Groups: u.Groups}), dis == 1
				if q != "%%" && !containsFold(u.Name+" "+u.Email, r.URL.Query().Get("q")) {
					continue
				}
				out = append(out, u)
			}
			return map[string]any{"users": out}, nil
		}})
	a.Handle(core.Op{Name: "users.update", Method: "PATCH", Path: "/api/v1/admin/users/{id}", Summary: "Make someone an admin, or disable their account", Access: core.AccessAdmin,
		Params: []core.Param{
			{Name: "id", In: "path", Type: "integer", Required: true},
			{Name: "isAdmin", In: "body", Type: "boolean"},
			{Name: "disabled", In: "body", Type: "boolean"},
		},
		Fn: func(r *http.Request) (any, error) {
			id, _ := strconv.ParseInt(r.PathValue("id"), 10, 64)
			var in struct {
				IsAdmin  *bool `json:"isAdmin"`
				Disabled *bool `json:"disabled"`
			}
			if err := core.Decode(r, &in); err != nil {
				return nil, err
			}
			if id == core.MustUser(r).ID && ((in.IsAdmin != nil && !*in.IsAdmin) || (in.Disabled != nil && *in.Disabled)) {
				return nil, core.BadRequest("you can't remove your own admin access or disable yourself")
			}
			if in.IsAdmin != nil {
				a.DB.ExecContext(r.Context(), `UPDATE users SET is_admin=? WHERE id=?`, boolInt(*in.IsAdmin), id)
			}
			if in.Disabled != nil {
				a.DB.ExecContext(r.Context(), `UPDATE users SET disabled=? WHERE id=?`, boolInt(*in.Disabled), id)
				if *in.Disabled {
					a.DB.ExecContext(r.Context(), `DELETE FROM sessions WHERE user_id=?`, id)
					a.DB.ExecContext(r.Context(), `DELETE FROM tokens WHERE user_id=?`, id)
				}
			}
			InvalidateUser(id)
			a.Audit(r.Context(), "admin.user_update", 0, strconv.FormatInt(id, 10), map[string]any{"isAdmin": in.IsAdmin, "disabled": in.Disabled})
			return map[string]any{"ok": true}, nil
		}})
}

func nonNil(g []core.Group) []core.Group {
	if g == nil {
		return []core.Group{}
	}
	return g
}

func boolInt(b bool) int {
	if b {
		return 1
	}
	return 0
}

func containsFold(s, sub string) bool {
	return len(sub) == 0 || indexFold(s, sub) >= 0
}

func indexFold(s, sub string) int {
	ls, lsub := []rune(lower(s)), []rune(lower(sub))
	for i := 0; i+len(lsub) <= len(ls); i++ {
		if string(ls[i:i+len(lsub)]) == string(lsub) {
			return i
		}
	}
	return -1
}

func lower(s string) string {
	b := []rune(s)
	for i, r := range b {
		if r >= 'A' && r <= 'Z' {
			b[i] = r + 32
		}
	}
	return string(b)
}
