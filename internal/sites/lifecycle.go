package sites

// Site lifecycle: moving sites when their owner leaves, and archiving
// sites nobody has used for a while (BUNKER_ARCHIVE_AFTER_DAYS).

import (
	"context"
	"database/sql"
	"fmt"
	"net/http"
	"strconv"
	"time"

	"github.com/kucukkanat/bunker/internal/core"
)

// TransferAll moves every site owned by fromID (trash included) to toID.
func TransferAll(ctx context.Context, fromID, toID int64) (int64, error) {
	if fromID == toID {
		return 0, nil
	}
	var from, to string
	app.DB.QueryRowContext(ctx, `SELECT email FROM users WHERE id=?`, fromID).Scan(&from)
	app.DB.QueryRowContext(ctx, `SELECT email FROM users WHERE id=?`, toID).Scan(&to)
	res, err := app.DB.ExecContext(ctx, `UPDATE sites SET owner_id=?, updated_at=? WHERE owner_id=?`, toID, core.Now(), fromID)
	if err != nil {
		return 0, err
	}
	n, _ := res.RowsAffected()
	invalidateAll()
	app.Audit(ctx, "admin.transfer_sites", 0, from+" -> "+to, map[string]any{"count": n})
	return n, nil
}

// ArchiveAfterDays is the idle period after which sites are archived
// (setting archive.after_days / env BUNKER_ARCHIVE_AFTER_DAYS; 0 = off).
func ArchiveAfterDays() int64 {
	n, err := strconv.ParseInt(app.Setting("archive.after_days"), 10, 64)
	if err != nil || n < 0 {
		return 0
	}
	return n
}

func registerLifecycle(a *core.App) {
	a.On("user.disabled", func(ctx context.Context, p any) {
		ev, ok := p.(*core.UserDisabled)
		if !ok || ev.TransferTo == 0 {
			return
		}
		if n, err := TransferAll(ctx, ev.UserID, ev.TransferTo); err == nil {
			ev.Moved = n
		}
	})

	a.Handle(core.Op{Name: "sites.archive", Method: "POST", Path: "/api/v1/sites/{site}/archive", Summary: "Archive a site: nobody can open it until an editor restores it",
		Params: []core.Param{siteParam},
		Fn: func(r *http.Request) (any, error) {
			s, _, err := Full(r.Context(), core.MustUser(r), r.PathValue("site"), core.RoleEditor)
			if err != nil {
				return nil, err
			}
			if s.ArchivedAt == nil {
				archive(r.Context(), s, 0)
			}
			return GetBySlug(r.Context(), s.Slug)
		}})
	a.Handle(core.Op{Name: "sites.unarchive", Method: "POST", Path: "/api/v1/sites/{site}/unarchive", Summary: "Restore an archived site so people can open it again",
		Params: []core.Param{siteParam},
		Fn: func(r *http.Request) (any, error) {
			s, _, err := Full(r.Context(), core.MustUser(r), r.PathValue("site"), core.RoleEditor)
			if err != nil {
				return nil, err
			}
			unarchive(r.Context(), s)
			return GetBySlug(r.Context(), s.Slug)
		}})

	// "Restore" button on the archived page: a same-site form POST with a
	// MAC bound to user+site (like /access-request) instead of X-Bunker.
	a.Public("/archive-restore")
	a.Mux.HandleFunc("POST /archive-restore", func(w http.ResponseWriter, r *http.Request) {
		u := core.UserFrom(r.Context())
		if u == nil {
			http.Redirect(w, r, "/auth/login", http.StatusSeeOther)
			return
		}
		slug := r.FormValue("site")
		if !core.MACEqual(r.FormValue("t"), restoreToken(u.ID, slug)) {
			http.Error(w, "This form expired. Go back and try again.", http.StatusForbidden)
			return
		}
		s, _, err := Full(r.Context(), u, slug, core.RoleEditor)
		if err != nil {
			http.Error(w, "You can't restore this site.", http.StatusForbidden)
			return
		}
		unarchive(r.Context(), s)
		http.Redirect(w, r, "/s/"+s.Slug+"/", http.StatusSeeOther)
	})

	go archiveLoop(a)
}

func restoreToken(uid int64, slug string) string { return core.MAC(fmt.Sprintf("restore:%d:%s", uid, slug)) }

func archive(ctx context.Context, s *SiteRecord, days int64) {
	app.DB.ExecContext(ctx, `UPDATE sites SET archived_at=?, archived_days=? WHERE id=? AND archived_at IS NULL`, core.Now(), days, s.ID)
	Invalidate(s.Slug)
	app.Audit(ctx, "site.archive", s.ID, s.Slug, map[string]any{"idleDays": days})
	app.Emit(ctx, "site.archived", map[string]any{"siteId": s.ID, "days": days})
}

