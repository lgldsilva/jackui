import type { AIBenchmarkCase } from '../../api/client'

// The cases editor uses a plain textarea (one "raw => expected" per line) — far
// less fiddly on mobile than a grid of paired inputs, and trivially round-trips.
// The expected label encodes the structure: "Filme - Ano", "Série - S03E07",
// "Série - E01", or just a bare title (then only the title is scored).
//
// Multi-task: an optional leading "[task]" token (e.g. "[schedule]") selects which
// AI task the case measures — rename (default, no prefix), schedule or identify. A
// line WITHOUT a prefix is the rename task exactly as before, so old saved sets and
// hand-typed lines keep working; the prefix only round-trips the task we now carry.
const TASK_RE = /^\[(\w+)\]\s*/

export function casesToText(cases: AIBenchmarkCase[]): string {
  return cases.map(c => {
    const prefix = c.task && c.task !== 'rename' ? `[${c.task}] ` : ''
    return `${prefix}${c.raw} => ${c.expect}`
  }).join('\n')
}

export function textToCases(text: string): AIBenchmarkCase[] {
  return text.split('\n')
    .map(line => {
      let rest = line
      let task: string | undefined
      const m = TASK_RE.exec(rest)
      if (m) { task = m[1]; rest = rest.slice(m[0].length) }
      const i = rest.indexOf('=>')
      if (i < 0) return null
      const raw = rest.slice(0, i).trim()
      const expect = rest.slice(i + 2).trim()
      return raw ? { raw, expect, ...(task ? { task } : {}) } : null
    })
    .filter((c): c is AIBenchmarkCase => c !== null)
}
