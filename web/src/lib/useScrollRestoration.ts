// Manual scroll restoration. react-router's <ScrollRestoration> needs a data
// router; this app uses a plain BrowserRouter, so we save window.scrollY per
// history entry to sessionStorage and restore it once the page signals that its
// content has loaded (`ready`) — restoring before the list exists would scroll to
// a height that isn't there yet.
//
// Keyed by location.key (unique per history entry, so back/forward to the same
// pathname with different scroll positions each restore correctly). On the first
// entry / hard reload location.key is "default", so we fall back to the pathname —
// a reload still restores. sessionStorage (not localStorage): scroll is volatile
// per-tab state, and it clears on the PWA cold-boot, which is acceptable.

import { useEffect, useLayoutEffect, useRef } from 'react'
import { useLocation } from 'react-router-dom'

const KEY_PREFIX = 'jackui.scroll:'

// scrollKey + clampScroll are pure and exported for unit tests (the rest of the
// hook is DOM-bound). scrollKey prefers the per-entry location.key; "default"
// (first entry / hard reload) falls back to the pathname.
export function scrollKey(key: string, pathname: string): string {
  return KEY_PREFIX + (key && key !== 'default' ? key : 'path:' + pathname)
}

// clampScroll bounds the target to what the document can actually scroll right now
// (maxY = scrollHeight - innerHeight), never negative.
export function clampScroll(target: number, maxY: number): number {
  return Math.min(target, Math.max(0, maxY))
}

function readY(storeKey: string): number {
  try {
    return Number(sessionStorage.getItem(storeKey)) || 0
  } catch {
    return 0
  }
}

function writeY(storeKey: string, y: number): void {
  try {
    sessionStorage.setItem(storeKey, String(y))
  } catch {
    /* private mode / quota — ignore */
  }
}

// useScrollRestoration saves/restores window scroll for the current route. Pass a
// `ready` flag that becomes true once the page's content is rendered (e.g.
// `!loading` or `items.length > 0`) so the restore lands on real content.
export function useScrollRestoration(ready: boolean): void {
  const location = useLocation()
  const storeKey = scrollKey(location.key, location.pathname)
  const restoredKeyRef = useRef('')

  // Persist while scrolling (throttled to one write per frame), and once more when
  // leaving the route or the tab is hidden.
  useEffect(() => {
    let raf = 0
    const onScroll = () => {
      if (raf) return
      raf = requestAnimationFrame(() => {
        raf = 0
        writeY(storeKey, window.scrollY)
      })
    }
    const onHide = () => writeY(storeKey, window.scrollY)
    window.addEventListener('scroll', onScroll, { passive: true })
    window.addEventListener('pagehide', onHide)
    return () => {
      writeY(storeKey, window.scrollY)
      window.removeEventListener('scroll', onScroll)
      window.removeEventListener('pagehide', onHide)
      if (raf) cancelAnimationFrame(raf)
    }
  }, [storeKey])

  // Restore once, when content is ready. Retry across a few frames because async
  // lists grow after mount — keep nudging until the document is tall enough to
  // reach the saved offset (capped so it can't loop forever).
  useLayoutEffect(() => {
    if (!ready || restoredKeyRef.current === storeKey) return
    const target = readY(storeKey)
    if (target <= 0) {
      restoredKeyRef.current = storeKey
      return
    }
    let frames = 0
    const tick = () => {
      const maxY = document.documentElement.scrollHeight - window.innerHeight
      window.scrollTo(0, clampScroll(target, maxY))
      frames += 1
      if (maxY < target && frames < 10) {
        requestAnimationFrame(tick)
      } else {
        restoredKeyRef.current = storeKey
      }
    }
    requestAnimationFrame(tick)
  }, [ready, storeKey])
}
