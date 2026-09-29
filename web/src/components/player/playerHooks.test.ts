import { describe, it, expect } from 'vitest'
import { backstopStuck, backstopShouldFire, hlsFatalAction, startGapNudgeTarget } from './playerHooks'

// Regression of the Star Wars bug (a376440b): an H264/AAC/MP4 (browser-safe) that
// stalls from lack of data (the MP4 moov hasn't downloaded yet → readyState 0,
// buffered 0) must NOT fire the backstop and force transcode. Transcoding
// H264→H264 from the same cold source doesn't speed anything up. The backstop only exists for
// Safari's SILENT HEVC failure (a codec that genuinely needs transcode).

describe('backstopStuck', () => {
  it('detects stall: readyState<2 + currentTime<0.1 + buffered<0.5', () => {
    expect(backstopStuck(0, 0, 0)).toBe(true)
    expect(backstopStuck(1, 0.05, 0.2)).toBe(true)
  })
  it('not a stall when a frame is already playable (readyState>=2)', () => {
    expect(backstopStuck(2, 0, 0)).toBe(false)
    expect(backstopStuck(4, 0, 0)).toBe(false)
  })
  it('not a stall when time has already moved', () => {
    expect(backstopStuck(0, 0.5, 0)).toBe(false)
  })
  it('not a stall when the buffer is already sufficient', () => {
    expect(backstopStuck(1, 0, 1)).toBe(false)
  })
})

describe('backstopShouldFire', () => {
  const stuck = true

  it('does NOT fire for a browser-safe codec (needsTranscode=false) — the FIX', () => {
    // Star Wars case: H264/AAC/MP4, stuck on moov/network, GPU available.
    expect(backstopShouldFire(stuck, false, true)).toBe(false)
  })

  it('fires for a codec that needs transcode (HEVC) with an encoder', () => {
    expect(backstopShouldFire(stuck, true, true)).toBe(true)
  })

  it('fires when the codec is unknown (probe hasn\'t arrived) with an encoder', () => {
    // Preserves the historical behavior: in doubt, try the fallback.
    expect(backstopShouldFire(stuck, undefined, true)).toBe(true)
  })

  it('does NOT fire without a GPU encoder, even when transcode is needed', () => {
    expect(backstopShouldFire(stuck, true, false)).toBe(false)
    expect(backstopShouldFire(stuck, undefined, false)).toBe(false)
  })

  it('does NOT fire when not stuck, any codec', () => {
    expect(backstopShouldFire(false, true, true)).toBe(false)
    expect(backstopShouldFire(false, undefined, true)).toBe(false)
    expect(backstopShouldFire(false, false, true)).toBe(false)
  })
})

// Regression of "28 Years Later" (local MKV/H264 file on Safari): the EVENT/live
// transcode buffers 12s starting at buffered.start=0.000002, but
// currentTime stays at 0 (right BEFORE the buffer) → Safari never reaches canplay and
// stalls. The nudge advances currentTime into the buffer.
describe('startGapNudgeTarget', () => {
  it('nudges when stuck at 0 with a sub-tick hole (0.000002)', () => {
    expect(startGapNudgeTarget(0, 0.000002)).toBeCloseTo(0.050002, 5)
  })
  it('nudges at the historical 1.4s initial_offset', () => {
    expect(startGapNudgeTarget(0, 1.4)).toBeCloseTo(1.45, 5)
  })
  it('does NOT nudge with no buffer yet', () => {
    expect(startGapNudgeTarget(0, null)).toBeNull()
  })
  it('does NOT nudge when the buffer already covers t=0 (gap<=0)', () => {
    expect(startGapNudgeTarget(0, 0)).toBeNull()
    expect(startGapNudgeTarget(0.1, 0.05)).toBeNull()
  })
  it('does NOT nudge when time has already moved (>0.25) — normal playback', () => {
    expect(startGapNudgeTarget(5, 5.2)).toBeNull()
    expect(startGapNudgeTarget(0.3, 0.5)).toBeNull()
  })
  it('does NOT nudge with a too-large gap (>1.5s) — it would skip real content', () => {
    expect(startGapNudgeTarget(0, 3)).toBeNull()
  })
})

describe('hlsFatalAction', () => {
  // Mirrors hls.js's Hls.ErrorTypes enum (string literals).
  const TYPES = { NETWORK_ERROR: 'networkError', MEDIA_ERROR: 'mediaError' }

  it('NETWORK_ERROR → startLoad (reloads the stream)', () => {
    expect(hlsFatalAction(TYPES.NETWORK_ERROR, TYPES)).toBe('startLoad')
  })
  it('MEDIA_ERROR → recoverMedia', () => {
    expect(hlsFatalAction(TYPES.MEDIA_ERROR, TYPES)).toBe('recoverMedia')
  })
  it('any other type → destroy', () => {
    expect(hlsFatalAction('muxError', TYPES)).toBe('destroy')
    expect(hlsFatalAction('otherError', TYPES)).toBe('destroy')
    expect(hlsFatalAction('', TYPES)).toBe('destroy')
  })
})
