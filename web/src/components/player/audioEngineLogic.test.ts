import { describe, it, expect } from 'vitest'
import { crossfadeDue, peekNextIndex, engineEligible } from './audioEngineLogic'
import { clampCrossfadeSec, looksDirectAudio, CROSSFADE_MIN, CROSSFADE_MAX } from './transition'

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
