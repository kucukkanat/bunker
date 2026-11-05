package templates

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/kucukkanat/bunker/internal/testkit"
)

func TestSeeded(t *testing.T) {
	k := testkit.New(t)
	c := k.As("", "")
	res := c.Must("GET", "/api/v1/sites?filter=templates", nil)
	list, _ := res["sites"].([]any)
	if len(list) != len(All) {
		t.Fatalf("got %d templates, want %d: %v", len(list), len(All), res)
	}
	// A regular user can see and copy a template.
	u := k.As("amy@corp.test", "Sales")
	r := u.Do("GET", "/s/template-poll/", nil)
	if r.Status != 200 {
		t.Fatalf("template page: %d", r.Status)
	}
	// Seeding is one-time: deleting a template sticks.
	c.Must("DELETE", "/api/v1/sites/template-poll", nil)
	if err := Seed(context.Background(), k.App); err != nil {
		t.Fatal(err)
	}
	var n int
	k.App.DB.QueryRow(`SELECT COUNT(*) FROM sites WHERE slug='template-poll' AND deleted_at IS NULL`).Scan(&n)
	if n != 0 {
		t.Fatal("deleted template came back")
	}
	// The system owner can't sign in.
	var disabled int
	k.App.DB.QueryRow(`SELECT disabled FROM users WHERE subject='system:bunker'`).Scan(&disabled)
	if disabled != 1 {
		t.Fatal("system user must be disabled")
	}
}

// An install seeded by an older release (only templates.seeded set) gets
// the templates added since, once, and keeps what admins deleted or edited.
func TestSeedAddsNewTemplatesToOldInstalls(t *testing.T) {
	k := testkit.New(t)
	c := k.As("", "")
	ctx := context.Background()
	c.Must("DELETE", "/api/v1/sites/template-poll", nil)
	k.App.DB.Exec(`UPDATE sites SET title='Our requests' WHERE slug='template-requests'`)
	// Pretend the newer templates never existed and this is an old install.
	newer := All[len(firstRelease):]
	for _, tp := range newer {
		k.App.DB.Exec(`UPDATE sites SET slug=?, is_template=0 WHERE slug=?`, "old-"+tp.Slug, tp.Slug)
	}
	k.App.DB.Exec(`DELETE FROM settings WHERE key='templates.known'`)
	k.App.SetSetting(ctx, "templates.seeded", "1")
	for i := 0; i < 2; i++ {
		if err := Seed(ctx, k.App); err != nil {
			t.Fatal(err)
		}
	}
	count := func(q string, args ...any) (n int) {
		k.App.DB.QueryRow(q, args...).Scan(&n)
		return
	}
	for _, tp := range newer {
		if n := count(`SELECT COUNT(*) FROM sites WHERE slug=? AND is_template=1 AND deleted_at IS NULL`, tp.Slug); n != 1 {
			t.Errorf("%s: %d live copies after upgrade, want 1", tp.Slug, n)
		}
	}
	if count(`SELECT COUNT(*) FROM sites WHERE slug='template-poll' AND deleted_at IS NULL`) != 0 {
		t.Error("deleted template came back")
	}
	var title string
	k.App.DB.QueryRow(`SELECT title FROM sites WHERE slug='template-requests'`).Scan(&title)
	if title != "Our requests" {
		t.Errorf("edited template was overwritten: %q", title)
	}
	if got := count(`SELECT COUNT(*) FROM sites WHERE is_template=1 AND deleted_at IS NULL`); got != len(All)-1 {
		t.Errorf("templates after upgrade = %d, want %d", got, len(All)-1)
	}
	// Deleting a new template sticks too.
	c.Must("DELETE", "/api/v1/sites/template-kudos", nil)
	Seed(ctx, k.App)
	if count(`SELECT COUNT(*) FROM sites WHERE slug='template-kudos' AND deleted_at IS NULL`) != 0 {
		t.Error("deleted new template came back")
	}
}

// A first start with templates already present seeds nothing.
func TestSeedSkipsWhenTemplatesExistOnFirstStart(t *testing.T) {
	k := testkit.New(t)
	ctx := context.Background()
	k.App.DB.Exec(`UPDATE sites SET slug='mine-'||slug WHERE slug LIKE 'template-%'`)
	k.App.DB.Exec(`DELETE FROM settings WHERE key IN ('templates.known','templates.seeded')`)
	if err := Seed(ctx, k.App); err != nil {
		t.Fatal(err)
	}
	var n int
	k.App.DB.QueryRow(`SELECT COUNT(*) FROM sites WHERE slug LIKE 'template-%'`).Scan(&n)
	if n != 0 {
		t.Fatalf("seeded %d templates over existing ones", n)
	}
}

var scriptRe = regexp.MustCompile(`(?s)<script>(.*?)</script>`)

func TestTemplatesWellFormed(t *testing.T) {
	node, nodeErr := exec.LookPath("node")
	for _, tp := range All {
		b, err := Render(tp.Dir)
		if err != nil {
			t.Fatal(tp.Dir, err)
		}
		s := string(b)
		for _, want := range []string{"<!doctype html>", `/_/kit.css`, `name="viewport"`, "var BK =", "--forest"} {
			if !strings.Contains(s, want) {
				t.Errorf("%s: missing %q", tp.Dir, want)
			}
		}
		if strings.Contains(s, "/*BK:") || strings.Contains(s, "http://") || regexp.MustCompile(`src="https?://`).MatchString(s) {
			t.Errorf("%s: placeholder or external URL left", tp.Dir)
		}
		if nodeErr == nil {
			for i, m := range scriptRe.FindAllStringSubmatch(s, -1) {
				f := filepath.Join(t.TempDir(), "s.js")
				os.WriteFile(f, []byte(m[1]), 0o600)
				if out, err := exec.Command(node, "--check", f).CombinedOutput(); err != nil {
					t.Errorf("%s script %d: %s", tp.Dir, i, out)
				}
			}
		}
	}
}

// Server functions shipped with templates are valid ES modules with a
// default export, and the seeded site keeps them.
func TestTemplateFunctions(t *testing.T) {
	node, nodeErr := exec.LookPath("node")
	found := 0
	for _, tp := range All {
		fs, err := Files(tp.Dir)
		if err != nil {
			t.Fatal(tp.Dir, err)
		}
		for p, b := range fs {
			if !strings.HasPrefix(p, "api/") || !strings.HasSuffix(p, ".js") {
				continue
			}
			found++
			if !strings.Contains(string(b), "export default") {
				t.Errorf("%s/%s: no export default", tp.Dir, p)
			}
			if strings.Contains(string(b), "document.") || strings.Contains(string(b), "window.") {
				t.Errorf("%s/%s: uses browser globals", tp.Dir, p)
			}
			if nodeErr == nil {
				f := filepath.Join(t.TempDir(), "fn.mjs")
				os.WriteFile(f, b, 0o600)
				if out, err := exec.Command(node, "--check", f).CombinedOutput(); err != nil {
					t.Errorf("%s/%s: %s", tp.Dir, p, out)
				}
			}
		}
	}
	if found == 0 {
		t.Fatal("no template ships a server function")
	}
	k := testkit.New(t)
	r := k.As("", "").Must("GET", "/api/v1/sites/template-contact/files", nil)
	if !strings.Contains(fmt.Sprint(r), "api/contact.js") {
		t.Fatalf("seeded contact template misses its function: %v", r)
	}
}
