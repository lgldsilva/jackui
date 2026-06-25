// Pure retry policy for idempotent GET requests, consumed by the axios response
// interceptor in http.ts. Kept dependency-free so it can be unit-tested without
// mocking axios. Mirrors the backend internal/httpretry package: only GETs are
// retried, and only on transient failures (network error/timeout, 429, 5xx).

export const RETRY_MAX = 2
const RETRY_BASE_MS = 300
const RETRY_CAP_MS = 5000

// isRetryableGet reports whether a failed request should be retried: only GETs
// (idempotent), and only on a transient condition — no response (network
// error/timeout), 429, or 5xx. POSTs and 4xx (except 429) are never retried.
export function isRetryableGet(method: string | undefined, status: number | undefined): boolean {
  if ((method ?? '').toLowerCase() !== 'get') return false
  if (status === undefined) return true // network error / timeout
  return status === 429 || status >= 500
}

// retryDelayMs picks the backoff before the next attempt (0-based): the server's
// Retry-After when present (typical on 429), else exponential base*2^attempt
// with jitter, capped. rand is injectable for deterministic tests.
export function retryDelayMs(attempt: number, retryAfterSec?: number, rand: () => number = Math.random): number {
  if (retryAfterSec !== undefined && Number.isFinite(retryAfterSec) && retryAfterSec > 0) {
    return Math.min(retryAfterSec * 1000, RETRY_CAP_MS)
  }
  const backoff = RETRY_BASE_MS * 2 ** attempt + rand() * RETRY_BASE_MS
  return Math.min(backoff, RETRY_CAP_MS)
}
