import { describe, it, expect } from 'vitest'
import { completedViewCounts } from './DownloadsPage'
import type { DownloadEntry, TorrentInfo } from '../api/client'

const dl = (over: Partial<DownloadEntry>): DownloadEntry =>
  ({ id: 1, status: 'completed', infoHash: '', name: 'x', filePath: '', fileSize: 0, ...over } as DownloadEntry)

const tor = (infoHash: string): TorrentInfo => ({ infoHash } as TorrentInfo)

describe('completedViewCounts', () => {
  it('counts on-disk when the torrent is no longer live', () => {
    const downloads = [dl({ id: 1, infoHash: 'a', status: 'completed' })]
    const c = completedViewCounts(downloads, [])
    expect(c).toEqual({ seeding: 0, onDisk: 1 })
  })

  it('counts seeding when a completed download still has a live torrent', () => {
    const downloads = [dl({ id: 1, infoHash: 'a', status: 'completed' })]
    const c = completedViewCounts(downloads, [tor('a')])
    expect(c).toEqual({ seeding: 1, onDisk: 0 })
  })

  it('counts a streaming-only torrent (no completed row) as seeding', () => {
    const c = completedViewCounts([], [tor('live')])
    expect(c).toEqual({ seeding: 1, onDisk: 0 })
  })

  it('ignores non-completed downloads', () => {
    const downloads = [
      dl({ id: 1, infoHash: 'a', status: 'downloading' }),
      dl({ id: 2, infoHash: 'b', status: 'queued' }),
    ]
    expect(completedViewCounts(downloads, [])).toEqual({ seeding: 0, onDisk: 0 })
  })

  it('groups multiple files of one torrent into a single group', () => {
    const downloads = [
      dl({ id: 1, infoHash: 'a', status: 'completed', filePath: 'S01/E01.mkv' }),
      dl({ id: 2, infoHash: 'a', status: 'completed', filePath: 'S01/E02.mkv' }),
    ]
    expect(completedViewCounts(downloads, [])).toEqual({ seeding: 0, onDisk: 1 })
  })

  it('mixes seeding and on-disk groups', () => {
    const downloads = [
      dl({ id: 1, infoHash: 'a', status: 'completed' }),
      dl({ id: 2, infoHash: 'b', status: 'completed' }),
    ]
    expect(completedViewCounts(downloads, [tor('a')])).toEqual({ seeding: 1, onDisk: 1 })
  })
})
