package postgres

import (
	"strconv"
	"time"

	"vpsbill/internal/clock"
)

// Billing cycles are the four named ones or a custom length: "d<N>" for N
// days (1-365) or "m<N>" for N months (1-60). A custom month length that
// matches a named cycle is stored under the name.
var namedCycleMonths = map[string]int{"monthly": 1, "quarterly": 3, "semiannual": 6, "annual": 12}

// ParseBillingCycle returns the cycle's length in months or days (one of the
// two is zero) and whether the cycle is valid.
func ParseBillingCycle(cycle string) (months, days int, ok bool) {
	if months, ok := namedCycleMonths[cycle]; ok {
		return months, 0, true
	}
	if len(cycle) < 2 || len(cycle) > 4 || cycle[1] == '0' {
		return 0, 0, false
	}
	n, err := strconv.Atoi(cycle[1:])
	if err != nil {
		return 0, 0, false
	}
	switch {
	case cycle[0] == 'd' && n >= 1 && n <= 365:
		return 0, n, true
	case cycle[0] == 'm' && n >= 1 && n <= 60:
		return n, 0, true
	}
	return 0, 0, false
}

// NormalizeBillingCycle maps "m1", "m3", "m6" and "m12" to their names and
// returns "" for an invalid cycle.
func NormalizeBillingCycle(cycle string) string {
	months, _, ok := ParseBillingCycle(cycle)
	if !ok {
		return ""
	}
	for name, length := range namedCycleMonths {
		if cycle[0] == 'm' && months == length {
			return name
		}
	}
	return cycle
}

// QuoteLease marks each plan price that the node's lease cuts short with
// what a buyer ordering at now would pay and until when.
func (n *HostedNode) QuoteLease(now time.Time) {
	expires, err := time.ParseInLocation("2006-01-02", n.ExpiresAt, clock.Zone)
	if err != nil {
		return
	}
	lease := LeaseEnd(&expires)
	for i := range n.Plans {
		for j := range n.Plans[i].Prices {
			price := &n.Plans[i].Prices[j]
			if charge, end := ProrateToLease(price.AmountMinor, now, lease, price.BillingCycle); charge != price.AmountMinor {
				price.ChargeMinor, price.PeriodEnd = &charge, &end
			}
		}
	}
}

// BillingCycleName is the cycle as customers read it.
func BillingCycleName(cycle string) string {
	names := map[string]string{"monthly": "月付", "quarterly": "季付", "semiannual": "半年付", "annual": "年付"}
	if name, ok := names[cycle]; ok {
		return name
	}
	months, days, ok := ParseBillingCycle(cycle)
	switch {
	case !ok:
		return cycle
	case days > 0:
		return strconv.Itoa(days) + " 天"
	default:
		return strconv.Itoa(months) + " 个月"
	}
}

func addBillingCycle(value time.Time, cycle string) time.Time {
	months, days, ok := ParseBillingCycle(cycle)
	switch {
	case !ok:
		return addMonthsClamped(value, 1)
	case days > 0:
		return value.AddDate(0, 0, days)
	default:
		return addMonthsClamped(value, months)
	}
}

// LeaseEnd is when a hosted node's lease runs out: the end of its expiry
// date in platform time.
func LeaseEnd(expires *time.Time) *time.Time {
	if expires == nil {
		return nil
	}
	end := time.Date(expires.Year(), expires.Month(), expires.Day()+1, 0, 0, 0, 0, clock.Zone)
	return &end
}

// ProrateToLease charges for the part of a cycle starting at start that fits
// before leaseEnd. Month cycles count whole calendar months plus the used
// share of the last month, so a quarter cut to two months costs two thirds.
// It returns the amount and the period end, which is the cycle end when the
// lease outlasts the cycle.
func ProrateToLease(amount int64, start time.Time, leaseEnd *time.Time, cycle string) (int64, time.Time) {
	full := addBillingCycle(start, cycle)
	if leaseEnd == nil || !leaseEnd.Before(full) {
		return amount, full
	}
	end := *leaseEnd
	if !end.After(start) {
		return 0, start
	}
	months, days, _ := ParseBillingCycle(cycle)
	var used float64
	if days > 0 {
		used = end.Sub(start).Hours() / 24 / float64(days)
	} else {
		whole := 0
		for whole < months && !addMonthsClamped(start, whole+1).After(end) {
			whole++
		}
		from, to := addMonthsClamped(start, whole), addMonthsClamped(start, whole+1)
		used = (float64(whole) + float64(end.Sub(from))/float64(to.Sub(from))) / float64(months)
	}
	charged := int64(float64(amount)*used + 0.5)
	if charged < 1 && amount > 0 {
		charged = 1
	}
	if charged > amount {
		charged = amount
	}
	return charged, end
}
