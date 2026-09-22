package clicd

import "testing"

func TestCapacityFromHostInfo(t *testing.T) {
	got := CapacityFromHostInfo(map[string]any{
		"cpu":  map[string]any{"cores": float64(16)},
		"ram":  map[string]any{"total_mb": float64(32768)},
		"disk": map[string]any{"total_gb": float64(900)},
	})
	if got.VCPU != 16 || got.RAMMB != 32768 || got.DiskGB != 900 {
		t.Fatalf("unexpected capacity: %+v", got)
	}
}

func TestCapacityFromHostInfoMissingValues(t *testing.T) {
	got := CapacityFromHostInfo(map[string]any{"cpu": "invalid"})
	if got != (CapacityTotals{}) {
		t.Fatalf("expected zero capacity, got %+v", got)
	}
}
