package auth

import (
	"net/http/httptest"
	"testing"
)

// Emails need full links even when the server doesn't know its own address
// yet (reached by IP, no BUNKER_URL): use the address the admin is using.
func TestAbsLink(t *testing.T) {
	r := httptest.NewRequest("POST", "/api/v1/admin/invites", nil)
	r.Host = "192.168.1.20:8080"
	if got := absLink(r, "/auth/invite?token=x"); got != "http://192.168.1.20:8080/auth/invite?token=x" {
		t.Errorf("relative: %s", got)
	}
	if got := absLink(r, "https://bunker.example.com/auth/invite?token=x"); got != "https://bunker.example.com/auth/invite?token=x" {
		t.Errorf("absolute changed: %s", got)
	}
	r.Host = "evil.example/@x"
	if got := absLink(r, "/auth/invite"); got != "/auth/invite" {
		t.Errorf("odd host used: %s", got)
	}
}
