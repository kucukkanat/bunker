package admin_test

import (
	"testing"

	_ "github.com/kucukkanat/bunker/internal/admin"
	"github.com/kucukkanat/bunker/internal/testkit"
)

// A bad number in a policy change used to come after the other fields had
// already been saved.
func TestPolicyUpdateIsAllOrNothing(t *testing.T) {
	k := testkit.New(t)
	admin := k.As("", "")
	if r := admin.Do("PUT", "/api/v1/admin/policy", map[string]any{"editorsCanShare": false, "maxSitesPerUser": 5, "siteMaxMB": 0}); r.Status != 400 {
		t.Fatalf("bad siteMaxMB: %d %s", r.Status, r.Body)
	}
	p := admin.Must("GET", "/api/v1/admin/policy", nil)["policy"].(map[string]any)
	if p["editorsCanShare"] != true || p["maxSitesPerUser"].(float64) != 500 {
		t.Errorf("rejected update was partly saved: %v", p)
	}
}

// The documented alias BUNKER_ORG_WIDE_SHARING=false showed as "off" on the
// policy page but owners could still share with everyone.
func TestOrgWideSharingEnvAliasIsEnforced(t *testing.T) {
	t.Setenv("BUNKER_ORG_WIDE_SHARING", "false")
	k := testkit.New(t)
	ada := k.As("ada@corp.com", "")
	ada.Must("POST", "/api/v1/drop", map[string]any{"title": "Team", "files": map[string]string{"index.html": "x"}})
	if r := ada.Do("PATCH", "/api/v1/sites/team", map[string]any{"orgView": true}); r.Status != 403 {
		t.Errorf("org-wide sharing allowed despite BUNKER_ORG_WIDE_SHARING=false: %d", r.Status)
	}
	if me := ada.Must("GET", "/api/v1/me", nil)["policy"].(map[string]any); me["orgWideSharing"] != false {
		t.Errorf("me.policy: %v", me)
	}
}
