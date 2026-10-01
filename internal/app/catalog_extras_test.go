package app

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"
	"time"

	"vpsbill/internal/provider"
	"vpsbill/internal/store/postgres"
)

func TestCleanPlanLabels(t *testing.T) {
	plan := postgres.Plan{Description: "  线路好  ", Tags: []string{" CN2  GIA ", "", "原生 IP", "CN2 GIA"}}
	if message := cleanPlanLabels(&plan); message != "" {
		t.Fatal(message)
	}
	if plan.Description != "线路好" || !slices.Equal(plan.Tags, []string{"CN2 GIA", "原生 IP"}) {
		t.Fatalf("cleaned to %q %q", plan.Description, plan.Tags)
	}
	long := postgres.Plan{Tags: []string{strings.Repeat("长", maxPlanTagRunes+1)}}
	if message := cleanPlanLabels(&long); !strings.Contains(message, "太长") {
		t.Fatalf("long tag: %q", message)
	}
	many := postgres.Plan{}
	for i := 0; i <= maxPlanTags; i++ {
		many.Tags = append(many.Tags, strings.Repeat("a", i+1))
	}
	if message := cleanPlanLabels(&many); !strings.Contains(message, "最多") {
		t.Fatalf("too many tags: %q", message)
	}
	if message := cleanPlanLabels(&postgres.Plan{Description: strings.Repeat("字", maxPlanDescRunes+1)}); message == "" {
		t.Fatal("long description accepted")
	}
}

func TestOfferedImage(t *testing.T) {
	cases := []struct {
		provider, virt, id string
		want               bool
	}{
		{"hatch", "podman", "localhost/hatch-debian12:latest", true},
		{"hatch", "podman", "localhost/hatch-alpine3.22:latest", true},
		{"hatch", "podman", "localhost/hatch-alpine:latest", false},
		{"hatch", "podman", "docker.io/library/debian:12", false},
		{"hatch", "podman", "docker.io/library/postgres:16-alpine", false},
		{"hatch", "lxc", "debian12", true},
		{"lxdapi", "lxc", "anything", true},
	}
	for _, tc := range cases {
		if got := offeredImage(tc.provider, provider.Image{ID: tc.id, Virtualization: tc.virt}); got != tc.want {
			t.Errorf("%s %s %s = %v, want %v", tc.provider, tc.virt, tc.id, got, tc.want)
		}
	}
}

// fakeMapper keeps rules in memory and fails UDP rules when told to.
type fakeMapper struct {
	rules   []provider.PortMapping
	failUDP bool
}

func (f *fakeMapper) FreePort(context.Context, string) (int, error) { return 40000, nil }
func (f *fakeMapper) AddPortMapping(_ context.Context, _ string, m provider.PortMapping) ([]provider.PortMapping, error) {
	if f.failUDP && m.Protocol == "udp" {
		return nil, errors.New("udp refused")
	}
	f.rules = append(f.rules, m)
	return slices.Clone(f.rules), nil
}
func (f *fakeMapper) UpdatePortMapping(context.Context, string, int, provider.PortMapping) ([]provider.PortMapping, error) {
	return nil, errors.New("unused")
}
func (f *fakeMapper) DeletePortMapping(_ context.Context, _ string, index int) ([]provider.PortMapping, error) {
	f.rules = slices.Delete(f.rules, index, index+1)
	return slices.Clone(f.rules), nil
}

func TestAddUDPTwin(t *testing.T) {
	ctx := context.Background()
	ssh := provider.PortMapping{ContainerPort: 22, HostPort: 40022, Protocol: "tcp", Description: "SSH"}
	tcp := provider.PortMapping{ContainerPort: 53, HostPort: 40053, Protocol: "tcp", Description: "dns"}

	mapper := &fakeMapper{rules: []provider.PortMapping{ssh}}
	added, _ := mapper.AddPortMapping(ctx, "x", tcp)
	got, err := addUDPTwin(ctx, mapper, "x", tcp, added)
	if err != nil || len(got) != 3 || got[2].Protocol != "udp" || got[2].HostPort != 40053 || got[2].ContainerPort != 53 {
		t.Fatalf("twin = %+v err=%v", got, err)
	}

	// A refused UDP rule takes the TCP rule back out.
	mapper = &fakeMapper{rules: []provider.PortMapping{ssh}, failUDP: true}
	added, _ = mapper.AddPortMapping(ctx, "x", tcp)
	if _, err := addUDPTwin(ctx, mapper, "x", tcp, added); err == nil || len(mapper.rules) != 1 || mapper.rules[0].Description != "SSH" {
		t.Fatalf("rollback left %+v err=%v", mapper.rules, err)
	}
}

func TestProxyWarningClearsWhenFixed(t *testing.T) {
	misrouted.Store(0)
	routed.Store(0)
	defer func() { misrouted.Store(0); routed.Store(0) }()
	handler := watchProxy(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	request := func(peer string) {
		r := httptest.NewRequest(http.MethodGet, "/", nil)
		r.Header.Set("CF-Connecting-IP", "198.51.100.7")
		r.RemoteAddr = peer + ":1234"
		handler.ServeHTTP(httptest.NewRecorder(), r)
	}
	request("172.18.0.1")
	if proxyWarning() == "" {
		t.Fatal("misrouted request did not warn")
	}
	// The next correctly routed request (one second later, as timestamps
	// are in seconds) clears it.
	routed.Store(time.Now().Unix() + 1)
	if proxyWarning() != "" {
		t.Fatal("warning stayed after a correct request")
	}
}
