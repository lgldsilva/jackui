import { describe, it, expect } from 'vitest'
import { groupByHash } from './DownloadsPage'
import type { DownloadEntry, TorrentInfo } from '../api/client'

const dl = (over: Partial<DownloadEntry>): DownloadEntry =>
  ({
    id: 1, status: 'downloading', infoHash: '', fileIndex: 0, name: 'x', filePath: '',
    fileSize: 0, bytesDownloaded: 0, progress: 0, magnet: '', createdAt: '', userId: 1,
    ...over,
  } as DownloadEntry)

const tor = (infoHash: string): TorrentInfo => ({ infoHash } as TorrentInfo)

describe('groupByHash', () => {
  it('folds N rows of the same hash into ONE group', () => {
    const groups = groupByHash(
      [
        dl({ id: 1, infoHash: 'a', fileIndex: 0, filePath: 'S01/E01.mkv' }),
        dl({ id: 2, infoHash: 'a', fileIndex: 1, filePath: 'S01/E02.mkv' }),
        dl({ id: 3, infoHash: 'a', fileIndex: 2, filePath: 'S01/E03.mkv' }),
      ],
      [],
    )
    expect(groups).toHaveLength(1)
    expect(groups[0].key).toBe('a')
    expect(groups[0].files).toHaveLength(3)
  })

  it('keeps a single-file torrent as a group of one', () => {
    const groups = groupByHash([dl({ id: 1, infoHash: 'solo', fileIndex: 0 })], [])
    expect(groups).toHaveLength(1)
    expect(groups[0].files).toHaveLength(1)
  })

  it('keeps a whole-torrent (-2) item as a group of one', () => {
    const groups = groupByHash([dl({ id: 1, infoHash: 'whole', fileIndex: -2 })], [])
    expect(groups).toHaveLength(1)
    expect(groups[0].files).toHaveLength(1)
    expect(groups[0].files[0].fileIndex).toBe(-2)
  })

  it('marks a group seeding when its torrent is live', () => {
    const live = groupByHash([dl({ id: 1, infoHash: 'a' })], [tor('a')])
    const dead = groupByHash([dl({ id: 1, infoHash: 'b' })], [tor('a')])
    expect(live[0].seeding).toBe(true)
    expect(dead[0].seeding).toBe(false)
  })

  it('aggregates files into one group regardless of mixed statuses on the same hash', () => {
    const groups = groupByHash(
      [
        dl({ id: 1, infoHash: 'a', fileIndex: 0, status: 'downloading' }),
        dl({ id: 2, infoHash: 'a', fileIndex: 1, status: 'paused' }),
        dl({ id: 3, infoHash: 'a', fileIndex: 2, status: 'completed' }),
      ],
      [],
    )
    expect(groups).toHaveLength(1)
    expect(groups[0].files.map(f => f.status).sort()).toEqual(['completed', 'downloading', 'paused'])
  })

  it('preserves first-seen order between distinct torrents', () => {
    const groups = groupByHash(
      [dl({ id: 1, infoHash: 'b' }), dl({ id: 2, infoHash: 'a' }), dl({ id: 3, infoHash: 'b' })],
      [],
    )
    expect(groups.map(g => g.key)).toEqual(['b', 'a'])
  })

  it('falls back to id-keyed groups when infoHash is missing (never merges)', () => {
    const groups = groupByHash([dl({ id: 1, infoHash: '' }), dl({ id: 2, infoHash: '' })], [])
    expect(groups).toHaveLength(2)
    expect(groups.map(g => g.key)).toEqual(['id:1', 'id:2'])
  })

  it('orders files of a multi-file group naturally by path (E2 before E10)', () => {
    const groups = groupByHash(
      [
        dl({ id: 1, infoHash: 'a', fileIndex: 0, filePath: 'S01/E10.mkv' }),
        dl({ id: 2, infoHash: 'a', fileIndex: 1, filePath: 'S01/E02.mkv' }),
      ],
      [],
    )
    expect(groups[0].files.map(f => f.filePath)).toEqual(['S01/E02.mkv', 'S01/E10.mkv'])
  })
})
