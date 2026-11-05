package templates

import (
	"testing"

	_ "github.com/kucukkanat/bunker/internal/data"
	"github.com/kucukkanat/bunker/internal/testkit"
)

func TestCopyKeepsTemplateRules(t *testing.T) {
	k := testkit.New(t)
	u := k.As("amy@corp.test", "")
	s := u.Must("POST", "/api/v1/sites", map[string]any{"title": "Our board", "template": "template-tracker"})
	var rule string
	k.App.DB.QueryRow(`SELECT c.rule FROM collections c JOIN sites s ON s.id=c.site_id WHERE s.slug=? AND c.name='cards'`, s["slug"]).Scan(&rule)
	if rule != "open" {
		t.Fatalf("cards rule on the copy = %q, want open", rule)
	}
}
