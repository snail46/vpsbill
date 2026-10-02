import { ReactNode, useEffect, useState } from 'react'
import { FilterX, Search } from 'lucide-react'
import { AccountRecord, api, cached } from '../api'
import { t } from '../shared/i18n'

// Admin list filters live in the address bar (/admin/services?status=overdue),
// so a link from another page (the overview cards, a customer's row) opens a
// list already filtered, and a filtered list can be bookmarked or shared.

// navigateAdmin opens an admin page such as /admin/services?status=overdue;
// the shell follows it through the popstate event.
export function navigateAdmin(path: string) {
  if (window.location.pathname + window.location.search !== path) window.history.pushState(null, '', path)
  window.dispatchEvent(new PopStateEvent('popstate'))
}

// adminHref builds a link to an admin page with filters, leaving out empty
// values.
export function adminHref(view: string, filters: Record<string, string | number | undefined> = {}, section = '') {
  const query = new URLSearchParams()
  for (const [key, value] of Object.entries(filters)) if (value !== undefined && value !== '') query.set(key, String(value))
  const text = query.toString()
  return `/admin/${view}${text ? `?${text}` : ''}${section ? `#${section}` : ''}`
}

// AdminLink is a link to another admin page that opens without a reload.
export function AdminLink({ href, className, title, children }: { href: string; className?: string; title?: string; children: ReactNode }) {
  return (
    <a
      href={href}
      className={className}
      title={title}
      onClick={event => {
        if (event.metaKey || event.ctrlKey || event.shiftKey || event.button !== 0) return
        event.preventDefault()
        event.stopPropagation()
        navigateAdmin(href)
      }}
    >
      {children}
    </a>
  )
}

// useUrlFilters keeps a page's filters in the address bar. Changing one
// replaces the history entry, so the back button leaves the page rather than
// stepping through every keystroke.
export function useUrlFilters<K extends string>(keys: readonly K[]) {
  const read = () => {
    const query = new URLSearchParams(window.location.search)
    return Object.fromEntries(keys.map(key => [key, query.get(key) ?? ''])) as Record<K, string>
  }
  const [filters, setFilters] = useState<Record<K, string>>(read)
  const update = (change: (current: Record<K, string>) => Record<K, string>) =>
    setFilters(current => {
      const next = change(current)
      const query = new URLSearchParams(window.location.search)
      for (const key of keys) {
        if (next[key]) query.set(key, next[key])
        else query.delete(key)
      }
      const text = query.toString()
      window.history.replaceState(null, '', `${window.location.pathname}${text ? `?${text}` : ''}${window.location.hash}`)
      return next
    })
  const set = (key: K, value: string) => update(current => ({ ...current, [key]: value }))
  const reset = () => update(() => Object.fromEntries(keys.map(key => [key, ''])) as Record<K, string>)
  const active = keys.filter(key => filters[key]).length
  return { filters, set, reset, active }
}

// useCustomerOptions lists the customers for a customer filter.
export function useCustomerOptions(): Option[] {
  const [customers, setCustomers] = useState<AccountRecord[]>(() => cached<AccountRecord[]>('/api/v1/admin/customers') ?? [])
  useEffect(() => {
    void api<AccountRecord[]>('/api/v1/admin/customers').then(setCustomers).catch(() => {})
  }, [])
  return customers.map(customer => [customer.id, customer.display_name] as Option)
}

// useSection scrolls to the part of a page a link named (#jobs).
export function useSection() {
  useEffect(() => {
    const id = window.location.hash.slice(1)
    if (!id) return
    const timer = window.setTimeout(() => document.getElementById(id)?.scrollIntoView({ behavior: 'smooth', block: 'start' }), 60)
    return () => window.clearTimeout(timer)
  }, [])
}

// matchesAny reports whether value is one of a filter's comma-separated
// values; an empty filter matches everything.
export function matchesAny(filter: string, value: string) {
  return !filter || filter.split(',').includes(value)
}

