package mcp

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"testing"

	"github.com/kucukkanat/bunker/internal/auth"
	"github.com/kucukkanat/bunker/internal/core"
)

// OAuth access tokens live an hour; the hourly token sweep deleted them
// once expired, and a refresh then read the missing row as "revoked in
// Settings": every MCP client had to reconnect an hour or two after it
// went idle.
func TestRefreshWorksAfterAccessTokenExpiredAndWasSwept(t *testing.T) {
	ts := newServer(t)
	resp, err := http.Get(ts.URL + "/api/v1/me") // dev mode: creates the admin
	if err != nil {
		t.Fatal(err)
	}
	var me struct{ ID int64 }
	json.NewDecoder(resp.Body).Decode(&me)
	resp.Body.Close()
	ctx := context.Background()
	out, err := issue(ctx, me.ID, "client-1", "Agent", "read,write", ts.URL+"/mcp", "")
	if err != nil {
		t.Fatal(err)
	}
	app.DB.Exec(`UPDATE tokens SET expires_at=? WHERE client='mcp:Agent'`, core.Now()-60)
	auth.SweepExpired(ctx)
	sweepOAuth(ctx)
	refresh := func(rt string) (int, map[string]any) {
		r, err := http.PostForm(ts.URL+"/oauth/token", url.Values{"grant_type": {"refresh_token"}, "refresh_token": {rt}, "client_id": {"client-1"}})
		if err != nil {
			t.Fatal(err)
		}
		defer r.Body.Close()
		var m map[string]any
		json.NewDecoder(r.Body).Decode(&m)
		return r.StatusCode, m
	}
	st, res := refresh(out["refresh_token"].(string))
	if st != 200 {
		t.Fatalf("refresh after the access token expired: %d %v", st, res)
	}
	// Revoking the app in Settings (deleting its token) still ends it.
	var tid int64
	app.DB.QueryRow(`SELECT id FROM tokens WHERE client='mcp:Agent'`).Scan(&tid)
	app.DB.Exec(`DELETE FROM tokens WHERE id=?`, tid)
	if st, _ := refresh(res["refresh_token"].(string)); st != 400 {
		t.Errorf("refresh after revocation: %d", st)
	}
}