func unarchive(ctx context.Context, s *SiteRecord) {
	if s.ArchivedAt == nil {
		return
	}
	// updated_at counts as activity, so the site isn't archived again
	// right away.
	app.DB.ExecContext(ctx, `UPDATE sites SET archived_at=NULL, archived_days=0, archive_warned_at=NULL, updated_at=? WHERE id=?`, core.Now(), s.ID)
	Invalidate(s.Slug)
	app.Audit(ctx, "site.unarchive", s.ID, s.Slug, nil)
}

func archiveLoop(a *core.App) {
	time.Sleep(2 * time.Minute)
	t := time.NewTicker(24 * time.Hour)
	defer t.Stop()
	for {
		RunArchive(context.Background())
		<-t.C
	}
}

// RunArchive warns owners of sites idle for (N-7) days and archives sites
// idle for N days whose owner was warned at least 7 days earlier
// (N = ArchiveAfterDays; shorter periods warn N days ahead). Activity is
// the latest of: an edit or deploy, a version, a draft save, or anyone
// opening the site. Templates are never archived. Returns (warned, archived).
func RunArchive(ctx context.Context) (warned, archived int) {
	days := ArchiveAfterDays()
	if days == 0 {
		return 0, 0
	}
	lead := min(days, 7) * 86400
	now := core.Now()
	idleAfter := days * 86400
	rows, err := app.DB.QueryContext(ctx, `WITH vis AS (SELECT site_id, MAX(visited_at) m FROM site_stars GROUP BY site_id),
ver AS (SELECT site_id, MAX(created_at) m FROM versions GROUP BY site_id)
SELECT s.id, s.slug, s.archive_warned_at,
  MAX(s.created_at, s.updated_at, COALESCE(vis.m,0), COALESCE(ver.m,0), COALESCE(d.updated_at,0)) AS active
FROM sites s LEFT JOIN vis ON vis.site_id=s.id LEFT JOIN ver ON ver.site_id=s.id LEFT JOIN drafts d ON d.site_id=s.id
WHERE s.deleted_at IS NULL AND s.archived_at IS NULL AND s.is_template=0`)
	if err != nil {
		app.Log.Warn("archive scan failed", "err", err)
		return 0, 0
	}
	type cand struct {
		id     int64
		slug   string
		warned sql.NullInt64
		active int64
	}
	var cs []cand
	for rows.Next() {
		var c cand
		if rows.Scan(&c.id, &c.slug, &c.warned, &c.active) == nil {
			cs = append(cs, c)
		}
	}
	rows.Close()
	for _, c := range cs {
		idle := now - c.active
		if idle < idleAfter-lead {
			continue
		}
		warnedSinceActive := c.warned.Valid && c.warned.Int64 >= c.active
		if !warnedSinceActive {
			app.DB.ExecContext(ctx, `UPDATE sites SET archive_warned_at=? WHERE id=?`, now, c.id)
			app.Emit(ctx, "site.archive_warning", map[string]any{"siteId": c.id, "days": days, "archiveAt": max(c.active+idleAfter, now+lead)})
			warned++
			continue
		}
		if idle >= idleAfter && now-c.warned.Int64 >= lead {
			if s, err := GetByID(ctx, c.id, false); err == nil {
				archive(ctx, s, days)
				archived++
			}
		}
	}
	return warned, archived
}

func renderArchived(w http.ResponseWriter, u *core.User, s *SiteRecord, role core.Role) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Content-Security-Policy", "default-src 'none'; style-src 'self' 'unsafe-inline'; img-src 'self'; form-action 'self'")
	why := "This site was archived."
	if s.ArchivedDays > 0 {
		why = fmt.Sprintf("This site was archived because nobody used it for %d days.", s.ArchivedDays)
	}
	if role < core.RoleEditor {
		simplePage(w, s.Title, why+" Ask the owner to restore it if you still need it.", "/", "Go to Bunker")
		return
	}
	fmt.Fprintf(w, `<!doctype html><meta charset=utf-8><meta name=viewport content="width=device-width,initial-scale=1"><title>%s</title><link rel=stylesheet href="/_/kit.css">
<main class="bk-center"><form class="bk-card" method="post" action="/archive-restore"><img src="/_/icon.svg" width=40 height=40 alt=""><h1>%s</h1>
<p>%s Its files, data and versions are all still here. Restore it and everyone you shared it with can open it again.</p>
<input type="hidden" name="site" value="%s"><input type="hidden" name="t" value="%s">
<p><button class="bk-btn" type="submit" style="border:0;cursor:pointer;font:inherit;font-weight:600">Restore</button></p>
<p style="font-size:14px;opacity:.75"><a href="/sites/%s">Open the editor</a> · <a href="/">Go to Bunker</a></p></form></main>`,
		htmlEscape(s.Title), htmlEscape(s.Title), htmlEscape(why), htmlEscape(s.Slug), htmlEscape(restoreToken(u.ID, s.Slug)), htmlEscape(s.Slug))
}
