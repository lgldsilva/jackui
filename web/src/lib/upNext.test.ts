import { describe, it, expect } from 'vitest'
import { remainingPlayhead, shouldShowUpNext, upNextCountdown } from './upNext'

describe('remainingPlayhead', () => {
  it('clamps invalid numbers to 0', () => {
    expect(remainingPlayhead(NaN, 100)).toBe(0)
    expect(remainingPlayhead(10, 0)).toBe(0)
    expect(remainingPlayhead(120, 100)).toBe(0)
  })
  it('returns time left', () => {
    expect(remainingPlayhead(85, 100)).toBe(15)
  })
})

describe('shouldShowUpNext', () => {
  const base = { currentTime: 90, duration: 100, hasNext: true, dismissed: false }
  it('shows in the last 15s of a long title with a next item', () => {
    expect(shouldShowUpNext(base)).toBe(true)
  })
  it('hides when dismissed, no next, audio, or too short', () => {
    expect(shouldShowUpNext({ ...base, dismissed: true })).toBe(false)
    expect(shouldShowUpNext({ ...base, hasNext: false })).toBe(false)
    expect(shouldShowUpNext({ ...base, audioMode: true })).toBe(false)
    expect(shouldShowUpNext({ ...base, duration: 40, currentTime: 30 })).toBe(false)
  })
  it('hides before the window', () => {
    expect(shouldShowUpNext({ ...base, currentTime: 80 })).toBe(false)
  })
})

describe('upNextCountdown', () => {
  it('ceils remaining seconds', () => {
    expect(upNextCountdown(94.2, 100)).toBe(6)
    expect(upNextCountdown(100, 100)).toBe(0)
  })
})
