package vendorlib_test

import (
	"strings"
	"testing"

	"github.com/kucukkanat/bunker/internal/testkit"
	"github.com/kucukkanat/bunker/internal/vendorlib"
)

func TestResolve(t *testing.T) {
	cases := map[string]string{
		"https://cdn.jsdelivr.net/npm/chart.js":                                   "/_/lib/chart.js/chart.umd.min.js",
		"https://cdn.jsdelivr.net/npm/chart.js@4.4.0/dist/chart.umd.min.js":       "/_/lib/chart.js/chart.umd.min.js",
		"https://cdn.tailwindcss.com?plugins=forms":                               "/_/lib/tailwindcss/browser.js",
		"https://unpkg.com/react@18/umd/react.production.min.js":                  "/_/lib/react/react.production.min.js",
		"https://unpkg.com/react-dom@18/umd/react-dom.development.js":             "/_/lib/react-dom/react-dom.production.min.js",
		"https://cdnjs.cloudflare.com/ajax/libs/Chart.js/4.4.1/chart.umd.js":      "/_/lib/chart.js/chart.umd.min.js",
		"https://cdn.jsdelivr.net/npm/bootstrap@5.3.2/dist/css/bootstrap.min.css": "/_/lib/bootstrap/bootstrap.min.css",
		"//code.jquery.com/jquery-3.7.1.min.js":                                   "/_/lib/jquery/jquery.min.js",
		"https://cdn.jsdelivr.net/npm/@tailwindcss/browser@4":                     "/_/lib/tailwindcss/browser.js",
		"https://unpkg.com/leaflet@1.9.4/dist/leaflet.css":                        "/_/lib/leaflet/leaflet.css",
		"https://cdn.jsdelivr.net/npm/leaflet@1/dist/leaflet.js":                  "/_/lib/leaflet/leaflet.js",
		"https://example.com/x.js":                                                "",
		"https://cdn.jsdelivr.net/npm/left-pad":                                   "",
	}
	for in, want := range cases {
		if got := vendorlib.Resolve(in); got != want {
			t.Errorf("%s → %q want %q", in, got, want)
		}
	}
}

func TestDropRewritesCDN(t *testing.T) {
	k := testkit.New(t)
	ada := k.As("ada@corp.com", "")
	html := `<html><head><link rel="preconnect" href="https://fonts.googleapis.com"><link href="https://fonts.googleapis.com/css2?family=Inter" rel="stylesheet">
<script src="https://cdn.tailwindcss.com"></script><script src="https://cdn.jsdelivr.net/npm/chart.js@4" integrity="sha384-abc" crossorigin="anonymous"></script>
<script src="https://evil.example/track.js"></script></head><body></body></html>`
	res := ada.Must("POST", "/api/v1/drop", map[string]any{"title": "cdn", "files": map[string]string{"index.html": html}})
	if w, _ := res["warnings"].([]any); len(w) != 1 || !strings.Contains(w[0].(string), "evil.example") {
		t.Errorf("warnings = %v", res["warnings"])
	}
	got := string(ada.Do("GET", "/api/v1/sites/cdn/files/index.html", nil).Body)
	if !strings.Contains(got, `src="/_/lib/tailwindcss/browser.js"`) || !strings.Contains(got, `src="/_/lib/chart.js/chart.umd.min.js"></script>`) || strings.Contains(got, "fonts.googleapis") || strings.Contains(got, "integrity") {
		t.Errorf("not rewritten: %s", got)
	}
	r := ada.Do("GET", "/_/lib/chart.js/chart.umd.min.js", nil, "Accept-Encoding", "br")
	if r.Status != 200 || r.Header.Get("Content-Encoding") != "br" {
		t.Errorf("lib serve: %d %v", r.Status, r.Header)
	}
}
