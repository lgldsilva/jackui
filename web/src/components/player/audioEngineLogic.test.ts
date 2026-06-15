import { describe, it, expect } from 'vitest'
import { crossfadeDue, peekNextIndex, engineEligible, firstDirectAudioTrack, resolveEngineNext, equalPowerCurve } from './audioEngineLogic'
import { clampCrossfadeSec, looksDirectAudio, CROSSFADE_MIN, CROSSFADE_MAX } from './transition'
import type { PlaylistGroup } from './playlistTracks'

const grp = (itemIndex: number, infoHash: string, status: PlaylistGroup['status'], tracks: PlaylistGroup['tracks']): PlaylistGroup =>
  ({ itemIndex, title: `item${itemIndex}`, infoHash, isLocal: false, status, tracks })
const trk = (fileIndex: number, path: string, kind: 'audio' | 'video' = 'audio') =>
  ({ fileIndex, name: path.split('/').pop() ?? path, path, size: 1, kind })

describe('crossfadeDue', () => {
  it('é falso fora da janela (resta mais que crossfadeSec)', () => {
    expect(crossfadeDue(100, 180, 6)).toBe(false) // restam 80s
  })
  it('é verdadeiro ao entrar na janela', () => {
    expect(crossfadeDue(175, 180, 6)).toBe(true) // restam 5s <= 6
    expect(crossfadeDue(174, 180, 6)).toBe(true) // restam 6s <= 6 (limite inclusivo)
  })
  it('é falso com duração desconhecida/infinita/zero', () => {
    expect(crossfadeDue(10, Number.NaN, 6)).toBe(false)
    expect(crossfadeDue(10, Number.POSITIVE_INFINITY, 6)).toBe(false)
    expect(crossfadeDue(10, 0, 6)).toBe(false)
  })
})

describe('peekNextIndex', () => {
  it('sequência normal: próxima é a seguinte', () => {
    expect(peekNextIndex(5, 0, 'none')).toBe(1)
    expect(peekNextIndex(5, 3, 'none')).toBe(4)
  })
  it("repeat 'none': no fim retorna -1", () => {
    expect(peekNextIndex(5, 4, 'none')).toBe(-1)
  })
  it("repeat 'all': no fim circula pro início", () => {
    expect(peekNextIndex(5, 4, 'all')).toBe(0)
    expect(peekNextIndex(5, 2, 'all')).toBe(3) // no meio segue normal
  })
  it("repeat 'one': sempre a mesma faixa", () => {
    expect(peekNextIndex(5, 2, 'one')).toBe(2)
    expect(peekNextIndex(5, 4, 'one')).toBe(4) // até no fim
  })
  it('lista vazia ou índice inválido → -1', () => {
    expect(peekNextIndex(0, 0, 'all')).toBe(-1)
    expect(peekNextIndex(5, -1, 'all')).toBe(-1)
    expect(peekNextIndex(5, 5, 'all')).toBe(-1)
  })
  it('lista de 1 faixa: none→-1, all→ela mesma, one→ela mesma', () => {
    expect(peekNextIndex(1, 0, 'none')).toBe(-1)
    expect(peekNextIndex(1, 0, 'all')).toBe(0)
    expect(peekNextIndex(1, 0, 'one')).toBe(0)
  })
})

describe('engineEligible', () => {
  it('liga só com áudio direct-play e transição ligada', () => {
    expect(engineEligible({ mode: 'gapless', isAudio: true, isTranscoded: false, repeat: 'none' })).toBe(true)
    expect(engineEligible({ mode: 'crossfade', isAudio: true, isTranscoded: false, repeat: 'all' })).toBe(true)
  })
  it("desliga com transição 'off'", () => {
    expect(engineEligible({ mode: 'off', isAudio: true, isTranscoded: false, repeat: 'none' })).toBe(false)
  })
  it('desliga em vídeo', () => {
    expect(engineEligible({ mode: 'crossfade', isAudio: false, isTranscoded: false, repeat: 'none' })).toBe(false)
  })
  it('desliga em áudio transcodado (HLS)', () => {
    expect(engineEligible({ mode: 'gapless', isAudio: true, isTranscoded: true, repeat: 'none' })).toBe(false)
  })
  it("desliga em repeat 'one' (replay-loop fica no caminho normal)", () => {
    expect(engineEligible({ mode: 'crossfade', isAudio: true, isTranscoded: false, repeat: 'one' })).toBe(false)
  })
})

describe('firstDirectAudioTrack', () => {
  const groups = [
    grp(0, 'aaa', 'ready', [trk(0, 'A/01.flac'), trk(1, 'A/02.flac')]),
    grp(1, 'bbb', 'pending', []),
    grp(2, 'ccc', 'ready', [trk(5, 'C/intro.mkv', 'video'), trk(7, 'C/song.mp3')]),
  ]
  it('pega a 1ª faixa de áudio direct-play do item', () => {
    expect(firstDirectAudioTrack(groups, 0)).toEqual({ itemIndex: 0, infoHash: 'aaa', fileIndex: 0, path: 'A/01.flac' })
  })
  it('pula faixa de vídeo → pega o 1º áudio (mp3)', () => {
    expect(firstDirectAudioTrack(groups, 2)).toEqual({ itemIndex: 2, infoHash: 'ccc', fileIndex: 7, path: 'C/song.mp3' })
  })
  it('item não resolvido (pending) → null', () => {
    expect(firstDirectAudioTrack(groups, 1)).toBeNull()
  })
  it('índice inexistente / -1 → null', () => {
    expect(firstDirectAudioTrack(groups, -1)).toBeNull()
    expect(firstDirectAudioTrack(groups, 9)).toBeNull()
  })
})

