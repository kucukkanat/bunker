package auth

import "testing"

// Entra only sends acct when it's configured as an optional claim, so a
// guest (B2B) account must also be recognized from idp, which names the
// home identity provider when it isn't this tenant.
func TestEntraGuestDetection(t *testing.T) {
	const tid = "11111111-2222-3333-4444-555555555555"
	one, zero := 1, 0
	cases := []struct {
		acct *int
		idp  string
		want bool
	}{
		{nil, "", false},
		{nil, "https://sts.windows.net/" + tid + "/", false},
		{nil, "https://sts.windows.net/99999999-2222-3333-4444-555555555555/", true},
		{nil, "live.com", true},
		{&one, "", true},
		{&zero, "", false},
		{&zero, "live.com", true},
	}
	for _, c := range cases {
		if got := entraGuest(c.acct, c.idp, tid); got != c.want {
			t.Errorf("entraGuest(%v, %q) = %v, want %v", c.acct, c.idp, got, c.want)
		}
	}
}
