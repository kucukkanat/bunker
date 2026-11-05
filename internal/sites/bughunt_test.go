package sites_test

import (
	"fmt"
	"strings"
	"sync"
	"testing"
	"unicode/utf8"

	"github.com/kucukkanat/bunker/internal/testkit"
)

// A PATCH that fails validation must not have saved the fields before the
// bad one (and the site cache must not hide what was written).
func TestSiteUpdateIsAllOrNothing(t *testing.T) {
	k := testkit.New(t)
	ada := k.As("ada@corp.com", "")
	drop(t, ada, map[string]string{"index.html": "<title>Alpha</title>hi"})
	if r := ada.Do("PATCH", "/api/v1/sites/alpha", map[string]any{"title": "Renamed", "slug": "Not A Slug"}); r.Status != 400 {
		t.Fatalf("bad slug: %d %s", r.Status, r.Body)
	}
	var got string
	k.App.DB.QueryRow(`SELECT title FROM sites WHERE slug=?`, "alpha").Scan(&got)
	if got != "Alpha" {
		t.Errorf("title changed by a rejected update: %v", got)
	}
	k.App.SetSetting(t.Context(), "sharing.public_links", "")
	if r := ada.Do("PATCH", "/api/v1/sites/alpha", map[string]any{"description": "new words", "public": true}); r.Status != 403 {
		t.Fatalf("public with links off: %d %s", r.Status, r.Body)
	}
	k.App.DB.QueryRow(`SELECT description FROM sites WHERE slug=?`, "alpha").Scan(&got)
	if got != "" {
		t.Errorf("description changed by a rejected update: %v", got)
	}
}

// Truncating icons/descriptions by bytes cut emoji in half (invalid UTF-8).
func TestSiteFieldsTruncateOnRuneBoundaries(t *testing.T) {
	k := testkit.New(t)
	ada := k.As("ada@corp.com", "")
	s := ada.Must("POST", "/api/v1/sites", map[string]any{"title": "Emoji", "icon": "👩‍👩‍👧‍👦👩‍👩‍👧‍👦", "description": strings.Repeat("é", 400)})
	var icon, desc string
	k.App.DB.QueryRow(`SELECT icon, description FROM sites WHERE id=?`, s["id"]).Scan(&icon, &desc)
	if !utf8.ValidString(icon) || !utf8.ValidString(desc) {
		t.Errorf("invalid UTF-8 after truncation: %q / %q", s["icon"], s["description"])
	}
}

// Creating a site from a template that can't be used must not leave an
// empty site behind; neither must a failed duplicate.
func TestFailedCreateFromTemplateLeavesNoSite(t *testing.T) {
	k := testkit.New(t)
	ada := k.As("ada@corp.com", "")
	before := len(ada.Must("GET", "/api/v1/sites?filter=mine", nil)["sites"].([]any))
	if r := ada.Do("POST", "/api/v1/sites", map[string]any{"title": "From nothing", "template": "no-such-template"}); r.Status != 404 {
		t.Fatalf("missing template: %d %s", r.Status, r.Body)
	}
	if after := len(ada.Must("GET", "/api/v1/sites?filter=mine", nil)["sites"].([]any)); after != before {
		t.Errorf("a failed create left %d site(s) behind", after-before)
	}
}

// A copy of a single-page app must keep its settings (SPA routing, entry
// page), or its deep links stop working.
func TestDuplicateKeepsSettings(t *testing.T) {
	k := testkit.New(t)
	ada := k.As("ada@corp.com", "")
	drop(t, ada, map[string]string{"index.html": "<title>Router</title>app"})
	ada.Must("PATCH", "/api/v1/sites/router", map[string]any{"settings": map[string]any{"spa": true, "hideBadge": true}})
	cp := ada.Must("POST", "/api/v1/sites/router/duplicate", map[string]any{})
	st := cp["settings"].(map[string]any)
	if st["spa"] != true || st["hideBadge"] != true {
		t.Errorf("copy lost its settings: %v", st)
	}
}

// Parallel file writes to the draft (CLI, agents) must not lose files.
func TestParallelDraftWritesKeepEveryFile(t *testing.T) {
	k := testkit.New(t)
	ada := k.As("ada@corp.com", "")
	drop(t, ada, map[string]string{"index.html": "<title>Many</title>x"})
	var wg sync.WaitGroup
	for i := 0; i < 16; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			ada.Do("PUT", fmt.Sprintf("/api/v1/sites/many/files/f%d.txt", i), fmt.Sprintf("file %d", i))
		}(i)
	}
	wg.Wait()
	files := ada.Must("GET", "/api/v1/sites/many/files", nil)["files"].([]any)
	if len(files) != 17 {
		t.Errorf("draft has %d files, want 17", len(files))
	}
}

func TestOneLetterSlugRejected(t *testing.T) {
	k := testkit.New(t)
	ada := k.As("ada@corp.com", "")
	if r := ada.Do("POST", "/api/v1/sites", map[string]any{"title": "A", "slug": "a"}); r.Status != 400 {
		t.Errorf("one-letter address accepted: %d", r.Status)
	}
}

// Admins named in the "admins" setting (or BUNKER_ADMINS) open any site in
// the shell, but the sandboxed frame's requests carry no cookie and were
// checked against the users table only, so they got "Access revoked".
func TestConfiguredAdminCanLoadSiteContent(t *testing.T) {
	k := testkit.New(t)
	ada := k.As("ada@corp.com", "")
	drop(t, ada, map[string]string{"index.html": "<title>Private</title>secret"})
	k.App.SetSetting(t.Context(), "admins", "bob@corp.com")
	bob := k.As("bob@corp.com", "")
	r := bob.Do("GET", "/s/private/", nil)
	body := string(r.Body)
	i := strings.Index(body, `src="/s/private/~r/`)
	if r.Status != 200 || i < 0 {
		t.Fatalf("shell: %d %s", r.Status, body)
	}
	src := body[i+5 : i+5+strings.Index(body[i+5:], `"`)]
	src = strings.ReplaceAll(src, "&#43;", "+")
	if raw := k.As("", "").Do("GET", src, nil, "Sec-Fetch-Dest", "iframe"); raw.Status != 200 {
		t.Errorf("content for a configured admin: %d %s", raw.Status, raw.Body)
	}
}
