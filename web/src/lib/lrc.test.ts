import { describe, it, expect } from 'vitest'
import { parseLrc, activeLineIndex } from './lrc'

describe('parseLrc', () => {
  it('parses timestamped lines sorted by time', () => {
    const lrc = '[ti:Song]\n[00:05.00]Line B\n[00:01.50]Line A\n'
    expect(parseLrc(lrc)).toEqual([
      { time: 1.5, text: 'Line A' },
      { time: 5, text: 'Line B' },
    ])
  })

  it('expands multiple timestamps on the same line', () => {
    const lines = parseLrc('[00:01.00][00:10.00]Chorus')
    expect(lines.map((l) => l.time)).toEqual([1, 10])
    expect(lines.every((l) => l.text === 'Chorus')).toBe(true)
  })

  it('skips metadata / untimed lines', () => {
    expect(parseLrc('[ar:Artist]\nno timestamp here')).toEqual([])
  })

  it('handles colon-fraction and missing fraction', () => {
    expect(parseLrc('[01:02]X')).toEqual([{ time: 62, text: 'X' }])
  })
})

describe('activeLineIndex', () => {
  const lines = [
    { time: 1, text: 'a' },
    { time: 5, text: 'b' },
    { time: 9, text: 'c' },
  ]
  it('returns -1 before the first line', () => {
    expect(activeLineIndex(lines, 0)).toBe(-1)
  })
  it('returns the last line whose time <= currentTime', () => {
    expect(activeLineIndex(lines, 1)).toBe(0)
    expect(activeLineIndex(lines, 6)).toBe(1)
    expect(activeLineIndex(lines, 100)).toBe(2)
  })
  it('handles an empty list', () => {
    expect(activeLineIndex([], 5)).toBe(-1)
  })
})
