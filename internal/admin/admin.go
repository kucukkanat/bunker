// Package admin is the admin overview and operations: usage stats, the
// audit log (query, export, streaming to a SIEM, retention), sharing and
// size policy, and backups (on-demand download and scheduled snapshots).
package admin

import (
	"context"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/kucukkanat/bunker/internal/core"
)

func init() {
	core.Register(core.Module{Name: "admin", Order: 80, Init: initAdmin})
}

var app *core.App

func initAdmin(a *core.App) error {
	app = a
	ctx, cancel := context.WithCancel(context.Background())
	a.On("shutdown", func(context.Context, any) {
		cancel()
		flushSinks()
	})
	registerStats(a)
	registerPolicy(a)
	registerVersion(a)
	registerAudit(a)
	registerBackup(a)
	startSinks(a)
	go retentionLoop(ctx, a)
	go scheduleLoop(ctx, a)
	go autoLoop(ctx)
	return nil
}

// ---- stats ----

var (
	storageMu  sync.Mutex
	storageAt  time.Time
	storageVal int64
	storageN   int64
)

// blobUsage walks the blob store (cached 5 minutes: it can be large).
func blobUsage() (int64, int64) {
	storageMu.Lock()
	defer storageMu.Unlock()
	if time.Since(storageAt) < 5*time.Minute && !storageAt.IsZero() {
		return storageVal, storageN
	}
	var total, n int64
	filepath.WalkDir(app.Blobs.Dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() {
			if d.Name() == "tmp" {
				return filepath.SkipDir
			}
			return nil
		}
		if info, err := d.Info(); err == nil {
			total += info.Size()
			if !strings.Contains(d.Name(), ".") {
				n++
			}
		}
		return nil
	})
	storageVal, storageN, storageAt = total, n, time.Now()
	return total, n
}

func dbSize() int64 {
	var total int64
	for _, s := range []string{"", "-wal", "-shm"} {
		if st, err := os.Stat(filepath.Join(app.Cfg.DataDir, "bunker.db"+s)); err == nil {
			total += st.Size()
		}
	}
	return total
}

func count(ctx context.Context, q string, args ...any) int64 {
	var n int64
	app.DB.QueryRowContext(ctx, q, args...).Scan(&n)
	return n
}

func registerStats(a *core.App) {
	a.Handle(core.Op{Name: "admin.stats", Method: "GET", Path: "/api/v1/admin/stats", Access: core.AccessAdmin, Summary: "Usage overview: people, sites, storage, top sites",
		Params: []core.Param{{Name: "days", In: "query", Type: "integer", Desc: "Window for top sites (default 30)"}},
		Fn: func(r *http.Request) (any, error) {
			ctx := r.Context()
			now := core.Now()
			days, _ := strconv.Atoi(r.URL.Query().Get("days"))
			if days <= 0 || days > 3650 {
				days = 30
			}
			bytes, blobs := blobUsage()
			type top struct {
				ID     int64  `json:"id"`
				Slug   string `json:"slug"`
				Title  string `json:"title"`
				Views  int64  `json:"views"`
				People int64  `json:"people"`
			}
			tops := []top{}
			rows, err := a.DB.QueryContext(ctx, `SELECT a.site_id, COALESCE(s.slug,''), COALESCE(s.title,''), COUNT(*), COUNT(DISTINCT a.actor_id) FROM audit a LEFT JOIN sites s ON s.id=a.site_id
WHERE a.event='site.view' AND a.ts>=? AND a.site_id IS NOT NULL GROUP BY a.site_id ORDER BY COUNT(*) DESC LIMIT 10`, now-int64(days)*86400)
			if err == nil {
				for rows.Next() {
					var t top
					rows.Scan(&t.ID, &t.Slug, &t.Title, &t.Views, &t.People)
					tops = append(tops, t)
				}
				rows.Close()
			}
			return map[string]any{
				"users":         count(ctx, `SELECT COUNT(*) FROM users WHERE disabled=0 AND subject NOT LIKE 'system:%'`),
				"admins":        count(ctx, `SELECT COUNT(*) FROM users WHERE disabled=0 AND is_admin=1`),
				"activeUsers7d": count(ctx, `SELECT COUNT(*) FROM users WHERE disabled=0 AND last_seen_at>=?`, now-7*86400),
				"activeUsers1d": count(ctx, `SELECT COUNT(*) FROM users WHERE disabled=0 AND last_seen_at>=?`, now-86400),
				"sites":         count(ctx, `SELECT COUNT(*) FROM sites WHERE deleted_at IS NULL AND is_template=0`),
				"templates":     count(ctx, `SELECT COUNT(*) FROM sites WHERE deleted_at IS NULL AND is_template=1`),
				"trashedSites":  count(ctx, `SELECT COUNT(*) FROM sites WHERE deleted_at IS NOT NULL`),
				"newSites7d":    count(ctx, `SELECT COUNT(*) FROM sites WHERE created_at>=?`, now-7*86400),
				"versions":      count(ctx, `SELECT COUNT(*) FROM versions`),
				"storageBytes":  bytes,
				"blobs":         blobs,
				"dbBytes":       dbSize(),
				"auditEvents":   count(ctx, `SELECT COUNT(*) FROM audit`),
				"events24h":     count(ctx, `SELECT COUNT(*) FROM audit WHERE ts>=?`, now-86400),
				"topSites":      tops,
				"topSitesDays":  days,
				"version":       core.Version,
				"sinkDropped":   sinkDropped.Load(),
			}, nil
		}})
}

