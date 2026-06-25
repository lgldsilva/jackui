import { describe, it, expect } from 'vitest'
import { isRetryableGet, retryDelayMs, RETRY_MAX } from './retry'

describe('isRetryableGet', () => {
  it('retries GET on network error (no status)', () => {
    expect(isRetryableGet('get', undefined)).toBe(true)
  })
  it('retries GET on 429 and 5xx', () => {
    expect(isRetryableGet('GET', 429)).toBe(true)
    expect(isRetryableGet('get', 500)).toBe(true)
    expect(isRetryableGet('get', 503)).toBe(true)
  })
  it('does NOT retry GET on 404 / other 4xx', () => {
    expect(isRetryableGet('get', 404)).toBe(false)
    expect(isRetryableGet('get', 400)).toBe(false)
    expect(isRetryableGet('get', 200)).toBe(false)
  })
  it('does NOT retry non-GET methods even on 5xx', () => {
    expect(isRetryableGet('post', 500)).toBe(false)
    expect(isRetryableGet('post', undefined)).toBe(false)
    expect(isRetryableGet('delete', 503)).toBe(false)
    expect(isRetryableGet(undefined, 500)).toBe(false)
  })
})

describe('retryDelayMs', () => {
  it('honours Retry-After (seconds), capped at 5s', () => {
    expect(retryDelayMs(0, 1)).toBe(1000)
    expect(retryDelayMs(0, 30)).toBe(5000) // capped
  })
  it('uses exponential backoff with deterministic jitter when no Retry-After', () => {
    const noJitter = () => 0
    expect(retryDelayMs(0, undefined, noJitter)).toBe(300)
    expect(retryDelayMs(1, undefined, noJitter)).toBe(600)
  })
  it('caps the exponential backoff at 5s', () => {
    expect(retryDelayMs(10, undefined, () => 0)).toBe(5000)
  })
})

describe('RETRY_MAX', () => {
  it('is a small positive bound', () => {
    expect(RETRY_MAX).toBeGreaterThan(0)
    expect(RETRY_MAX).toBeLessThanOrEqual(3)
  })
})
