package sites

import (
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
	"sync"

	"github.com/kucukkanat/bunker/internal/core"
)

var siteParam = core.Param{Name: "site", In: "path", Required: true, Desc: "Site address (slug)"}

func person(ctx context.Context, id int64) *Person {
	p := &Person{ID: id}
	if app.DB.QueryRowContext(ctx, `SELECT name,email FROM users WHERE id=?`, id).Scan(&p.Name, &p.Email) != nil {
		return nil
	}
	return p
}

// Member is a sharing entry.
type Member struct {
	Type    string `json:"type"` // user | group | org
	ID      string `json:"id"`
	Name    string `json:"name"`
	Role    string `json:"role"`
	AddedAt int64  `json:"addedAt"`
}

func members(ctx context.Context, siteID int64) []Member {
	out := []Member{}
	rows, err := app.DB.QueryContext(ctx, `SELECT ptype,pid,pname,role,added_at FROM site_members WHERE site_id=? ORDER BY role DESC, pname`, siteID)
	if err != nil {
		return out
	}
	defer rows.Close()
	for rows.Next() {
		var m Member
		var r int
		rows.Scan(&m.Type, &m.ID, &m.Name, &r, &m.AddedAt)
		m.Role = core.Role(r).String()
		out = append(out, m)
	}
	return out
}

