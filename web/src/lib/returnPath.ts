// Post-login return path that survives a full-page OAuth round-trip.
//
// Password/passkey logins keep the target in router state, but Google OAuth
// leaves the SPA and lands on /auth/google/callback with no state — so a TV
// pairing link (/link?code=…) would be lost and the user dropped on "/".
// Only same-origin app paths are stored (never an absolute URL → no open
// redirect), and only for routes worth resuming (the pairing page).

const KEY = 'jackui.returnPath'
const RESUMABLE = /^\/link(\?|$)/

export function isResumablePath(path: string): boolean {
  return RESUMABLE.test(path)
}

export function rememberReturnPath(path: string): void {
  if (!isResumablePath(path)) return
  try { sessionStorage.setItem(KEY, path) } catch { /* storage blocked: best effort */ }
}

// takeReturnPath returns (and forgets) the remembered path, or fallback.
export function takeReturnPath(fallback: string): string {
  let saved: string | null = null
  try {
    saved = sessionStorage.getItem(KEY)
    sessionStorage.removeItem(KEY)
  } catch { /* storage blocked */ }
  return saved && isResumablePath(saved) ? saved : fallback
}

// fromLocation rebuilds "pathname?search" from the router state ProtectedRoute
// hands to /login (the query carries the pairing code).
export function fromLocation(state: unknown): string {
  const from = (state as { from?: { pathname?: string; search?: string } } | null)?.from
  if (!from?.pathname) return '/'
  return from.pathname + (from.search || '')
}
