// Pure helpers for the stats page — kept out of the component for vitest.

// formatWatchTime renders accumulated seconds as a compact "12h 34min" (or
// "34min" under an hour, "—" for zero).
export function formatWatchTime(seconds: number): string {
  if (!seconds || seconds <= 0) return '—'
  const totalMinutes = Math.floor(seconds / 60)
  const hours = Math.floor(totalMinutes / 60)
  const minutes = totalMinutes % 60
  if (hours === 0) return `${minutes}min`
  if (minutes === 0) return `${hours}h`
  return `${hours}h ${minutes}min`
}

// barHeights normalizes a series to 0–100 (% heights for CSS bars). An
// all-zero series maps to all zeros instead of dividing by zero.
export function barHeights(values: number[]): number[] {
  const max = Math.max(0, ...values)
  if (max === 0) return values.map(() => 0)
  return values.map(v => Math.round((v / max) * 100))
}

// monthLabel turns "2026-06" into a short localized label like "jun".
export function monthLabel(month: string, locale: string): string {
  const [y, m] = month.split('-').map(Number)
  if (!y || !m) return month
  return new Date(Date.UTC(y, m - 1, 1)).toLocaleDateString(locale, { month: 'short', timeZone: 'UTC' })
}
