// Cold-boot route restore, run BEFORE React mounts. On a PWA relaunch (iOS kills
// the webview and reopens at start_url "/"), BrowserRouter would mount SearchPage
// and the in-app RouteRestorer navigates to the saved route a frame later — a
// visible "flash of root". Jumping straight to the saved route here (history
// replaceState) makes BrowserRouter mount on the right screen, no flash. The
// in-app RouteRestorer stays as the post-auth safety net.

import { load } from './storage'

// bootRouteTarget decides where a cold boot should land: the saved lastRoute, but
// only when we're actually sitting on "/" with no player deep-link, and the saved
// route is a real screen (not "/" or an auth page). Pure + exported for tests.
export function bootRouteTarget(pathname: string, search: string, last: string): string | null {
  if (pathname !== '/' || search.includes('play=')) return null
  if (!last || last === '/' || last.startsWith('/login')) return null
  return last
}

export function bootRouteRestore(): void {
  try {
    const target = bootRouteTarget(
      globalThis.location.pathname,
      globalThis.location.search,
      load<string>('lastRoute', ''),
    )
    if (target) globalThis.history.replaceState(null, '', target)
  } catch {
    /* storage/history unavailable — fall back to the in-app RouteRestorer */
  }
}