func registerOps(a *core.App) {
	a.Handle(core.Op{Name: "sites.list", Method: "GET", Path: "/api/v1/sites", Summary: "List sites you can open",
		Params: []core.Param{
			{Name: "filter", In: "query", Enum: []string{"all", "mine", "shared", "org", "starred", "recent", "templates", "trash"}, Desc: "Which sites (default all)"},
			{Name: "q", In: "query", Desc: "Search titles"},
		},
		Fn: func(r *http.Request) (any, error) { return listSites(r) }})

	a.Handle(core.Op{Name: "sites.create", Method: "POST", Path: "/api/v1/sites", Summary: "Create a new site",
		Params: []core.Param{
			{Name: "title", In: "body", Required: true, Desc: "Name of the site"},
			{Name: "slug", In: "body", Desc: "Address; generated from the title if empty"},
			{Name: "description", In: "body"},
			{Name: "icon", In: "body", Desc: "An emoji"},
			{Name: "template", In: "body", Desc: "Start from this template site (slug)"},
		},
		Fn: func(r *http.Request) (any, error) {
			var in struct{ Title, Slug, Description, Icon, Template string }
			if err := core.Decode(r, &in); err != nil {
				return nil, err
			}
			s, err := CreateSite(r.Context(), core.MustUser(r), in.Title, in.Slug, in.Description, in.Icon)
			if err != nil {
				return nil, err
			}
			if in.Template != "" {
				if err := copyTemplate(r.Context(), core.MustUser(r), in.Template, s); err != nil {
					return nil, err
				}
				s, _ = GetByID(r.Context(), s.ID, false)
			}
			s.Role = "owner"
			return core.Created{V: s}, nil
		}})

	a.Handle(core.Op{Name: "sites.get", Method: "GET", Path: "/api/v1/sites/{site}", Summary: "Get a site", Params: []core.Param{siteParam},
		Fn: func(r *http.Request) (any, error) {
			s, role, err := Full(r.Context(), core.MustUser(r), r.PathValue("site"), core.RoleViewer)
			if err != nil {
				return nil, err
			}
			out := *s
			out.Role = role.String()
			out.Owner = person(r.Context(), s.OwnerID)
			out.Extra = map[string]any{}
			if role >= core.RoleEditor {
				out.Extra["members"] = members(r.Context(), s.ID)
				var hasDraft int
				a.DB.QueryRowContext(r.Context(), `SELECT COUNT(*) FROM drafts WHERE site_id=?`, s.ID).Scan(&hasDraft)
				out.Extra["hasDraft"] = hasDraft == 1
			}
			markVisit(r.Context(), core.MustUser(r).ID, s.ID)
			return out, nil
		}})

	a.Handle(core.Op{Name: "sites.update", Method: "PATCH", Path: "/api/v1/sites/{site}", Summary: "Rename, change address, description, icon or settings",
		Params: []core.Param{siteParam,
			{Name: "title", In: "body"}, {Name: "slug", In: "body", Desc: "New address"}, {Name: "description", In: "body"}, {Name: "icon", In: "body"},
			{Name: "orgView", In: "body", Type: "boolean", Desc: "Everyone in the organization can view"},
			{Name: "isTemplate", In: "body", Type: "boolean", Desc: "Offer as a template"},
			{Name: "settings", In: "body", Type: "object", Desc: "{spa, notFound, entry, hideBadge}"},
		},
		Fn: func(r *http.Request) (any, error) {
			u := core.MustUser(r)
			s, role, err := Full(r.Context(), u, r.PathValue("site"), core.RoleEditor)
			if err != nil {
				return nil, err
			}
			var in struct {
				Title, Slug, Description, Icon *string
				OrgView, IsTemplate            *bool
				Settings                       *Settings
			}
			if err := core.Decode(r, &in); err != nil {
				return nil, err
			}
			ctx := r.Context()
			// Owner-only changes are checked before anything is written.
			if role < core.RoleOwner && (in.OrgView != nil || in.IsTemplate != nil || (in.Slug != nil && *in.Slug != s.Slug)) {
				return nil, core.Forbidden("only the owner can change who can view, the address, or offer the site as a template")
			}
			if in.Title != nil {
				t := strings.TrimSpace(*in.Title)
				if t == "" || len(t) > 120 {
					return nil, core.BadRequest("title must be 1–120 characters")
				}
				a.DB.ExecContext(ctx, `UPDATE sites SET title=? WHERE id=?`, t, s.ID)
			}
			if in.Description != nil {
				a.DB.ExecContext(ctx, `UPDATE sites SET description=? WHERE id=?`, trunc(*in.Description, 500), s.ID)
			}
			if in.Icon != nil {
				a.DB.ExecContext(ctx, `UPDATE sites SET icon=? WHERE id=?`, trunc(*in.Icon, 16), s.ID)
			}
			if in.Settings != nil {
				b, _ := json.Marshal(in.Settings)
				a.DB.ExecContext(ctx, `UPDATE sites SET settings=? WHERE id=?`, string(b), s.ID)
			}
			if in.OrgView != nil {
				if role < core.RoleOwner {
					return nil, core.Forbidden("only the owner can change who can view")
				}
				if *in.OrgView && a.Setting("sharing.org_wide") == "false" {
					return nil, core.Forbidden("your admin has turned off organization-wide sharing")
				}
				a.DB.ExecContext(ctx, `UPDATE sites SET org_view=? WHERE id=?`, b2i(*in.OrgView), s.ID)
				a.Audit(ctx, "site.share", s.ID, "org", map[string]any{"orgView": *in.OrgView})
			}
			if in.IsTemplate != nil {
				a.DB.ExecContext(ctx, `UPDATE sites SET is_template=? WHERE id=?`, b2i(*in.IsTemplate), s.ID)
			}
			newSlug := s.Slug
			if in.Slug != nil && *in.Slug != s.Slug {
				if role < core.RoleOwner {
					return nil, core.Forbidden("only the owner can change the address")
				}
				if err := ValidSlug(*in.Slug); err != nil {
					return nil, err
				}
				if _, err := a.DB.ExecContext(ctx, `UPDATE sites SET slug=? WHERE id=?`, *in.Slug, s.ID); err != nil {
					return nil, core.Conflict("that address is taken")
				}
				newSlug = *in.Slug
				a.Audit(ctx, "site.rename", s.ID, newSlug, map[string]any{"from": s.Slug})
			}
			a.DB.ExecContext(ctx, `UPDATE sites SET updated_at=? WHERE id=?`, core.Now(), s.ID)
			Invalidate(s.Slug)
			Invalidate(newSlug)
			return GetBySlug(ctx, newSlug)
		}})

	a.Handle(core.Op{Name: "sites.delete", Method: "DELETE", Path: "/api/v1/sites/{site}", Summary: "Move a site to the trash (restorable for 30 days)", Destructive: true,
		Params: []core.Param{siteParam},
		Fn: func(r *http.Request) (any, error) {
			s, _, err := Full(r.Context(), core.MustUser(r), r.PathValue("site"), core.RoleOwner)
			if err != nil {
				return nil, err
			}
			// Free the slug for reuse while in the trash.
			trashed := "trash-" + itoa64(s.ID) + "-" + s.Slug
			if len(trashed) > 120 {
				trashed = trashed[:120]
			}
			if _, err := a.DB.ExecContext(r.Context(), `UPDATE sites SET deleted_at=?, slug=? WHERE id=?`, core.Now(), trashed, s.ID); err != nil {
				return nil, err
			}
			Invalidate(s.Slug)
			a.Audit(r.Context(), "site.delete", s.ID, s.Slug, nil)
			a.Emit(r.Context(), "site.deleted", s.ID)
			return map[string]any{"ok": true, "restoreWithin": core.EnvInt("TRASH_DAYS", 30)}, nil
		}})

	a.Handle(core.Op{Name: "sites.restore", Method: "POST", Path: "/api/v1/trash/{id}/restore", Summary: "Restore a site from the trash",
		Params: []core.Param{{Name: "id", In: "path", Type: "integer", Required: true, Desc: "Site ID from the trash list"}},
		Fn: func(r *http.Request) (any, error) {
			id, _ := strconv.ParseInt(r.PathValue("id"), 10, 64)
			u := core.MustUser(r)
			s, err := GetByID(r.Context(), id, true)
			if err != nil || s.DeletedAt == nil {
				return nil, core.NotFound("not in the trash")
			}
			if s.OwnerID != u.ID && !u.IsAdmin {
				return nil, core.NotFound("not in the trash")
			}
			orig := strings.TrimPrefix(s.Slug, "trash-"+itoa64(s.ID)+"-")
			slug := uniqueSlug(r.Context(), orig)
			a.DB.ExecContext(r.Context(), `UPDATE sites SET deleted_at=NULL, slug=?, updated_at=? WHERE id=?`, slug, core.Now(), id)
			a.Audit(r.Context(), "site.restore_trash", id, slug, nil)
			return GetBySlug(r.Context(), slug)
		}})

	a.Handle(core.Op{Name: "sites.transfer", Method: "POST", Path: "/api/v1/sites/{site}/transfer", Summary: "Give the site to someone else",
		Params: []core.Param{siteParam, {Name: "email", In: "body", Required: true, Desc: "New owner's email (they must have signed in once)"}},
		Fn: func(r *http.Request) (any, error) {
			u := core.MustUser(r)
			s, _, err := Full(r.Context(), u, r.PathValue("site"), core.RoleOwner)
			if err != nil {
				return nil, err
			}
			var in struct{ Email string }
			if err := core.Decode(r, &in); err != nil {
				return nil, err
			}
			var nid int64
			var name string
			if err := a.DB.QueryRowContext(r.Context(), `SELECT id,name FROM users WHERE lower(email)=lower(?) AND disabled=0`, strings.TrimSpace(in.Email)).Scan(&nid, &name); err != nil {
				return nil, core.NotFound("that person hasn't signed in to Bunker yet")
			}
			ctx := r.Context()
			a.DB.ExecContext(ctx, `UPDATE sites SET owner_id=?, updated_at=? WHERE id=?`, nid, core.Now(), s.ID)
			// The previous owner keeps edit access.
			if s.OwnerID != nid {
				if prev := person(ctx, s.OwnerID); prev != nil {
					a.DB.ExecContext(ctx, `INSERT INTO site_members(site_id,ptype,pid,pname,role,added_by,added_at) VALUES(?,?,?,?,?,?,?) ON CONFLICT DO UPDATE SET role=excluded.role`,
						s.ID, "user", strings.ToLower(prev.Email), prev.Name, int(core.RoleEditor), u.ID, core.Now())
				}
			}
			Invalidate(s.Slug)
			a.Audit(ctx, "site.transfer", s.ID, in.Email, nil)
			return map[string]any{"ok": true, "owner": name}, nil
		}})

	a.Handle(core.Op{Name: "sites.star", Method: "POST", Path: "/api/v1/sites/{site}/star", Summary: "Star or unstar a site",
		Params: []core.Param{siteParam, {Name: "starred", In: "body", Type: "boolean", Required: true}},
		Fn: func(r *http.Request) (any, error) {
			u := core.MustUser(r)
			s, _, err := Full(r.Context(), u, r.PathValue("site"), core.RoleViewer)
			if err != nil {
				return nil, err
			}
			var in struct{ Starred bool }
			if err := core.Decode(r, &in); err != nil {
				return nil, err
			}
			a.DB.ExecContext(r.Context(), `INSERT INTO site_stars(user_id,site_id,starred) VALUES(?,?,?) ON CONFLICT DO UPDATE SET starred=excluded.starred`, u.ID, s.ID, b2i(in.Starred))
			return map[string]any{"ok": true, "starred": in.Starred}, nil
		}})

	a.Handle(core.Op{Name: "sites.transfer_all", Method: "POST", Path: "/api/v1/admin/transfer-sites", Summary: "Move every site from one person to another (e.g. when someone leaves)", Access: core.AccessAdmin,
		Params: []core.Param{{Name: "from", In: "body", Required: true, Desc: "Current owner's email"}, {Name: "to", In: "body", Required: true, Desc: "New owner's email"}},
		Fn: func(r *http.Request) (any, error) {
			var in struct{ From, To string }
			if err := core.Decode(r, &in); err != nil {
				return nil, err
			}
			var fromID, toID int64
			if a.DB.QueryRowContext(r.Context(), `SELECT id FROM users WHERE lower(email)=lower(?)`, in.From).Scan(&fromID) != nil {
				return nil, core.NotFound("no user " + in.From)
			}
			if a.DB.QueryRowContext(r.Context(), `SELECT id FROM users WHERE lower(email)=lower(?) AND disabled=0`, in.To).Scan(&toID) != nil {
				return nil, core.NotFound(in.To + " hasn't signed in to Bunker yet")
			}
			res, err := a.DB.ExecContext(r.Context(), `UPDATE sites SET owner_id=?, updated_at=? WHERE owner_id=?`, toID, core.Now(), fromID)
			if err != nil {
				return nil, err
			}
			n, _ := res.RowsAffected()
			invalidateAll()
			a.Audit(r.Context(), "admin.transfer_sites", 0, in.From+" -> "+in.To, map[string]any{"count": n})
			return map[string]any{"ok": true, "moved": n}, nil
		}})

	// ---- sharing ----
	a.Handle(core.Op{Name: "sharing.list", Method: "GET", Path: "/api/v1/sites/{site}/members", Summary: "Who has access to a site", Params: []core.Param{siteParam},
		Fn: func(r *http.Request) (any, error) {
			s, _, err := Full(r.Context(), core.MustUser(r), r.PathValue("site"), core.RoleEditor)
			if err != nil {
				return nil, err
			}
			return map[string]any{"owner": person(r.Context(), s.OwnerID), "orgView": s.OrgView, "members": members(r.Context(), s.ID)}, nil
		}})

	a.Handle(core.Op{Name: "sharing.add", Method: "POST", Path: "/api/v1/sites/{site}/members", Summary: "Share a site with a person or group",
		Params: []core.Param{siteParam,
			{Name: "type", In: "body", Required: true, Enum: []string{"user", "group"}},
			{Name: "id", In: "body", Required: true, Desc: "Email for people, group ID or name for groups"},
			{Name: "name", In: "body", Desc: "Display name"},
			{Name: "role", In: "body", Required: true, Enum: []string{"viewer", "editor"}},
		},
		Fn: func(r *http.Request) (any, error) {
			u := core.MustUser(r)
			s, role, err := Full(r.Context(), u, r.PathValue("site"), core.RoleEditor)
			if err != nil {
				return nil, err
			}
			var in struct{ Type, ID, Name, Role string }
			if err := core.Decode(r, &in); err != nil {
				return nil, err
			}
			nr := core.ParseRole(in.Role)
			if nr != core.RoleViewer && nr != core.RoleEditor {
				return nil, core.BadRequest("role must be viewer or editor")
			}
			if role < core.RoleOwner && a.Setting("sharing.editors_can_share") == "false" {
				return nil, core.Forbidden("only the owner can share this site")
			}
			id := strings.TrimSpace(in.ID)
			switch in.Type {
			case "user":
				id = strings.ToLower(id)
				if !strings.Contains(id, "@") {
					return nil, core.BadRequest("use the person's email address")
				}
			case "group":
				if id == "" {
					return nil, core.BadRequest("group is required")
				}
			default:
				return nil, core.BadRequest("type must be user or group")
			}
			name := strings.TrimSpace(in.Name)
			if name == "" {
				name = id
				if in.Type == "user" {
					a.DB.QueryRowContext(r.Context(), `SELECT name FROM users WHERE lower(email)=?`, id).Scan(&name)
				}
			}
			_, err = a.DB.ExecContext(r.Context(), `INSERT INTO site_members(site_id,ptype,pid,pname,role,added_by,added_at) VALUES(?,?,?,?,?,?,?)
ON CONFLICT(site_id,ptype,pid) DO UPDATE SET role=excluded.role, pname=excluded.pname`, s.ID, in.Type, id, trunc(name, 200), int(nr), u.ID, core.Now())
			if err != nil {
				return nil, err
			}
			Invalidate(s.Slug)
			a.Audit(r.Context(), "site.share", s.ID, in.Type+":"+id, map[string]any{"role": nr.String()})
			a.Emit(r.Context(), "site.shared", map[string]any{"siteId": s.ID, "type": in.Type, "id": id, "role": nr.String(), "by": u.Name})
			return map[string]any{"members": members(r.Context(), s.ID)}, nil
		}})

	a.Handle(core.Op{Name: "sharing.remove", Method: "DELETE", Path: "/api/v1/sites/{site}/members/{type}/{id}", Summary: "Stop sharing with a person or group", Destructive: true,
		Params: []core.Param{siteParam, {Name: "type", In: "path", Required: true, Enum: []string{"user", "group"}}, {Name: "id", In: "path", Required: true}},
		Fn: func(r *http.Request) (any, error) {
			u := core.MustUser(r)
			s, _, err := Full(r.Context(), u, r.PathValue("site"), core.RoleEditor)
			if err != nil {
				return nil, err
			}
			pid := r.PathValue("id")
			if r.PathValue("type") == "user" {
				pid = strings.ToLower(pid)
			}
			a.DB.ExecContext(r.Context(), `DELETE FROM site_members WHERE site_id=? AND ptype=? AND pid=?`, s.ID, r.PathValue("type"), pid)
			Invalidate(s.Slug)
			a.Audit(r.Context(), "site.unshare", s.ID, r.PathValue("type")+":"+pid, nil)
			return map[string]any{"members": members(r.Context(), s.ID)}, nil
		}})
}

