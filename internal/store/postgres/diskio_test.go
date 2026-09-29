package postgres

import (
	"testing"

	"vpsbill/internal/provider"
)

func TestSuggestDiskIO(t *testing.T) {
	perf := provider.DiskPerf{ReadMBps: 400, WriteMBps: 200, ReadIOPS: 20000, WriteIOPS: 9000}
	cases := []struct {
		instances int
		want      provider.DiskIO
	}{
		// 32 instances: a quarter (8) busy at once share the disk.
		{32, provider.DiskIO{ReadMBps: 50, WriteMBps: 25, ReadIOPS: 2500, WriteIOPS: 1100}},
		// A few large instances: never more than half the disk each.
		{3, provider.DiskIO{ReadMBps: 200, WriteMBps: 100, ReadIOPS: 10000, WriteIOPS: 4500}},
		// Hundreds of tiny instances still get a usable floor of 1.
		{4000, provider.DiskIO{ReadMBps: 1, WriteMBps: 1, ReadIOPS: 20, WriteIOPS: 9}},
	}
	for _, c := range cases {
		if got := suggestDiskIO(perf, c.instances); got != c.want {
			t.Errorf("%d instances: got %+v, want %+v", c.instances, got, c.want)
		}
	}
}
