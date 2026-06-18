import { describe, it, expect } from 'vitest'
import { shouldShowStartOverlay, shouldShowStartAudioOverlay } from './playerOverlay'

const base = {
  videoError: false,
  engineActive: false,
  suppressStartOverlay: false,
  disableNativeAutoplay: false,
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

  it('NÃO mostra o spinner no iOS-áudio (dá lugar ao overlay "Tocar")', () => {
    expect(shouldShowStartOverlay({ ...base, disableNativeAutoplay: true })).toBe(false)
  })
})

const audioBase = {
  disableNativeAutoplay: true,
  startOverlayDismissed: false,
  videoError: false,
  showResumePrompt: false,
  currentTime: 0,
}

describe('shouldShowStartAudioOverlay', () => {
  it('mostra no iOS-áudio antes do tap (faixa aberta, parada)', () => {
    expect(shouldShowStartAudioOverlay(audioBase)).toBe(true)
  })

  it('NÃO mostra fora do iOS-áudio', () => {
    expect(shouldShowStartAudioOverlay({ ...audioBase, disableNativeAutoplay: false })).toBe(false)
  })

  it('NÃO mostra depois de dispensado (usuário tocou)', () => {
    expect(shouldShowStartAudioOverlay({ ...audioBase, startOverlayDismissed: true })).toBe(false)
  })

  it('NÃO mostra quando já tocou (currentTime > 0)', () => {
    expect(shouldShowStartAudioOverlay({ ...audioBase, currentTime: 8 })).toBe(false)
  })

  it('NÃO mostra sobre o prompt de resume nem em erro', () => {
    expect(shouldShowStartAudioOverlay({ ...audioBase, showResumePrompt: true })).toBe(false)
    expect(shouldShowStartAudioOverlay({ ...audioBase, videoError: true })).toBe(false)
  })
})
