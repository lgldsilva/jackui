import { describe, it, expect } from 'vitest'
import {
  seamlessAudioAvailable,
  probeAudioToPosition,
  nativeAudioCount,
  applyAudioSelection,
  type VideoWithAudioTracks,
} from './hlsAudioTracks'

describe('seamlessAudioAvailable', () => {
  it('exige >1 faixa (0/1 = caminho legado, 2+ = seamless)', () => {
    expect(seamlessAudioAvailable(0)).toBe(false)
    expect(seamlessAudioAvailable(1)).toBe(false)
    expect(seamlessAudioAvailable(2)).toBe(true)
    expect(seamlessAudioAvailable(5)).toBe(true)
  })
})

describe('probeAudioToPosition', () => {
  const probeAudio = [{ index: 1 }, { index: 3 }, { index: 5 }]

  it('null (default) → 0 (a 1ª rendition, DEFAULT muxada)', () => {
    expect(probeAudioToPosition(null, probeAudio)).toBe(0)
  })

  it('índice absoluto → posição na ordem de probe', () => {
    expect(probeAudioToPosition(1, probeAudio)).toBe(0)
    expect(probeAudioToPosition(3, probeAudio)).toBe(1)
    expect(probeAudioToPosition(5, probeAudio)).toBe(2)
  })

  it('índice inexistente → null (não aplica nada)', () => {
    expect(probeAudioToPosition(9, probeAudio)).toBeNull()
    expect(probeAudioToPosition(2, probeAudio)).toBeNull()
  })
})

describe('nativeAudioCount', () => {
  it('0 quando não há AudioTrackList (não-WebKit)', () => {
    expect(nativeAudioCount(null)).toBe(0)
    expect(nativeAudioCount({} as VideoWithAudioTracks)).toBe(0)
  })

  it('lê o length da AudioTrackList', () => {
    const v = { audioTracks: { length: 3 } } as unknown as VideoWithAudioTracks
    expect(nativeAudioCount(v)).toBe(3)
  })
})

// fakeHls monta o mínimo da superfície do hls.js usada por applyAudioSelection.
function fakeHls(trackIds: number[], current = trackIds[0] ?? -1) {
  const state = { audioTrack: current }
  return {
    audioTracks: trackIds.map(id => ({ id })),
    get audioTrack() { return state.audioTrack },
    set audioTrack(v: number) { state.audioTrack = v },
  } as unknown as import('hls.js').default & { audioTrack: number }
}

// fakeVideo monta uma AudioTrackList do WebKit com flags enabled mutáveis.
function fakeVideo(count: number, enabledIdx = 0): VideoWithAudioTracks {
  const tracks = Array.from({ length: count }, (_, i) => ({ enabled: i === enabledIdx }))
  const at: Record<string, unknown> = { length: count }
  tracks.forEach((tr, i) => { at[i] = tr })
  return { audioTracks: at, __tracks: tracks } as unknown as VideoWithAudioTracks & { __tracks: typeof tracks }
}

describe('applyAudioSelection', () => {
  it('hls.js com >1 faixa: seta hls.audioTrack pelo id da posição', () => {
    const hls = fakeHls([10, 11, 12])
    applyAudioSelection(hls, null, 2)
    expect(hls.audioTrack).toBe(12)
  })

  it('hls.js: não reescreve quando já está na faixa (idempotente)', () => {
    const hls = fakeHls([10, 11], 11)
    let writes = 0
    Object.defineProperty(hls, 'audioTrack', {
      get() { return 11 },
      set() { writes++ },
    })
    applyAudioSelection(hls, null, 1)
    expect(writes).toBe(0)
  })

  it('hls.js com ≤1 faixa: no-op (cai no legado)', () => {
    const hls = fakeHls([10])
    applyAudioSelection(hls, null, 0)
    expect(hls.audioTrack).toBe(10)
  })

  it('Safari nativo: liga só a faixa da posição na AudioTrackList', () => {
    const v = fakeVideo(3, 0) as VideoWithAudioTracks & { __tracks: { enabled: boolean }[] }
    applyAudioSelection(null, v, 2)
    expect(v.__tracks.map(t => t.enabled)).toEqual([false, false, true])
  })

  it('AudioTrackList com ≤1 faixa: no-op', () => {
    const v = fakeVideo(1, 0) as VideoWithAudioTracks & { __tracks: { enabled: boolean }[] }
    applyAudioSelection(null, v, 0)
    expect(v.__tracks.map(t => t.enabled)).toEqual([true])
  })

  it('posição fora do range: no-op', () => {
    const v = fakeVideo(2, 0) as VideoWithAudioTracks & { __tracks: { enabled: boolean }[] }
    applyAudioSelection(null, v, 5)
    expect(v.__tracks.map(t => t.enabled)).toEqual([true, false])
  })
})
