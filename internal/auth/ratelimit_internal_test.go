package auth

import (
	"strconv"
	"testing"
	"time"
)

// Filling the attempts table with throwaway keys (one per spoofed IP)
// must not wipe the per-email lockout of the account under attack.
func TestLoginLockoutSurvivesKeyFlood(t *testing.T) {
	attemptMu.Lock()
	attempts = map[string][]time.Time{}
	attemptMu.Unlock()
	for i := 0; i < 11; i++ {
		tooMany("ip:victim-guess", "email:boss@corp.com")
	}
	for i := 0; i < 100005; i++ {
		tooMany("ip:198.51." + strconv.Itoa(i))
	}
	if !tooMany("ip:203.0.113.1", "email:boss@corp.com") {
		t.Fatal("per-email lockout was reset by a flood of other keys")
	}
}
