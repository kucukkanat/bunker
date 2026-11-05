// Package sites owns sites, sharing, versions, drafts, deploys and the
// sandboxed serving of site content.
package sites

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/kucukkanat/bunker/internal/core"
)

func init() {
	core.Register(core.Module{
		Name:  "sites",
		Order: 20,
		Migrations: []string{`
CREATE TABLE sites (
  id INTEGER PRIMARY KEY,
  slug TEXT UNIQUE NOT NULL,
  title TEXT NOT NULL,
  description TEXT NOT NULL DEFAULT '',
  icon TEXT NOT NULL DEFAULT '',
  owner_id INTEGER NOT NULL REFERENCES users(id),
  kind TEXT NOT NULL DEFAULT 'html',
  org_view INTEGER NOT NULL DEFAULT 0,
  live_version INTEGER NOT NULL DEFAULT 0,
  is_template INTEGER NOT NULL DEFAULT 0,
  settings TEXT NOT NULL DEFAULT '{}',
  created_at INTEGER NOT NULL,
  updated_at INTEGER NOT NULL,
  deleted_at INTEGER
);
CREATE INDEX sites_owner ON sites(owner_id);
CREATE TABLE site_members (
  site_id INTEGER NOT NULL REFERENCES sites(id) ON DELETE CASCADE,
  ptype TEXT NOT NULL,
  pid TEXT NOT NULL,
  pname TEXT NOT NULL,
  role INTEGER NOT NULL,
  added_by INTEGER,
  added_at INTEGER NOT NULL,
  PRIMARY KEY(site_id, ptype, pid)
);
CREATE INDEX site_members_pid ON site_members(ptype, pid);
CREATE TABLE versions (
  site_id INTEGER NOT NULL REFERENCES sites(id) ON DELETE CASCADE,
  n INTEGER NOT NULL,
  created_at INTEGER NOT NULL,
  author_id INTEGER,
  message TEXT NOT NULL DEFAULT '',
  source TEXT NOT NULL DEFAULT 'upload',
  manifest TEXT NOT NULL,
  total_size INTEGER NOT NULL,
  file_count INTEGER NOT NULL,
  PRIMARY KEY(site_id, n)
);
CREATE TABLE drafts (
  site_id INTEGER PRIMARY KEY REFERENCES sites(id) ON DELETE CASCADE,
  manifest TEXT NOT NULL,
  updated_at INTEGER NOT NULL,
  updated_by INTEGER
);
CREATE TABLE site_stars (
  user_id INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  site_id INTEGER NOT NULL REFERENCES sites(id) ON DELETE CASCADE,
  visited_at INTEGER NOT NULL DEFAULT 0,
  starred INTEGER NOT NULL DEFAULT 0,
  PRIMARY KEY(user_id, site_id)
);
`,
			// 2: idle archival (BUNKER_ARCHIVE_AFTER_DAYS).
			`ALTER TABLE sites ADD COLUMN archived_at INTEGER;
ALTER TABLE sites ADD COLUMN archived_days INTEGER NOT NULL DEFAULT 0;
ALTER TABLE sites ADD COLUMN archive_warned_at INTEGER;`,
			`ALTER TABLE sites ADD COLUMN public INTEGER NOT NULL DEFAULT 0;`,
		},
		Init: initSites,
	})
}

var app *core.App

// SiteRecord is the full site row.
type SiteRecord struct {
	core.Site
	Description string   `json:"description"`
	Icon        string   `json:"icon"`
	Owner       *Person  `json:"owner,omitempty"`
	Template    bool     `json:"isTemplate"`
	Settings    Settings `json:"settings"`
	CreatedAt   int64    `json:"createdAt"`
	UpdatedAt   int64    `json:"updatedAt"`
	DeletedAt   *int64   `json:"deletedAt,omitempty"`
	// ArchivedAt is set when the site was archived (idle or by hand).
	ArchivedAt *int64 `json:"archivedAt,omitempty"`
	// ArchivedDays is the idle period that triggered archival (0 = by hand).
	ArchivedDays int64  `json:"archivedDays,omitempty"`
	Role         string `json:"role,omitempty"`
	// Public: anyone with the link can view without signing in (when the
	// admin allows public links).
	Public  bool           `json:"public"`
	URL     string         `json:"url"`
	Starred bool           `json:"starred,omitempty"`
	Extra   map[string]any `json:"extra,omitempty"`
}

