package agent

import (
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"strings"
	"time"
)

// countrySources answer with a Cloudflare trace, whose "loc" line is the
// country the asking address belongs to. The first is reachable from
// mainland China as well.
var countrySources = []string{
	"https://1.1.1.1/cdn-cgi/trace",
	"https://www.cloudflare.com/cdn-cgi/trace",
}

// DetectCountry asks where the host's IPv4 address belongs: a two-letter
// country code. The billing site files the host under it when its owner's
// description names no country, and shows it next to the description
// otherwise.
func DetectCountry(ctx context.Context) (string, error) {
	client := &http.Client{Transport: &http.Transport{
		Proxy: http.ProxyFromEnvironment,
		// Over IPv4, the address customers' NAT ports are on.
		DialContext: func(ctx context.Context, _, address string) (net.Conn, error) {
			var dialer net.Dialer
			return dialer.DialContext(ctx, "tcp4", address)
		},
	}}
	defer client.CloseIdleConnections()
	var failures []string
	for _, source := range countrySources {
		country, err := askCountry(ctx, client, source)
		if err == nil {
			return country, nil
		}
		failures = append(failures, err.Error())
		if ctx.Err() != nil {
			break
		}
	}
	return "", errors.New(strings.Join(failures, "; "))
}

func askCountry(ctx context.Context, client *http.Client, source string) (string, error) {
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
		return "", errors.New(source + " answered HTTP " + response.Status)
	}
	for _, line := range strings.Split(string(body), "\n") {
		value, ok := strings.CutPrefix(strings.TrimSpace(line), "loc=")
		if !ok {
			continue
		}
		// "XX" is unknown and "T1" is Tor.
		if len(value) == 2 && value != "XX" && strings.Trim(value, "ABCDEFGHIJKLMNOPQRSTUVWXYZ") == "" {
			return value, nil
		}
	}
	return "", errors.New(source + " named no country")
}
