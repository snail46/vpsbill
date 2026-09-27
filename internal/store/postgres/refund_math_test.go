package postgres

import (
	"testing"
	"time"
)

func TestCouponDiscount(t *testing.T) {
	cases := []struct {
		kind        string
		value, unit int64
		want        int64
	}{
		{"percent", 20, 1000, 200},
		{"percent", 15, 999, 150},
		{"percent", 99, 100, 99},
		{"amount", 300, 1000, 300},
		{"amount", 5000, 1000, 999}, // the buyer always pays at least 0.01
		{"amount", 1, 1, 0},
		{"bogus", 10, 1000, 0},
	}
	for _, c := range cases {
		if got := CouponDiscount(c.kind, c.value, c.unit); got != c.want {
			t.Errorf("CouponDiscount(%s,%d,%d) = %d, want %d", c.kind, c.value, c.unit, got, c.want)
		}
	}
}

func TestRefundSettle(t *testing.T) {
	start := time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)
	end := start.AddDate(0, 0, 30)
	escrow := func() refundEscrow {
		return refundEscrow{gross: 3000, share: 2400, start: start, end: end}
	}

	// Within the first day nothing was released; the current day counts as
	// used, so 29 of 30 days come back and the host keeps a day's share.
	e := escrow()
	e.settle(start.Add(30*time.Minute), false)
	if e.refund != 2900 || e.hostTarget != 80 {
		t.Fatalf("first day: refund %d host %d", e.refund, e.hostTarget)
	}

	// A full refund returns everything still held and pays the host nothing.
	e = escrow()
	e.settle(start.Add(30*time.Minute), true)
	if e.refund != 3000 || e.hostTarget != 0 {
		t.Fatalf("full: refund %d host %d", e.refund, e.hostTarget)
	}

	// Ten and a half days in with ten days released: 19 days come back.
	e = escrow()
	e.releasedGross, e.releasedHost = 1000, 800
	e.settle(start.Add(10*24*time.Hour+12*time.Hour), false)
	if e.refund != 1900 || e.hostTarget != 880 {
		t.Fatalf("day 11: refund %d host %d", e.refund, e.hostTarget)
	}

	// A renewal paid in advance has not started: all of it comes back.
	e = refundEscrow{gross: 3000, share: 2400, start: end, end: end.AddDate(0, 0, 30)}
	e.settle(start.Add(5*24*time.Hour), false)
	if e.refund != 3000 || e.hostTarget != 0 {
		t.Fatalf("future period: refund %d host %d", e.refund, e.hostTarget)
	}

	// Past the end nothing is left to refund.
	e = escrow()
	e.releasedGross, e.releasedHost = 3000, 2400
	e.settle(end.Add(time.Hour), false)
	if e.refund != 0 || e.hostTarget != 2400 {
		t.Fatalf("ended: refund %d host %d", e.refund, e.hostTarget)
	}
}