// Settings are per-site options.
type Settings struct {
	// SPA serves index.html for unknown paths (client-side routing).
	SPA bool `json:"spa"`
	// NotFound is a custom 404 page path.
	NotFound string `json:"notFound,omitempty"`
	// Entry overrides the start page (default index.html).
	Entry string `json:"entry,omitempty"`
	// Chrome shows the floating Bunker badge (default true).
	HideBadge bool `json:"hideBadge,omitempty"`
}

// Person is a lightweight user reference.
type Person struct {
	ID    int64  `json:"id"`
	Name  string `json:"name"`
	Email string `json:"email"`
}

func initSites(a *core.App) error {
	app = a
	invalidateAll()
	mcMu.Lock()
	mCache = map[string]Manifest{}
	mcMu.Unlock()
	rawMu.Lock()
	rawCache = map[string]rawEntry{}
	rawMu.Unlock()
	a.Sites = resolver{}
	a.On("sites.changed", func(context.Context, any) { invalidateAll() })
	registerOps(a)
	if extraOps != nil {
		extraOps(a)
	}
	registerDeploy(a)
	registerImport(a)
	registerServing(a)
	go purgeLoop(a)
	registerLifecycle(a)
	return nil
}

var slugRe = regexp.MustCompile(`^[a-z0-9](?:[a-z0-9-]{1,46}[a-z0-9])?$`)

var reservedSlugs = map[string]bool{"new": true, "admin": true, "api": true, "auth": true, "setup": true, "app": true, "lib": true, "static": true, "settings": true, "device": true, "mcp": true, "oauth": true, "well-known": true, "_": true}

// Slugify turns a title into a URL slug.
func Slugify(s string) string {
	s = strings.ToLower(s)
	var b strings.Builder
	dash := false
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			b.WriteRune(r)
			dash = false
		case r == 'ä' || r == 'á' || r == 'à' || r == 'â' || r == 'å':
			b.WriteByte('a')
			dash = false
		case r == 'ö' || r == 'ó' || r == 'ò' || r == 'ô' || r == 'ø':
			b.WriteByte('o')
			dash = false
		case r == 'ü' || r == 'ú' || r == 'ù' || r == 'û':
			b.WriteByte('u')
			dash = false
		case r == 'é' || r == 'è' || r == 'ê' || r == 'ë':
			b.WriteByte('e')
			dash = false
		case r == 'ı' || r == 'í' || r == 'î' || r == 'ï':
			b.WriteByte('i')
			dash = false
		case r == 'ç':
			b.WriteByte('c')
			dash = false
		case r == 'ş' || r == 'ß':
			b.WriteByte('s')
			dash = false
		case r == 'ğ':
			b.WriteByte('g')
			dash = false
		case r == 'ñ':
			b.WriteByte('n')
			dash = false
		default:
			if !dash && b.Len() > 0 {
				b.WriteByte('-')
				dash = true
			}
		}
	}
	out := strings.Trim(b.String(), "-")
	if len(out) > 48 {
		out = strings.Trim(out[:48], "-")
	}
	if len(out) < 3 {
		out = "site-" + strings.ToLower(core.RandomToken(3))
		out = strings.Map(func(r rune) rune {
			if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '-' {
				return r
			}
			return 'x'
		}, out)
	}
	return out
}

// ValidSlug checks a user-chosen slug.
func ValidSlug(s string) error {
	if !slugRe.MatchString(s) {
		return core.BadRequest("addresses use 3–48 lowercase letters, numbers and dashes")
	}
	if reservedSlugs[s] || strings.HasPrefix(s, "trash-") { // trashed sites are renamed trash-<id>-<slug>
		return core.BadRequest("that address is reserved")
	}
	return nil
}

