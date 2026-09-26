//go:build linux

package agent

import (
	"bufio"
	"os"
	"runtime"
	"strconv"
	"strings"
	"syscall"

	"vpsbill/internal/hatch/protocol"
)

// detectCapacity reports the host's CPUs, memory and the size of the file
// system holding dir. Operators usually override it in the config to keep
// headroom for the host itself.
func detectCapacity(dir string) protocol.Capacity {
	capacity := protocol.Capacity{VCPU: runtime.NumCPU()}
	if file, err := os.Open("/proc/meminfo"); err == nil {
		scanner := bufio.NewScanner(file)
		for scanner.Scan() {
			fields := strings.Fields(scanner.Text())
			if len(fields) >= 2 && fields[0] == "MemTotal:" {
				if kb, err := strconv.ParseInt(fields[1], 10, 64); err == nil {
					capacity.RAMMB = kb / 1024
				}
				break
			}
		}
		file.Close()
	}
	var stat syscall.Statfs_t
	if syscall.Statfs(dir, &stat) == nil {
		capacity.DiskGB = int64(stat.Blocks) * int64(stat.Bsize) >> 30
	}
	return capacity
}
