package sites_test

import (
	"archive/zip"
	"bytes"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/kucukkanat/bunker/internal/sites"
	"github.com/kucukkanat/bunker/internal/testkit"
)

func TestGitHubImport(t *testing.T) {
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for name, body := range map[string]string{"demo-main/README.md": "# hi", "demo-main/web/index.html": "<title>Demo Web</title><h1>From GitHub</h1>", "demo-main/web/app.js": "1"} {
		w, _ := zw.Create(name)
		w.Write([]byte(body))
	}
	zw.Close()
	fake := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/acme/demo/zip/main" {
			http.NotFound(w, r)
			return
		}
		w.Write(buf.Bytes())
	}))
	defer fake.Close()
	old := sites.CodeloadBase
	sites.CodeloadBase = fake.URL
	defer func() { sites.CodeloadBase = old }()

	for in, want := range map[string][4]string{
		"https://github.com/acme/demo":                     {"acme", "demo", "", ""},
		"https://github.com/acme/demo.git":                 {"acme", "demo", "", ""},
		"https://github.com/acme/demo/tree/main/web/site/": {"acme", "demo", "main", "web/site"},
	} {
		o, r, ref, d, ok := sites.GitHubSource(in)
		if !ok || [4]string{o, r, ref, d} != want {
			t.Errorf("%s → %v %v", in, [4]string{o, r, ref, d}, ok)
		}
	}
	if _, _, _, _, ok := sites.GitHubSource("https://evil.example/acme/demo"); ok {
		t.Error("non-GitHub URL accepted")
	}
	k := testkit.New(t)
	ada := k.As("ada@corp.com", "")
	res := ada.Must("POST", "/api/v1/import/github", map[string]any{"url": "https://github.com/acme/demo/tree/main/web"})
	if res["slug"] != "web" {
		t.Fatalf("import: %v", res)
	}
	got := string(ada.Do("GET", "/api/v1/sites/web/files/index.html", nil).Body)
	if !strings.Contains(got, "From GitHub") {
		t.Errorf("content: %s", got)
	}
	if r := ada.Do("POST", "/api/v1/import/github", map[string]any{"url": "https://github.com/acme/missing"}); r.Status != 404 {
		t.Errorf("missing repo: %d", r.Status)
	}
}
