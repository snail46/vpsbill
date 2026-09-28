package provider

import (
	"encoding/json"
	"strconv"
	"strings"
)

// Traffic is an instance's traffic for the current period. The platform
// counts both directions: Total is received plus sent.
type Traffic struct {
	RXBytes    int64 `json:"rx_bytes"`
	TXBytes    int64 `json:"tx_bytes"`
	TotalBytes int64 `json:"total_bytes"`
	// Split is false when the backend only reported a total.
	Split bool `json:"split"`
}

// ParseTraffic reads a traffic document from any backend. When both
// directions are present the total is their sum, whatever total the
// backend reports; otherwise total_used_bytes is used as it is.
func ParseTraffic(value any) (Traffic, bool) {
	if value == nil {
		return Traffic{}, false
	}
	data, err := json.Marshal(value)
	if err != nil {
		return Traffic{}, false
	}
	var document struct {
		RX    *json.Number `json:"rx_bytes"`
		TX    *json.Number `json:"tx_bytes"`
		Total *json.Number `json:"total_used_bytes"`
	}
	decoder := json.NewDecoder(strings.NewReader(string(data)))
	decoder.UseNumber()
	if err := decoder.Decode(&document); err != nil {
		return Traffic{}, false
	}
	rx, rxOK := number(document.RX)
	tx, txOK := number(document.TX)
	if rxOK && txOK {
		return Traffic{RXBytes: rx, TXBytes: tx, TotalBytes: rx + tx, Split: true}, true
	}
	if total, ok := number(document.Total); ok {
		return Traffic{TotalBytes: total}, true
	}
	return Traffic{}, false
}

func number(value *json.Number) (int64, bool) {
	if value == nil {
		return 0, false
	}
	if whole, err := value.Int64(); err == nil {
		return whole, true
	}
	if float, err := strconv.ParseFloat(value.String(), 64); err == nil {
		return int64(float), true
	}
	return 0, false
}
