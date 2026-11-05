package auth

import "testing"

// The invite email used to promise "expires in 7 days" whatever lifetime
// the admin picked.
func TestInviteDaysWording(t *testing.T) {
	for in, want := range map[int]string{0: "7 days", 1: "1 day", 14: "14 days", 31: "7 days"} {
		if got := inviteDays(in); got != want {
			t.Errorf("inviteDays(%d) = %q, want %q", in, got, want)
		}
	}
}
