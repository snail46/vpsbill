package agent

import (
	"reflect"
	"testing"

	"vpsbill/internal/hatch/protocol"
)

func TestDiskLimitsForBothRuntimes(t *testing.T) {
	spec := RuntimeSpec{DiskIO: protocol.DiskIO{ReadMBps: 100, WriteMBps: 50, WriteIOPS: 800}, Devices: []string{"254:0", "7:4"}}
	want := "lxc.cgroup2.io.max = 254:0 rbps=104857600 wbps=52428800 wiops=800\nlxc.cgroup2.io.max = 7:4 rbps=104857600 wbps=52428800 wiops=800"
	if got := lxcIOMax(spec); got != want {
		t.Fatalf("raw.lxc:\n%s\nwant\n%s", got, want)
	}
	blockIO := podmanBlockIO(spec)
	if _, ok := blockIO["throttleReadIOPSDevice"]; ok {
		t.Fatal("unset read IOPS must stay unlimited")
	}
	write := blockIO["throttleWriteBpsDevice"].([]map[string]int64)
	if !reflect.DeepEqual(write[0], map[string]int64{"major": 254, "minor": 0, "rate": 50 << 20}) || len(write) != 2 {
		t.Fatalf("podman write limit %v", write)
	}
	// No limits: nothing is written, so instances keep full speed.
	if lxcIOMax(RuntimeSpec{Devices: spec.Devices}) != "" || podmanBlockIO(RuntimeSpec{Devices: spec.Devices}) != nil {
		t.Fatal("unlimited spec produced limits")
	}
}
