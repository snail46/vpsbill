package agent

import (
	"bytes"
	"context"
	"fmt"
	"os/exec"
	"sort"
	"strings"
)

// NATApplier installs a complete nftables ruleset for the agent's table.
type NATApplier interface {
	Apply(ctx context.Context, ruleset string) error
}

// NFT applies rulesets with the nft command. The whole table is replaced in
// one transaction, so the host never sees a half-updated rule set.
type NFT struct{ Binary string }

func (n NFT) Apply(ctx context.Context, ruleset string) error {
	binary := n.Binary
	if binary == "" {
		binary = "nft"
	}
	command := exec.CommandContext(ctx, binary, "-f", "-")
	command.Stdin = strings.NewReader(ruleset)
	var output bytes.Buffer
	command.Stdout, command.Stderr = &output, &output
	if err := command.Run(); err != nil {
		return fmt.Errorf("nft: %w: %s", err, strings.TrimSpace(output.String()))
	}
	return nil
}

type natRule struct {
	Protocol   string
	PublicPort int
	Target     string
	TargetPort int
}

// connLimit is how many tracked connections one instance may hold in each
// direction: 64 per MB of memory, between 2048 and 65536, and never more
// than a quarter of the host's table, so one instance (P2P, a proxy) cannot
// fill it and cut every other instance and the host itself off.
func connLimit(ramMB int, hostMax int) int {
	limit := min(max(ramMB*64, 2048), 65536)
	if hostMax > 0 {
		limit = min(limit, max(hostMax/4, 1024))
	}
	return limit
}

// renderRuleset builds the agent's "ip <table>" table. Port forwards match packets
// addressed to any local address (fib daddr type local), which works both on
// hosts that own their public IP and behind 1:1 cloud NAT. hostMax is the
// host's conntrack table size (0 when unknown).
func renderRuleset(table string, records []InstanceRecord, hostMax int) string {
	if table == "" {
		table = "hatch"
	}
	var rules []natRule
	for _, record := range records {
		if record.PrivateIPv4 == "" {
			continue
		}
		for _, mapping := range record.Mappings {
			for _, protocol := range expandProtocol(mapping.Protocol) {
				rules = append(rules, natRule{Protocol: protocol, PublicPort: mapping.PublicPort, Target: record.PrivateIPv4, TargetPort: mapping.ContainerPort})
			}
		}
	}
	sort.Slice(rules, func(i, j int) bool {
		if rules[i].PublicPort == rules[j].PublicPort {
			return rules[i].Protocol < rules[j].Protocol
		}
		return rules[i].PublicPort < rules[j].PublicPort
	})
	var builder strings.Builder
	fmt.Fprintf(&builder, "table ip %[1]s\ndelete table ip %[1]s\ntable ip %[1]s {\n", table)
	builder.WriteString("  chain prerouting {\n    type nat hook prerouting priority dstnat; policy accept;\n")
	for _, rule := range rules {
		fmt.Fprintf(&builder, "    fib daddr type local %s dport %d dnat to %s:%d\n", rule.Protocol, rule.PublicPort, rule.Target, rule.TargetPort)
	}
	builder.WriteString("  }\n")
	// Connection limits run after conntrack has seen the packet: outgoing
	// ones in prerouting (to the internet or the host), incoming ones in
	// forward after the port forward rewrote the destination.
	limited := make([]InstanceRecord, 0, len(records))
	for _, record := range records {
		if record.PrivateIPv4 != "" {
			limited = append(limited, record)
		}
	}
	sort.Slice(limited, func(i, j int) bool { return limited[i].PrivateIPv4 < limited[j].PrivateIPv4 })
	builder.WriteString("  chain limits {\n    type filter hook prerouting priority mangle; policy accept;\n")
	for _, record := range limited {
		fmt.Fprintf(&builder, "    ip saddr %s ct state new ct count over %d counter drop\n", record.PrivateIPv4, connLimit(record.RAMMB, hostMax))
	}
	builder.WriteString("  }\n")
	builder.WriteString("  chain forward {\n    type filter hook forward priority filter - 1; policy accept;\n")
	for _, record := range limited {
		fmt.Fprintf(&builder, "    ip daddr %s ct state new ct count over %d counter drop\n", record.PrivateIPv4, connLimit(record.RAMMB, hostMax))
	}
	builder.WriteString("    ct status dnat accept\n  }\n")
	builder.WriteString("}\n")
	return builder.String()
}

func expandProtocol(protocol string) []string {
	switch protocol {
	case "udp":
		return []string{"udp"}
	case "both":
		return []string{"tcp", "udp"}
	default:
		return []string{"tcp"}
	}
}
