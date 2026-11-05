package sites_test

import (
	"fmt"
	"net/url"
	"regexp"
	"strings"
	"testing"

	"github.com/kucukkanat/bunker/internal/core"
	_ "github.com/kucukkanat/bunker/internal/notify"
	"github.com/kucukkanat/bunker/internal/sites"
	"github.com/kucukkanat/bunker/internal/testkit"
)

func userID(t *testing.T, admin *testkit.Client, email string) (float64, float64) {
	t.Helper()
	for _, x := range admin.Must("GET", "/api/v1/admin/users", nil)["users"].([]any) {
		u := x.(map[string]any)
		if u["email"] == email {
			return u["id"].(float64), u["sites"].(float64)
		}
	}
	t.Fatalf("no user %s", email)
	return 0, 0
}

func TestDisableUserTransfersSites(t *testing.T) {
	k := testkit.New(t)
	admin := k.As("", "")
	ada := k.As("ada@corp.com", "")
	bob := k.As("bob@corp.com", "")
	bob.Must("GET", "/api/v1/me", nil)
	a1 := drop(t, ada, map[string]string{"index.html": "<title>Ada One</title>"})["slug"].(string)
	drop(t, ada, map[string]string{"index.html": "<title>Ada Two</title>"})
	id, n := userID(t, admin, "ada@corp.com")
	if n != 2 {
		t.Fatalf("users.list sites count = %v", n)
	}
	path := fmt.Sprintf("/api/v1/admin/users/%d", int64(id))
	if r := admin.Do("PATCH", path, map[string]any{"transferTo": "bob@corp.com"}); r.Status != 400 {
		t.Fatalf("transferTo without disabled: %d", r.Status)
	}
	if r := admin.Do("PATCH", path, map[string]any{"disabled": true, "transferTo": "nobody@corp.com"}); r.Status != 404 {
		t.Fatalf("unknown transferTo: %d", r.Status)
	}
	res := admin.Must("PATCH", path, map[string]any{"disabled": true, "transferTo": "bob@corp.com"})
	if res["moved"] != float64(2) {
		t.Fatalf("moved: %v", res)
	}
	if got := bob.Must("GET", "/api/v1/sites/"+a1, nil); got["role"] != "owner" {
		t.Fatalf("bob should own %s: %v", a1, got["role"])
	}
	var c int
	k.App.DB.QueryRow(`SELECT COUNT(*) FROM audit WHERE event='admin.user_offboard'`).Scan(&c)
	if c != 1 {
		t.Fatal("missing offboarding audit entry")
	}
}

func TestIdleArchival(t *testing.T) {
	t.Setenv("BUNKER_ARCHIVE_AFTER_DAYS", "30")
	k := testkit.New(t)
	ada := k.As("ada@corp.com", "")
	bob := k.As("bob@corp.com", "")
	slug := drop(t, ada, map[string]string{"index.html": "<title>Old Tool</title><h1>hi</h1>"})["slug"].(string)
	ada.Must("POST", "/api/v1/sites/"+slug+"/members", map[string]any{"type": "user", "id": "bob@corp.com", "role": "viewer"})
	tpl := drop(t, ada, map[string]string{"index.html": "<title>Tpl</title>"})["slug"].(string)
	ada.Must("PATCH", "/api/v1/sites/"+tpl, map[string]any{"isTemplate": true})

	real := core.Now
	t.Cleanup(func() { core.Now = real })
	start := real()
	at := func(days int64) { core.Now = func() int64 { return start + days*86400 } }
	ctx := t.Context()

	at(10)
	if w, a := sites.RunArchive(ctx); w != 0 || a != 0 {
		t.Fatalf("too early: %d %d", w, a)
	}
	at(24)
	if w, a := sites.RunArchive(ctx); w != 1 || a != 0 {
		t.Fatalf("warning: warned %d archived %d", w, a)
	}
	var title string
	k.App.DB.QueryRow(`SELECT title FROM notifications WHERE user_email='ada@corp.com' AND kind='archive'`).Scan(&title)
	if !strings.Contains(title, "Old Tool") || !strings.Contains(title, "in 6 days") && !strings.Contains(title, "in 7 days") {
		t.Fatalf("warning notification: %q", title)
	}
	if w, _ := sites.RunArchive(ctx); w != 0 {
		t.Fatal("warn only once")
	}
	at(30)
	if _, a := sites.RunArchive(ctx); a != 0 {
		t.Fatal("must wait 7 days after the warning")
	}
	at(31)
	if _, a := sites.RunArchive(ctx); a != 1 {
		t.Fatal("expected the site to be archived")
	}
	core.Now = real // sessions would have expired a month later
	if g := ada.Must("GET", "/api/v1/sites/"+slug, nil); g["archivedAt"] == nil {
		t.Fatalf("archivedAt not set: %v", g)
	}
	// Editors see the restore page, viewers a plain notice.
	page := ada.Do("GET", "/s/"+slug+"/", nil)
	if !strings.Contains(string(page.Body), "nobody used it for 30 days") || !strings.Contains(string(page.Body), ">Restore<") {
		t.Fatalf("archived page: %s", page.Body)
	}
	if p := bob.Do("GET", "/s/"+slug+"/", nil); strings.Contains(string(p.Body), ">Restore<") || !strings.Contains(string(p.Body), "Ask the owner") {
		t.Fatalf("viewer page: %s", p.Body)
	}
	tok := regexp.MustCompile(`name="t" value="([^"]+)"`).FindStringSubmatch(string(page.Body))[1]
	if r := bob.Do("POST", "/archive-restore", "site="+slug+"&t="+url.QueryEscape(tok), "Content-Type", "application/x-www-form-urlencoded"); r.Status != 403 {
		t.Fatalf("someone else's token must fail: %d", r.Status)
	}
	if r := ada.Do("POST", "/archive-restore", "site="+slug+"&t="+url.QueryEscape(tok), "Content-Type", "application/x-www-form-urlencoded"); r.Status != 303 {
		t.Fatalf("restore form: %d %s", r.Status, r.Body)
	}
	if p := ada.Do("GET", "/s/"+slug+"/", nil); !strings.Contains(string(p.Body), `id="bk-frame"`) {
		t.Fatalf("restored site should serve: %s", p.Body)
	}
	if _, a := sites.RunArchive(ctx); a != 0 {
		t.Fatal("restoring counts as activity")
	}
	// Manual archive / unarchive ops; viewers can't.
	if r := bob.Do("POST", "/api/v1/sites/"+slug+"/archive", nil); r.Status < 400 {
		t.Fatalf("viewer archived: %d", r.Status)
	}
	if g := ada.Must("POST", "/api/v1/sites/"+slug+"/archive", nil); g["archivedAt"] == nil || g["archivedDays"] != nil {
		t.Fatalf("archive op: %v", g)
	}
	if g := ada.Must("POST", "/api/v1/sites/"+slug+"/unarchive", nil); g["archivedAt"] != nil {
		t.Fatalf("unarchive op: %v", g)
	}
	// Templates are never archived.
	if g := ada.Must("GET", "/api/v1/sites/"+tpl, nil); g["archivedAt"] != nil {
		t.Fatal("template archived")
	}
}