// includesText is a case-insensitive search over several fields.
export function includesText(query: string, ...fields: (string | undefined | null)[]) {
  const needle = query.trim().toLowerCase()
  return !needle || fields.some(field => (field ?? '').toLowerCase().includes(needle))
}

// withinDays reports whether a timestamp falls on a day between from and to
// (YYYY-MM-DD, either may be empty), counted in UTC+8 like the rest of the
// platform.
export function withinDays(value: string, from: string, to: string) {
  if (!from && !to) return true
  const day = new Date(new Date(value).getTime() + 8 * 3600000).toISOString().slice(0, 10)
  return (!from || day >= from) && (!to || day <= to)
}

// daysAgo is the UTC+8 date n days before today, for date filters.
export function daysAgo(days: number) {
  return new Date(Date.now() + 8 * 3600000 - days * 86400000).toISOString().slice(0, 10)
}

export type Option = [value: string, label: string]

// uniqueOptions lists the distinct non-empty values of a field, sorted.
export function uniqueOptions(values: (string | undefined)[]): Option[] {
  return [...new Set(values.filter((value): value is string => Boolean(value)))]
    .sort((a, b) => a.localeCompare(b, 'zh-CN'))
    .map(value => [value, value])
}

// FilterBar lays out a list's filters with a count of what is shown.
export function FilterBar({ shown, total, active, onReset, children }: { shown: number; total: number; active: number; onReset: () => void; children: ReactNode }) {
  return (
    <div className="filter-bar">
      <div className="filter-fields">{children}</div>
      <div className="filter-summary">
        <span>{active ? t('筛选出 {0} / {1} 条', shown, total) : t('共 {0} 条', total)}</span>
        {active > 0 && (
          <button type="button" className="text-button" onClick={onReset}>
            <FilterX size={13} />{t('清除筛选')}
          </button>
        )}
      </div>
    </div>
  )
}

export function SearchFilter({ label, value, onChange, placeholder }: { label: string; value: string; onChange: (value: string) => void; placeholder?: string }) {
  return (
    <label className="filter-field filter-search">
      <span>{label}</span>
      <div>
        <Search size={14} aria-hidden="true" />
        <input type="search" value={value} placeholder={placeholder} onChange={event => onChange(event.target.value)} />
      </div>
    </label>
  )
}

export function SelectFilter({ label, value, onChange, options, all = t('全部') }: { label: string; value: string; onChange: (value: string) => void; options: Option[]; all?: string }) {
  // A value from a link that is not among the options still shows.
  const known = !value || options.some(([option]) => option === value)
  return (
    <label className="filter-field">
      <span>{label}</span>
      <select value={value} onChange={event => onChange(event.target.value)}>
        <option value="">{all}</option>
        {!known && <option value={value}>{value}</option>}
        {options.map(([option, text]) => (
          <option key={option} value={option}>{text}</option>
        ))}
      </select>
    </label>
  )
}

export function DateRangeFilter({ label, from, to, onFrom, onTo }: { label: string; from: string; to: string; onFrom: (value: string) => void; onTo: (value: string) => void }) {
  return (
    <label className="filter-field filter-dates">
      <span>{label}</span>
      <div>
        <input type="date" value={from} max={to || undefined} onChange={event => onFrom(event.target.value)} aria-label={t('{0}起', label)} />
        <em>{t('至')}</em>
        <input type="date" value={to} min={from || undefined} onChange={event => onTo(event.target.value)} aria-label={t('{0}止', label)} />
      </div>
    </label>
  )
}

export function NumberFilter({ label, value, onChange, placeholder, suffix }: { label: string; value: string; onChange: (value: string) => void; placeholder?: string; suffix?: string }) {
  return (
    <label className="filter-field filter-number">
      <span>{label}</span>
      <div>
        <input type="number" min={0} value={value} placeholder={placeholder} onChange={event => onChange(event.target.value)} />
        {suffix && <em>{suffix}</em>}
      </div>
    </label>
  )
}
