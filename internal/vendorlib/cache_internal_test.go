package vendorlib

import (
	"strconv"
	"testing"
)

// /_/lib is public; every unknown path used to be remembered forever, so
// anonymous requests for made-up names grew memory without bound.
// Traversal attempts must miss too (embed.FS refuses "..").
func TestUnknownLibFilesNotCached(t *testing.T) {
	cacheMu.Lock()
	n0 := len(cache)
	cacheMu.Unlock()
	for i := 0; i < 1000; i++ {
		if get("nope", "x"+strconv.Itoa(i)+".js") != nil {
			t.Fatal("unexpected asset")
		}
	}
	for _, p := range []string{"../vendorlib.go", "../../go.mod", "./../x"} {
		if get("leaflet", p) != nil {
			t.Fatalf("traversal served %s", p)
		}
	}
	cacheMu.Lock()
	defer cacheMu.Unlock()
	if len(cache)-n0 > 10 {
		t.Fatalf("cache grew by %d entries for missing files", len(cache)-n0)
	}
}
