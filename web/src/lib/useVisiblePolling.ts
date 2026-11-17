import { useEffect, useRef } from 'react'

function isThenable(v: unknown): v is PromiseLike<unknown> {
  return typeof (v as { then?: unknown } | null)?.then === 'function'
}

/**
 * Runs `fn` every `intervalMs`, but ONLY while the browser tab is visible.
 *
 * When the tab is hidden the timer is cleared (no wasted fetches / CPU / battery
 * on a backgrounded tab); when it becomes visible again `fn` runs once
 * immediately (so the UI is fresh on return) and the loop resumes.
 *
 * The loop is self-scheduling: when `fn` returns a promise the next run is
 * armed only after it settles, so a slow or unreachable backend gets one
 * request at a time instead of a new one stacked every interval.
 *
 * Pass `enabled=false` to suspend polling entirely (e.g. a poll that should run
 * only while some operation is in flight). Latest `fn` is always used, so the
 * caller doesn't need to memoize it.
 */
export function useVisiblePolling(fn: () => unknown, intervalMs: number, enabled = true) {
  const saved = useRef(fn)
  saved.current = fn

  useEffect(() => {
    if (!enabled) return
    let timer: ReturnType<typeof setTimeout> | null = null
    let running = false
    let cancelled = false

    const stop = () => { if (timer !== null) { clearTimeout(timer); timer = null } }
    const schedule = () => {
      if (cancelled || document.hidden || running) return
      timer ??= setTimeout(tick, intervalMs)
    }
    const settled = () => { running = false; schedule() }
    function tick() {
      timer = null
      running = true
      let result: unknown
      try {
        result = saved.current()
      } finally {
        if (isThenable(result)) result.then(settled, settled)
        else settled()
      }
    }

    const onVisibility = () => {
      stop()
      if (document.hidden) return
      // A run still in flight re-arms the loop itself when it settles.
      if (!running) tick()
    }

    schedule()
    document.addEventListener('visibilitychange', onVisibility)
    return () => { cancelled = true; stop(); document.removeEventListener('visibilitychange', onVisibility) }
  }, [intervalMs, enabled])
}
