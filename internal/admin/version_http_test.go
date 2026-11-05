package admin_test

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/kucukkanat/bunker/internal/admin"
	"github.com/kucukkanat/bunker/internal/core"
	"github.com/kucukkanat/bunker/internal/testkit"
)

func TestUpdateNotice(t *testing.T) {
	gh := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"tag_name":"v9.9.9","html_url":"https://github.com/kucukkanat/bunker/releases/tag/v9.9.9"}`))
	}))
	defer gh.Close()
	old, oldV := admin.LatestReleaseURL, core.Version
	admin.LatestReleaseURL, core.Version = gh.URL, "v0.1.0"
	defer func() { admin.LatestReleaseURL, core.Version = old, oldV }()
	k := testkit.New(t)
	v := k.As("", "").Must("GET", "/api/v1/admin/version", nil)
	if v["updateAvailable"] != true || v["latest"] != "v9.9.9" {
		t.Fatalf("version: %v", v)
	}
	t.Setenv("BUNKER_UPDATE_CHECK", "false")
	if v := k.As("", "").Must("GET", "/api/v1/admin/version", nil); v["latest"] != nil {
		t.Errorf("checked GitHub although disabled: %v", v)
	}
}
