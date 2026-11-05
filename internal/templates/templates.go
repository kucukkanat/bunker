// Package templates seeds Bunker's built-in starter templates on first
// run. They are ordinary sites marked is_template=1, owned by a disabled
// "Bunker" system user and viewable by everyone, so people can preview
// them and start a copy. Seeding happens once (setting templates.seeded);
// templates admins later edit or delete are never touched again.
package templates

import (
	"context"
	"embed"
	"strings"

	"github.com/kucukkanat/bunker/internal/core"
	"github.com/kucukkanat/bunker/internal/sites"
)

//go:embed files
var files embed.FS

// Template describes one starter.
type Template struct {
	Slug, Dir, Title, Description, Icon string
}

// All is the built-in catalogue, in display order.
var All = []Template{
	{"template-requests", "request-approval", "Requests & approvals", "A request form with an approvers group, clear statuses and a full history.", "✅"},
	{"template-tracker", "team-tracker", "Team tracker", "A live kanban board: drag cards between columns, see changes instantly.", "🗂️"},
	{"template-signup", "signup-sheet", "Sign-up sheet", "Slots with limits for events, shifts or volunteering. No double booking.", "📝"},
	{"template-feedback", "feedback", "Feedback collector", "Ratings and comments with live results and an AI summary.", "💬"},
	{"template-poll", "poll", "Quick poll", "One question, one vote each, results update live.", "📊"},
	{"template-dashboard", "dashboard", "Simple dashboard", "KPIs and charts from a CSV, a SharePoint list or sample data.", "📈"},
	{"template-directory", "team-directory", "Team directory & who’s out", "Profiles, skills and a shared calendar of time away.", "👋"},
	{"template-meetings", "meeting-notes", "Meeting notes & decisions", "Notes, decisions and action items you can find again later.", "🗒️"},
}

func init() {
	core.Register(core.Module{Name: "templates", Order: 70, Init: initTemplates})
}

func initTemplates(a *core.App) error {
	if core.EnvBool("NO_TEMPLATES", false) {
		return nil
	}
	if err := Seed(context.Background(), a); err != nil {
		a.Log.Warn("template seeding failed", "err", err)
	}
	applyRules(context.Background(), a)
	return nil
}

// Rules are the data rules a built-in template works best with. Copies of
// a template start with its rules (see the data module), so a team tracker
// lets teammates move each other's cards without extra setup.
var Rules = map[string]map[string]string{
	"template-tracker":  {"cards": "open"},
	"template-meetings": {"meetings": "open"},
}

// applyRules sets missing rules on the built-in templates (an admin's
// change to a template's rule is kept).
func applyRules(ctx context.Context, a *core.App) {
	now := core.Now()
	for slug, rules := range Rules {
		var id int64
		if a.DB.QueryRowContext(ctx, `SELECT id FROM sites WHERE slug=? AND is_template=1`, slug).Scan(&id) != nil {
			continue
		}
		for coll, rule := range rules {
			a.DB.ExecContext(ctx, `INSERT OR IGNORE INTO collections(site_id,name,rule,created_at,updated_at) VALUES(?,?,?,?,?)`, id, coll, rule, now, now)
		}
	}
}

// Render returns a template's final index.html (shared CSS/JS inlined).
func Render(dir string) ([]byte, error) {
	b, err := files.ReadFile("files/" + dir + "/index.html")
	if err != nil {
		return nil, err
	}
	css, _ := files.ReadFile("files/shared/style.css")
	js, _ := files.ReadFile("files/shared/bk.js")
	s := strings.Replace(string(b), "/*BK:STYLE*/", string(css), 1)
	s = strings.Replace(s, "/*BK:JS*/", string(js), 1)
	return []byte(s), nil
}

// Seed creates the templates once. It does nothing if templates exist or
// were seeded before (so deleting or editing them sticks).
func Seed(ctx context.Context, a *core.App) error {
	if a.Setting("templates.seeded") == "1" {
		return nil
	}
	var n int
	a.DB.QueryRowContext(ctx, `SELECT COUNT(*) FROM sites WHERE is_template=1`).Scan(&n)
	if n > 0 {
		return a.SetSetting(ctx, "templates.seeded", "1")
	}
	owner, err := systemUser(ctx, a)
	if err != nil {
		return err
	}
	for _, t := range All {
		var exists int
		a.DB.QueryRowContext(ctx, `SELECT COUNT(*) FROM sites WHERE slug=?`, t.Slug).Scan(&exists)
		if exists > 0 {
			continue
		}
		html, err := Render(t.Dir)
		if err != nil {
			return err
		}
		s, err := sites.CreateSite(ctx, owner, t.Title, t.Slug, t.Description, t.Icon)
		if err != nil {
			return err
		}
		m, err := sites.StoreFiles(map[string][]byte{"index.html": html})
		if err != nil {
			return err
		}
		if _, err := sites.Commit(ctx, owner, s, m, "Built-in template", "template", true); err != nil {
			return err
		}
		a.DB.ExecContext(ctx, `UPDATE sites SET is_template=1, org_view=1, kind='html' WHERE id=?`, s.ID)
		sites.Invalidate(s.Slug)
	}
	return a.SetSetting(ctx, "templates.seeded", "1")
}

// systemUser returns the disabled account that owns built-in content.
func systemUser(ctx context.Context, a *core.App) (*core.User, error) {
	const subject = "system:bunker"
	var id int64
	err := a.DB.QueryRowContext(ctx, `SELECT id FROM users WHERE subject=?`, subject).Scan(&id)
	if err != nil {
		now := core.Now()
		res, err := a.DB.ExecContext(ctx, `INSERT INTO users(subject,email,name,is_admin,groups_json,created_at,last_seen_at,disabled) VALUES(?,?,?,0,'[]',?,?,1)`,
			subject, "templates@bunker.local", "Bunker", now, 0)
		if err != nil {
			return nil, err
		}
		id, _ = res.LastInsertId()
	}
	return &core.User{ID: id, Email: "templates@bunker.local", Name: "Bunker"}, nil
}
