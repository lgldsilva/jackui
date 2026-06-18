import { afterAll, beforeAll, describe, expect, it } from 'vitest'
import { webAudioBlocked, audioElementKey } from './playerFormat'

// webAudioBlocked blocks ONLY the HLS-on-WebKit combination: there
// createMediaElementSource reads zeros and the element goes mute (Apple #231656).
// Direct-play on WebKit is allowed again (guarded by useWebAudioGraph's readyState
// gate). Non-WebKit (Chrome/Firefox/Edge) is never blocked → EQ/visualizer mount.
describe('webAudioBlocked', () => {
  it('never blocks on a non-WebKit browser (no navigator → not Safari)', () => {
    expect(webAudioBlocked(true)).toBe(false)
    expect(webAudioBlocked(false)).toBe(false)
  })

  it('keeps the single reused element on non-WebKit (no per-transport remount)', () => {
    expect(audioElementKey(true, true)).toBe('media')
    expect(audioElementKey(true, false)).toBe('media')
  })

  describe('on WebKit (Safari/iOS)', () => {
    const g = globalThis as unknown as { window?: unknown; document?: unknown; navigator?: unknown }
    const hadWindow = 'window' in g
    const hadDocument = 'document' in g
    const hadNavigator = 'navigator' in g
    // isSafariBrowser() reads navigator.userAgent; canPlayNativeHls() (used by
    // audioElementKey) reads document.createElement().canPlayType(). Stub both so
    // the WebKit branch evaluates true. beforeAll (not the describe body) so it
    // applies AFTER the non-WebKit test above has run.
    beforeAll(() => {
      g.window = {}
      g.document = { createElement: () => ({ canPlayType: () => 'maybe' }) }
      g.navigator = { userAgent: 'Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/17.0 Safari/605.1.15', maxTouchPoints: 0 }
    })
    afterAll(() => {
      if (!hadWindow) delete g.window
      if (!hadDocument) delete g.document
      if (!hadNavigator) delete g.navigator
    })

    it('blocks HLS on WebKit but ALLOWS direct-play (readyState gate keeps it safe)', () => {
      expect(webAudioBlocked(true)).toBe(true)   // HLS on Safari → blocked
      expect(webAudioBlocked(false)).toBe(false) // direct-play on Safari → allowed
    })

    it('remounts only the audio HLS element (so it never inherits a tapped source)', () => {
      expect(audioElementKey(true, true)).toBe('audio-hls') // audio + HLS on WebKit
      expect(audioElementKey(true, false)).toBe('media') // audio direct-play
      expect(audioElementKey(false, true)).toBe('media') // video mode untouched
    })
  })
})
