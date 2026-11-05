package templates

import (
	"context"
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
