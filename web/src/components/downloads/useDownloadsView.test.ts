import { describe, it, expect } from 'vitest'
import { renderHook } from '@testing-library/react'
import { useDownloadsView } from './useDownloadsView'
import type { DownloadEntry, TorrentInfo } from '../../api/client'

const dl = (over: Partial<DownloadEntry> & { id: number }): DownloadEntry => ({
  status: 'downloading',
  infoHash: 'hash',
  fileIndex: 0,
  name: 'x',
  filePath: '',
  fileSize: 1000,
  bytesDownloaded: 0,
  progress: 0,
  magnet: '',
  createdAt: '',
  userId: 1,
  ...over,
} as DownloadEntry)

const torrent = (over: Partial<TorrentInfo> & { infoHash: string }): TorrentInfo => ({
  name: 't',
  status: 'downloading',
  progress: 0,
  downRate: 0,
  upRate: 0,
  peers: 0,
  ...over,
} as TorrentInfo)

const defaultParams = {
  items: [] as DownloadEntry[],
  torrents: [] as TorrentInfo[],
  sortCol: 'created_at',
  sortDir: 'desc',
  maxActive: 0,
  activeTab: 'downloading' as const,
  completedFilter: 'all' as const,
}

describe('useDownloadsView', () => {
  it('aba downloading contém apenas downloading + queued, nunca paused/failed', () => {
    const items = [
      dl({ id: 1, status: 'downloading' }),
      dl({ id: 2, status: 'queued' }),
      dl({ id: 3, status: 'paused' }),
      dl({ id: 4, status: 'failed' }),
    ]
    const { result } = renderHook(() => useDownloadsView({ ...defaultParams, items }))
    expect(result.current.tabDownloads.downloading.map(d => d.id)).toEqual([1, 2])
    expect(result.current.tabDownloads.paused.map(d => d.id)).toEqual([3])
    expect(result.current.tabDownloads.failed.map(d => d.id)).toEqual([4])
  })

  it('torrents de streaming pausados aparecem na aba paused, não em downloading', () => {
    const torrents = [
      torrent({ infoHash: 'a', status: 'downloading' }),
      torrent({ infoHash: 'b', status: 'paused' }),
      torrent({ infoHash: 'c', status: 'complete' }),
    ]
    const { result } = renderHook(() => useDownloadsView({ ...defaultParams, torrents }))
    expect(result.current.tabTorrents.downloading.map(t => t.infoHash)).toEqual(['a'])
    expect(result.current.tabTorrents.paused.map(t => t.infoHash)).toEqual(['b'])
    expect(result.current.tabTorrents.completed.map(t => t.infoHash)).toEqual(['c'])
    expect(result.current.tabCounts.downloading).toBe(1)
    expect(result.current.tabCounts.paused).toBe(1)
    expect(result.current.tabCounts.completed).toBe(1)
  })

  it('displayTorrents omite torrentes que possuem DownloadEntry', () => {
    const items = [dl({ id: 1, infoHash: 'a' })]
    const torrents = [
      torrent({ infoHash: 'a' }),
      torrent({ infoHash: 'b' }),
    ]
    const { result } = renderHook(() => useDownloadsView({ ...defaultParams, items, torrents }))
    expect(result.current.tabTorrents.all.map(t => t.infoHash)).toEqual(['b'])
  })

  it('tabCounts.paused soma streaming paused + downloads paused', () => {
    const items = [
      dl({ id: 1, infoHash: 'a', status: 'paused' }),
      dl({ id: 2, infoHash: 'a', status: 'paused' }),
      dl({ id: 3, infoHash: 'b', status: 'paused' }),
    ]
    const torrents = [torrent({ infoHash: 'c', status: 'paused' })]
    const { result } = renderHook(() => useDownloadsView({ ...defaultParams, items, torrents }))
    // 2 torrents pausados em background (a e b) + 1 streaming pausado (c)
    expect(result.current.tabCounts.paused).toBe(3)
  })

  it('stalledCount considera downRate === 0 e bytesDownloaded < fileSize', () => {
    const items = [
      dl({ id: 1, status: 'downloading', downRate: 0, bytesDownloaded: 100, fileSize: 1000 }),
      dl({ id: 2, status: 'downloading', downRate: 100, bytesDownloaded: 100, fileSize: 1000 }),
      dl({ id: 3, status: 'downloading', downRate: 0, bytesDownloaded: 1000, fileSize: 1000 }),
    ]
    const { result } = renderHook(() => useDownloadsView({ ...defaultParams, items }))
    expect(result.current.stalledCount).toBe(1)
  })
})
