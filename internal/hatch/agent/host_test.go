package agent

import (
	"strings"
	"testing"

	"vpsbill/internal/hatch/protocol"
)

const sampleMountinfo = `22 1 252:1 / / rw,relatime shared:1 - xfs /dev/vda1 rw,attr2,inode64,logbufs=8,logbsize=32k,noquota
30 22 0:25 / /run rw,nosuid,nodev shared:5 - tmpfs tmpfs rw,size=400000k
91 22 7:0 / /var/lib/hatch/podman-storage rw,relatime shared:40 - xfs /dev/loop0 rw,attr2,inode64,logbufs=8,logbsize=32k,prjquota
95 22 7:1 / /srv/with\040space rw,relatime shared:41 - ext4 /dev/loop1 rw
`

func TestFindMountPicksLongestPrefix(t *testing.T) {
	cases := map[string]string{
		"/var/lib/hatch/podman-storage/storage": "/var/lib/hatch/podman-storage",
		"/var/lib/hatch/podman-storage":         "/var/lib/hatch/podman-storage",
		"/var/lib/hatch/podman-storagex":        "/",
		"/srv/with space/data":                  "/srv/with space",
		"/etc":                                  "/",
	}
	for path, want := range cases {
		entry, ok := findMount(strings.NewReader(sampleMountinfo), path)
		if !ok || entry.Point != want {
			t.Errorf("findMount(%s) = %q, want %q", path, entry.Point, want)
		}
	}
	quota, _ := findMount(strings.NewReader(sampleMountinfo), "/var/lib/hatch/podman-storage/storage")
	root, _ := findMount(strings.NewReader(sampleMountinfo), "/var/lib/containers/storage")
	if !hasProjectQuota(quota) || hasProjectQuota(root) {
		t.Fatalf("project quota detection: loop=%v root=%v", hasProjectQuota(quota), hasProjectQuota(root))
	}
}

func TestConfigCapacityOnlyLowers(t *testing.T) {
	detected := protocol.Capacity{VCPU: 4, RAMMB: 4096, DiskGB: 100}
	got := lowerCapacity(detected, CapacityConfig{VCPU: 16, RAMMB: 2048, DiskGB: 500})
	if got.VCPU != 4 || got.RAMMB != 2048 || got.DiskGB != 100 {
		t.Fatalf("capacity = %+v", got)
	}
}

func TestParseHostSamples(t *testing.T) {
	info := parseMeminfo(strings.NewReader("MemTotal:        4008448 kB\nMemAvailable:    1220608 kB\nSwapTotal:       0 kB\n"))
	if info["MemTotal"] != 3914 || info["MemAvailable"] != 1192 || info["SwapTotal"] != 0 {
		t.Fatalf("meminfo = %v", info)
	}
	if l1, l5, l15 := parseLoadavg("0.52 0.40 1.25 2/310 12345\n"); l1 != 0.52 || l5 != 0.40 || l15 != 1.25 {
		t.Fatalf("loadavg = %v %v %v", l1, l5, l15)
	}
}
