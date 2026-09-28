package postgres

import (
	"errors"
	"strings"
	"testing"
	"time"

	"vpsbill/internal/provider"
)

func TestEvaluateHealthHoldsOnlySustainedOverload(t *testing.T) {
	start := time.Date(2026, 9, 28, 12, 0, 0, 0, time.UTC)
	low := &provider.HostHealth{CPUs: 2, MemTotalMB: 1000, MemAvailableMB: 20, Load15: 1}
	since, reason := evaluateHealth(low, healthSince{}, start)
	if since.Mem == nil || reason != "" {
		t.Fatalf("first low sample: since=%v reason=%q", since.Mem, reason)
	}
	since, reason = evaluateHealth(low, since, start.Add(29*time.Minute))
	if reason != "" || !since.Mem.Equal(start) {
		t.Fatalf("held before the window: %q", reason)
	}
	since, reason = evaluateHealth(low, since, start.Add(31*time.Minute))
	if !strings.Contains(reason, "可用内存") {
		t.Fatalf("not held after the window: %q", reason)
	}
	healthy := &provider.HostHealth{CPUs: 2, MemTotalMB: 1000, MemAvailableMB: 400, Load15: 1}
	since, reason = evaluateHealth(healthy, since, start.Add(32*time.Minute))
	if since.Mem != nil || reason != "" {
		t.Fatalf("recovery did not clear: %+v %q", since, reason)
	}
	full := &provider.HostHealth{CPUs: 2, MemTotalMB: 1000, MemAvailableMB: 400, Disks: []provider.DiskUsage{{Name: "podman", TotalGB: 100, UsedGB: 97}}}
	since, _ = evaluateHealth(full, healthSince{}, start)
	if _, reason = evaluateHealth(full, since, start.Add(11*time.Minute)); !strings.Contains(reason, "podman 实例存储已用 97%") {
		t.Fatalf("disk: %q", reason)
	}
	busy := &provider.HostHealth{CPUs: 2, MemTotalMB: 1000, MemAvailableMB: 400, Load15: 7}
	since, _ = evaluateHealth(busy, healthSince{}, start)
	if _, reason = evaluateHealth(busy, since, start.Add(23*time.Hour)); reason != "" {
		t.Fatalf("load held early: %q", reason)
	}
	if _, reason = evaluateHealth(busy, since, start.Add(24*time.Hour)); !strings.Contains(reason, "负载") {
		t.Fatalf("load: %q", reason)
	}
	quota := &provider.HostHealth{CPUs: 2, MemTotalMB: 1000, MemAvailableMB: 400, QuotaErrors: map[string]string{"podman": "no prjquota"}}
	if _, reason = evaluateHealth(quota, healthSince{}, start); !strings.Contains(reason, "podman 无法限制实例硬盘") {
		t.Fatalf("quota errors must hold at once: %q", reason)
	}
}

func TestOvercommitValidate(t *testing.T) {
	limits := OvercommitLimits{CPU: 4, RAM: 1.5, Disk: 2, Traffic: 3}
	if err := (Overcommit{CPU: 4, RAM: 1.5, Disk: 1, Traffic: 3}).Validate(limits); err != nil {
		t.Fatal(err)
	}
	for _, bad := range []Overcommit{{CPU: 4.5, RAM: 1, Disk: 1, Traffic: 1}, {CPU: 1, RAM: 0.5, Disk: 1, Traffic: 1}, {CPU: 1, RAM: 1, Disk: 1, Traffic: 3.5}} {
		if err := bad.Validate(limits); !errors.Is(err, ErrOvercommitInvalid) {
			t.Fatalf("%+v accepted", bad)
		}
	}
}
