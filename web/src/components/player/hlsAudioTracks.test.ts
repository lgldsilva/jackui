import { describe, it, expect } from 'vitest'
import {
  seamlessAudioAvailable,
  probeAudioToPosition,
  nativeAudioCount,
  applyAudioSelection,
  type VideoWithAudioTracks,
} from './hlsAudioTracks'

describe('seamlessAudioAvailable', () => {
  it('requires >1 track (0/1 = legacy path, 2+ = seamless)', () => {
    expect(seamlessAudioAvailable(0)).toBe(false)
    expect(seamlessAudioAvailable(1)).toBe(false)
    expect(seamlessAudioAvailable(2)).toBe(true)
    expect(seamlessAudioAvailable(5)).toBe(true)
  })
})

describe('probeAudioToPosition', () => {
  const probeAudio = [{ index: 1 }, { index: 3 }, { index: 5 }]

  it('null (default) → 0 (the 1st rendition, the muxed DEFAULT)', () => {
    expect(probeAudioToPosition(null, probeAudio)).toBe(0)
  })

  it('absolute index → position in probe order', () => {
    expect(probeAudioToPosition(1, probeAudio)).toBe(0)
    expect(probeAudioToPosition(3, probeAudio)).toBe(1)
    expect(probeAudioToPosition(5, probeAudio)).toBe(2)
  })

  it('nonexistent index → null (applies nothing)', () => {
    expect(probeAudioToPosition(9, probeAudio)).toBeNull()
    expect(probeAudioToPosition(2, probeAudio)).toBeNull()
  })
})

describe('nativeAudioCount', () => {
  it('0 when there\'s no AudioTrackList (non-WebKit)', () => {
    expect(nativeAudioCount(null)).toBe(0)
    expect(nativeAudioCount({} as VideoWithAudioTracks)).toBe(0)
  })

  it('reads the AudioTrackList length', () => {
    const v = { audioTracks: { length: 3 } } as unknown as VideoWithAudioTracks
    expect(nativeAudioCount(v)).toBe(3)
  })
})

// fakeHls builds the minimum hls.js surface used by applyAudioSelection.
function fakeHls(trackIds: number[], current = trackIds[0] ?? -1) {
  const state = { audioTrack: current }
  return {
    audioTracks: trackIds.map(id => ({ id })),
    get audioTrack() { return state.audioTrack },
    set audioTrack(v: number) { state.audioTrack = v },
  } as unknown as import('hls.js').default & { audioTrack: number }
}

// fakeVideo builds a WebKit AudioTrackList with mutable enabled flags.
function fakeVideo(count: number, enabledIdx = 0): VideoWithAudioTracks {
  const tracks = Array.from({ length: count }, (_, i) => ({ enabled: i === enabledIdx }))
  const at: Record<string, unknown> = { length: count }
  tracks.forEach((tr, i) => { at[i] = tr })
  return { audioTracks: at, __tracks: tracks } as unknown as VideoWithAudioTracks & { __tracks: typeof tracks }
}

describe('applyAudioSelection', () => {
  it('hls.js with >1 track: sets hls.audioTrack by the position\'s id', () => {
    const hls = fakeHls([10, 11, 12])
    applyAudioSelection(hls, null, 2)
    expect(hls.audioTrack).toBe(12)
  })

  it('hls.js: doesn\'t rewrite when already on the track (idempotent)', () => {
    const hls = fakeHls([10, 11], 11)
    let writes = 0
    Object.defineProperty(hls, 'audioTrack', {
      get() { return 11 },
      set() { writes++ },
    })
    applyAudioSelection(hls, null, 1)
    expect(writes).toBe(0)
  })

  it('hls.js with ≤1 track: no-op (falls back to legacy)', () => {
    const hls = fakeHls([10])
    applyAudioSelection(hls, null, 0)
    expect(hls.audioTrack).toBe(10)
  })

  it('native Safari: enables only the position\'s track on the AudioTrackList', () => {
    const v = fakeVideo(3, 0) as VideoWithAudioTracks & { __tracks: { enabled: boolean }[] }
    applyAudioSelection(null, v, 2)
    expect(v.__tracks.map(t => t.enabled)).toEqual([false, false, true])
  })

  it('AudioTrackList with ≤1 track: no-op', () => {
    const v = fakeVideo(1, 0) as VideoWithAudioTracks & { __tracks: { enabled: boolean }[] }
    applyAudioSelection(null, v, 0)
    expect(v.__tracks.map(t => t.enabled)).toEqual([true])
  })

  it('position out of range: no-op', () => {
    const v = fakeVideo(2, 0) as VideoWithAudioTracks & { __tracks: { enabled: boolean }[] }
    applyAudioSelection(null, v, 5)
    expect(v.__tracks.map(t => t.enabled)).toEqual([true, false])
  })
})
