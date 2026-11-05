package server

import "testing"

func TestParseBytes(t *testing.T) {
	for in, want := range map[string]int64{"400MiB": 400 << 20, "1GB": 1e9, "1 GiB": 1 << 30, "512m": 512 << 20, "1048576": 1 << 20, "10KB": 10e3} {
		if got, ok := parseBytes(in); !ok || got != want {
			t.Errorf("parseBytes(%q) = %d, %v; want %d", in, got, ok, want)
		}
	}
	for _, in := range []string{"", "lots", "-5MB", "1.5GB", "MB"} {
		if _, ok := parseBytes(in); ok {
			t.Errorf("parseBytes(%q) accepted", in)
		}
	}
}
