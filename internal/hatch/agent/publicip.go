package agent

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/netip"
	"strings"
	"time"
)

// cgnat is the carrier-grade NAT range, private in practice.
var cgnat = netip.MustParsePrefix("100.64.0.0/10")

// isPublicIPv4 reports whether customers on the internet can reach addr.
func isPublicIPv4(addr netip.Addr) bool {
	return addr.Is4() && addr.IsGlobalUnicast() && !addr.IsPrivate() && !cgnat.Contains(addr)
}

// DetectPublicIPv4 finds the address customers reach this host on: the
// source address of the default route when it is public, otherwise the
// address the billing server sees the host connect from (hosts behind a
// provider's 1:1 NAT, such as Oracle Cloud, only carry a private address).
func DetectPublicIPv4(ctx context.Context, config Config) (string, error) {
	if conn, err := net.Dial("udp4", "1.1.1.1:53"); err == nil {
		// A UDP "dial" sends nothing; it only picks the route.
		local, _ := netip.ParseAddrPort(conn.LocalAddr().String())
		conn.Close()
		if isPublicIPv4(local.Addr()) {
			return local.Addr().String(), nil
		}
	}
	client, err := serverClient(config)
	if err != nil {
		return "", err
	}
	// Ask over IPv4 so the server sees the IPv4 address.
	client.Transport.(*http.Transport).DialContext = func(ctx context.Context, _, address string) (net.Conn, error) {
		var dialer net.Dialer
		return dialer.DialContext(ctx, "tcp4", address)
	}
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, strings.TrimRight(config.ServerURL, "/")+"/api/v1/agent/ip", nil)
	if err != nil {
		return "", err
	}
	response, err := client.Do(request)
	if err != nil {
		return "", err
	}
	defer response.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(response.Body, 64))
	if response.StatusCode != http.StatusOK {
		return "", fmt.Errorf("server answered HTTP %d", response.StatusCode)
	}
	addr, err := netip.ParseAddr(strings.TrimSpace(string(body)))
	if err != nil || !isPublicIPv4(addr) {
		return "", errors.New("no public IPv4 found; set public_ipv4 in the config")
	}
	return addr.String(), nil
}