func uniqueSlug(ctx context.Context, base string) string {
	if strings.HasPrefix(base, "trash-") {
		base = "site-" + base
		if len(base) > 48 {
			base = strings.Trim(base[:48], "-")
		}
	}
	s := base
	for i := 2; i < 1000; i++ {
		var n int
		app.DB.QueryRowContext(ctx, `SELECT COUNT(*) FROM sites WHERE slug=?`, s).Scan(&n)
		if n == 0 && !reservedSlugs[s] {
			return s
		}
		suffix := "-" + itoa(i)
		b := base
		if len(b)+len(suffix) > 48 {
			b = b[:48-len(suffix)]
		}
		s = b + suffix
	}
	return base + "-" + strings.ToLower(core.RandomToken(4))
}

func itoa(i int) string { return strconv.Itoa(i) }

// ---- site lookups & role resolution ----

type siteCacheEntry struct {
	s   *SiteRecord
	exp time.Time
}

var (
	scMu      sync.Mutex
	siteCache = map[string]siteCacheEntry{}
	roleCache = map[string]roleEntry{}
)

type roleEntry struct {
	r   core.Role
	exp time.Time
}

// Invalidate clears caches for a site (after updates, shares, deploys).
func Invalidate(slug string) {
	scMu.Lock()
	defer scMu.Unlock()
	delete(siteCache, slug)
	roleCache = map[string]roleEntry{}
}

func invalidateAll() {
	scMu.Lock()
	defer scMu.Unlock()
	siteCache = map[string]siteCacheEntry{}
	roleCache = map[string]roleEntry{}
}

const siteCols = `id,slug,title,description,icon,owner_id,kind,org_view,live_version,is_template,settings,created_at,updated_at,deleted_at,archived_at,archived_days,public`

func scanSite(row interface{ Scan(...any) error }) (*SiteRecord, error) {
	s := &SiteRecord{}
	var org, tpl, pub int
	var settings string
	var del, arch sql.NullInt64
	if err := row.Scan(&s.ID, &s.Slug, &s.Title, &s.Description, &s.Icon, &s.OwnerID, &s.Kind, &org, &s.Version, &tpl, &settings, &s.CreatedAt, &s.UpdatedAt, &del, &arch, &s.ArchivedDays, &pub); err != nil {
		return nil, err
	}
	s.Public = pub == 1 && PublicLinksAllowed()
	s.OrgView, s.Template = org == 1, tpl == 1
	json.Unmarshal([]byte(settings), &s.Settings)
	if del.Valid {
		s.DeletedAt = &del.Int64
	}
	if arch.Valid {
		s.ArchivedAt = &arch.Int64
	}
	s.URL = app.Cfg.URL + "/s/" + s.Slug + "/"
	return s, nil
}

// GetBySlug loads a non-deleted site (cached briefly).
func GetBySlug(ctx context.Context, slug string) (*SiteRecord, error) {
	scMu.Lock()
	if e, ok := siteCache[slug]; ok && time.Now().Before(e.exp) {
		scMu.Unlock()
		return e.s, nil
	}
	scMu.Unlock()
	s, err := scanSite(app.DB.QueryRowContext(ctx, `SELECT `+siteCols+` FROM sites WHERE slug=? AND deleted_at IS NULL`, slug))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, core.NotFound("site not found")
	}
	if err != nil {
		return nil, err
	}
	scMu.Lock()
	if len(siteCache) > 20000 {
		siteCache = map[string]siteCacheEntry{}
	}
	siteCache[slug] = siteCacheEntry{s: s, exp: time.Now().Add(20 * time.Second)}
	scMu.Unlock()
	return s, nil
}

