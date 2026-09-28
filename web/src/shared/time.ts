// The platform calendar is UTC+8 whatever the browser's time zone: traffic
// months, coupon expiry dates and every time shown on the page.
const TIME_ZONE = 'Asia/Shanghai'

type DateLike = string | number | Date

// sv-SE formats as YYYY-MM-DD HH:mm, which reads the same for everyone.
const dateTimeFormat = new Intl.DateTimeFormat('sv-SE', {
  timeZone: TIME_ZONE,
  year: 'numeric',
  month: '2-digit',
  day: '2-digit',
  hour: '2-digit',
  minute: '2-digit',
})
const dateFormat = new Intl.DateTimeFormat('sv-SE', { timeZone: TIME_ZONE, year: 'numeric', month: '2-digit', day: '2-digit' })

export function formatTime(value: DateLike) {
  return dateTimeFormat.format(new Date(value))
}

// formatDate returns the UTC+8 calendar day, YYYY-MM-DD, which is also what
// a date input expects.
export function formatDate(value: DateLike) {
  return dateFormat.format(new Date(value))
}

// platformMonth is the traffic period, YYYY-MM.
export function platformMonth(value: DateLike = Date.now()) {
  return formatDate(value).slice(0, 7)
}

// startOfDay is midnight UTC+8 of a YYYY-MM-DD day.
export function startOfDay(day: string) {
  return new Date(`${day}T00:00:00+08:00`)
}
