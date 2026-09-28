package clock

import (
	"testing"
	"time"
)

func TestMonthIsUTCPlus8(t *testing.T) {
	cases := []struct {
		at   time.Time
		want string
	}{
		{time.Date(2026, 9, 30, 15, 59, 59, 0, time.UTC), "2026-09"},
		{time.Date(2026, 9, 30, 16, 0, 0, 0, time.UTC), "2026-10"},
		{time.Date(2026, 12, 31, 16, 0, 0, 0, time.UTC), "2027-01"},
	}
	for _, c := range cases {
		if got := Month(c.at); got != c.want {
			t.Errorf("Month(%s) = %s, want %s", c.at, got, c.want)
		}
	}
}