// ---- policy ----

type policy struct {
	OrgWideSharing  bool   `json:"orgWideSharing"`
	EditorsCanShare bool   `json:"editorsCanShare"`
	MaxSitesPerUser int    `json:"maxSitesPerUser"`
	SiteMaxMB       int    `json:"siteMaxMB"`
	UploadMaxMB     int    `json:"uploadMaxMB"`
	AllowedGroups   string `json:"allowedGroups"`
	PublicLinks     bool   `json:"publicLinks"`
}

// Setting keys; the sites module reads the same keys (sharing.* as
// "false" to disable; numbers via sites.SettingInt). Each can be pinned
// by environment: BUNKER_SHARING_ORG_WIDE, BUNKER_SITE_MAX_MB, ...
const (
	kOrgWide  = "sharing.org_wide"
	kEditors  = "sharing.editors_can_share"
	kMaxSites = "max_sites_per_user"
	kSiteMB   = "site_max_mb"
	kUploadMB = "upload_max_mb"
	kGroups   = "auth.allowed_groups"
	kPublic   = "sharing.public_links"
)

func intSetting(k string, def int) int {
	if n, err := strconv.Atoi(app.Setting(k)); err == nil {
		return n
	}
	return def
}

func loadPolicy() policy {
	return policy{
		OrgWideSharing:  app.OrgWideSharing(),
		EditorsCanShare: app.Setting(kEditors) != "false",
		MaxSitesPerUser: intSetting(kMaxSites, 500),
		SiteMaxMB:       intSetting(kSiteMB, 100),
		UploadMaxMB:     intSetting(kUploadMB, 100),
		AllowedGroups:   firstNonEmpty(core.Env("ALLOWED_GROUPS", ""), app.Setting(kGroups)),
		PublicLinks:     app.Setting(kPublic) == "true",
	}
}

func firstNonEmpty(v ...string) string {
	for _, s := range v {
		if s != "" {
			return s
		}
	}
	return ""
}

