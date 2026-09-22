package clicd

import (
	"encoding/json"
	"math"
)

// CapacityTotals is the normalized allocatable capacity used by the scheduler.
// The original host-info document is still retained for diagnostics.
type CapacityTotals struct {
	VCPU   int   `json:"vcpu"`
	RAMMB  int64 `json:"ram_mb"`
	DiskGB int64 `json:"disk_gb"`
}

func CapacityFromHostInfo(info map[string]any) CapacityTotals {
	return CapacityTotals{
		VCPU:   int(numberAt(info, "cpu", "cores")),
		RAMMB:  numberAt(info, "ram", "total_mb"),
		DiskGB: numberAt(info, "disk", "total_gb"),
	}
}

func numberAt(value map[string]any, path ...string) int64 {
	var current any = value
	for _, part := range path {
		object, ok := current.(map[string]any)
		if !ok {
			return 0
		}
		current, ok = object[part]
		if !ok {
			return 0
		}
	}
	switch number := current.(type) {
	case float64:
		if number < 0 || number > math.MaxInt64 {
			return 0
		}
		return int64(number)
	case int:
		return int64(number)
	case int64:
		return number
	case json.Number:
		result, _ := number.Int64()
		return result
	default:
		return 0
	}
}
