// Package templates seeds Bunker's built-in starter templates on first
// run. They are ordinary sites marked is_template=1, owned by a disabled
// "Bunker" system user and viewable by everyone, so people can preview
// them and start a copy. Each built-in template is seeded once, by slug
// (setting templates.known lists the slugs already handled), so a new
// release adds its new templates to existing installs, while templates
// admins later edit or delete are never touched again.
package templates

import (
	"context"
	"embed"
	"io/fs"
	"sort"
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
	{"template-assets", "asset-checkout", "Equipment check-out", "Laptops, cameras and kit: who has what, until when, with a full history.", "💻"},
	{"template-booking", "room-booking", "Room & desk booking", "A day grid of rooms and desks. Book a slot in two taps; no double booking.", "🗓️"},
	{"template-inventory", "inventory", "Inventory & stock count", "Stock levels with low-stock alerts, quick counts, a change log and CSV export.", "📦"},
	{"template-incidents", "incident-log", "Incident & issue log", "Report issues by severity, follow a live timeline and alert the on-call group.", "🚨"},
	{"template-onboarding", "onboarding", "Onboarding checklist", "A first-weeks checklist each new starter ticks off privately; managers see progress.", "🧭"},
	{"template-okrs", "okrs", "Goals & OKRs", "Quarterly objectives with key results, owners, progress bars and check-ins.", "🎯"},
	{"template-kudos", "kudos", "Kudos wall", "Thank a teammate in public. Live wall with reactions and company values.", "🙌"},
	{"template-lunch", "lunch-orders", "Lunch orders", "A weekly menu, one order per person per day and live tallies for the organizer.", "🥪"},
	{"template-contact", "contact-form", "Contact form", "A contact form checked on the server, with a private inbox and notifications for the team.", "✉️"},
}

// firstRelease are the templates of the first release. Installs seeded by
// it only have templates.seeded set; these count as already handled there.
var firstRelease = []string{"template-requests", "template-tracker", "template-signup", "template-feedback", "template-poll", "template-dashboard", "template-directory", "template-meetings"}

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
// Collections not listed use the default rule, "own" (everyone reads,
// people change only what they added).
var Rules = map[string]map[string]string{
	"template-tracker":    {"cards": "open"},
	"template-meetings":   {"meetings": "open"},
	"template-assets":     {"assets": "read", "loans": "own"},
	"template-booking":    {"rooms": "read", "bookings": "own"},
	"template-inventory":  {"items": "open", "stocklog": "own"},
	"template-incidents":  {"incidents": "open", "updates": "own"},
	"template-onboarding": {"tasks": "read", "progress": "private"},
	"template-okrs":       {"objectives": "open"},
	"template-kudos":      {"kudos": "own", "reactions": "own"},
	"template-lunch":      {"menu": "read", "orders": "own"},
	// Messages are written by the elevated api/contact.js function (as
	// the owner), so senders can't read anyone's messages.
	"template-contact": {"messages": "private"},
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

// Files returns all of a template's files: the rendered index.html plus
// anything else in its folder (server functions in api/, data files...).
func Files(dir string) (map[string][]byte, error) {
	html, err := Render(dir)
	if err != nil {
		return nil, err
	}
	out := map[string][]byte{"index.html": html}
	root := "files/" + dir
	err = fs.WalkDir(files, root, func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		rel := strings.TrimPrefix(p, root+"/")
		if rel == "index.html" {
			return nil
		}
		b, err := files.ReadFile(p)
		out[rel] = b
		return err
	})
	return out, err
}

// Seed creates each built-in template once. A template is skipped when
// its slug was handled before (seeded, or deleted since) or the address is
// taken. On the very first start, if templates already exist, the current
// catalogue is marked as handled without creating anything. Seed is
// idempotent and safe to run on every start.
func Seed(ctx context.Context, a *core.App) error {
	known := map[string]bool{}
	for _, s := range strings.Split(a.Setting("templates.known"), ",") {
		if s = strings.TrimSpace(s); s != "" {
			known[s] = true
		}
	}
	if a.Setting("templates.seeded") == "1" && len(known) == 0 {
		for _, s := range firstRelease {
			known[s] = true
		}
	}
	if a.Setting("templates.seeded") != "1" && len(known) == 0 {
		var n int
		a.DB.QueryRowContext(ctx, `SELECT COUNT(*) FROM sites WHERE is_template=1`).Scan(&n)
		if n > 0 {
			for _, t := range All {
				known[t.Slug] = true
			}
		}
	}
	var owner *core.User
	var err error
	for _, t := range All {
		if known[t.Slug] {
			continue
		}
		var exists int
		a.DB.QueryRowContext(ctx, `SELECT COUNT(*) FROM sites WHERE slug=?`, t.Slug).Scan(&exists)
		if exists == 0 {
			if owner == nil {
				if owner, err = systemUser(ctx, a); err != nil {
					return err
				}
			}
			if err := create(ctx, a, owner, t); err != nil {
				return err
			}
		}
		known[t.Slug] = true
		if err := saveKnown(ctx, a, known); err != nil {
			return err
		}
	}
	if err := saveKnown(ctx, a, known); err != nil {
		return err
	}
	return a.SetSetting(ctx, "templates.seeded", "1")
}

func saveKnown(ctx context.Context, a *core.App, known map[string]bool) error {
	list := make([]string, 0, len(known))
	for s := range known {
		list = append(list, s)
	}
	sort.Strings(list)
	return a.SetSetting(ctx, "templates.known", strings.Join(list, ","))
}

// create publishes one built-in template as a site owned by owner.
func create(ctx context.Context, a *core.App, owner *core.User, t Template) error {
	tf, err := Files(t.Dir)
	if err != nil {
		return err
	}
	s, err := sites.CreateSite(ctx, owner, t.Title, t.Slug, t.Description, t.Icon)
	if err != nil {
		return err
	}
	m, err := sites.StoreFiles(tf)
	if err != nil {
		return err
	}
	if _, err := sites.Commit(ctx, owner, s, m, "Built-in template", "template", true); err != nil {
		return err
	}
	a.DB.ExecContext(ctx, `UPDATE sites SET is_template=1, org_view=1, kind='html' WHERE id=?`, s.ID)
	sites.Invalidate(s.Slug)
	return nil
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
