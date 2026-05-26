import { api } from '../api/client'

/**
 * Fire-and-forget client log shipper. Writes both to the console (devtools
 * keep working) AND to the backend via POST /api/diag/log, where it lands in
 * `docker logs jackui` with the user id attached.
 *
 * Why this exists: Safari users hit codec/HEVC issues we can't reproduce
 * locally, and walking them through opening devtools costs more friction than
 * the bug itself. With this, anything we flag client-side shows up server-side
 * automatically — we grep the container log instead of asking for console paste.
 *
 * Failure is silent on purpose: a logging endpoint that crashes the app would
 * be worse than no logging at all.
 */

type Level = 'info' | 'warn' | 'error'

// Coalesce bursts: if the same `msg` fires 10× in 200ms we ship one entry.
// Cheap dedupe so a tight onTimeUpdate loop doesn't drown the server log.
const recent = new Map<string, number>()
const DEDUP_WINDOW_MS = 200

export function clientLog(level: Level, tag: string, msg: string, data?: Record<string, unknown>): void {
  // Console first — keep devtools experience exactly as before.
  const consoleFn = level === 'error' ? console.error : level === 'warn' ? console.warn : console.info
  consoleFn(`[${tag}] ${msg}`, data ?? '')

  // Dedup window
  const key = `${level}:${tag}:${msg}`
  const last = recent.get(key) ?? 0
  const now = Date.now()
  if (now - last < DEDUP_WINDOW_MS) return
  recent.set(key, now)

  // Ship to server. Use the existing axios instance so we get the auth token
  // header automatically. Never await — this should not affect any UX timing.
  api.post('/diag/log', { level, tag, msg, data }).catch(() => {
    // Intentionally silent. If the server is down or auth expired, the user
    // doesn't need a popup about a diagnostic post failing.
  })
}
