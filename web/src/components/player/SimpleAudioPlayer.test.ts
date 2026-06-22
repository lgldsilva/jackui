import { describe, it, expect } from 'vitest'
import { computeAudioPreload, computeEffectiveSrc, shouldShowAudioOverlay } from './SimpleAudioPlayer'

describe('computeAudioPreload', () => {
  it('is none on WebKit before user gesture', () => {
    expect(computeAudioPreload(true, false)).toBe('none')
  })

  it('is auto after user gesture even on WebKit', () => {
    expect(computeAudioPreload(true, true)).toBe('auto')
  })

  it('is auto on non-WebKit browsers', () => {
    expect(computeAudioPreload(false, false)).toBe('auto')
    expect(computeAudioPreload(false, true)).toBe('auto')
  })
})

describe('computeEffectiveSrc', () => {
  it('hides src before WebKit gesture', () => {
    expect(computeEffectiveSrc(true, false, '/song.mp3')).toBeUndefined()
  })

  it('exposes src after WebKit gesture', () => {
    expect(computeEffectiveSrc(true, true, '/song.mp3')).toBe('/song.mp3')
  })

  it('exposes src on non-WebKit', () => {
    expect(computeEffectiveSrc(false, false, '/song.mp3')).toBe('/song.mp3')
  })

  it('falls back to undefined for empty src', () => {
    expect(computeEffectiveSrc(false, false, '')).toBeUndefined()
  })
})

describe('shouldShowAudioOverlay', () => {
  it('shows overlay only on unblessed WebKit before tap', () => {
    expect(shouldShowAudioOverlay(true, false, false, false)).toBe(true)
  })

  it('hides overlay after user gesture', () => {
    expect(shouldShowAudioOverlay(true, true, false, false)).toBe(false)
  })

  it('hides overlay on non-WebKit', () => {
    expect(shouldShowAudioOverlay(false, false, false, false)).toBe(false)
  })

  it('hides overlay once dismissed or errored', () => {
    expect(shouldShowAudioOverlay(true, false, true, false)).toBe(false)
    expect(shouldShowAudioOverlay(true, false, false, true)).toBe(false)
  })
})
