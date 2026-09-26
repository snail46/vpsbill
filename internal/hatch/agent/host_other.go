//go:build !linux

package agent

import (
	"runtime"

	"vpsbill/internal/hatch/protocol"
)

// detectCapacity only knows the CPU count off Linux; the agent is meant to
// run on Linux hosts and non-Linux builds exist for tests.
func detectCapacity(string) protocol.Capacity {
	return protocol.Capacity{VCPU: runtime.NumCPU()}
}
