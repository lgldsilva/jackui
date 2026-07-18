import { describe, expect, it } from 'vitest'
import { buildLocalHash, streamFileURL, streamHLSMasterURL, type TorrentInfo } from '../../api/client'
import { computeIsTranscoded } from './mediaUrls'

function localInfo(kind: 'direct' | 'hls', path = 'Movies/My Film 4K.mp4'): TorrentInfo {
  return {
    infoHash: buildLocalHash('Media', path),
    name: path,
    totalSize: 0,
    files: [{ index: 0, path, size: 0, isVideo: true, downloaded: 0, progress: 1, priority: 'normal' }],
    peers: 0,
    seeders: 0,
    downRate: 0,
    upRate: 0,
    progress: 1,
    primaryFile: 0,
    localPlaybackKind: kind,
  }
}

const decision = (info: TorrentInfo, forceH264 = false) => computeIsTranscoded({
  info,
  selectedFile: 0,
  transcodeAudio: null,
  forceH264,
  burnSubTrack: null,
  probe: null,
})

describe('local playback URL decisions', () => {
  it('trusts an authoritative direct decision for a 4K local filename', () => {
    expect(decision(localInfo('direct'))).toBe(false)
  })

  it('trusts an authoritative HLS decision even for a native-looking file', () => {
    expect(decision(localInfo('hls', 'Movies/film.mp4'))).toBe(true)
  })

  it('forceH264 overrides direct playback and builds a distinct local HLS URL', () => {
    const info = localInfo('direct')
    expect(decision(info, true)).toBe(true)
    expect(streamHLSMasterURL(info.infoHash, 0, 'media-token')).toBe(
      '/api/local/hls/index.m3u8?mount=Media&path=Movies%2FMy%20Film%204K.mp4&token=media-token',
    )
    expect(streamHLSMasterURL(info.infoHash, 0, 'media-token')).not.toBe(streamFileURL(info.infoHash, 0, 'media-token'))
  })

  it('includes a playback ID in the HLS master URL when supplied', () => {
    const info = localInfo('hls')
    expect(streamHLSMasterURL(info.infoHash, 0, 'media-token', undefined, 'viewer-a')).toContain('playback=viewer-a')
  })
})
