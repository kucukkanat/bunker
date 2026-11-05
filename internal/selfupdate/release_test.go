package selfupdate

import "testing"

func TestNewer(t *testing.T) {
	for _, c := range []struct {
		a, b string
		want bool
	}{
		{"v0.2.0", "v0.1.9", true}, {"v0.1.0", "v0.1.0", false}, {"v1.0.0", "v0.9.9", true},
		{"v0.1.0", "v0.2.0-rc1", false}, {"v0.10.0", "v0.9.0", true},
		{"v0.2.0", "v0.2.0-rc.1", true}, {"v0.2.0-rc.1", "v0.2.0", false}, {"v0.2.0", "dev", true},
	} {
		if got := Newer(c.a, c.b); got != c.want {
			t.Errorf("Newer(%s,%s)=%v", c.a, c.b, got)
		}
	}
}

func TestReleased(t *testing.T) {
	for v, want := range map[string]bool{"v0.1.0": true, "0.2.3": true, "v1.0.0-rc.1": true, "dev": false, "source": false, "": false, "v1.2": false} {
		if Released(v) != want {
			t.Errorf("Released(%q) = %v", v, !want)
		}
	}
}
