import { describe, it, expect } from 'vitest'
import { extractTracks, orderPending, basename, totalReadyTracks, type PlaylistGroup } from './playlistTracks'
import type { TorrentInfo } from '../../api/client'

// Minimal TorrentInfo factory — extractTracks only touches `files`.
function info(files: { index: number; path: string; size?: number; isVideo?: boolean }[]): TorrentInfo {
  return {
    files: files.map(f => ({
      index: f.index, path: f.path, size: f.size ?? 1000,
      isVideo: f.isVideo ?? false, downloaded: 0, progress: 0, priority: 'normal',
    })),
  } as unknown as TorrentInfo
}

describe('basename', () => {
  it('returns the last path segment', () => {
    expect(basename('Album/01 - Track.flac')).toBe('01 - Track.flac')
    expect(basename('single.mp3')).toBe('single.mp3')
  })
})

describe('extractTracks', () => {
  it('keeps only playable (audio/video) files and maps fields', () => {
    const tracks = extractTracks(info([
      { index: 0, path: 'Album/cover.jpg' },
      { index: 1, path: 'Album/01.flac', size: 5000 },
      { index: 2, path: 'Album/notes.nfo' },
      { index: 3, path: 'Album/movie.mkv', isVideo: true },
    ]))
    expect(tracks.map(t => t.fileIndex)).toEqual([1, 3])
    expect(tracks[0]).toMatchObject({ fileIndex: 1, name: '01.flac', kind: 'audio', size: 5000 })
    expect(tracks[1]).toMatchObject({ fileIndex: 3, name: 'movie.mkv', kind: 'video' })
  })

  it('orders like the player list: extras last, then natural order', () => {
    const tracks = extractTracks(info([
      { index: 0, path: 'Album/bonus/extra.mp3' },
      { index: 1, path: 'Album/01.mp3' },
      { index: 2, path: 'Album/02.mp3' },
    ]))
    // The "extra" lives under a bonus folder → pushed to the end by filterAndSortFiles.
    expect(tracks.map(t => t.name)).toEqual(['01.mp3', '02.mp3', 'extra.mp3'])
  })

  it('returns empty for null / no files', () => {
    expect(extractTracks(null)).toEqual([])
    expect(extractTracks(info([]))).toEqual([])
  })
})

describe('orderPending', () => {
  const groups: PlaylistGroup[] = [
    { itemIndex: 0, title: 'a', infoHash: 'a', isLocal: false, status: 'ready', tracks: [] },
    { itemIndex: 1, title: 'b', infoHash: 'b', isLocal: false, status: 'pending', tracks: [] },
    { itemIndex: 2, title: 'c', infoHash: 'c', isLocal: false, status: 'pending', tracks: [] },
    { itemIndex: 3, title: 'd', infoHash: 'd', isLocal: false, status: 'loading', tracks: [] },
  ]

  it('puts the current item first, then ascending, skipping non-pending & in-flight', () => {
    expect(orderPending(groups, 2, new Set())).toEqual([2, 1])
  })

  it('excludes items already in flight', () => {
    expect(orderPending(groups, -1, new Set([1]))).toEqual([2])
  })
})

describe('totalReadyTracks', () => {
  it('sums tracks across groups', () => {
    const g: PlaylistGroup[] = [
      { itemIndex: 0, title: 'a', infoHash: 'a', isLocal: false, status: 'ready', tracks: [{ fileIndex: 0, name: 'x', path: 'x', size: 1, kind: 'audio' }] },
      { itemIndex: 1, title: 'b', infoHash: 'b', isLocal: false, status: 'ready', tracks: [{ fileIndex: 0, name: 'y', path: 'y', size: 1, kind: 'audio' }, { fileIndex: 1, name: 'z', path: 'z', size: 1, kind: 'audio' }] },
    ]
    expect(totalReadyTracks(g)).toBe(3)
  })
})
