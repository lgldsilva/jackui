import { describe, expect, it } from 'vitest'
import { autoFilterSummary } from './watchlist'

const GiB = 1024 * 1024 * 1024

describe('autoFilterSummary', () => {
  it('returns only the label when no filters are set', () => {
    expect(autoFilterSummary({ minResolution: '', maxSizeBytes: 0, codec: '' }, 'Auto')).toBe('Auto')
  })

  it('joins all active filters', () => {
    expect(
      autoFilterSummary({ minResolution: '1080p', maxSizeBytes: 8 * GiB, codec: 'x265' }, 'Auto'),
    ).toBe('Auto · 1080p+ · x265 · ≤8GB')
  })

  it('rounds fractional sizes to one decimal', () => {
    expect(
      autoFilterSummary({ minResolution: '', maxSizeBytes: 1.55 * GiB, codec: '' }, 'Auto'),
    ).toBe('Auto · ≤1.6GB')
  })
})
