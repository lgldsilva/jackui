import { describe, it, expect } from 'vitest'
import { shouldShowStartOverlay, shouldShowStartAudioOverlay, transcodeHintKey } from './playerOverlay'

const base = {
  videoError: false,
  engineActive: false,
  suppressStartOverlay: false,
  disableNativeAutoplay: false,
  currentTime: 0,
  bufferedEnd: 0,
}

describe('shouldShowStartOverlay', () => {
  it('shows on cold open (nothing played/buffered yet)', () => {
    expect(shouldShowStartOverlay(base)).toBe(true)
  })

  it('does NOT show on warm switch (track change)', () => {
    expect(shouldShowStartOverlay({ ...base, suppressStartOverlay: true })).toBe(false)
  })

  it('does NOT show when the gapless engine is active', () => {
    expect(shouldShowStartOverlay({ ...base, engineActive: true })).toBe(false)
  })

  it('does NOT show on media error', () => {
    expect(shouldShowStartOverlay({ ...base, videoError: true })).toBe(false)
  })

  it('does NOT show once something has played (currentTime > 0)', () => {
    expect(shouldShowStartOverlay({ ...base, currentTime: 12 })).toBe(false)
  })

  it('does NOT show when there is already buffer ahead', () => {
    expect(shouldShowStartOverlay({ ...base, bufferedEnd: 5 })).toBe(false)
  })

  it('does NOT show the spinner on iOS-audio (the "Play" overlay takes over)', () => {
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
  it('shows on iOS-audio before the tap (track opened, paused)', () => {
    expect(shouldShowStartAudioOverlay(audioBase)).toBe(true)
  })

  it('does NOT show outside iOS-audio', () => {
    expect(shouldShowStartAudioOverlay({ ...audioBase, disableNativeAutoplay: false })).toBe(false)
  })

  it('does NOT show once dismissed (user tapped)', () => {
    expect(shouldShowStartAudioOverlay({ ...audioBase, startOverlayDismissed: true })).toBe(false)
  })

  it('does NOT show once it has played (currentTime > 0)', () => {
    expect(shouldShowStartAudioOverlay({ ...audioBase, currentTime: 8 })).toBe(false)
  })

  it('does NOT show over the resume prompt or on error', () => {
    expect(shouldShowStartAudioOverlay({ ...audioBase, showResumePrompt: true })).toBe(false)
    expect(shouldShowStartAudioOverlay({ ...audioBase, videoError: true })).toBe(false)
  })
})

const hintBase = { isTranscoded: true, codecIncompat: false, slowSourceFallback: false }

describe('transcodeHintKey', () => {
  // The production mislabel: a fallback engaged on a STARVED swarm showed
  // "incompatible original codec (HEVC/AV1)" even though the codec was fine
  // (or unknown — the probe itself timed out). Only the probe may blame the codec.
  it('never claims codec incompatibility without probe backing', () => {
    expect(transcodeHintKey(hintBase)).toBe('player.overlays.transcodingActive')
  })

  it('claims codec incompatibility only when the probe confirms it', () => {
    expect(transcodeHintKey({ ...hintBase, codecIncompat: true })).toBe('player.overlays.convertingGpuIncompat')
  })

  it('blames the slow source when the fallback engaged on a browser-safe codec', () => {
    expect(transcodeHintKey({ ...hintBase, slowSourceFallback: true })).toBe('player.overlays.fallbackSlowSource')
  })

  it('codec evidence wins over the stall guess', () => {
    expect(transcodeHintKey({ ...hintBase, codecIncompat: true, slowSourceFallback: true })).toBe('player.overlays.convertingGpuIncompat')
  })

  it('shows no transcode line on direct play', () => {
    expect(transcodeHintKey({ ...hintBase, isTranscoded: false, codecIncompat: true })).toBeNull()
  })
})
