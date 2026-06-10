// Pure sorting + formatting helpers for the AI benchmark results table,
// split out of AIBenchmarkCard so they stay unit-testable without React.

import type { AISlotScore } from '../../api/client'
import { compareWithMissing, type SortDir } from '../../lib/useTableSort'

export type BenchSortKey = 'model' | 'accuracy' | 'latency' | 'cost' | 'score' | 'failure'

// Quality metrics read best from the top, so these columns start descending.
export const BENCH_DESC_FIRST: readonly BenchSortKey[] = ['accuracy', 'score']

// formatCost renders $/1M; small local-energy costs (cents) keep 3 decimals so
// they don't round to "$0.00".
export function formatCost(c?: number): string {
  if (!c || c <= 0) return 'grátis'
  const decimals = c < 0.1 ? 3 : 2
  return `$${c.toFixed(decimals)}/1M`
}

// A result is worth re-running ("Rodar faltantes") when it was left incomplete OR
// failed with a rate limit — the latter also covers results saved before the
// incomplete flag existed, so the button shows for pre-existing rate-limited rows.
export function needsRerun(s: AISlotScore): boolean {
  return !!s.incomplete || /rate limit/i.test(s.failureReason || '')
}

// Per-column comparison. Missing-value semantics differ per column:
// latency 0 means the model never answered (sinks); cost 0 is a legitimate
// "free" (does NOT sink); composite 0 means unscored (sinks); empty failure
// text sinks so rows WITH a failure surface first when sorting by it.
const columnCompare: Record<BenchSortKey, (a: AISlotScore, b: AISlotScore, dir: SortDir) => number> = {
  model: (a, b, dir) => {
    const c = a.model.localeCompare(b.model) || a.provider.localeCompare(b.provider)
    return dir === 'asc' ? c : -c
  },
  accuracy: (a, b, dir) => compareWithMissing(a.accuracy, b.accuracy, dir),
  latency: (a, b, dir) => compareWithMissing(a.avgLatencyMs, b.avgLatencyMs, dir, v => v <= 0),
  cost: (a, b, dir) => compareWithMissing(a.costPer1M ?? 0, b.costPer1M ?? 0, dir),
  score: (a, b, dir) => compareWithMissing(a.composite, b.composite, dir, v => v <= 0),
  failure: (a, b, dir) => compareWithMissing(a.failureReason || '', b.failureReason || '', dir, v => v === ''),
}

// sortScores returns a new array ordered by `key`/`dir`. Universal tie-break:
// composite desc, then model asc, then provider asc — deterministic no matter
// which column is active or what order the backend returned.
export function sortScores(rows: AISlotScore[], key: BenchSortKey, dir: SortDir): AISlotScore[] {
  const primary = columnCompare[key]
  return [...rows].sort((a, b) =>
    primary(a, b, dir)
    || b.composite - a.composite
    || a.model.localeCompare(b.model)
    || a.provider.localeCompare(b.provider)
  )
}
