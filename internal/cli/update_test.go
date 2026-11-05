package cli

import (
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
)

func TestUpdateRemote(t *testing.T) {
	var installed, health atomic.Int32
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.Method + " " + r.URL.Path {
		case "POST /api/v1/admin/update/check":
			w.Write([]byte(`{"current":"v0.1.0","latest":"v0.2.0","updateAvailable":true,"canUpdate":true,"job":{"state":"idle"}}`))
		case "POST /api/v1/admin/update":
			installed.Add(1)
			w.Write([]byte(`{"job":{"state":"downloading","version":"v0.2.0"}}`))
		case "GET /api/v1/admin/update":
			w.Write([]byte(`{"current":"v0.1.0","job":{"state":"restarting","version":"v0.2.0"}}`))
		case "GET /healthz":
			if health.Add(1) < 2 {
				w.Write([]byte(`{"ok":true,"version":"v0.1.0"}`))
			} else {
				w.Write([]byte(`{"ok":true,"version":"v0.2.0"}`))
			}
		default:
			w.WriteHeader(404)
		}
	}))
	defer ts.Close()
	env := map[string]string{"BUNKER_CONFIG": filepath.Join(t.TempDir(), "c.json"), "BUNKER_SERVER": ts.URL, "BUNKER_TOKEN": "bk_test"}

	out, e, code := run(t, env, "", "update", "--remote", "--check")
	if code != 0 || !strings.Contains(out+e, "v0.2.0 is available") || installed.Load() != 0 {
		t.Fatalf("check: %d %s %s", code, out, e)
	}
	out, e, code = run(t, env, "", "update", "--remote")
	if code != 0 || !strings.Contains(out+e, "runs v0.2.0 now") || installed.Load() != 1 {
		t.Fatalf("update: %d %s %s", code, out, e)
	}
}
