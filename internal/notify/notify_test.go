package notify_test

import (
	"testing"

	_ "github.com/kucukkanat/bunker/internal/notify"
	"github.com/kucukkanat/bunker/internal/testkit"
)

func TestAccessRequestFlow(t *testing.T) {
	k := testkit.New(t)
	ada := k.As("ada@corp.com", "")
	ada.Must("POST", "/api/v1/drop", map[string]any{"title": "Payroll", "files": map[string]string{"index.html": "secret"}})
	bob := k.As("bob@corp.com", "")
	bob.Must("POST", "/api/v1/sites/payroll/access-requests", map[string]any{"message": "please"})
	bob.Must("POST", "/api/v1/sites/nope/access-requests", map[string]any{}) // no enumeration
	n := ada.Must("GET", "/api/v1/notifications", nil)
	if n["unread"].(float64) != 1 {
		t.Fatalf("ada notifications: %v", n)
	}
	reqs := ada.Must("GET", "/api/v1/sites/payroll/access-requests", nil)["requests"].([]any)
	if len(reqs) != 1 {
		t.Fatal(reqs)
	}
	id := int(reqs[0].(map[string]any)["id"].(float64))
	ada.Must("POST", "/api/v1/sites/payroll/access-requests/"+string(rune('0'+id)), map[string]any{"approve": true})
	if r := bob.Do("GET", "/api/v1/sites/payroll", nil); r.Status != 200 {
		t.Fatalf("bob still locked out: %d", r.Status)
	}
	bn := bob.Must("GET", "/api/v1/notifications", nil)
	if bn["unread"].(float64) != 1 {
		t.Errorf("bob not notified: %v", bn)
	}
	st := ada.Must("GET", "/api/v1/sites/payroll/stats", nil)
	if st["days"].(float64) != 30 {
		t.Error(st)
	}
}

func TestPageNotify(t *testing.T) {
	k := testkit.New(t)
	ada := k.As("ada@corp.com", "")
	ada.Must("POST", "/api/v1/drop", map[string]any{"title": "Expenses", "files": map[string]string{"index.html": "x"}})
	k.As("fin@corp.com", "Finance") // signs in once so the group is known
	k.As("eve@corp.com", "Finance")
	ada.Must("POST", "/api/v1/sites/expenses/members", map[string]any{"type": "user", "id": "fin@corp.com", "role": "viewer"})
	page := k.As("ada@corp.com", "")
	page.Page = true
	res := page.Must("POST", "/api/v1/sites/expenses/notify", map[string]any{"to": []string{"Finance"}, "title": "New request", "path": "/r/1"})
	if res["sent"].(float64) != 1 { // eve is in Finance but has no access
		t.Fatalf("sent = %v", res)
	}
	n := k.As("fin@corp.com", "Finance").Must("GET", "/api/v1/notifications", nil)["notifications"].([]any)
	found := false
	for _, x := range n {
		if x.(map[string]any)["link"] == "/s/expenses/r/1" {
			found = true
		}
	}
	if !found {
		t.Errorf("notification missing: %v", n)
	}
}
