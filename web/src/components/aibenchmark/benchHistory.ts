// Pure helpers that turn the durable run-history fields on AISlotScore into the
// labels the benchmark table shows: whether the last run succeeded or errored,
// whether the error persisted, and when the model last succeeded. Kept React-free
// so they unit-test without rendering the card.

import type { AISlotScore } from '../../api/client'
import { formatDate } from '../../lib/format'

export type RunStatus = 'ok' | 'incomplete' | 'error' | 'unknown'

// runStatus is the canonical last-run status. It prefers the backend's recorded
// outcome; for legacy rows (persisted before history existed) it derives a
// best-effort status from the live measurement so the column still says something.
export function runStatus(s: AISlotScore): RunStatus {
  if (s.lastOutcome === 'ok' || s.lastOutcome === 'incomplete' || s.lastOutcome === 'error') {
    return s.lastOutcome
  }
  if (s.incomplete) return 'incomplete'
  if (s.samples > 0) return 'ok'
  if (s.failureReason) return 'error'
  return 'unknown'
}

// lastSuccessLabel: "último OK: 3h atrás" style. Reads 'nunca deu certo' only for
// a genuine hard failure (zero usable replies) with no prior success; empty when
// there's no history at all (legacy rows stay quiet instead of claiming a
// misleading "nunca") OR the last run was 'incomplete' — cut short by a rate
// limit, but it can still have scored real samples (composite > 0), so labeling
// it "nunca deu certo" would contradict the score shown right next to it.
export function lastSuccessLabel(s: AISlotScore): string {
  if (s.lastSuccessAt) return `último OK: ${formatDate(s.lastSuccessAt)}`
  if (runStatus(s) === 'error') return 'nunca deu certo'
  return ''
}

// persistenceLabel surfaces a SUSTAINED failure ("o erro se manteve"): only when
// the model errored on the last run AND the streak is ≥2, e.g.
// "erro persiste: 3 falhas seguidas desde 12 jun".
export function persistenceLabel(s: AISlotScore): string {
  const n = s.consecutiveFailures ?? 0
  if (runStatus(s) !== 'error' || n < 2) return ''
  const since = s.firstFailureAt ? ` desde ${formatDate(s.firstFailureAt)}` : ''
  return `erro persiste: ${n} falhas seguidas${since}`
}

// absoluteDateTime gives the full pt-BR timestamp for a tooltip (the inline labels
// are relative). Empty for missing/unparseable input.
export function absoluteDateTime(iso?: string): string {
  if (!iso) return ''
  const d = new Date(iso)
  if (Number.isNaN(d.getTime())) return ''
  return d.toLocaleString('pt-BR')
}
