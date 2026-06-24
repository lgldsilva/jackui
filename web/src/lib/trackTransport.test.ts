import { describe, it, expect } from 'vitest'
import { nextTrack, prevTrack } from './trackTransport'

const order = [10, 11, 12, 13] // fileIndices em ordem de reprodução

describe('nextTrack', () => {
  it('meio do álbum → próxima faixa', () => {
    expect(nextTrack(order, 11, 'none', false)).toEqual({ kind: 'track', fileIndex: 12 })
  })

  it('fim + há próximo na playlist → spill (sem wrap duplo)', () => {
    expect(nextTrack(order, 13, 'all', true)).toEqual({ kind: 'spill' })
  })

  it('fim + repeat-all + sem playlist → wrap-rebuild', () => {
    expect(nextTrack(order, 13, 'all', false)).toEqual({ kind: 'wrap-rebuild' })
  })

  it('fim + repeat-none + sem playlist → spill (no-op no caller)', () => {
    expect(nextTrack(order, 13, 'none', false)).toEqual({ kind: 'spill' })
  })

  it('cursor -1 (faixa fora da ordem) → toca a primeira', () => {
    expect(nextTrack(order, 999, 'none', false)).toEqual({ kind: 'track', fileIndex: 10 })
  })

  it('repeat-one NÃO curto-circuita: botão pula faixa normalmente', () => {
    expect(nextTrack(order, 11, 'one', false)).toEqual({ kind: 'track', fileIndex: 12 })
  })

  it('álbum de 1 faixa, fim, repeat-all sem playlist → wrap-rebuild', () => {
    expect(nextTrack([42], 42, 'all', false)).toEqual({ kind: 'wrap-rebuild' })
  })

  it('ordem vazia → spill', () => {
    expect(nextTrack([], 0, 'all', false)).toEqual({ kind: 'spill' })
  })
})

describe('prevTrack', () => {
  it('meio do álbum → faixa anterior', () => {
    expect(prevTrack(order, 12, 'none', false)).toEqual({ kind: 'track', fileIndex: 11 })
  })

  it('início + há anterior na playlist → spill', () => {
    expect(prevTrack(order, 10, 'all', true)).toEqual({ kind: 'spill' })
  })

  it('início + repeat-all + sem playlist → última faixa', () => {
    expect(prevTrack(order, 10, 'all', false)).toEqual({ kind: 'track', fileIndex: 13 })
  })

  it('início + repeat-none + sem playlist → spill', () => {
    expect(prevTrack(order, 10, 'none', false)).toEqual({ kind: 'spill' })
  })

  it('cursor -1 → toca a primeira', () => {
    expect(prevTrack(order, 999, 'none', false)).toEqual({ kind: 'track', fileIndex: 10 })
  })
})
