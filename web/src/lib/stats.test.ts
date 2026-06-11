import { describe, expect, it } from 'vitest'
import { formatWatchTime, barHeights, monthLabel } from './stats'

describe('formatWatchTime', () => {
  it('renders dashes for zero/negative', () => {
    expect(formatWatchTime(0)).toBe('—')
    expect(formatWatchTime(-5)).toBe('—')
  })
  it('renders minutes only under an hour', () => {
    expect(formatWatchTime(45 * 60)).toBe('45min')
  })
  it('renders whole hours without minutes', () => {
    expect(formatWatchTime(2 * 3600)).toBe('2h')
  })
  it('renders hours and minutes', () => {
    expect(formatWatchTime(2 * 3600 + 30 * 60 + 59)).toBe('2h 30min')
  })
})

describe('barHeights', () => {
  it('maps all-zero series to zeros', () => {
    expect(barHeights([0, 0, 0])).toEqual([0, 0, 0])
  })
  it('normalizes to percentage of the max', () => {
    expect(barHeights([1, 2, 4])).toEqual([25, 50, 100])
  })
})

describe('monthLabel', () => {
  it('formats a YYYY-MM into a short month name', () => {
    expect(monthLabel('2026-06', 'en-US').toLowerCase()).toContain('jun')
  })
  it('falls back to the raw string when malformed', () => {
    expect(monthLabel('garbage', 'en-US')).toBe('garbage')
  })
})
