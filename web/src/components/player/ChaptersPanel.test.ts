import { describe, it, expect } from 'vitest'
import { activeChapterIndex } from './ChaptersPanel'
import type { MediaChapter } from '../../api/client'

const chapters: MediaChapter[] = [
  { index: 0, startSec: 0, endSec: 60 },
  { index: 1, startSec: 60, endSec: 180 },
  { index: 2, startSec: 180, endSec: 300 },
]

describe('activeChapterIndex', () => {
  it('returns -1 before the first chapter start', () => {
    expect(activeChapterIndex(chapters, -5)).toBe(-1)
  })

  it('marks the chapter whose start is at or before the current time', () => {
    expect(activeChapterIndex(chapters, 0)).toBe(0)
    expect(activeChapterIndex(chapters, 59)).toBe(0)
    expect(activeChapterIndex(chapters, 60)).toBe(1)
    expect(activeChapterIndex(chapters, 200)).toBe(2)
    expect(activeChapterIndex(chapters, 99999)).toBe(2)
  })

  it('handles an empty chapter list', () => {
    expect(activeChapterIndex([], 10)).toBe(-1)
  })
})
