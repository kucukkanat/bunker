package templates

import (
	"strings"
	"testing"

	_ "github.com/kucukkanat/bunker/internal/data"
	"github.com/kucukkanat/bunker/internal/testkit"
)

func TestCopyKeepsTemplateRules(t *testing.T) {
	k := testkit.New(t)
	u := k.As("amy@corp.test", "")
	for slug, rules := range Rules {
		s := u.Must("POST", "/api/v1/sites", map[string]any{"title": "Copy of " + slug, "template": slug})
		for coll, want := range rules {
			rule := "own" // the default when a collection has no row yet
			k.App.DB.QueryRow(`SELECT c.rule FROM collections c JOIN sites s ON s.id=c.site_id WHERE s.slug=? AND c.name=?`, s["slug"], coll).Scan(&rule)
			if rule != want {
				t.Errorf("%s: %s rule on the copy = %q, want %q", slug, coll, rule, want)
			}
		}
	}
}

// Every rule names a real template and a collection its page uses.
func TestRulesMatchTemplates(t *testing.T) {
	dirs := map[string]string{}
	for _, tp := range All {
		dirs[tp.Slug] = tp.Dir
	}
	for slug, rules := range Rules {
		dir, ok := dirs[slug]
		if !ok {
			t.Errorf("rules for unknown template %s", slug)
			continue
		}
		b, _ := Render(dir)
		for coll := range rules {
			if !strings.Contains(string(b), "col('"+coll+"')") {
				t.Errorf("%s: page never uses collection %q", slug, coll)
			}
		}
	}
}
