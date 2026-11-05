package admin

import "testing"

func TestNewer(t *testing.T) {
	for _, c := range []struct {
		a, b string
		want bool
	}{{"v0.2.0", "v0.1.9", true}, {"v0.1.0", "v0.1.0", false}, {"v1.0.0", "v0.9.9", true}, {"v0.1.0", "v0.2.0-rc1", false}, {"v0.10.0", "v0.9.0", true}} {
		if got := newer(c.a, c.b); got != c.want {
			t.Errorf("newer(%s,%s)=%v", c.a, c.b, got)
		}
	}
}
