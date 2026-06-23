import { describe, it, expect } from 'vitest'
import { computeAudioDirectUrl } from './useAudioDirectUrl'
import { buildLocalHash, type TorrentInfo } from '../../api/client'

describe('computeAudioDirectUrl', () => {
  const baseInfo: TorrentInfo = {
    infoHash: 'aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa',
    name: 'album',
    files: [{ index: 0, path: 'song.mp3', size: 100, downloaded: 0, progress: 0, priority: 'normal' }],
    totalSize: 100,
    peers: 0,
    seeders: 0,
    downRate: 0,
    upRate: 0,
    progress: 1,
    primaryFile: 0,
  }

  it('returns empty when info is null', () => {
    expect(computeAudioDirectUrl(null, 0, 'tok')).toBe('')
  })

  it('returns empty when selectedFile is negative', () => {
    expect(computeAudioDirectUrl(baseInfo, -1, 'tok')).toBe('')
  })

  it('returns empty when media token is missing', () => {
    expect(computeAudioDirectUrl(baseInfo, 0, '')).toBe('')
  })

  it('returns torrent direct URL for regular hash', () => {
    expect(computeAudioDirectUrl(baseInfo, 0, 'tok')).toBe(
      '/api/stream/aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa/0?token=tok',
    )
  })

  it('returns local file URL for local pseudo-hash', () => {
    const loc = { mount: 'Downloads', path: 'music/song.m4a' }
    const info: TorrentInfo = {
      ...baseInfo,
      infoHash: buildLocalHash(loc.mount, loc.path),
      files: [{ index: 0, path: loc.path, size: 100, downloaded: 0, progress: 0, priority: 'normal' }],
    }
    expect(computeAudioDirectUrl(info, 0, 'tok')).toBe(
      '/api/local/file?mount=Downloads&path=music%2Fsong.m4a&token=tok',
    )
  })

  it('returns empty for malformed local hash', () => {
    const info: TorrentInfo = { ...baseInfo, infoHash: 'local-' }
    expect(computeAudioDirectUrl(info, 0, 'tok')).toBe('')
  })
})
