package agent

import (
	"net/netip"
	"testing"
)

func TestIsPublicIPv4(t *testing.T) {
	for address, want := range map[string]bool{
		"203.0.113.10": true, "45.114.127.73": true,
		"10.0.0.5": false, "172.16.3.1": false, "192.168.1.2": false,
		"100.64.1.1": false, "127.0.0.1": false, "169.254.1.1": false, "2001:db8::1": false,
	} {
		if got := isPublicIPv4(netip.MustParseAddr(address)); got != want {
			t.Errorf("%s: got %v, want %v", address, got, want)
		}
	}
}
