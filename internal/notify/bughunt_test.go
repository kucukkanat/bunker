package notify_test

import (
	"fmt"
	"testing"

	"github.com/kucukkanat/bunker/internal/testkit"
)

// access.request documents a role (viewer|editor) but always asked for
// viewer access, and people who could already view couldn't ask to edit.
func TestViewerCanAskToEdit(t *testing.T) {
	k := testkit.New(t)
	ada := k.As("ada@corp.com", "")
	ada.Must("POST", "/api/v1/drop", map[string]any{"title": "Roster", "files": map[string]string{"index.html": "x"}})
	ada.Must("POST", "/api/v1/sites/roster/members", map[string]any{"type": "user", "id": "bob@corp.com", "role": "viewer"})
	bob := k.As("bob@corp.com", "")
	bob.Must("POST", "/api/v1/sites/roster/access-requests", map[string]any{"message": "I keep it up to date", "role": "editor"})
	reqs := ada.Must("GET", "/api/v1/sites/roster/access-requests", nil)["requests"].([]any)
	if len(reqs) != 1 {
		t.Fatalf("requests: %v", reqs)
	}
	rq := reqs[0].(map[string]any)
	if rq["role"] != "editor" {
		t.Errorf("requested role = %v", rq["role"])
	}
	ada.Must("POST", fmt.Sprintf("/api/v1/sites/roster/access-requests/%d", int(rq["id"].(float64))), map[string]any{"approve": true})
	if r := bob.Must("GET", "/api/v1/sites/roster", nil); r["role"] != "editor" {
		t.Errorf("bob's role after approval: %v", r["role"])
	}
}
