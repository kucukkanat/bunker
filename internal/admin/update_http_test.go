//go:build !windows

package admin_test

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/kucukkanat/bunker/internal/admin"
	"github.com/kucukkanat/bunker/internal/core"
	"github.com/kucukkanat/bunker/internal/selfupdate"
	"github.com/kucukkanat/bunker/internal/testkit"
)

// fakeGitHub serves one release whose binary is a script reporting tag.
func fakeGitHub(t *testing.T, tag string, badSum bool) *httptest.Server {
	bin := []byte("#!/bin/sh\necho bunker " + tag + "\n")
	name := "bunker-" + runtime.GOOS + "-" + runtime.GOARCH
	sum := sha256.Sum256(bin)
	sums := hex.EncodeToString(sum[:]) + "  " + name + "\n"
	if badSum {
		sums = strings.Repeat("0", 64) + "  " + name + "\n"
	}
	var srv *httptest.Server
	srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/repos/kucukkanat/bunker/releases/latest", "/repos/kucukkanat/bunker/releases/tags/" + tag:
			fmt.Fprintf(w, `{"tag_name":%q,"html_url":"https://example.com/%s","body":"## New\n- things","published_at":"2026-11-01T10:00:00Z","assets":[{"name":%q,"browser_download_url":"%s/dl/bin","size":%d},{"name":"checksums.txt","browser_download_url":"%s/dl/sums"}]}`,
				tag, tag, name, srv.URL, len(bin), srv.URL)
		case "/dl/bin":
			w.Write(bin)
		case "/dl/sums":
			w.Write([]byte(sums))
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)
	return srv
}

func useRelease(t *testing.T, gh *httptest.Server, version string) {
	old, oldV := admin.LatestReleaseURL, core.Version
	admin.LatestReleaseURL, core.Version = gh.URL, version
	t.Cleanup(func() { admin.LatestReleaseURL, core.Version = old, oldV })
}

func waitJob(t *testing.T, c *testkit.Client, want string) map[string]any {
	for i := 0; i < 200; i++ {
		s := c.Must("GET", "/api/v1/admin/update", nil)
		j := s["job"].(map[string]any)
		if j["state"] == want {
			return s
		}
		if j["state"] == "failed" && want != "failed" {
			t.Fatalf("update failed: %v", j["error"])
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("job never reached %s", want)
	return nil
}

func TestSelfUpdate(t *testing.T) {
	gh := fakeGitHub(t, "v9.9.9", false)
	useRelease(t, gh, "v0.1.0")
	restarts, undo := admin.StubRestart()
	defer undo()
	k := testkit.New(t)
	ad := k.As("", "")

	s := ad.Must("POST", "/api/v1/admin/update/check", nil)
	if s["updateAvailable"] != true || s["canUpdate"] != true || s["latest"] != "v9.9.9" {
		t.Fatalf("status: %v", s)
	}
	if rel := s["release"].(map[string]any); !strings.Contains(rel["notes"].(string), "things") {
		t.Errorf("release notes missing: %v", rel)
	}
	if k.As("bob@corp.com", "").Do("POST", "/api/v1/admin/update", nil).Status != 403 {
		t.Error("non-admin could update")
	}

	ad.Must("POST", "/api/v1/admin/update", map[string]any{})
	waitJob(t, ad, "restarting")
	select {
	case <-restarts:
	case <-time.After(2 * time.Second):
		t.Fatal("no restart requested")
	}
	st := selfupdate.ReadState(k.App.Cfg.DataDir)
	if st == nil || st.Current.Version != "v9.9.9" || st.InstalledBy == "" {
		t.Fatalf("not staged: %+v", st)
	}
	dbs, _ := filepath.Glob(filepath.Join(k.App.Cfg.DataDir, "updates", "before-0.1.0-*.db"))
	if len(dbs) != 1 {
		t.Errorf("database copy: %v", dbs)
	}
	var n int
	k.App.DB.QueryRow(`SELECT COUNT(*) FROM audit WHERE event IN ('admin.update.started','admin.update.installed')`).Scan(&n)
	if n != 2 {
		t.Errorf("audit events = %d", n)
	}
	// Running the installed binary: nothing to roll back to.
	if c := ad.Do("POST", "/api/v1/admin/update/rollback", nil).Status; c != 409 {
		t.Errorf("rollback = %d", c)
	}
	// Same version again is refused.
	core.Version = "v9.9.9"
	if c := ad.Do("POST", "/api/v1/admin/update", map[string]any{"version": "v9.9.9"}).Status; c != 409 {
		t.Errorf("reinstall same version = %d", c)
	}
}

func TestSelfUpdateBadChecksum(t *testing.T) {
	gh := fakeGitHub(t, "v9.9.9", true)
	useRelease(t, gh, "v0.1.0")
	_, undo := admin.StubRestart()
	defer undo()
	k := testkit.New(t)
	ad := k.As("", "")
	ad.Must("POST", "/api/v1/admin/update", nil)
	s := waitJob(t, ad, "failed")
	if e := s["job"].(map[string]any)["error"].(string); !strings.Contains(e, "checksum") {
		t.Errorf("error: %s", e)
	}
	if selfupdate.ReadState(k.App.Cfg.DataDir) != nil {
		t.Error("staged a binary with a bad checksum")
	}
	if _, err := os.Stat(filepath.Join(k.App.Cfg.DataDir, "bin", "bunker-v9.9.9")); !os.IsNotExist(err) {
		t.Error("binary written")
	}
}

func TestSelfUpdateRefused(t *testing.T) {
	gh := fakeGitHub(t, "v9.9.9", false)
	useRelease(t, gh, "dev")
	_, undo := admin.StubRestart()
	defer undo()
	k := testkit.New(t)
	ad := k.As("", "")
	s := ad.Must("GET", "/api/v1/admin/update", nil)
	if s["canUpdate"] != false || !strings.Contains(s["reason"].(string), "development build") {
		t.Fatalf("dev build: %v", s)
	}
	if c := ad.Do("POST", "/api/v1/admin/update", nil).Status; c != 409 {
		t.Errorf("dev build update = %d", c)
	}
	core.Version = "v0.1.0"
	t.Setenv("BUNKER_UPDATE_SELF", "false")
	if c := ad.Do("POST", "/api/v1/admin/update", nil).Status; c != 409 {
		t.Errorf("BUNKER_UPDATE_SELF=false update = %d", c)
	}
}

func TestUpdateSettings(t *testing.T) {
	gh := fakeGitHub(t, "v9.9.9", false)
	useRelease(t, gh, "v0.1.0")
	k := testkit.New(t)
	ad := k.As("", "")
	ad.Must("PUT", "/api/v1/admin/update/settings", map[string]any{"auto": true, "hour": 5})
	a := ad.Must("GET", "/api/v1/admin/update", nil)["auto"].(map[string]any)
	if a["enabled"] != true || a["hour"] != float64(5) {
		t.Fatalf("auto: %v", a)
	}
	if c := ad.Do("PUT", "/api/v1/admin/update/settings", map[string]any{"hour": 24}).Status; c != 400 {
		t.Errorf("hour 24 = %d", c)
	}
	t.Setenv("BUNKER_UPDATE_AUTO", "false")
	if c := ad.Do("PUT", "/api/v1/admin/update/settings", map[string]any{"auto": true}).Status; c != 409 {
		t.Errorf("pinned auto = %d", c)
	}
}
