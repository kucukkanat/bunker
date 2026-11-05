package auth_test

import (
	"testing"

	"github.com/kucukkanat/bunker/internal/auth"
)

// Codes from a platform's secret generator (Railway, Render, Heroku) are
// longer than the built-in XXXX-XXXX code; the setup form must still match.
func TestSetupCodeMatchesEnvCodes(t *testing.T) {
	cases := []struct {
		env, typed string
		ok         bool
	}{
		{"qs0cmv2sogxh83n5dvb7jb5m", "qs0cmv2sogxh83n5dvb7jb5m", true},
		{"qs0cmv2sogxh83n5dvb7jb5m", "QS0CMV2SOGXH83N5DVB7JB5M", true},
		{"qs0cmv2sogxh83n5dvb7jb5m", "  qs0cmv2sogxh83n5dvb7jb5m\n", true},
		{"qs0cmv2sogxh83n5dvb7jb5m", "QS0C-MV2S", false},
		{"BCDF-GHJK", "bcdfghjk", true},
		{"BCDF-GHJK", "BCDF-GHJK", true},
		{"BCDF-GHJK", "BCDF-GHJL", false},
		{"change-me", "CHAN-GEME", true},
		{"a+b/c=", "ABC", true},
		{"---", "---", true},
		{"---", "", false},
		{"BCDF-GHJK", "", false},
	}
	for _, c := range cases {
		t.Setenv("BUNKER_SETUP_CODE", c.env)
		if got := auth.SetupCodeMatches(c.typed); got != c.ok {
			t.Errorf("code %q, typed %q: got %v, want %v", c.env, c.typed, got, c.ok)
		}
	}
}
