package agent

import (
	"strings"
	"testing"

	"vpsbill/internal/hatch/protocol"
)

func TestRenderRulesetUsesConfiguredTable(t *testing.T) {
	records := []InstanceRecord{{PrivateIPv4: "10.0.0.2", Mappings: []protocol.PortMapping{{Protocol: "tcp", PublicPort: 30001, ContainerPort: 22}}}}
	ruleset := renderRuleset("hatch_lxd", records, 0)
	if !strings.HasPrefix(ruleset, "table ip hatch_lxd\ndelete table ip hatch_lxd\ntable ip hatch_lxd {") {
		t.Fatalf("unexpected ruleset header:\n%s", ruleset)
	}
	if strings.Contains(ruleset, "ip hatch\n") {
		t.Fatalf("ruleset touches the default table:\n%s", ruleset)
	}
	if !strings.HasPrefix(renderRuleset("", nil, 0), "table ip hatch\n") {
		t.Fatal("empty table name should fall back to hatch")
	}
}

func TestConnectionLimits(t *testing.T) {
	// 64 per MB, at least 2048, at most 65536 and a quarter of the host table.
	for _, c := range []struct{ ram, host, want int }{{64, 0, 4096}, {16, 0, 2048}, {4096, 0, 65536}, {1024, 16384, 4096}, {64, 2048, 1024}} {
		if got := connLimit(c.ram, c.host); got != c.want {
			t.Errorf("connLimit(%d, %d) = %d, want %d", c.ram, c.host, got, c.want)
		}
	}
	records := []InstanceRecord{{PrivateIPv4: "10.0.0.3", RAMMB: 64}, {PrivateIPv4: "10.0.0.2", RAMMB: 512}}
	ruleset := renderRuleset("hatch", records, 65536)
	for _, want := range []string{
		"type filter hook prerouting priority mangle",
		"ip saddr 10.0.0.2 ct state new ct count over 16384 counter drop",
		"ip saddr 10.0.0.3 ct state new ct count over 4096 counter drop",
		"ip daddr 10.0.0.3 ct state new ct count over 4096 counter drop",
	} {
		if !strings.Contains(ruleset, want) {
			t.Fatalf("missing %q in:\n%s", want, ruleset)
		}
	}
	if strings.Index(ruleset, "ip daddr 10.0.0.3") > strings.Index(ruleset, "ct status dnat accept") {
		t.Fatal("inbound limit must come before the dnat accept")
	}
}

func TestConfigValidatesNFTTable(t *testing.T) {
	config := Config{ServerURL: "http://127.0.0.1:8088", Token: strings.Repeat("a", 32), LXD: &LXDConfig{Socket: "/tmp/x"}}
	config.ApplyDefaults()
	if config.NFTTable != "hatch" {
		t.Fatalf("default table = %q", config.NFTTable)
	}
	if err := config.Validate(); err != nil {
		t.Fatal(err)
	}
	for _, bad := range []string{"Hatch", "hatch;flush ruleset", "1hatch", strings.Repeat("a", 33)} {
		config.NFTTable = bad
		if err := config.Validate(); err == nil {
			t.Fatalf("table %q accepted", bad)
		}
	}
}
