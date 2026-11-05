package auth

import (
	"encoding/json"
	"net/http"
	"strconv"
	"strings"

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
	Sites     int64        `json:"sites"` // sites they own (not in the trash)
}

func registerUsers(a *core.App) {
	a.Handle(core.Op{Name: "me.get", Method: "GET", Path: "/api/v1/me", Summary: "Who am I",
		Fn: func(r *http.Request) (any, error) {
			u := core.MustUser(r)
			return map[string]any{"id": u.ID, "email": u.Email, "name": u.Name, "isAdmin": u.IsAdmin, "groups": nonNil(u.Groups), "dev": a.Cfg.Dev, "viaToken": u.ViaToken,
				"policy": map[string]any{"publicLinks": a.Setting("sharing.public_links") == "true", "orgWideSharing": a.OrgWideSharing()}}, nil
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
			// Filter in SQL: filtering after LIMIT 500 hid everyone but the
			// 500 most recently seen people from the search box.
			q := "%" + likeEscape(r.URL.Query().Get("q")) + "%"
			rows, err := a.DB.QueryContext(r.Context(), `SELECT id,email,name,is_admin,disabled,groups_json,created_at,last_seen_at FROM users WHERE (subject NOT LIKE 'dev:%' OR ? = 1) AND subject NOT LIKE 'system:%' AND (name || ' ' || email) LIKE ? ESCAPE '\' ORDER BY last_seen_at DESC LIMIT 500`, boolInt(a.Cfg.Dev), q)
			if err != nil {
				return nil, err
			}
			defer rows.Close()
			owned := map[int64]int64{}
			if srows, err := a.DB.QueryContext(r.Context(), `SELECT owner_id, COUNT(*) FROM sites WHERE deleted_at IS NULL GROUP BY owner_id`); err == nil {
				for srows.Next() {
					var id, n int64
					srows.Scan(&id, &n)
					owned[id] = n
				}
				srows.Close()
			}
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
				u.Sites = owned[u.ID]
				out = append(out, u)
			}
			return map[string]any{"users": out}, nil
		}})
	a.Handle(core.Op{Name: "users.update", Method: "PATCH", Path: "/api/v1/admin/users/{id}", Summary: "Make someone an admin, or disable their account", Access: core.AccessAdmin,
		Params: []core.Param{
			{Name: "id", In: "path", Type: "integer", Required: true},
			{Name: "isAdmin", In: "body", Type: "boolean"},
			{Name: "disabled", In: "body", Type: "boolean"},
			{Name: "transferTo", In: "body", Desc: "When disabling: email of the person who gets their sites"},
		},
		Fn: func(r *http.Request) (any, error) {
			id, _ := strconv.ParseInt(r.PathValue("id"), 10, 64)
			var in struct {
				IsAdmin    *bool  `json:"isAdmin"`
				Disabled   *bool  `json:"disabled"`
				TransferTo string `json:"transferTo"`
			}
			if err := core.Decode(r, &in); err != nil {
				return nil, err
			}
			var exists int
			if a.DB.QueryRowContext(r.Context(), `SELECT 1 FROM users WHERE id=?`, id).Scan(&exists) != nil {
				return nil, core.NotFound("that person isn't in Bunker. Refresh the list and try again.")
			}
			var toID int64
			if t :=strings.TrimSpace(in.TransferTo); t != "" {
				if in.Disabled == nil || !*in.Disabled {
					return nil, core.BadRequest("transferTo only works together with disabled: true")
				}
				if a.DB.QueryRowContext(r.Context(), `SELECT id FROM users WHERE lower(email)=lower(?) AND disabled=0`, t).Scan(&toID) != nil {
					return nil, core.NotFound(t + " hasn't signed in to Bunker yet, or their account is disabled. Pick someone else.")
				}
				if toID == id {
					return nil, core.BadRequest("pick someone other than the person you're disabling")
				}
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
			out := map[string]any{"ok": true}
			if in.Disabled != nil && *in.Disabled {
				// The sites module moves their sites (auth can't import it).
				ev := &core.UserDisabled{UserID: id, TransferTo: toID}
				a.Emit(r.Context(), "user.disabled", ev)
				if toID != 0 {
					a.Audit(r.Context(), "admin.user_offboard", 0, strconv.FormatInt(id, 10), map[string]any{"transferTo": strings.ToLower(strings.TrimSpace(in.TransferTo)), "moved": ev.Moved})
					out["moved"] = ev.Moved
				}
			}
			return out, nil
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

func likeEscape(s string) string {
	return strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`).Replace(s)
}
