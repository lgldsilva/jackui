import { describe, it, expect } from 'vitest'
import { shouldShowStartOverlay } from './playerOverlay'

const base = {
  videoError: false,
  engineActive: false,
  suppressStartOverlay: false,
  currentTime: 0,
  bufferedEnd: 0,
}

describe('shouldShowStartOverlay', () => {
  it('mostra na abertura fria (nada tocou/bufferizou)', () => {
    expect(shouldShowStartOverlay(base)).toBe(true)
  })

  it('NÃO mostra em warm switch (troca de faixa)', () => {
    expect(shouldShowStartOverlay({ ...base, suppressStartOverlay: true })).toBe(false)
  })

  it('NÃO mostra quando o motor gapless está ativo', () => {
    expect(shouldShowStartOverlay({ ...base, engineActive: true })).toBe(false)
  })

  it('NÃO mostra em erro de mídia', () => {
    expect(shouldShowStartOverlay({ ...base, videoError: true })).toBe(false)
  })

  it('NÃO mostra depois que algo já tocou (currentTime > 0)', () => {
    expect(shouldShowStartOverlay({ ...base, currentTime: 12 })).toBe(false)
  })

  it('NÃO mostra quando já há buffer à frente', () => {
    expect(shouldShowStartOverlay({ ...base, bufferedEnd: 5 })).toBe(false)
  })
})
