import { errMessage } from './errMessage'

// Global safety net for promise rejections nobody awaited/caught (fire-and-forget
// handlers, effects calling async loaders). Without it a failed action just does
// nothing — no feedback. Handlers should still catch their own errors; this only
// guarantees the user sees *something*.

const IGNORED_NAMES = new Set(['AbortError', 'CanceledError', 'NotAllowedError'])
const DEDUPE_MS = 5000

// shouldToastRejection filters out rejections that are expected noise rather
// than failures: aborted fetches/axios cancels, media play() interrupted or
// blocked by autoplay policy, and 401s (the auth interceptor already refreshes
// or logs out — a toast would be redundant).
export function shouldToastRejection(reason: unknown): boolean {
  if (reason == null) return false
  const r = reason as { name?: unknown; code?: unknown; response?: { status?: unknown } }
  if (typeof r.name === 'string' && IGNORED_NAMES.has(r.name)) return false
  if (r.code === 'ERR_CANCELED') return false
  if (r.response?.status === 401) return false
  return true
}

type Notify = (message: string) => void

// createRejectionHandler returns the listener with a small dedupe window so a
// burst of identical failures (e.g. a polling loop) shows one toast.
export function createRejectionHandler(notify: Notify, now: () => number = Date.now) {
  let lastMsg = ''
  let lastAt = -Infinity
  return (ev: { reason?: unknown }) => {
    if (!shouldToastRejection(ev.reason)) return
    const msg = errMessage(ev.reason)
    const t = now()
    if (msg === lastMsg && t - lastAt < DEDUPE_MS) return
    lastMsg = msg
    lastAt = t
    notify(msg)
  }
}

// installUnhandledRejectionToast wires the handler on window; returns an
// uninstall function (tests, HMR).
export function installUnhandledRejectionToast(notify: Notify, target: Pick<Window, 'addEventListener' | 'removeEventListener'> = globalThis.window): () => void {
  const handler = createRejectionHandler(notify) as (ev: Event) => void
  target.addEventListener('unhandledrejection', handler)
  return () => target.removeEventListener('unhandledrejection', handler)
}
