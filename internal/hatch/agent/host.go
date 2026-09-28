package agent

import (
	"bufio"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"os"
	pathpkg "path"
	"slices"
	"strconv"
	"strings"

	"vpsbill/internal/hatch/protocol"
)

// parseMeminfo reads /proc/meminfo into megabytes per field.
func parseMeminfo(r io.Reader) map[string]int64 {
	result := map[string]int64{}
	scanner := bufio.NewScanner(r)
	for scanner.Scan() {
		fields := strings.Fields(scanner.Text())
		if len(fields) < 2 {
			continue
		}
		if kb, err := strconv.ParseInt(fields[1], 10, 64); err == nil {
			result[strings.TrimSuffix(fields[0], ":")] = kb / 1024
		}
	}
	return result
}

// parseLoadavg reads the three averages from /proc/loadavg.
func parseLoadavg(text string) (load1, load5, load15 float64) {
	fields := strings.Fields(text)
	if len(fields) < 3 {
		return 0, 0, 0
	}
	load1, _ = strconv.ParseFloat(fields[0], 64)
	load5, _ = strconv.ParseFloat(fields[1], 64)
	load15, _ = strconv.ParseFloat(fields[2], 64)
	return load1, load5, load15
}

// mountEntry is one line of /proc/self/mountinfo.
type mountEntry struct {
	Point, FSType, Source, Options, SuperOptions string
}

// findMount returns the mount holding path: the entry with the longest
// mount point that is a prefix of it.
func findMount(r io.Reader, path string) (mountEntry, bool) {
	path = pathpkg.Clean(path)
	var best mountEntry
	found := false
	scanner := bufio.NewScanner(r)
	scanner.Buffer(make([]byte, 64*1024), 1024*1024)
	for scanner.Scan() {
		// id parent major:minor root point options [optional...] - fstype source superoptions
		left, right, ok := strings.Cut(scanner.Text(), " - ")
		if !ok {
			continue
		}
		fields, tail := strings.Fields(left), strings.Fields(right)
		if len(fields) < 6 || len(tail) < 3 {
			continue
		}
		point := unescapeMount(fields[4])
		if point != "/" && path != point && !strings.HasPrefix(path, point+"/") {
			continue
		}
		if !found || len(point) >= len(best.Point) {
			best, found = mountEntry{Point: point, Options: fields[5], FSType: tail[0], Source: tail[1], SuperOptions: tail[2]}, true
		}
	}
	return best, found
}

// unescapeMount decodes the octal escapes mountinfo uses for spaces etc.
func unescapeMount(value string) string {
	if !strings.Contains(value, `\`) {
		return value
	}
	var b strings.Builder
	for i := 0; i < len(value); i++ {
		if value[i] == '\\' && i+3 < len(value) {
			if n, err := strconv.ParseUint(value[i+1:i+4], 8, 8); err == nil {
				b.WriteByte(byte(n))
				i += 3
				continue
			}
		}
		b.WriteByte(value[i])
	}
	return b.String()
}

// hasProjectQuota tells whether an XFS mount enforces project quotas, which
// overlay needs to limit a container's size.
func hasProjectQuota(entry mountEntry) bool {
	if entry.FSType != "xfs" {
		return false
	}
	for _, option := range strings.Split(entry.SuperOptions+","+entry.Options, ",") {
		switch option {
		case "prjquota", "pquota", "pqnoenforce":
			return option != "pqnoenforce"
		}
	}
	return false
}

// errNoProjectQuota explains how to give Podman a quota-capable store.
var errNoProjectQuota = errors.New("Podman 存储目录不在开启项目配额（prjquota）的 XFS 上，无法限制实例硬盘；请用安装脚本的 --podman-disk 创建 XFS 数据盘")

// machineID hashes /etc/machine-id so the raw value never leaves the host.
func machineID() string {
	data, err := os.ReadFile("/etc/machine-id")
	if err != nil {
		return ""
	}
	id := strings.TrimSpace(string(data))
	if id == "" {
		return ""
	}
	sum := sha256.Sum256([]byte("hatch-machine:" + id))
	return hex.EncodeToString(sum[:16])
}

// lowerCapacity applies the operator's config: each set value may only
// lower the detected one, so a host cannot advertise hardware it lacks.
func lowerCapacity(detected protocol.Capacity, config CapacityConfig) protocol.Capacity {
	result := detected
	if config.VCPU > 0 && (detected.VCPU == 0 || config.VCPU < detected.VCPU) {
		result.VCPU = config.VCPU
	}
	if config.RAMMB > 0 && (detected.RAMMB == 0 || config.RAMMB < detected.RAMMB) {
		result.RAMMB = config.RAMMB
	}
	if config.DiskGB > 0 && (detected.DiskGB == 0 || config.DiskGB < detected.DiskGB) {
		result.DiskGB = config.DiskGB
	}
	return result
}

func sortedKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for key := range m {
		keys = append(keys, key)
	}
	slices.Sort(keys)
	return keys
}
