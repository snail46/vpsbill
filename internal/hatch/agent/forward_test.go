package agent

import (
	"context"
	"errors"
	"strings"
	"testing"
)

func TestForwardingAddsMissingRulesOnce(t *testing.T) {
	present := map[string]bool{}
	var inserted []string
	run := func(_ context.Context, name string, args ...string) (string, error) {
		if name == "ip" {
			return "default via 10.0.148.1 dev ens17 proto static\n", nil
		}
		rule := strings.Join(args[3:], " ")
		if args[1] == "-C" {
			if present[rule] {
				return "", nil
			}
			return "", errors.New("missing")
		}
		rule = strings.Join(args[4:], " ")
		present[rule] = true
		inserted = append(inserted, rule)
		return "", nil
	}
	forwarding := Forwarding{PortStart: 20000, PortEnd: 60000, Bridge: "hatchbr0", Run: run}
	if err := forwarding.Ensure(context.Background()); err != nil {
		t.Fatal(err)
	}
	want := []string{
		"-p tcp -m conntrack --ctstate DNAT --ctorigdstport 20000:60000 -m comment --comment hatch-agent -j ACCEPT",
		"-p udp -m conntrack --ctstate DNAT --ctorigdstport 20000:60000 -m comment --comment hatch-agent -j ACCEPT",
		"-i hatchbr0 -o ens17 -m comment --comment hatch-agent -j ACCEPT",
		"-i ens17 -o hatchbr0 -m conntrack --ctstate RELATED,ESTABLISHED -m comment --comment hatch-agent -j ACCEPT",
	}
	if strings.Join(inserted, "\n") != strings.Join(want, "\n") {
		t.Fatalf("inserted:\n%s", strings.Join(inserted, "\n"))
	}
	if err := forwarding.Ensure(context.Background()); err != nil || len(inserted) != len(want) {
		t.Fatalf("second pass inserted again: %v %v", err, inserted)
	}
}

func TestForwardingWithoutBridgeOnlyAllowsPortForwards(t *testing.T) {
	var commands []string
	run := func(_ context.Context, name string, args ...string) (string, error) {
		commands = append(commands, name+" "+strings.Join(args, " "))
		return "", errors.New("missing")
	}
	_ = Forwarding{PortStart: 20000, PortEnd: 60000, Run: run}.Ensure(context.Background())
	for _, command := range commands {
		if strings.Contains(command, " -i ") || strings.HasPrefix(command, "ip ") {
			t.Fatalf("unexpected bridge rule: %s", command)
		}
	}
}
