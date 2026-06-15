import { afterAll, beforeAll, describe, expect, it } from 'vitest'
import { webAudioBlocked, audioElementKey } from './playerFormat'

// webAudioBlocked must block exactly ONE combination: a transcoded HLS track on
// WebKit (Safari/iOS), where createMediaElementSource yields zero audio data and
// mutes the element. Everything else (direct-play anywhere, HLS on non-WebKit)
// must stay UNBLOCKED so the EQ/visualizer mount. This replaces the old
// shouldUseHlsJs test as the home of the HLS audio-routing decision.
describe('webAudioBlocked', () => {
  // canPlayNativeHls() returns false WITHOUT caching when window/document are
  // absent (the vitest 'node' env). This test runs before the WebKit stub's
  // beforeAll, so the module cache stays untouched and the WebKit branch below
  // still evaluates fresh.
  it('never blocks on a non-WebKit browser (HLS routes via MSE / direct plays)', () => {
    expect(webAudioBlocked(true)).toBe(false)
    expect(webAudioBlocked(false)).toBe(false)
  })

  it('keeps the single reused element on non-WebKit (no per-transport remount)', () => {
    expect(audioElementKey(true, true)).toBe('media')
    expect(audioElementKey(true, false)).toBe('media')
  })

  describe('on WebKit (Safari/iOS)', () => {
    const g = globalThis as unknown as { window?: unknown; document?: unknown }
    const hadWindow = 'window' in g
    const hadDocument = 'document' in g
    // beforeAll (not the describe body) so the stub is applied AFTER the
    // non-WebKit test above has run — a <video> that "maybe" plays the HLS MIME
    // makes canPlayNativeHls() cache a true (WebKit) verdict.
    beforeAll(() => {
      g.window = {}
      g.document = { createElement: () => ({ canPlayType: () => 'maybe' }) }
    })
    afterAll(() => {
      if (!hadWindow) delete g.window
      if (!hadDocument) delete g.document
    })

    it('blocks a transcoded HLS track (createMediaElementSource is mute for HLS)', () => {
      expect(webAudioBlocked(true)).toBe(true)
    })

    it('allows a direct-play track (MP3/M4A/AAC/FLAC work on iOS)', () => {
      expect(webAudioBlocked(false)).toBe(false)
    })

    it('remounts only the audio HLS element (so it never inherits a tapped source)', () => {
      expect(audioElementKey(true, true)).toBe('audio-hls') // audio + HLS on WebKit
      expect(audioElementKey(true, false)).toBe('media') // audio direct-play
      expect(audioElementKey(false, true)).toBe('media') // video mode untouched
    })
  })
})
