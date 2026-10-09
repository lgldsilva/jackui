// Pure visibility logic for the "loading" overlay at the start of a track.
// Extracted from VideoPlayerElement to (a) keep the component's cognitive complexity
// below the gate (the && chain weighed on the body) and (b) be testable.

export type StartOverlayInput = {
  // media error → the error UI takes over, don't show the spinner.
  videoError: boolean
  // gapless engine active → the <video> is muted/src-less (bufferedEnd stays 0 always),
  // so the spinner doesn't reflect real loading.
  engineActive: boolean
  // track switch in an already-populated session (warm switch) → the cover/seekbar
  // stay; suppresses the spinner that flashed on every track.
  suppressStartOverlay: boolean
  // iOS-audio (tap-to-play): nothing buffers without a gesture, so the spinner would spin
  // forever. Suppresses the spinner in favor of the "Play" overlay.
  disableNativeAutoplay: boolean
  currentTime: number
  bufferedEnd: number
}

// The start spinner only appears on a COLD open (the instance's first track),
// while nothing has played or buffered yet — and never on iOS-audio (there the
// "Play" overlay takes over, otherwise the spinner would spin forever waiting for a gesture).
export function shouldShowStartOverlay(o: StartOverlayInput): boolean {
  return !o.videoError && !o.engineActive && !o.suppressStartOverlay && !o.disableNativeAutoplay
    && o.currentTime === 0 && o.bufferedEnd === 0
}

export type StartAudioOverlayInput = {
  // iOS-audio: Apple requires a gesture to play; the "Play" overlay is the trigger.
  disableNativeAutoplay: boolean
  // the user already tapped the overlay (immediate dismissal, before the playhead moves).
  startOverlayDismissed: boolean
  videoError: boolean
  // the resume prompt has its own buttons (continue/restart) — don't overlay it.
  showResumePrompt: boolean
  currentTime: number
}

// The "Play" overlay (iOS-audio) appears when the track opened but hasn't played yet:
// iOS-audio only, before the tap (not dismissed), no error, no resume prompt and
// playhead at 0. Tapping it (gesture) is what actually starts playback on iPhone/iPad.
export function shouldShowStartAudioOverlay(o: StartAudioOverlayInput): boolean {
  return o.disableNativeAutoplay && !o.startOverlayDismissed
    && !o.videoError && !o.showResumePrompt && o.currentTime === 0
}

export type TranscodeHintInput = {
  isTranscoded: boolean
  // the probe says the source codec/container needs transcoding (HEVC/AV1/MKV…)
  codecIncompat: boolean
  // the auto-fallback engaged although the probe said the codec was browser-safe
  // → the switch was stall-driven (starved swarm), not codec-driven
  slowSourceFallback: boolean
}

// Which i18n key the loading overlay's transcode line uses. The old code showed
// "incompatible original codec (HEVC/AV1)" whenever the auto-fallback had been
// ATTEMPTED — but the fallback also engages on network stalls, and on starved
// swarms the probe never even answered. Claim codec incompatibility ONLY when
// the probe confirms it; a fallback with a browser-safe codec says "slow
// source"; anything else (including no probe at all) stays neutral.
export function transcodeHintKey(o: TranscodeHintInput): string | null {
  if (!o.isTranscoded) return null
  if (o.codecIncompat) return 'player.overlays.convertingGpuIncompat'
  if (o.slowSourceFallback) return 'player.overlays.fallbackSlowSource'
  return 'player.overlays.transcodingActive'
}
