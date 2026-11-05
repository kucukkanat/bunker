package hooks_test

import (
	"testing"

	"github.com/kucukkanat/bunker/internal/testkit"
)

// A hook for a collection bunker.db can't hold was created fine, and then
// every call to it failed.
func TestHookCollectionNameValidated(t *testing.T) {
	k := testkit.New(t)
	ada := k.As("ada@corp.com", "")
	ada.Must("POST", "/api/v1/drop", map[string]any{"title": "Forms", "files": map[string]string{"index.html": "x"}})
	if r := ada.Do("POST", "/api/v1/sites/forms/hooks", map[string]any{"name": "Zapier", "collection": "new hires"}); r.Status != 400 {
		t.Errorf("invalid collection accepted: %d %s", r.Status, r.Body)
	}
}
