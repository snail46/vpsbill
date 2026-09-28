//go:build linux

package agent

import (
	"os"
	"runtime"
	"syscall"

	"vpsbill/internal/hatch/protocol"
)

// detectCapacity reports the host's CPUs, memory and the size of the file
// system holding dir. Runtimes with their own storage replace the disk size.
func detectCapacity(dir string) protocol.Capacity {
	capacity := protocol.Capacity{VCPU: runtime.NumCPU()}
	if file, err := os.Open("/proc/meminfo"); err == nil {
		capacity.RAMMB = parseMeminfo(file)["MemTotal"]
		file.Close()
	}
	if total, _, err := fsSpace(dir); err == nil {
		capacity.DiskGB = total >> 30
	}
	return capacity
}

// readHealth samples the host's load and memory.
func readHealth() *protocol.HostHealth {
	health := &protocol.HostHealth{CPUs: runtime.NumCPU()}
	if file, err := os.Open("/proc/meminfo"); err == nil {
		info := parseMeminfo(file)
		file.Close()
		health.MemTotalMB, health.MemAvailableMB = info["MemTotal"], info["MemAvailable"]
		health.SwapTotalMB, health.SwapFreeMB = info["SwapTotal"], info["SwapFree"]
	}
	if data, err := os.ReadFile("/proc/loadavg"); err == nil {
		health.Load1, health.Load5, health.Load15 = parseLoadavg(string(data))
	}
	return health
}

// fsSpace returns the size and used bytes of the file system holding path.
func fsSpace(path string) (total, used int64, err error) {
	var stat syscall.Statfs_t
	if err := syscall.Statfs(path, &stat); err != nil {
		return 0, 0, err
	}
	total = int64(stat.Blocks) * int64(stat.Bsize)
	return total, total - int64(stat.Bfree)*int64(stat.Bsize), nil
}

// mountOf finds the mount holding path in the agent's mount table.
func mountOf(path string) (mountEntry, bool) {
	file, err := os.Open("/proc/self/mountinfo")
	if err != nil {
		return mountEntry{}, false
	}
	defer file.Close()
	return findMount(file, path)
}
