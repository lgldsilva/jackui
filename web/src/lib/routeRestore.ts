// Route-restore policy for the installed PWA. Split out so the "should we
// redirect to the saved route?" decision is unit-testable and so the bug it
// fixes is pinned: on a normal browser REFRESH the current URL is authoritative
// — redirecting to a previously-opened page (the old behaviour) is wrong. We
// restore ONLY in a standalone PWA, where the OS kills the webview and relaunches
// at the start_url ("/"), losing the route.

// isStandalonePWA reports whether the app runs as an installed PWA (display-mode
// standalone, or iOS Safari's navigator.standalone), as opposed to a normal tab.
export function isStandalonePWA(): boolean {
  const mm = globalThis.matchMedia?.('(display-mode: standalone)')
  if (mm?.matches) return true
  return (globalThis.navigator as { standalone?: boolean } | undefined)?.standalone === true
}

// shouldRestoreRoute decides whether RouteRestorer should redirect to the saved
// route. Restores only in a PWA, when authenticated, sitting on "/" with no
// active player deep-link, and a meaningful (non-"/") saved route exists.
export function shouldRestoreRoute(opts: {
  standalone: boolean
  authenticated: boolean
  pathname: string
  search: string
  lastRoute: string
}): boolean {
  if (!opts.standalone || !opts.authenticated) return false
  if (opts.search.includes('play=')) return false
  if (opts.pathname !== '/') return false
  return opts.lastRoute !== '' && opts.lastRoute !== '/'
}
