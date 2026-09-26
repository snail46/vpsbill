package agent

import (
	"bytes"
	"context"
	"fmt"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
)

// CommandRunner runs a host command and returns its combined output. It is
// replaced in tests.
type CommandRunner func(ctx context.Context, name string, args ...string) (string, error)

func runCommand(ctx context.Context, name string, args ...string) (string, error) {
	command := exec.CommandContext(ctx, name, args...)
	var output bytes.Buffer
	command.Stdout, command.Stderr = &output, &output
	err := command.Run()
	if err != nil {
		return output.String(), fmt.Errorf("%s %s: %w: %s", name, strings.Join(args, " "), err, strings.TrimSpace(output.String()))
	}
	return output.String(), nil
}

// TC limits a container's bandwidth on the host side of its veth pair:
// traffic leaving the host interface is the container's download, traffic
// entering it is the container's upload.
type TC struct{ Run CommandRunner }

func (t TC) Apply(ctx context.Context, iface string, downMbps, upMbps int) error {
	run := t.Run
	if run == nil {
		run = runCommand
	}
	// Clear previous limits; missing qdiscs are not an error.
	_, _ = run(ctx, "tc", "qdisc", "del", "dev", iface, "root")
	_, _ = run(ctx, "tc", "qdisc", "del", "dev", iface, "ingress")
	if downMbps > 0 {
		if _, err := run(ctx, "tc", "qdisc", "add", "dev", iface, "root", "tbf",
			"rate", strconv.Itoa(downMbps)+"mbit", "burst", burstBytes(downMbps), "latency", "50ms"); err != nil {
			return err
		}
	}
	if upMbps > 0 {
		if _, err := run(ctx, "tc", "qdisc", "add", "dev", iface, "handle", "ffff:", "ingress"); err != nil {
			return err
		}
		if _, err := run(ctx, "tc", "filter", "add", "dev", iface, "parent", "ffff:", "protocol", "all", "prio", "1",
			"matchall", "action", "police", "rate", strconv.Itoa(upMbps)+"mbit", "burst", burstBytes(upMbps), "conform-exceed", "drop"); err != nil {
			return err
		}
	}
	return nil
}

// burstBytes allows 10ms of traffic at the given rate, at least 32 KiB.
func burstBytes(mbps int) string {
	return strconv.Itoa(max(32768, mbps*1_000_000/8/100)) + "b"
}

var peerIndex = regexp.MustCompile(`@if(\d+)`)

// hostPeerIndex returns the host-side ifindex of the container's eth0 by
// reading "eth0@ifN" inside the container's network namespace.
func hostPeerIndex(ctx context.Context, run CommandRunner, pid int) (int, error) {
	output, err := run(ctx, "nsenter", "-t", strconv.Itoa(pid), "-n", "ip", "-o", "link", "show", "eth0")
	if err != nil {
		return 0, err
	}
	match := peerIndex.FindStringSubmatch(output)
	if match == nil {
		return 0, fmt.Errorf("cannot find veth peer in %q", strings.TrimSpace(output))
	}
	return strconv.Atoi(match[1])
}
