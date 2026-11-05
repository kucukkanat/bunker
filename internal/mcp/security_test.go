package mcp

import (
	"net"
	"testing"
)

// Client metadata documents are fetched from a URL any client picks; the
// fetch must not reach internal networks (CGNAT/Tailscale, NAT64, "this
// network", benchmarking ranges...), not only RFC 1918 and loopback.
func TestCIMDBlocksNonPublicRanges(t *testing.T) {
	for _, s := range []string{"100.64.1.2", "100.127.255.1", "0.0.0.1", "198.18.0.5", "64:ff9b::a00:1", "::ffff:10.0.0.1", "169.254.169.254", "fd00::1", "127.0.0.1", "10.1.2.3"} {
		if publicIP(net.ParseIP(s)) {
			t.Errorf("%s treated as public", s)
		}
	}
	for _, s := range []string{"8.8.8.8", "2606:4700::1111"} {
		if !publicIP(net.ParseIP(s)) {
			t.Errorf("%s treated as private", s)
		}
	}
}