describe('resolveEngineNext', () => {
  const curFiles = [{ index: 0, path: 'Alb/01.flac' }, { index: 1, path: 'Alb/02.flac' }, { index: 2, path: 'Alb/03.mkv' }]
  const groups = [grp(0, 'cur', 'ready', []), grp(1, 'nxt', 'ready', [trk(0, 'N/01.mp3')])]
  const base = { curInfoHash: 'cur', curFiles, groups, nextItemIndex: -1 }

  it('próxima do álbum (mesmo torrent) quando há próxima direct', () => {
    const r = resolveEngineNext({ ...base, inPlaylist: false, mediaIndices: [0, 1, 2], mediaCursor: 0, repeat: 'none' })
    expect(r).toEqual({ itemIndex: -1, infoHash: 'cur', fileIndex: 1, path: 'Alb/02.flac' })
  })
  it('próxima do álbum não-direct (vídeo/HLS) → null (hard-cut)', () => {
    const r = resolveEngineNext({ ...base, inPlaylist: false, mediaIndices: [0, 1, 2], mediaCursor: 1, repeat: 'none' })
    expect(r).toBeNull()
  })
  it('álbum solto repeat=all circula pro início', () => {
    const r = resolveEngineNext({ ...base, inPlaylist: false, mediaIndices: [0, 1], mediaCursor: 1, repeat: 'all' })
    expect(r?.fileIndex).toBe(0)
  })
  it('numa playlist, fim do álbum NÃO circula — vai pro próximo item (cross)', () => {
    const r = resolveEngineNext({ inPlaylist: true, mediaIndices: [0, 1], mediaCursor: 1, repeat: 'all', curInfoHash: 'cur', curFiles, groups, nextItemIndex: 1 })
    expect(r).toEqual({ itemIndex: 1, infoHash: 'nxt', fileIndex: 0, path: 'N/01.mp3' })
  })
  it('fim do álbum sem próximo item (nextItemIndex -1) → null', () => {
    const r = resolveEngineNext({ ...base, inPlaylist: true, mediaIndices: [0, 1], mediaCursor: 1, repeat: 'none' })
    expect(r).toBeNull()
  })
})

describe('clampCrossfadeSec', () => {
  it('mantém valores dentro do intervalo', () => {
    expect(clampCrossfadeSec(6)).toBe(6)
    expect(clampCrossfadeSec(CROSSFADE_MIN)).toBe(CROSSFADE_MIN)
    expect(clampCrossfadeSec(CROSSFADE_MAX)).toBe(CROSSFADE_MAX)
  })
  it('clampa fora do intervalo', () => {
    expect(clampCrossfadeSec(0)).toBe(CROSSFADE_MIN)
    expect(clampCrossfadeSec(-5)).toBe(CROSSFADE_MIN)
    expect(clampCrossfadeSec(99)).toBe(CROSSFADE_MAX)
  })
})

describe('looksDirectAudio', () => {
  it('reconhece extensões de áudio direct-play', () => {
    for (const ext of ['mp3', 'm4a', 'aac', 'ogg', 'opus', 'wav', 'flac', 'alac', 'wma']) {
      expect(looksDirectAudio(`Album/01 - Track.${ext}`)).toBe(true)
    }
  })
  it('é case-insensitive', () => {
    expect(looksDirectAudio('Song.FLAC')).toBe(true)
  })
  it('rejeita vídeo/containers e não-mídia', () => {
    expect(looksDirectAudio('Movie.mkv')).toBe(false)
    expect(looksDirectAudio('clip.mp4')).toBe(false)
    expect(looksDirectAudio('cover.jpg')).toBe(false)
    expect(looksDirectAudio('noext')).toBe(false)
  })
})

describe('equalPowerCurve', () => {
  it('out vai de 1 → 0; in vai de 0 → 1', () => {
    const out = equalPowerCurve(64, 'out')
    const inn = equalPowerCurve(64, 'in')
    expect(out[0]).toBeCloseTo(1, 6)
    expect(out[out.length - 1]).toBeCloseTo(0, 6)
    expect(inn[0]).toBeCloseTo(0, 6)
    expect(inn[inn.length - 1]).toBeCloseTo(1, 6)
  })

  it('soma de potências (out² + in²) = 1 em todo ponto (equal-power, sem dip)', () => {
    const n = 64
    const out = equalPowerCurve(n, 'out')
    const inn = equalPowerCurve(n, 'in')
    for (let i = 0; i < n; i++) {
      expect(out[i] ** 2 + inn[i] ** 2).toBeCloseTo(1, 6)
    }
  })

  it('no ponto médio cada lado vale ~0,707 (vs 0,5 da rampa linear que causava o dip)', () => {
    const out = equalPowerCurve(65, 'out') // 65 pontos → índice 32 é o centro exato
    expect(out[32]).toBeCloseTo(Math.SQRT1_2, 4)
  })

  it('clampa steps mínimos a 2 (evita curva degenerada)', () => {
    expect(equalPowerCurve(1, 'out').length).toBe(2)
    expect(equalPowerCurve(0, 'in').length).toBe(2)
  })
})
