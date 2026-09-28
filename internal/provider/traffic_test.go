package provider

import "testing"

func TestParseTraffic(t *testing.T) {
	cases := []struct {
		in    any
		want  Traffic
		found bool
	}{
		{map[string]any{"rx_bytes": 100, "tx_bytes": 50, "total_used_bytes": 100}, Traffic{RXBytes: 100, TXBytes: 50, TotalBytes: 150, Split: true}, true},
		{map[string]any{"total_used_bytes": 2.5e9}, Traffic{TotalBytes: 2500000000}, true},
		{map[string]any{"rx_bytes": 1}, Traffic{}, false},
		{nil, Traffic{}, false},
	}
	for _, c := range cases {
		got, found := ParseTraffic(c.in)
		if got != c.want || found != c.found {
			t.Errorf("ParseTraffic(%v) = %+v %v", c.in, got, found)
		}
	}
}
