// Pure helpers for the refresh-token flow, split out so the "when do we log the
// user out?" decision is unit-testable without React/axios.
//
// The bug they fix: during a deploy the backend is briefly down. If the access
// token expires in that window, a request 401s → the interceptor refreshes →
// the /auth/refresh POST hits the restarting backend and fails with a NETWORK
// error or 502 (NOT 401). The old code treated ANY refresh failure as an auth
// failure and logged the user out. A transient failure must instead be retried
// (safe thanks to the backend's rotation grace window) and, if still failing,
// left alone — the session stays and recovers once the backend is back.

export const REFRESH_MAX_ATTEMPTS = 4

// httpStatusOf extracts an HTTP status from an axios-style error, or undefined
// for a transport error (no response: network down / timeout / CORS).
export function httpStatusOf(e: unknown): number | undefined {
  return (e as { response?: { status?: number } } | null | undefined)?.response?.status
}

// isAuthRejection reports a genuine "your credentials are no longer valid"
// response — the only server reply that should drop the session. A missing
// response (transient) or a 5xx is NOT a rejection.
export function isAuthRejection(status: number | undefined): boolean {
  return status === 401 || status === 403
}

// refreshBackoffMs is the wait before refresh retry `attempt` (0-based):
// 0.5s, 1s, 2s, capped at 4s. Spans a typical deploy's restart window.
export function refreshBackoffMs(attempt: number): number {
  return Math.min(500 * 2 ** attempt, 4000)
}
