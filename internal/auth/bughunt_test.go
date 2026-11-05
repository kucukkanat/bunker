package auth_test

import (
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/kucukkanat/bunker/internal/core"
	"github.com/kucukkanat/bunker/internal/testkit"
)

// The admin people search filtered after LIMIT 500, so with more than 500
// people only the most recently seen could ever be found.
func TestUserSearchFindsPeopleBeyondFirst500(t *testing.T) {
	k := testkit.New(t)
	admin := k.As("", "")
	admin.Must("GET", "/api/v1/me", nil)
	tx, _ := k.App.DB.Begin()
	tx.Exec(`INSERT INTO users(subject,email,name,created_at,last_seen_at) VALUES('oidc:old','zelda@corp.com','Zelda Old',1,1)`)
	for i := 0; i < 600; i++ {
		tx.Exec(`INSERT INTO users(subject,email,name,created_at,last_seen_at) VALUES(?,?,?,?,?)`, "oidc:"+strings.Repeat("x", 1)+string(rune('a'+i%26))+core.RandomToken(4), core.RandomToken(4)+"@corp.com", "Someone", 100, 100+i)
	}
	tx.Commit()
	got := admin.Must("GET", "/api/v1/admin/users?q=zelda", nil)["users"].([]any)
	if len(got) != 1 {
		t.Fatalf("search for zelda found %d people", len(got))
	}
	if n := len(admin.Must("GET", "/api/v1/admin/users?q=50%25", nil)["users"].([]any)); n != 0 {
		t.Errorf("%% in the search box matched %d people", n)
	}
	if r := admin.Do("PATCH", "/api/v1/admin/users/999999", map[string]any{"isAdmin": true}); r.Status != 404 {
		t.Errorf("updating a missing person: %d", r.Status)
	}
}

// An office behind one NAT address: successful sign-ins must not count
// toward the per-IP lockout (only failed guesses should). The request goes
// straight to the mux so the server-wide request limiter isn't involved.
func TestSuccessfulLoginsDontLockOutSharedIP(t *testing.T) {
	k := testkit.NewProd(t)
	anon := k.As("", "")
	anon.Must("POST", "/api/v1/setup", map[string]any{"provider": "local", "email": "me@home.dev", "name": "Me", "password": "correct horse battery"})
	for i := 0; i < 15; i++ {
		v := url.Values{"email": {"me@home.dev"}, "password": {"correct horse battery"}}
		req := httptest.NewRequest("POST", "/auth/password", strings.NewReader(v.Encode()))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		req = req.WithContext(core.WithIP(req.Context(), "203.0.113.7"))
		w := httptest.NewRecorder()
		k.App.Mux.ServeHTTP(w, req)
		if w.Code != 303 {
			t.Fatalf("sign-in %d from the same address was refused (%d)", i+1, w.Code)
		}
	}
}
