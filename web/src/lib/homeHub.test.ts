import { describe, it, expect } from 'vitest'
import { isUnfinished, pickContinueWatching, pickRecentlyCompleted, homePlayFileIndex, homeIsEmpty } from './homeHub'
import type { LibraryEntry } from '../api/library'
import type { DownloadEntry } from '../api/downloads'

function lib(over: Partial<LibraryEntry> & Pick<LibraryEntry, 'id'>): LibraryEntry {
  return {
    userId: 1, infoHash: `h${over.id}`, magnet: 'magnet:?xt=urn:btih:abc', name: 'Title',
    primaryFileIndex: 0, lastFileIndex: -1, totalSize: 1, resumeSeconds: 0, durationSeconds: 100,
    kind: 'video', lastPlayedAt: '', addedAt: '', ...over,
  }
}

function dl(over: Partial<DownloadEntry> & Pick<DownloadEntry, 'id' | 'infoHash'>): DownloadEntry {
  return {
    userId: 1, fileIndex: 0, filePath: 'a.mkv', fileSize: 1, name: 'Pack', magnet: 'magnet:?xt=urn:btih:x',
    status: 'completed', bytesDownloaded: 1, progress: 1, createdAt: '', ...over,
  }
}

describe('isUnfinished', () => {
  it('treats 95%+ as finished and 0 as not-started', () => {
    expect(isUnfinished(lib({ id: 1, resumeSeconds: 0 }))).toBe(false)
    expect(isUnfinished(lib({ id: 2, resumeSeconds: 96 }))).toBe(false)
    expect(isUnfinished(lib({ id: 3, resumeSeconds: 40 }))).toBe(true)
  })
})

describe('pickContinueWatching', () => {
  it('keeps unfinished only, respects the limit', () => {
    const rows = [
      lib({ id: 1, resumeSeconds: 20 }),
      lib({ id: 2, resumeSeconds: 99 }),
      lib({ id: 3, resumeSeconds: 10 }),
    ]
    expect(pickContinueWatching(rows, 1).map(e => e.id)).toEqual([1])
    expect(pickContinueWatching(rows).map(e => e.id)).toEqual([1, 3])
  })
})

describe('pickRecentlyCompleted', () => {
  it('dedupes by infoHash and skips non-completed', () => {
    const rows = [
      dl({ id: 1, infoHash: 'AAA', status: 'completed' }),
      dl({ id: 2, infoHash: 'aaa', status: 'completed' }),
      dl({ id: 3, infoHash: 'BBB', status: 'downloading' }),
      dl({ id: 4, infoHash: 'CCC', status: 'completed' }),
    ]
    expect(pickRecentlyCompleted(rows).map(r => r.id)).toEqual([1, 4])
  })
})

describe('homePlayFileIndex', () => {
  it('keeps lastFileIndex 0 (first file in a season pack)', () => {
    expect(homePlayFileIndex({ lastFileIndex: 0, primaryFileIndex: 4 })).toBe(0)
  })
  it('falls back to a positive primary, not the 0 default', () => {
    expect(homePlayFileIndex({ lastFileIndex: -1, primaryFileIndex: 3 })).toBe(3)
    expect(homePlayFileIndex({ lastFileIndex: -1, primaryFileIndex: 0 })).toBeUndefined()
  })
})

describe('homeIsEmpty', () => {
  const none = { continueCount: 0, recentCount: 0, recCount: 0, trendCount: 0, albumCount: 0 }
  it('is empty only when every rail is empty', () => {
    expect(homeIsEmpty(none)).toBe(true)
    expect(homeIsEmpty({ ...none, albumCount: 4 })).toBe(false)
    expect(homeIsEmpty({ ...none, continueCount: 1 })).toBe(false)
  })
})
