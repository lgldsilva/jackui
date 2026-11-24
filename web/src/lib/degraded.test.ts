import { describe, expect, it } from 'vitest'
import { isReadOnlyDegraded } from './degraded'

describe('isReadOnlyDegraded', () => {
  it('is true only for a degraded edge', () => {
    expect(isReadOnlyDegraded({ role: 'edge', degraded: true })).toBe(true)
  })
  it('ignores healthy edges, primaries and missing data', () => {
    expect(isReadOnlyDegraded({ role: 'edge', degraded: false })).toBe(false)
    expect(isReadOnlyDegraded({ role: 'primary', degraded: true })).toBe(false)
    expect(isReadOnlyDegraded({})).toBe(false)
    expect(isReadOnlyDegraded(null)).toBe(false)
    expect(isReadOnlyDegraded(undefined)).toBe(false)
  })
})
