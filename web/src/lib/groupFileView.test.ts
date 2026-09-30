import { describe, it, expect } from 'vitest'
import { viewGroupFiles, groupStatusCounts } from './groupFileView'
import type { DownloadEntry } from '../api/downloads'

// Minimal factory — only the fields viewGroupFiles/groupStatusCounts read.
function mk(over: Partial<DownloadEntry>): DownloadEntry {
  return {
    id: 0, userId: 0, infoHash: 'h', fileIndex: 0, filePath: '', fileSize: 0,
    name: '', magnet: '', status: 'downloading', bytesDownloaded: 0, progress: 0,
    createdAt: '',
    ...over,
  } as DownloadEntry
}

describe('viewGroupFiles', () => {
  const files = [
    mk({ id: 1, filePath: 'S01E10.mkv', fileSize: 300, status: 'completed' }),
    mk({ id: 2, filePath: 'S01E02.mkv', fileSize: 100, status: 'downloading', bytesDownloaded: 50 }),
    mk({ id: 3, filePath: 'S01E01.mkv', fileSize: 200, status: 'queued', bytesDownloaded: 0 }),
  ]

  it('filters completed only', () => {
    const r = viewGroupFiles(files, 'completed', 'name', 'asc')
    expect(r.map((d) => d.id)).toEqual([1])
  })

  it('filters active only (not completed)', () => {
    const r = viewGroupFiles(files, 'active', 'name', 'asc')
    expect(r.map((d) => d.id)).toEqual([3, 2]) // E01, E02 in natural order
  })

  it('sorts by name natural (asc)', () => {
    const r = viewGroupFiles(files, 'all', 'name', 'asc')
    expect(r.map((d) => d.id)).toEqual([3, 2, 1]) // E01, E02, E10
  })

  it('sorts by name desc', () => {
    const r = viewGroupFiles(files, 'all', 'name', 'desc')
    expect(r.map((d) => d.id)).toEqual([1, 2, 3])
  })

  it('sorts by size asc', () => {
    const r = viewGroupFiles(files, 'all', 'size', 'asc')
    expect(r.map((d) => d.fileSize)).toEqual([100, 200, 300])
  })

  it('sorts by progress (completed=1 on top in desc)', () => {
    const r = viewGroupFiles(files, 'all', 'progress', 'desc')
    expect(r[0].id).toBe(1) // completed = progress 1
  })

  it('is stable on status ties (preserves input order)', () => {
    const same = [
      mk({ id: 10, filePath: 'b', fileSize: 0, status: 'downloading' }),
      mk({ id: 11, filePath: 'a', fileSize: 0, status: 'downloading' }),
    ]
    // sorting by size (all 0 → tie) keeps the original order
    expect(viewGroupFiles(same, 'all', 'size', 'asc').map((d) => d.id)).toEqual([10, 11])
  })

  it('does not mutate the input array', () => {
    const copy = [...files]
    viewGroupFiles(files, 'all', 'size', 'desc')
    expect(files).toEqual(copy)
  })
})

describe('groupStatusCounts', () => {
  it('counts total, active and completed', () => {
    const files = [
      mk({ status: 'completed' }),
      mk({ status: 'downloading' }),
      mk({ status: 'queued' }),
      mk({ status: 'completed' }),
    ]
    expect(groupStatusCounts(files)).toEqual({ all: 4, active: 2, completed: 2 })
  })

  it('empty list → all zero', () => {
    expect(groupStatusCounts([])).toEqual({ all: 0, active: 0, completed: 0 })
  })
})