func listSites(r *http.Request) (any, error) {
	u := core.MustUser(r)
	ctx := r.Context()
	filter := r.URL.Query().Get("filter")
	q := strings.ToLower(strings.TrimSpace(r.URL.Query().Get("q")))
	var rows *sql.Rows
	var err error
	cols := `s.id,s.slug,s.title,s.description,s.icon,s.owner_id,s.kind,s.org_view,s.live_version,s.is_template,s.settings,s.created_at,s.updated_at,s.deleted_at`
	switch filter {
	case "trash":
		rows, err = app.DB.QueryContext(ctx, `SELECT `+cols+` FROM sites s WHERE s.deleted_at IS NOT NULL AND (s.owner_id=? OR ?=1) ORDER BY s.deleted_at DESC`, u.ID, b2i(u.IsAdmin))
	case "templates":
		rows, err = app.DB.QueryContext(ctx, `SELECT `+cols+` FROM sites s WHERE s.deleted_at IS NULL AND s.is_template=1 ORDER BY s.title`)
	default:
		if u.IsAdmin && filter == "everything" {
			rows, err = app.DB.QueryContext(ctx, `SELECT `+cols+` FROM sites s WHERE s.deleted_at IS NULL ORDER BY s.updated_at DESC LIMIT 2000`)
		} else {
			// Candidates: owned, org-visible, or directly shared. Group shares are
			// filtered in Go against the user's groups.
			groupIDs := []any{u.ID, strings.ToLower(u.Email)}
			ph := ""
			for _, g := range u.Groups {
				ph += ",?,?"
				groupIDs = append(groupIDs, g.ID, g.Name)
			}
			groupClause := "0"
			if ph != "" {
				groupClause = `m.ptype='group' AND m.pid IN (` + ph[1:] + `)`
			}
			rows, err = app.DB.QueryContext(ctx, `SELECT DISTINCT `+cols+` FROM sites s LEFT JOIN site_members m ON m.site_id=s.id
WHERE s.deleted_at IS NULL AND (s.owner_id=? OR s.org_view=1 OR (m.ptype='user' AND m.pid=?) OR (`+groupClause+`)) ORDER BY s.updated_at DESC LIMIT 2000`, groupIDs...)
		}
	}
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var list []*SiteRecord
	for rows.Next() {
		s, err := scanSite(rows)
		if err != nil {
			return nil, err
		}
		list = append(list, s)
	}
	rows.Close()
	stars := map[int64][2]int64{}
	srows, err := app.DB.QueryContext(ctx, `SELECT site_id,starred,visited_at FROM site_stars WHERE user_id=?`, u.ID)
	if err == nil {
		for srows.Next() {
			var id, st, vis int64
			srows.Scan(&id, &st, &vis)
			stars[id] = [2]int64{st, vis}
		}
		srows.Close()
	}
	owners := map[int64]*Person{}
	out := []*SiteRecord{}
	var systemID int64 = -1
	app.DB.QueryRowContext(ctx, `SELECT id FROM users WHERE subject LIKE 'system:%' LIMIT 1`).Scan(&systemID)
	for _, s := range list {
		// Built-in templates live under Templates, not in everyone's site list.
		if s.Template && s.OwnerID == systemID && filter != "templates" && filter != "everything" {
			continue
		}
		role := RoleOf(ctx, u, s)
		if filter == "trash" {
			role = core.RoleOwner
		}
		if role == core.RoleNone {
			continue
		}
		s.Role = role.String()
		s.Starred = stars[s.ID][0] == 1
		if q != "" && !strings.Contains(strings.ToLower(s.Title+" "+s.Slug+" "+s.Description), q) {
			continue
		}
		switch filter {
		case "mine":
			if s.OwnerID != u.ID {
				continue
			}
		case "shared":
			if s.OwnerID == u.ID || role == core.RoleAdmin && !s.OrgView {
				if s.OwnerID == u.ID {
					continue
				}
			}
		case "org":
			if !s.OrgView {
				continue
			}
		case "starred":
			if !s.Starred {
				continue
			}
		case "recent":
			if stars[s.ID][1] == 0 {
				continue
			}
			s.Extra = map[string]any{"visitedAt": stars[s.ID][1]}
		}
		if owners[s.OwnerID] == nil {
			owners[s.OwnerID] = person(ctx, s.OwnerID)
		}
		s.Owner = owners[s.OwnerID]
		out = append(out, s)
	}
	return map[string]any{"sites": out}, nil
}

