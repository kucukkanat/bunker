package functions

import (
	"testing"
	"time"
)

func TestCron(t *testing.T) {
	at := time.Date(2026, 11, 5, 10, 7, 30, 0, time.UTC) // a Thursday
	cases := []struct{ spec, want string }{
		{"* * * * *", "2026-11-05T10:08:00Z"},
		{"*/15 * * * *", "2026-11-05T10:15:00Z"},
		{"0 9 * * *", "2026-11-06T09:00:00Z"},
		{"30 8 * * 1-5", "2026-11-06T08:30:00Z"},
		{"0 0 1 * *", "2026-12-01T00:00:00Z"},
		{"@hourly", "2026-11-05T11:00:00Z"},
		{"0 12 * * 0", "2026-11-08T12:00:00Z"},
		{"0 12 * * 7", "2026-11-08T12:00:00Z"},
		{"0 0 13 * 5", "2026-11-06T00:00:00Z"}, // either day field may match
		{"0 0 29 2 *", "2028-02-29T00:00:00Z"},
	}
	for _, c := range cases {
		cr, err := parseCron(c.spec)
		if err != nil {
			t.Fatalf("%s: %v", c.spec, err)
		}
		if got := cr.next(at).Format(time.RFC3339); got != c.want {
			t.Errorf("%s: next = %s, want %s", c.spec, got, c.want)
		}
	}
	for _, bad := range []string{"", "* * * *", "60 * * * *", "* 24 * * *", "*/0 * * * *", "a * * * *", "5-1 * * * *"} {
		if _, err := parseCron(bad); err == nil {
			t.Errorf("%q accepted", bad)
		}
	}
}

func TestNames(t *testing.T) {
	for p, want := range map[string]string{"api/orders.js": "orders", "api/orders/refund.ts": "orders/refund", "api/x.mjs": "x"} {
		if !isCode(p) || isModule(p) || nameOf(p) != want {
			t.Errorf("%s → %s", p, nameOf(p))
		}
	}
	for _, p := range []string{"api/_lib.js", "api/_shared/db.ts"} {
		if !isModule(p) {
			t.Errorf("%s should be a module", p)
		}
	}
	for _, p := range []string{"api/data.json", "src/api/x.js", "api/types.d.ts", "apix/a.js"} {
		if isCode(p) {
			t.Errorf("%s isn't server code", p)
		}
	}
}
