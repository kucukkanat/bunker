package vendorlib_test

import (
	"net/http"
	"strings"
	"testing"

	"github.com/kucukkanat/bunker/internal/testkit"
)

func TestLeafletAssets(t *testing.T) {
	k := testkit.New(t)
	for p, ct := range map[string]string{
		"/_/lib/leaflet/leaflet.js":             "text/javascript",
		"/_/lib/leaflet/leaflet.css":            "text/css",
		"/_/lib/leaflet/images/marker-icon.png": "image/png",
	} {
		res, err := http.Get(k.Srv.URL + p) // no session: sandboxed frames send no cookies
		if err != nil {
			t.Fatal(err)
		}
		res.Body.Close()
		if res.StatusCode != 200 || !strings.HasPrefix(res.Header.Get("Content-Type"), ct) {
			t.Errorf("%s: %d %s", p, res.StatusCode, res.Header.Get("Content-Type"))
		}
	}
	if res, err := http.Get(k.Srv.URL + "/_/lib/leaflet/images/nope.png"); err != nil || res.StatusCode != 404 {
		t.Errorf("missing asset should 404: %v %v", err, res)
	}
}
