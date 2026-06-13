import { describe, it, expect } from 'vitest'
import { shouldUseHlsJs } from './playerFormat'

// nativeHls is passed explicitly so the decision is testable without a DOM.
describe('shouldUseHlsJs', () => {
  it('never uses hls.js for non-HLS sources', () => {
    expect(shouldUseHlsJs({ isHls: false, audioMode: true, hlsSupported: true, nativeHls: false })).toBe(false)
  })

  it('falls back to native when MSE is unavailable (older iPhones)', () => {
    expect(shouldUseHlsJs({ isHls: true, audioMode: true, hlsSupported: false, nativeHls: true })).toBe(false)
  })

  it('desktop (no native HLS) always uses hls.js', () => {
    expect(shouldUseHlsJs({ isHls: true, audioMode: false, hlsSupported: true, nativeHls: false })).toBe(true)
    expect(shouldUseHlsJs({ isHls: true, audioMode: true, hlsSupported: true, nativeHls: false })).toBe(true)
  })

  it('WebKit forces hls.js only in audio mode (video stays native)', () => {
    expect(shouldUseHlsJs({ isHls: true, audioMode: true, hlsSupported: true, nativeHls: true })).toBe(true)
    expect(shouldUseHlsJs({ isHls: true, audioMode: false, hlsSupported: true, nativeHls: true })).toBe(false)
  })
})
