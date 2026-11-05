package cli

import (
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
)

// A rate-limited poll (429, e.g. many people signing in from one office
// address) ended `bunker login` with an error instead of slowing down.
func TestDeviceLoginSurvivesRateLimit(t *testing.T) {
	var polls atomic.Int32
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/healthz":
			w.Write([]byte(`{"ok":true}`))
		case "/api/v1/auth/device":
			w.Write([]byte(`{"deviceCode":"d","userCode":"ABCD-EFGH","expiresIn":60,"interval":1,"verificationUriComplete":"/device?code=ABCD-EFGH"}`))
		case "/api/v1/auth/device/token":
			if polls.Add(1) == 1 {
				w.WriteHeader(429)
				w.Write([]byte(`{"error":{"code":"quota_exceeded","message":"too many requests: slow down"}}`))
				return
			}
			w.Write([]byte(`{"token":"bk_test"}`))
		case "/api/v1/me":
			w.Write([]byte(`{"id":1,"email":"a@b.c","name":"A"}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer ts.Close()
	env := map[string]string{"BUNKER_CONFIG": filepath.Join(t.TempDir(), "c.json")}
	if _, e, code := run(t, env, "", "login", ts.URL, "--no-browser"); code != 0 {
		t.Fatalf("login failed after a 429: %s", e)
	}
}

func TestTitlesFromFilesAndFolders(t *testing.T) {
	if got := htmlTitle([]byte("<title>Tom &amp; Jerry</title>")); got != "Tom & Jerry" {
		t.Errorf("htmlTitle = %q", got)
	}
	if got := prettyName("équipe-paris"); got != "Équipe Paris" {
		t.Errorf("prettyName = %q", got)
	}
	if strings.ContainsRune(prettyName("über_app"), '�') {
		t.Error("prettyName broke a character")
	}
}