func registerPolicy(a *core.App) {
	a.Handle(core.Op{Name: "admin.policy.get", Method: "GET", Path: "/api/v1/admin/policy", Access: core.AccessAdmin, Summary: "Sharing and size policy",
		Fn: func(r *http.Request) (any, error) {
			pinned := map[string]bool{}
			for k, s := range map[string]string{"orgWideSharing": kOrgWide, "editorsCanShare": kEditors, "maxSitesPerUser": kMaxSites, "siteMaxMB": kSiteMB, "uploadMaxMB": kUploadMB, "allowedGroups": kGroups, "publicLinks": kPublic} {
				pinned[k] = a.SettingFromEnv(s)
			}
			pinned["orgWideSharing"] = pinned["orgWideSharing"] || core.Env("ORG_WIDE_SHARING", "") != ""
			pinned["allowedGroups"] = pinned["allowedGroups"] || core.Env("ALLOWED_GROUPS", "") != ""
			return map[string]any{"policy": loadPolicy(), "pinned": pinned}, nil
		}})
	a.Handle(core.Op{Name: "admin.policy.update", Method: "PUT", Path: "/api/v1/admin/policy", Access: core.AccessAdmin, Summary: "Change sharing and size policy",
		Params: []core.Param{
			{Name: "orgWideSharing", In: "body", Type: "boolean", Desc: "Allow “everyone in the organization can view”"},
			{Name: "editorsCanShare", In: "body", Type: "boolean", Desc: "Editors (not just owners) can share"},
			{Name: "maxSitesPerUser", In: "body", Type: "integer"}, {Name: "siteMaxMB", In: "body", Type: "integer"}, {Name: "uploadMaxMB", In: "body", Type: "integer"},
			{Name: "allowedGroups", In: "body", Desc: "Comma-separated groups allowed to sign in (empty = everyone)"},
			{Name: "publicLinks", In: "body", Type: "boolean", Desc: "Let owners make a site viewable by anyone with the link, without signing in (off by default)"},
		},
		Fn: func(r *http.Request) (any, error) {
			var in struct {
				OrgWideSharing  *bool   `json:"orgWideSharing"`
				EditorsCanShare *bool   `json:"editorsCanShare"`
				MaxSitesPerUser *int    `json:"maxSitesPerUser"`
				SiteMaxMB       *int    `json:"siteMaxMB"`
				UploadMaxMB     *int    `json:"uploadMaxMB"`
				AllowedGroups   *string `json:"allowedGroups"`
				PublicLinks     *bool   `json:"publicLinks"`
			}
			if err := core.Decode(r, &in); err != nil {
				return nil, err
			}
			ctx := r.Context()
			old := loadPolicy()
			set := func(k, v string) error {
				if a.SettingFromEnv(k) {
					return core.Conflict(k + " is set by the server environment; change it there")
				}
				return a.SetSetting(ctx, k, v)
			}
			nums := []struct {
				k      string
				v      *int
				lo, hi int
				label  string
			}{
				{kMaxSites, in.MaxSitesPerUser, 0, 100000, "maxSitesPerUser"},
				{kSiteMB, in.SiteMaxMB, 1, 10240, "siteMaxMB"},
				{kUploadMB, in.UploadMaxMB, 1, 10240, "uploadMaxMB"},
			}
			// Check every value before saving any, so a bad number doesn't
			// leave the rest of the change half-applied.
			for _, n := range nums {
				if n.v != nil && (*n.v < n.lo || *n.v > n.hi) {
					return nil, core.BadRequest(n.label + " must be between " + strconv.Itoa(n.lo) + " and " + strconv.Itoa(n.hi))
				}
			}
			for k, changed := range map[string]bool{kOrgWide: in.OrgWideSharing != nil, kPublic: in.PublicLinks != nil, kEditors: in.EditorsCanShare != nil,
				kMaxSites: in.MaxSitesPerUser != nil, kSiteMB: in.SiteMaxMB != nil, kUploadMB: in.UploadMaxMB != nil, kGroups: in.AllowedGroups != nil} {
				if changed && a.SettingFromEnv(k) {
					return nil, core.Conflict(k + " is set by the server environment; change it there")
				}
			}
			if in.OrgWideSharing != nil {
				if err := set(kOrgWide, strconv.FormatBool(*in.OrgWideSharing)); err != nil {
					return nil, err
				}
				a.Emit(ctx, "sites.changed", nil)
			}
			if in.PublicLinks != nil {
				if err := set(kPublic, strconv.FormatBool(*in.PublicLinks)); err != nil {
					return nil, err
				}
				if !*in.PublicLinks {
					// Turning the feature off closes every public link at once.
					a.DB.ExecContext(ctx, `UPDATE sites SET public=0 WHERE public=1`)
					a.Emit(ctx, "sites.changed", nil)
				}
			}
			if in.EditorsCanShare != nil {
				if err := set(kEditors, strconv.FormatBool(*in.EditorsCanShare)); err != nil {
					return nil, err
				}
			}
			for _, n := range nums {
				if n.v != nil {
					if err := set(n.k, strconv.Itoa(*n.v)); err != nil {
						return nil, err
					}
				}
			}
			if in.AllowedGroups != nil {
				if err := set(kGroups, strings.TrimSpace(*in.AllowedGroups)); err != nil {
					return nil, err
				}
			}
			p := loadPolicy()
			a.Audit(ctx, "admin.config", 0, "policy", map[string]any{"old": old, "new": p})
			return map[string]any{"policy": p}, nil
		}})
}
