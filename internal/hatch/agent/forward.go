package agent

import (
	"context"
	"fmt"
	"os/exec"
	"strings"
)

// Forwarding lets instance traffic through the iptables FORWARD chain.
// Docker, and some firewalls, set its policy to drop, and an accept in the
// agent's own nftables table cannot override a drop in another table: port
// forwards then time out and LXC instances have no network at all (Podman
// adds its own rules). Only what the agent is responsible for is allowed,
// so instances still cannot reach Docker's containers.
type Forwarding struct {
	// Ports is the public port range of the port forwards.
	PortStart, PortEnd int
	// Bridge is the LXD / Incus instance network, allowed out through the
	// default route.
	Bridge string
	Run    CommandRunner
}

const forwardComment = "hatch-agent"

// Ensure adds the missing rules. Hosts without iptables need none.
func (f Forwarding) Ensure(ctx context.Context) error {
	run := f.Run
	if run == nil {
		if _, err := exec.LookPath("iptables"); err != nil {
			return nil
		}
		run = runCommand
	}
	var failed []string
	for _, rule := range f.rules(ctx, run) {
		check := append([]string{"-w", "-C", "FORWARD"}, rule...)
		if _, err := run(ctx, "iptables", check...); err == nil {
			continue
		}
		insert := append([]string{"-w", "-I", "FORWARD", "1"}, rule...)
		if _, err := run(ctx, "iptables", insert...); err != nil {
			failed = append(failed, err.Error())
		}
	}
	if len(failed) > 0 {
		return fmt.Errorf("allow forwarding in iptables: %s", strings.Join(failed, "; "))
	}
	return nil
}

func (f Forwarding) rules(ctx context.Context, run CommandRunner) [][]string {
	tag := []string{"-m", "comment", "--comment", forwardComment, "-j", "ACCEPT"}
	var rules [][]string
	if f.PortStart > 0 && f.PortEnd >= f.PortStart {
		// Both directions of a forwarded connection; the original
		// destination port keeps Docker's own port publishing out of it.
		ports := fmt.Sprintf("%d:%d", f.PortStart, f.PortEnd)
		for _, protocol := range []string{"tcp", "udp"} {
			rules = append(rules, append([]string{"-p", protocol, "-m", "conntrack", "--ctstate", "DNAT", "--ctorigdstport", ports}, tag...))
		}
	}
	if f.Bridge != "" {
		if uplink := defaultRouteDevice(ctx, run); uplink != "" && uplink != f.Bridge {
			rules = append(rules,
				append([]string{"-i", f.Bridge, "-o", uplink}, tag...),
				append([]string{"-i", uplink, "-o", f.Bridge, "-m", "conntrack", "--ctstate", "RELATED,ESTABLISHED"}, tag...),
			)
		}
	}
	return rules
}

// defaultRouteDevice is the interface of the IPv4 default route.
func defaultRouteDevice(ctx context.Context, run CommandRunner) string {
	output, err := run(ctx, "ip", "-4", "-o", "route", "show", "default")
	if err != nil {
		return ""
	}
	fields := strings.Fields(output)
	for i := 0; i+1 < len(fields); i++ {
		if fields[i] == "dev" {
			return fields[i+1]
		}
	}
	return ""
}
