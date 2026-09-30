import { describe, it, expect } from 'vitest'
import { filterAndSortFiles, parseEpisodeTag } from './playerFormat'
import { buildMediaQueue } from './playerHooks'
import { chapterSeekTargets } from './ChaptersPanel'
import type { MediaChapter, StreamFile } from '../../api/client'

// Torrent with episodes OUT of order in the indices — the real case that made the
// "Next" button skip episodes: the displayed list sorts by SxxEyy, but the old
// queue followed the raw file order.
const mk = (index: number, path: string, size: number, isVideo: boolean): StreamFile =>
  ({ index, path, size, isVideo, downloaded: 0, progress: 0, priority: 'normal' })

const files = [
  mk(0, 'Show/S01E03.mkv', 300, true),
  mk(1, 'Show/S01E01.mkv', 100, true),
  mk(2, 'Show/extras/Making of.mkv', 50, true),
  mk(3, 'Show/S01E02.mkv', 200, true),
  mk(4, 'Show/poster.jpg', 1, false),
]

describe('filterAndSortFiles', () => {
  it('sorts by episode with extras at the end (the visible list order)', () => {
    const out = filterAndSortFiles(files, { filter: '', typeFilter: 'all', sortBySize: false, sizeDesc: true })
    expect(out.map(f => f.index)).toEqual([1, 3, 0, 4, 2])
  })

  it('respects the size sort when enabled', () => {
    const out = filterAndSortFiles(files, { filter: '', typeFilter: 'video', sortBySize: true, sizeDesc: true })
    expect(out.map(f => f.index)).toEqual([0, 3, 1, 2])
  })

  it('filters by text and by episode tag', () => {
    const out = filterAndSortFiles(files, { filter: 's01e02', typeFilter: 'all', sortBySize: false, sizeDesc: true })
    expect(out.map(f => f.index)).toEqual([3])
  })
})

describe('buildMediaQueue over the displayed order', () => {
  const ordered = filterAndSortFiles(files, { filter: '', typeFilter: 'all', sortBySize: false, sizeDesc: true })

  it('next follows the visible list: E01 → E02 → E03 (not index order)', () => {
    const fromE01 = buildMediaQueue(ordered, 1)
    expect(fromE01.nextIdx).toBe(3) // E02
    const fromE02 = buildMediaQueue(ordered, 3)
    expect(fromE02.prevIdx).toBe(1) // E01
    expect(fromE02.nextIdx).toBe(0) // E03
  })

  it('excludes non-playable files from the queue', () => {
    const q = buildMediaQueue(ordered, 1)
    expect(q.indices).not.toContain(4) // poster.jpg out
  })

  it('file outside the queue → cursor -1 and no next/prev', () => {
    const q = buildMediaQueue(ordered, 4)
    expect(q.cursor).toBe(-1)
    expect(q.nextIdx).toBe(-1)
    expect(q.prevIdx).toBe(-1)
  })
})

describe('parseEpisodeTag', () => {
  it('normalizes SxxEyy variations', () => {
    expect(parseEpisodeTag('Show.s1e2.mkv')).toBe('S01E02')
    expect(parseEpisodeTag('Show S01 E10.mkv')).toBe('S01E10')
    expect(parseEpisodeTag('Filme.2024.mkv')).toBeNull()
  })
})

describe('chapterSeekTargets', () => {
  const chapters: MediaChapter[] = [
    { index: 0, startSec: 0, endSec: 60 },
    { index: 1, startSec: 60, endSec: 180 },
    { index: 2, startSec: 180, endSec: 300 },
  ]

  it('advances to the start of the next chapter', () => {
    expect(chapterSeekTargets(chapters, 30).nextSec).toBe(60)
    expect(chapterSeekTargets(chapters, 60).nextSec).toBe(180)
  })

  it('on the last chapter there is no next', () => {
    expect(chapterSeekTargets(chapters, 200).nextSec).toBeNull()
  })

  it('>3s into the chapter, prev goes back to ITS start', () => {
    expect(chapterSeekTargets(chapters, 70).prevSec).toBe(60)
  })

  it('at the chapter start, prev goes to the previous chapter', () => {
    expect(chapterSeekTargets(chapters, 61).prevSec).toBe(0)
  })

  it('at the video start there is no previous', () => {
    expect(chapterSeekTargets(chapters, 1).prevSec).toBeNull()
  })

  it('empty list disables both', () => {
    expect(chapterSeekTargets([], 10)).toEqual({ prevSec: null, nextSec: null })
  })
})
