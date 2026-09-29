import { describe, it, expect } from 'vitest'
import { sortByLiveMetric, isLiveSortKey, applyDownloadSort } from './downloadSort'
import type { DownloadEntry } from '../api/downloads'

const mk = (id: number, extra: Partial<DownloadEntry>): DownloadEntry => ({
  id, userId: 1, infoHash: `h${id}`, fileIndex: 0, filePath: '', fileSize: 100,
  name: `d${id}`, magnet: '', status: 'downloading', bytesDownloaded: 0,
  progress: 0, createdAt: '2026-01-01', ...extra,
})

describe('isLiveSortKey', () => {
  it('reconhece as chaves ao vivo e rejeita as server-side', () => {
    expect(isLiveSortKey('downRate')).toBe(true)
    expect(isLiveSortKey('upRate')).toBe(true)
    expect(isLiveSortKey('seeders')).toBe(true)
    expect(isLiveSortKey('created_at')).toBe(false)
    expect(isLiveSortKey('name')).toBe(false)
  })
})

describe('sortByLiveMetric', () => {
  it('sorts by downRate desc (fastest first)', () => {
    const items = [mk(1, { downRate: 10 }), mk(2, { downRate: 50 }), mk(3, { downRate: 30 })]
    expect(sortByLiveMetric(items, 'downRate', 'desc').map(d => d.id)).toEqual([2, 3, 1])
  })

  it('sorts by upRate asc', () => {
    const items = [mk(1, { upRate: 10 }), mk(2, { upRate: 50 }), mk(3, { upRate: 30 })]
    expect(sortByLiveMetric(items, 'upRate', 'asc').map(d => d.id)).toEqual([1, 3, 2])
  })

  it('sorts by seeders desc, treating missing as 0', () => {
    const items = [mk(1, { seeders: 5 }), mk(2, {}), mk(3, { seeders: 12 })]
    expect(sortByLiveMetric(items, 'seeders', 'desc').map(d => d.id)).toEqual([3, 1, 2])
  })

  it('is stable: ties preserve the input order (backend created_at)', () => {
    const items = [mk(1, { downRate: 0 }), mk(2, { downRate: 0 }), mk(3, { downRate: 0 })]
    expect(sortByLiveMetric(items, 'downRate', 'desc').map(d => d.id)).toEqual([1, 2, 3])
  })

  it('does not mutate the input array', () => {
    const items = [mk(1, { downRate: 10 }), mk(2, { downRate: 50 })]
    const before = items.map(d => d.id)
    sortByLiveMetric(items, 'downRate', 'desc')
    expect(items.map(d => d.id)).toEqual(before)
  })
})

describe('applyDownloadSort', () => {
  it('sorts client-side when the key is live', () => {
    const items = [mk(1, { downRate: 10 }), mk(2, { downRate: 50 })]
    expect(applyDownloadSort(items, 'downRate', 'desc').map(d => d.id)).toEqual([2, 1])
  })

  it('returns the list untouched (same reference) for a server-side key', () => {
    const items = [mk(1, { downRate: 10 }), mk(2, { downRate: 50 })]
    const out = applyDownloadSort(items, 'created_at', 'desc')
    expect(out).toBe(items) // no reordering: preserves the backend order
  })
})
