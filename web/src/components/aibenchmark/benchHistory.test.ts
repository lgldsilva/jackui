import { describe, it, expect } from 'vitest'
import type { AISlotScore } from '../../api/client'
import { runStatus, lastSuccessLabel, persistenceLabel, absoluteDateTime } from './benchHistory'

const mk = (over: Partial<AISlotScore>): AISlotScore => ({
  slotId: 'groq:m',
  provider: 'groq',
  model: 'm',
  accuracy: 0,
  avgLatencyMs: 0,
  composite: 0,
  samples: 0,
  ...over,
})

describe('runStatus', () => {
  it('prefers the backend-recorded outcome', () => {
    expect(runStatus(mk({ lastOutcome: 'ok' }))).toBe('ok')
    expect(runStatus(mk({ lastOutcome: 'error' }))).toBe('error')
    expect(runStatus(mk({ lastOutcome: 'incomplete' }))).toBe('incomplete')
  })
  it('invalid outcome falls back to the live measurement', () => {
    expect(runStatus(mk({ lastOutcome: 'garbage' }))).not.toBe('garbage')
  })
  it('legacy fallback (no history): incomplete > samples > failure > unknown', () => {
    expect(runStatus(mk({ incomplete: true }))).toBe('incomplete')
    expect(runStatus(mk({ samples: 5 }))).toBe('ok')
    expect(runStatus(mk({ failureReason: 'boom' }))).toBe('error')
    expect(runStatus(mk({}))).toBe('unknown')
  })
})

describe('lastSuccessLabel', () => {
  it('shows the relative date when there was a success', () => {
    const iso = new Date(Date.now() - 3 * 3_600_000).toISOString()
    expect(lastSuccessLabel(mk({ lastSuccessAt: iso, lastOutcome: 'error' }))).toMatch(/^last OK: /)
  })
  it('"never worked" when it ran but never succeeded', () => {
    expect(lastSuccessLabel(mk({ lastOutcome: 'error' }))).toBe('never worked')
  })
  it('empty for a legacy row without any history', () => {
    expect(lastSuccessLabel(mk({ samples: 3 }))).toBe('')
  })
  it('empty for "incomplete" with a real score — it is not "never worked"', () => {
    expect(lastSuccessLabel(mk({ lastOutcome: 'incomplete', samples: 4, composite: 0.7 }))).toBe('')
  })
})

describe('persistenceLabel', () => {
  it('only shows with a current error and streak >= 2', () => {
    expect(persistenceLabel(mk({ lastOutcome: 'error', consecutiveFailures: 1 }))).toBe('')
    expect(persistenceLabel(mk({ lastOutcome: 'ok', consecutiveFailures: 5 }))).toBe('')
    expect(persistenceLabel(mk({ lastOutcome: 'error', consecutiveFailures: 3 }))).toMatch(/3 failures in a row/)
  })
  it('includes "since <date>" when firstFailureAt is present', () => {
    const iso = new Date(Date.now() - 5 * 86_400_000).toISOString()
    expect(persistenceLabel(mk({ lastOutcome: 'error', consecutiveFailures: 4, firstFailureAt: iso }))).toMatch(/since /)
  })
  it('without firstFailureAt it doesn\'t add "since"', () => {
    expect(persistenceLabel(mk({ lastOutcome: 'error', consecutiveFailures: 2 }))).not.toMatch(/since/)
  })
})

describe('absoluteDateTime', () => {
  it('empty for missing or invalid input', () => {
    expect(absoluteDateTime(undefined)).toBe('')
    expect(absoluteDateTime('not-a-date')).toBe('')
  })
  it('formats a valid date', () => {
    expect(absoluteDateTime('2020-01-01T00:00:00Z')).not.toBe('')
  })
})
