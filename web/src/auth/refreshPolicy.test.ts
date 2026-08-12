import { describe, it, expect } from 'vitest'
import { httpStatusOf, isAuthRejection, refreshBackoffMs, REFRESH_MAX_ATTEMPTS, shouldAttemptRefresh } from './refreshPolicy'

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

describe('shouldAttemptRefresh', () => {
  const fresh = { retried: false, isRefreshCall: false, skipAuthRefresh: false }

  it('lets a plain 401 enter the refresh path', () => {
    expect(shouldAttemptRefresh({ status: 401, ...fresh })).toBe(true)
  })
  it('blocks non-401 responses (transient / 5xx left to retry or caller)', () => {
    expect(shouldAttemptRefresh({ status: undefined, ...fresh })).toBe(false)
    expect(shouldAttemptRefresh({ status: 500, ...fresh })).toBe(false)
    expect(shouldAttemptRefresh({ status: 502, ...fresh })).toBe(false)
    expect(shouldAttemptRefresh({ status: 403, ...fresh })).toBe(false)
    expect(shouldAttemptRefresh({ status: 200, ...fresh })).toBe(false)
  })
  it('blocks the second retry of the same request (request storm guard)', () => {
    expect(shouldAttemptRefresh({ ...fresh, status: 401, retried: true })).toBe(false)
  })
  it('blocks the refresh call itself from refreshing again (recursion guard)', () => {
    expect(shouldAttemptRefresh({ ...fresh, status: 401, isRefreshCall: true })).toBe(false)
  })
  it('blocks session-lifecycle calls (skipAuthRefresh) — the logout↔refresh recursion fix', () => {
    // logout() → DELETE /user/incognito → 401 on a dead session must NOT
    // trigger refresh → logout() → … infinite mutual recursion.
    expect(shouldAttemptRefresh({ ...fresh, status: 401, skipAuthRefresh: true })).toBe(false)
  })
  it('only needs the 401 + skipAuthRefresh flag regardless of retry state', () => {
    expect(shouldAttemptRefresh({ status: 401, retried: true, isRefreshCall: true, skipAuthRefresh: true })).toBe(false)
  })
})
