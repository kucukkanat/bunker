package admin

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/kucukkanat/bunker/internal/core"
)

// Update check: only when an admin asks (opening Admin), at most every
// 12 hours, and never when BUNKER_UPDATE_CHECK=false.
var (
	verMu      sync.Mutex
	verLatest  string
	verURL     string
	verChecked time.Time
)

// LatestReleaseURL is overridable in tests.
var LatestReleaseURL = "https://api.github.com/repos/kucukkanat/bunker/releases/latest"

func latestRelease(ctx context.Context) (string, string) {
	verMu.Lock()
	defer verMu.Unlock()
	if time.Since(verChecked) < 12*time.Hour {
		return verLatest, verURL
	}
	verChecked = time.Now()
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, "GET", LatestReleaseURL, nil)
	req.Header.Set("Accept", "application/vnd.github+json")
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		return verLatest, verURL
	}
	defer res.Body.Close()
	var body struct {
		Tag string `json:"tag_name"`
		URL string `json:"html_url"`
	}
	if res.StatusCode == 200 && json.NewDecoder(res.Body).Decode(&body) == nil {
		verLatest, verURL = body.Tag, body.URL
	}
	return verLatest, verURL
}

func registerVersion(a *core.App) {
	a.Handle(core.Op{Name: "admin.version", Method: "GET", Path: "/api/v1/admin/version", Summary: "This server's version and whether a newer release exists", Access: core.AccessAdmin,
		Fn: func(r *http.Request) (any, error) {
			out := map[string]any{"current": core.Version, "checks": core.EnvBool("UPDATE_CHECK", true)}
			if !core.EnvBool("UPDATE_CHECK", true) {
				return out, nil
			}
			latest, url := latestRelease(r.Context())
			out["latest"], out["url"] = latest, url
			out["updateAvailable"] = latest != "" && core.Version != "dev" && core.Version != "source" && newer(latest, core.Version)
			out["how"] = "Linux installer: run the install command again (or `bunker update`). Docker: pull the new image and recreate the container. Render/Railway/Fly: redeploy."
			return out, nil
		}})
}

// newer compares vX.Y.Z strings.
func newer(a, b string) bool {
	pa, pb := parts(a), parts(b)
	for i := 0; i < 3; i++ {
		if pa[i] != pb[i] {
			return pa[i] > pb[i]
		}
	}
	return false
}

func parts(v string) [3]int {
	v = strings.TrimPrefix(strings.SplitN(v, "-", 2)[0], "v")
	var out [3]int
	for i, p := range strings.SplitN(v, ".", 3) {
		n := 0
		for _, c := range p {
			if c < '0' || c > '9' {
				break
			}
			n = n*10 + int(c-'0')
		}
		out[i] = n
	}
	return out
}
