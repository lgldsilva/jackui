import { useEffect, useRef } from 'react'

/**
 * Runs `fn` every `intervalMs`, but ONLY while the browser tab is visible.
 *
 * When the tab is hidden the timer is cleared (no wasted fetches / CPU / battery
 * on a backgrounded tab); when it becomes visible again `fn` runs once
 * immediately (so the UI is fresh on return) and the interval resumes.
 *
 * Pass `enabled=false` to suspend polling entirely (e.g. a poll that should run
 * only while some operation is in flight). Latest `fn` is always used, so the
 * caller doesn't need to memoize it.
 */
export function useVisiblePolling(fn: () => void, intervalMs: number, enabled = true) {
  const saved = useRef(fn)
  saved.current = fn

  useEffect(() => {
    if (!enabled) return
    let timer: ReturnType<typeof setInterval> | null = null

    const tick = () => saved.current()
    const start = () => { if (timer === null) timer = setInterval(tick, intervalMs) }
    const stop = () => { if (timer !== null) { clearInterval(timer); timer = null } }

    const onVisibility = () => {
      if (document.hidden) { stop() } else { tick(); start() }
    }

    if (!document.hidden) start()
    document.addEventListener('visibilitychange', onVisibility)
    return () => { stop(); document.removeEventListener('visibilitychange', onVisibility) }
  }, [intervalMs, enabled])
}