var (
	visitMu   sync.Mutex
	visitSeen = map[[2]int64]int64{}
)

// markVisit records "recently opened" at most every 5 minutes per viewer
// and site, so page views don't turn into a write per request.
func markVisit(ctx context.Context, uid, sid int64) {
	now := core.Now()
	k := [2]int64{uid, sid}
	visitMu.Lock()
	if now-visitSeen[k] < 300 {
		visitMu.Unlock()
		return
	}
	if len(visitSeen) > 200000 {
		visitSeen = map[[2]int64]int64{}
	}
	visitSeen[k] = now
	visitMu.Unlock()
	app.DB.ExecContext(ctx, `INSERT INTO site_stars(user_id,site_id,visited_at) VALUES(?,?,?) ON CONFLICT DO UPDATE SET visited_at=excluded.visited_at`, uid, sid, core.Now())
}

// CreateSite creates an empty site owned by u.
func CreateSite(ctx context.Context, u *core.User, title, slug, desc, icon string) (*SiteRecord, error) {
	title = strings.TrimSpace(title)
	if title == "" {
		title = "Untitled site"
	}
	if len(title) > 120 {
		return nil, core.BadRequest("title is too long")
	}
	if slug != "" {
		slug = strings.ToLower(strings.TrimSpace(slug))
		if err := ValidSlug(slug); err != nil {
			return nil, err
		}
		var n int
		app.DB.QueryRowContext(ctx, `SELECT COUNT(*) FROM sites WHERE slug=?`, slug).Scan(&n)
		if n > 0 {
			return nil, core.Conflict("the address " + slug + " is taken")
		}
	} else {
		slug = uniqueSlug(ctx, Slugify(title))
	}
	if max := SettingInt("max_sites_per_user", 500); max > 0 {
		var n int
		app.DB.QueryRowContext(ctx, `SELECT COUNT(*) FROM sites WHERE owner_id=? AND deleted_at IS NULL`, u.ID).Scan(&n)
		if n >= max {
			return nil, core.Quota("you've reached the limit of sites per person")
		}
	}
	now := core.Now()
	res, err := app.DB.ExecContext(ctx, `INSERT INTO sites(slug,title,description,icon,owner_id,created_at,updated_at) VALUES(?,?,?,?,?,?,?)`, slug, title, trunc(desc, 500), trunc(icon, 16), u.ID, now, now)
	if err != nil {
		return nil, core.Conflict("could not create the site: " + err.Error())
	}
	id, _ := res.LastInsertId()
	app.Audit(ctx, "site.create", id, slug, nil)
	return GetByID(ctx, id, false)
}

func copyTemplate(ctx context.Context, u *core.User, tplSlug string, dst *SiteRecord) error {
	tpl, err := GetBySlug(ctx, tplSlug)
	if err != nil {
		return err
	}
	// Built-in templates are org-visible; user templates need view access.
	if RoleOf(ctx, u, tpl) < core.RoleViewer {
		return core.NotFound("template not found")
	}
	if tpl.Version == 0 {
		return core.BadRequest("that template has no published version")
	}
	m, err := LoadManifest(ctx, tpl.ID, tpl.Version)
	if err != nil {
		return err
	}
	_, err = Commit(ctx, u, dst, m, "Started from template "+tpl.Title, "template", true)
	if err == nil {
		app.Emit(ctx, "site.from_template", map[string]any{"from": tpl.ID, "to": dst.ID})
	}
	return err
}

func b2i(b bool) int {
	if b {
		return 1
	}
	return 0
}

func trunc(s string, n int) string {
	s = strings.TrimSpace(s)
	if len(s) > n {
		return s[:n]
	}
	return s
}
