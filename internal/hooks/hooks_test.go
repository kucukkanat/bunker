package hooks_test

import (
	"net/http"
	"strings"
	"testing"

	_ "github.com/kucukkanat/bunker/internal/data"
	_ "github.com/kucukkanat/bunker/internal/hooks"
	"github.com/kucukkanat/bunker/internal/testkit"
)

func TestInboundWebhook(t *testing.T) {
	t.Setenv("BUNKER_ADMINS", "")
	k := testkit.New(t)
	ada := k.As("ada@corp.com", "")
	ada.Must("POST", "/api/v1/drop", map[string]any{"title": "Hires", "files": map[string]string{"index.html": "x"}})
	h := ada.Must("POST", "/api/v1/sites/hires/hooks", map[string]any{"name": "Power Automate", "collection": "people"})
	u := h["url"].(string)
	post := func(ct, body string) int {
		res, err := http.Post(u, ct, strings.NewReader(body))
		if err != nil {
			t.Fatal(err)
		}
		res.Body.Close()
		return res.StatusCode
	}
	if c := post("application/json", `{"name":"Grace","_author":"forged"}`); c != 201 {
		t.Fatalf("post: %d", c)
	}
	if c := post("application/json", `[{"name":"Linus"},{"name":"Ken"}]`); c != 201 {
		t.Fatalf("batch: %d", c)
	}
	if c := post("application/x-www-form-urlencoded", "name=Barbara"); c != 201 {
		t.Fatalf("form: %d", c)
	}
	if c := post("application/json", `nope`); c != 400 {
		t.Errorf("bad json: %d", c)
	}
	list := ada.Must("GET", "/api/v1/sites/hires/db/people?limit=10", nil)
	items, _ := list["items"].([]any)
	if items == nil {
		items, _ = list["docs"].([]any)
	}
	if len(items) != 4 {
		t.Fatalf("docs = %v", list)
	}
	for _, it := range items {
		if it.(map[string]any)["_author"] == "forged" {
			t.Error("system field forged")
		}
	}
	// Viewers can't manage hooks; deleting stops the URL.
	bob := k.As("bob@corp.com", "")
	if r := bob.Do("GET", "/api/v1/sites/hires/hooks", nil); r.Status != 404 {
		t.Errorf("bob sees hooks: %d", r.Status)
	}
	hs := ada.Must("GET", "/api/v1/sites/hires/hooks", nil)["hooks"].([]any)
	if hs[0].(map[string]any)["calls"].(float64) != 4 {
		t.Errorf("calls = %v", hs[0])
	}
	ada.Must("DELETE", "/api/v1/sites/hires/hooks/1", nil)
	if c := post("application/json", `{"x":1}`); c != 404 {
		t.Errorf("deleted hook works: %d", c)
	}
}
