// lrc parses LRC-format synced lyrics ([mm:ss.xx] timestamps) into time-ordered
// lines, and finds the active line for a playback position. Pure functions →
// unit-tested (lrc.test.ts) without a DOM.

export type LrcLine = { time: number; text: string }

const TIMESTAMP = /\[(\d{1,2}):(\d{2})(?:[.:](\d{1,3}))?\]/g

// parseLrc turns LRC text into time-sorted lines. A single text line may carry
// multiple timestamps (repeated chorus) → one entry each. Lines without a
// timestamp are skipped (metadata tags like [ar:], [ti:]).
export function parseLrc(lrc: string): LrcLine[] {
  const out: LrcLine[] = []
  for (const raw of lrc.split('\n')) {
    const stamps = [...raw.matchAll(TIMESTAMP)]
    if (stamps.length === 0) continue
    const text = raw.replace(TIMESTAMP, '').trim()
    for (const m of stamps) {
      const min = Number(m[1])
      const sec = Number(m[2])
      const frac = m[3] ? Number((m[3] + '00').slice(0, 3)) / 1000 : 0
      out.push({ time: min * 60 + sec + frac, text })
    }
  }
  return out.sort((a, b) => a.time - b.time)
}

// activeLineIndex returns the index of the last line whose timestamp is ≤
// currentTime (the line currently being sung), or -1 before the first line.
export function activeLineIndex(lines: LrcLine[], currentTime: number): number {
  let idx = -1
  for (let i = 0; i < lines.length; i++) {
    if (lines[i].time <= currentTime) idx = i
    else break
  }
  return idx
}
