package agent

import (
	"context"
	"net/http"
	"net/http/httptest"
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

func TestDetectPublicIPv4Sources(t *testing.T) {
	// The billing server cannot tell (it answers nothing); the next source
	// answers with a Cloudflare-style trace.
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	defer server.Close()
	trace := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("fl=1\nh=1.1.1.1\nip=203.0.113.44\nts=1\n"))
	}))
	defer trace.Close()
	previous := echoServices
	echoServices = []string{trace.URL}
	defer func() { echoServices = previous }()
	client := &http.Client{}
	if _, err := askAddress(context.Background(), client, server.URL); err == nil {
		t.Fatal("an empty answer was accepted")
	}
	address, err := askAddress(context.Background(), client, trace.URL)
	if err != nil || address != "203.0.113.44" {
		t.Fatalf("trace: %q %v", address, err)
	}
	private := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("10.0.0.5"))
	}))
	defer private.Close()
	if _, err := askAddress(context.Background(), client, private.URL); err == nil {
		t.Fatal("a private address was accepted")
	}
}
