package agent

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/netip"
	"regexp"
	"strings"
	"time"
)

// cgnat is the carrier-grade NAT range, private in practice.
var cgnat = netip.MustParsePrefix("100.64.0.0/10")

// isPublicIPv4 reports whether customers on the internet can reach addr.
func isPublicIPv4(addr netip.Addr) bool {
	return addr.Is4() && addr.IsGlobalUnicast() && !addr.IsPrivate() && !cgnat.Contains(addr)
}

// echoServices answer with the address a request comes from. They are asked
// only when neither the host's route nor the billing server can tell,
// e.g. when the server sits behind a Cloudflare Tunnel on this very host.
// The first two are reachable from mainland China as well.
var echoServices = []string{
	"https://1.1.1.1/cdn-cgi/trace",
	"https://4.ipw.cn",
	"https://api.ipify.org",
	"https://ipv4.icanhazip.com",
}

// DetectPublicIPv4 finds the address customers reach this host on: the
// source address of the default route when it is public, otherwise the
// address the billing server sees the host connect from (hosts behind a
// provider's 1:1 NAT, such as Oracle Cloud, only carry a private address),
// otherwise what public echo services see.
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
	// Ask over IPv4 so the other side sees the IPv4 address.
	client.Transport.(*http.Transport).DialContext = func(ctx context.Context, _, address string) (net.Conn, error) {
		var dialer net.Dialer
		return dialer.DialContext(ctx, "tcp4", address)
	}
	sources := append([]string{strings.TrimRight(config.ServerURL, "/") + "/api/v1/agent/ip"}, echoServices...)
	var failures []string
	for _, source := range sources {
		address, err := askAddress(ctx, client, source)
		if err == nil {
			return address, nil
		}
		failures = append(failures, err.Error())
		if ctx.Err() != nil {
			break
		}
	}
	return "", fmt.Errorf("no public IPv4 found (%s); set public_ipv4 in the config", strings.Join(failures, "; "))
}

var ipv4Pattern = regexp.MustCompile(`\b\d{1,3}(?:\.\d{1,3}){3}\b`)

// askAddress reads the first public IPv4 address in source's answer: a
// bare address, or one inside text such as Cloudflare's "ip=..." trace.
func askAddress(ctx context.Context, client *http.Client, source string) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, source, nil)
	if err != nil {
		return "", err
	}
	request.Header.Set("User-Agent", "curl/8")
	response, err := client.Do(request)
	if err != nil {
		return "", err
	}
	defer response.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(response.Body, 4096))
	if response.StatusCode != http.StatusOK {
		return "", fmt.Errorf("%s answered HTTP %d", source, response.StatusCode)
	}
	text := string(body)
	// Cloudflare's trace names its own address (h=1.1.1.1) before ours.
	for _, line := range strings.Split(text, "\n") {
		if value, ok := strings.CutPrefix(strings.TrimSpace(line), "ip="); ok {
			text = value
			break
		}
	}
	for _, match := range ipv4Pattern.FindAllString(text, -1) {
		if addr, err := netip.ParseAddr(match); err == nil && isPublicIPv4(addr) {
			return addr.String(), nil
		}
	}
	return "", errors.New(source + " named no public IPv4")
}
