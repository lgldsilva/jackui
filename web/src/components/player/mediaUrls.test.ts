import { describe, expect, it } from 'vitest'
import { buildLocalHash, streamFileURL, streamHLSMasterURL, subtitleDownloadURL, type TorrentInfo } from '../../api/client'
import { burnSubTranscodeURL, computeIsTranscoded, computeMediaUrls } from './mediaUrls'

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

// Lane W2: burn_default + an active virtual auto subtitle must force the
// TORRENT through the PROGRESSIVE transcode with ?burnsub — the HLS muxer
// drops subtitle streams, so the burn would silently no-op there.
describe('burnsub routing (auto subtitle + burn_default)', () => {
  const HASH = 'a'.repeat(40)

  function torrentInfo(): TorrentInfo {
    return {
      infoHash: HASH,
      name: 'Show S01E01',
      totalSize: 100,
      // mp4: the name heuristic alone must NOT suggest a transcode.
      files: [{ index: 0, path: 'Show S01E01.mp4', size: 1, isVideo: true, downloaded: 1, progress: 1, priority: 'normal' }],
      peers: 1,
      seeders: 2,
      downRate: 0,
      upRate: 0,
      progress: 1,
      primaryFile: 0,
    }
  }

  function urls(over: Partial<Parameters<typeof computeMediaUrls>[0]> = {}) {
    return computeMediaUrls({
      info: torrentInfo(),
      selectedFile: 0,
      serverReady: true,
      mediaToken: 'tok',
      transcodeAudio: null,
      forceH264: false,
      burnSubTrack: null,
      subActive: null,
      sidecarIdx: null,
      embeddedSub: null,
      customSubURL: null,
      localEmbeddedVttURL: '',
      autoSub: null,
      burnDefault: false,
      caps: null,
      authEnabled: false,
      probe: null,
      ...over,
    })
  }

  it('routes burn_default + auto subtitle through the progressive transcode with ?burnsub', () => {
    const out = urls({ autoSub: { id: '77', language: 'pt-BR' }, burnDefault: true })
    expect(out.isTranscoded).toBe(true)
    expect(out.streamURL).toBe(burnSubTranscodeURL(HASH, 0, '77', 'tok'))
    expect(out.streamURL).toContain(`/api/stream/transcode/${HASH}/0`)
    expect(out.streamURL).toContain('burnsub=77')
    expect(out.streamURL).toContain('token=tok')
    // Routing decision is exposed so the error chain can detect the burn URL.
    expect(out.burnSubFileId).toBe('77')
    // The picture carries the subtitle → the overlay <track> stays silent
    // (otherwise Chrome/Firefox render the same text twice). It comes back
    // only when the burn routing drops (review finding 2).
    expect(out.subtitleVttURL).toBe('')
  })

  it('a latched burnFailed drops the burn routing: non-burn stream URL and the <track> keeps the subtitle unburned', () => {
    // The state after the onError chain latched the 400 (no-libass ffmpeg):
    // playback must continue on the direct route — never hard-fail over a
    // subtitle (review finding 1).
    const out = urls({ autoSub: { id: '77', language: 'pt-BR' }, burnDefault: true, burnFailed: true })
    expect(out.burnSubFileId).toBeNull()
    expect(out.isTranscoded).toBe(false)
    expect(out.streamURL).toBe(streamFileURL(HASH, 0, 'tok'))
    expect(out.streamURL).not.toContain('burnsub')
    // The auto subtitle returns as the unburned overlay fallback.
    expect(out.subtitleVttURL).toBe(subtitleDownloadURL('77', 'tok'))
  })

  it('after a burn failure the fallback still honors a codec-forced route (HLS, not the burn transcode)', () => {
    const out = urls({ autoSub: { id: '77', language: 'pt-BR' }, burnDefault: true, burnFailed: true, forceH264: true })
    expect(out.streamURL).not.toContain('burnsub')
    expect(out.streamURL).toContain('hls')
    expect(out.subtitleVttURL).toBe(subtitleDownloadURL('77', 'tok'))
  })

  it('leaves playback unchanged when burn_default is off', () => {
    const out = urls({ autoSub: { id: '77', language: 'pt-BR' }, burnDefault: false })
    expect(out.isTranscoded).toBe(false)
    expect(out.streamURL).toBe(streamFileURL(HASH, 0, 'tok'))
    expect(out.streamURL).not.toContain('burnsub')
    expect(out.subtitleVttURL).toBe(subtitleDownloadURL('77', 'tok'))
  })

  it('does not burn when no auto subtitle is active', () => {
    const out = urls({ burnDefault: true })
    expect(out.isTranscoded).toBe(false)
    expect(out.streamURL).toBe(streamFileURL(HASH, 0, 'tok'))
  })

  it('keeps local playback on its normal routing (burnsub is torrent-only)', () => {
    const info = localInfo('direct')
    const out = urls({ info, autoSub: { id: '77', language: 'pt-BR' }, burnDefault: true })
    expect(out.isTranscoded).toBe(false)
    expect(out.streamURL).toBe(streamFileURL(info.infoHash, 0, 'tok'))
    expect(out.streamURL).not.toContain('burnsub')
  })

  it('the burn path wins over the HLS path when a transcode would otherwise be used', () => {
    const out = urls({ autoSub: { id: '77', language: 'pt-BR' }, burnDefault: true, forceH264: true })
    expect(out.isTranscoded).toBe(true)
    expect(out.streamURL).toContain('burnsub=77')
    expect(out.streamURL).not.toContain('hls')
  })

  it('any manual pick replaces the auto subtitle as the active <track>', () => {
    const out = urls({ autoSub: { id: '77', language: 'pt-BR' }, burnDefault: true, subActive: '55' })
    expect(out.subtitleVttURL).toBe(subtitleDownloadURL('55', 'tok'))
    expect(out.subtitleVttURL).not.toContain('77')
  })

  it('encodes the OpenSubtitles file id in the burn URL', () => {
    expect(burnSubTranscodeURL(HASH, 2, 'a b/c')).toContain('burnsub=a%20b%2Fc')
  })

  // Cross-cutting review finding 2: the progressive transcode COPIES the source
  // audio unless told otherwise, so a caller-selected track (useVideoFallback
  // auto-selects for AC3/DTS/…) must ride along — or burn_default + an
  // incompatible audio track plays the burned picture with no sound.
  it('threads the selected audio track (+aac) into the burn URL so the picture is not silent', () => {
    const out = urls({ autoSub: { id: '77', language: 'pt-BR' }, burnDefault: true, transcodeAudio: 1 })
    expect(out.streamURL).toBe(burnSubTranscodeURL(HASH, 0, '77', 'tok', 1))
    expect(out.streamURL).toContain('burnsub=77')
    expect(out.streamURL).toContain('audio=1')
    expect(out.streamURL).toContain('acodec=aac')
  })

  it('keeps the burn URL free of audio params when no audio track is selected', () => {
    const out = urls({ autoSub: { id: '77', language: 'pt-BR' }, burnDefault: true, transcodeAudio: null })
    expect(out.streamURL).toBe(burnSubTranscodeURL(HASH, 0, '77', 'tok'))
    expect(out.streamURL).not.toContain('audio=')
    expect(out.streamURL).not.toContain('acodec')
    // Builder level: a null/absent selection must never emit a partial param.
    expect(burnSubTranscodeURL(HASH, 0, '77')).not.toContain('audio=')
    expect(burnSubTranscodeURL(HASH, 0, '77', undefined, null)).not.toContain('audio=')
  })

  it('computeIsTranscoded treats a burnsub file id as an explicit transcode', () => {
    expect(computeIsTranscoded({ info: torrentInfo(), selectedFile: 0, transcodeAudio: null, forceH264: false, burnSubTrack: null, burnSubFileId: '77', probe: null })).toBe(true)
    expect(computeIsTranscoded({ info: torrentInfo(), selectedFile: 0, transcodeAudio: null, forceH264: false, burnSubTrack: null, burnSubFileId: null, probe: null })).toBe(false)
  })
})