// GetByID loads a site by id (including deleted when withDeleted).
func GetByID(ctx context.Context, id int64, withDeleted bool) (*SiteRecord, error) {
	q := `SELECT ` + siteCols + ` FROM sites WHERE id=?`
	if !withDeleted {
		q += ` AND deleted_at IS NULL`
	}
	s, err := scanSite(app.DB.QueryRowContext(ctx, q, id))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, core.NotFound("site not found")
	}
	return s, err
}

// RoleOf computes u's role on s.
func RoleOf(ctx context.Context, u *core.User, s *SiteRecord) core.Role {
	if u == nil || u.ID == 0 {
		if s.Public && s.ArchivedAt == nil {
			return core.RoleViewer
		}
		return core.RoleNone
	}
	if u.IsAdmin {
		return core.RoleAdmin
	}
	if s.OwnerID == u.ID {
		return core.RoleOwner
	}
	key := itoa64(s.ID) + ":" + itoa64(u.ID)
	scMu.Lock()
	if e, ok := roleCache[key]; ok && time.Now().Before(e.exp) {
		scMu.Unlock()
		return e.r
	}
	scMu.Unlock()
	role := core.RoleNone
	if s.OrgView {
		role = core.RoleViewer
	}
	rows, err := app.DB.QueryContext(ctx, `SELECT ptype,pid,role FROM site_members WHERE site_id=?`, s.ID)
	if err == nil {
		for rows.Next() {
			var pt, pid string
			var r int
			rows.Scan(&pt, &pid, &r)
			match := (pt == "user" && strings.EqualFold(pid, u.Email)) || (pt == "group" && u.InGroup(pid))
			if match && core.Role(r) > role {
				role = core.Role(r)
			}
		}
		rows.Close()
	}
	scMu.Lock()
	if len(roleCache) > 100000 {
		roleCache = map[string]roleEntry{}
	}
	roleCache[key] = roleEntry{r: role, exp: time.Now().Add(15 * time.Second)}
	scMu.Unlock()
	return role
}

func itoa64(i int64) string { return strconv.FormatInt(i, 10) }

type resolver struct{}

func (resolver) Resolve(ctx context.Context, u *core.User, slug string) (*core.Site, core.Role, error) {
	s, err := GetBySlug(ctx, slug)
	if err != nil {
		return nil, core.RoleNone, err
	}
	return &s.Site, RoleOf(ctx, u, s), nil
}

// Full resolves {site} from the request into the full record and enforces min.
func Full(ctx context.Context, u *core.User, slug string, min core.Role) (*SiteRecord, core.Role, error) {
	if u == nil {
		return nil, core.RoleNone, core.Unauthorized("sign in required")
	}
	s, err := GetBySlug(ctx, slug)
	if err != nil {
		return nil, core.RoleNone, err
	}
	role := RoleOf(ctx, u, s)
	if role == core.RoleNone {
		return nil, role, core.NotFound("site not found")
	}
	if role < min {
		return nil, role, core.Forbidden("you need " + min.String() + " access to this site")
	}
	return s, role, nil
}

func purgeLoop(a *core.App) {
	t := time.NewTicker(6 * time.Hour)
	defer t.Stop()
	for range t.C {
		days := int64(core.EnvInt("TRASH_DAYS", 30))
		rows, err := a.DB.Query(`SELECT id FROM sites WHERE deleted_at IS NOT NULL AND deleted_at < ?`, core.Now()-days*86400)
		if err != nil {
			continue
		}
		var ids []int64
		for rows.Next() {
			var id int64
			rows.Scan(&id)
			ids = append(ids, id)
		}
		rows.Close()
		for _, id := range ids {
			a.Emit(context.Background(), "site.purge", id)
			a.DB.Exec(`DELETE FROM sites WHERE id=?`, id)
		}
		if len(ids) > 0 {
			GCBlobs(context.Background())
		}
	}
}

// PublicLinksAllowed reports whether the admin allows public sites.
func PublicLinksAllowed() bool { return app.Setting("sharing.public_links") == "true" }

// Guest is the anonymous viewer of public sites.
var Guest = &core.User{ID: 0, Name: "Guest", Email: ""}
