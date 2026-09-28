// Package clock holds the platform's calendar. Every month and day boundary
// (traffic periods, coupon expiry dates, times shown in mail) is in UTC+8,
// whatever time zone the server or database runs in.
package clock

import "time"

// Zone is UTC+8. A fixed zone needs no tzdata, so the Hatch agent binary
// works on hosts without it.
var Zone = time.FixedZone("UTC+8", 8*60*60)

// DatabaseZone is the same zone by IANA name, used as the PostgreSQL session
// time zone. Note that PostgreSQL reads POSIX names like "UTC+8" as UTC-8.
const DatabaseZone = "Asia/Shanghai"

// Month returns the traffic period t falls in, as YYYY-MM.
func Month(t time.Time) string { return t.In(Zone).Format("2006-01") }
