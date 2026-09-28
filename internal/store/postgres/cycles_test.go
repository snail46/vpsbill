package postgres

import (
	"testing"
	"time"

	"vpsbill/internal/clock"
)

func TestParseBillingCycle(t *testing.T) {
	for cycle, want := range map[string]string{
		"monthly": "monthly", "annual": "annual", "m3": "quarterly", "m12": "annual", "m24": "m24",
		"d7": "d7", "d365": "d365", "d0": "", "d366": "", "m61": "", "m03": "", "weekly": "", "": "", "d": "",
	} {
		if got := NormalizeBillingCycle(cycle); got != want {
			t.Errorf("NormalizeBillingCycle(%q) = %q, want %q", cycle, got, want)
		}
	}
	start := time.Date(2026, 1, 31, 12, 0, 0, 0, clock.Zone)
	if got := addBillingCycle(start, "d7"); !got.Equal(start.AddDate(0, 0, 7)) {
		t.Errorf("d7 = %v", got)
	}
	if got := addBillingCycle(start, "m24"); !got.Equal(time.Date(2028, 1, 31, 12, 0, 0, 0, clock.Zone)) {
		t.Errorf("m24 = %v", got)
	}
	if BillingCycleName("d15") != "15 天" || BillingCycleName("m24") != "24 个月" || BillingCycleName("quarterly") != "季付" {
		t.Error("cycle names")
	}
}

func TestProrateToLease(t *testing.T) {
	start := time.Date(2026, 9, 28, 10, 0, 0, 0, clock.Zone)
	twoMonths := addMonthsClamped(start, 2)
	// The example from the hosting rules: a ¥30 quarter with two months of
	// lease left costs ¥20; a ¥10 month still costs ¥10.
	if amount, end := ProrateToLease(3000, start, &twoMonths, "quarterly"); amount != 2000 || !end.Equal(twoMonths) {
		t.Fatalf("quarter cut to two months = %d until %v", amount, end)
	}
	if amount, end := ProrateToLease(1000, start, &twoMonths, "monthly"); amount != 1000 || !end.Equal(addMonthsClamped(start, 1)) {
		t.Fatalf("month inside the lease = %d until %v", amount, end)
	}
	// The part month counts by its own length: 15 of the 31 days from
	// Oct 28 to Nov 28, so (1 + 15/31) / 3 of the price.
	lease := time.Date(2026, 10, 28, 10, 0, 0, 0, clock.Zone).Add(15 * 24 * time.Hour)
	if amount, _ := ProrateToLease(3000, start, &lease, "quarterly"); amount != 1484 {
		t.Fatalf("one month and 15 days of a quarter = %d", amount)
	}
	tenDays := start.AddDate(0, 0, 10)
	if amount, _ := ProrateToLease(3000, start, &tenDays, "d30"); amount != 1000 {
		t.Fatalf("ten of thirty days = %d", amount)
	}
	if amount, _ := ProrateToLease(3000, start, nil, "annual"); amount != 3000 {
		t.Fatal("no lease must not prorate")
	}
	expires := time.Date(2026, 11, 27, 0, 0, 0, 0, time.UTC)
	if end := LeaseEnd(&expires); !end.Equal(time.Date(2026, 11, 28, 0, 0, 0, 0, clock.Zone)) {
		t.Fatalf("lease end = %v", end)
	}
}
