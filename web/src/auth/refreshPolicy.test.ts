import { describe, it, expect } from 'vitest'
import { httpStatusOf, isAuthRejection, refreshBackoffMs, REFRESH_MAX_ATTEMPTS } from './refreshPolicy'

describe('httpStatusOf', () => {
  it('extracts the status from an axios-style error', () => {
    expect(httpStatusOf({ response: { status: 401 } })).toBe(401)
  })
  it('returns undefined for a transport error (no response)', () => {
    expect(httpStatusOf(new Error('Network Error'))).toBeUndefined()
    expect(httpStatusOf(null)).toBeUndefined()
    expect(httpStatusOf(undefined)).toBeUndefined()
  })
})

describe('isAuthRejection', () => {
  it('is true only for 401/403 (credentials no longer valid)', () => {
    expect(isAuthRejection(401)).toBe(true)
    expect(isAuthRejection(403)).toBe(true)
  })
  it('is false for transient conditions (no response, 5xx, 502)', () => {
    expect(isAuthRejection(undefined)).toBe(false) // backend down during deploy
    expect(isAuthRejection(502)).toBe(false)
    expect(isAuthRejection(503)).toBe(false)
    expect(isAuthRejection(500)).toBe(false)
    expect(isAuthRejection(200)).toBe(false)
    expect(isAuthRejection(404)).toBe(false)
  })
})

describe('refreshBackoffMs', () => {
  it('grows exponentially then caps at 4s', () => {
    expect(refreshBackoffMs(0)).toBe(500)
    expect(refreshBackoffMs(1)).toBe(1000)
    expect(refreshBackoffMs(2)).toBe(2000)
    expect(refreshBackoffMs(3)).toBe(4000)
    expect(refreshBackoffMs(10)).toBe(4000) // capped
  })
  it('spans a few seconds across the attempts (a deploy restart window)', () => {
    let total = 0
    for (let i = 0; i < REFRESH_MAX_ATTEMPTS - 1; i++) total += refreshBackoffMs(i)
    expect(total).toBeGreaterThanOrEqual(3000)
  })
})
