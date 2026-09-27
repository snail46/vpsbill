package agent

import (
	"strings"
	"testing"

	"vpsbill/internal/hatch/protocol"
)

func TestRenderRulesetUsesConfiguredTable(t *testing.T) {
	records := []InstanceRecord{{PrivateIPv4: "10.0.0.2", Mappings: []protocol.PortMapping{{Protocol: "tcp", PublicPort: 30001, ContainerPort: 22}}}}
	ruleset := renderRuleset("hatch_lxd", records)
	if !strings.HasPrefix(ruleset, "table ip hatch_lxd\ndelete table ip hatch_lxd\ntable ip hatch_lxd {") {
		t.Fatalf("unexpected ruleset header:\n%s", ruleset)
	}
	if strings.Contains(ruleset, "ip hatch\n") {
		t.Fatalf("ruleset touches the default table:\n%s", ruleset)
	}
	if !strings.HasPrefix(renderRuleset("", nil), "table ip hatch\n") {
		t.Fatal("empty table name should fall back to hatch")
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
