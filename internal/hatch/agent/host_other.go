//go:build !linux

package agent

import (
	"errors"
	"runtime"

	"vpsbill/internal/hatch/protocol"
)

// The agent is meant to run on Linux hosts; non-Linux builds exist for
// tests and only know the CPU count.

func detectCapacity(string) protocol.Capacity {
	return protocol.Capacity{VCPU: runtime.NumCPU()}
}

func readHealth() *protocol.HostHealth {
	return &protocol.HostHealth{CPUs: runtime.NumCPU()}
}

func fsSpace(string) (int64, int64, error) { return 0, 0, errors.ErrUnsupported }

func mountOf(string) (mountEntry, bool) { return mountEntry{}, false }
