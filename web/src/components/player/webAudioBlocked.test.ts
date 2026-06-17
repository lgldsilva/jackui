import { afterAll, beforeAll, describe, expect, it } from 'vitest'
import { webAudioBlocked, audioElementKey } from './playerFormat'

// webAudioBlocked blocks the Web Audio graph on ALL of WebKit (Safari/iOS):
// createMediaElementSource freezes the element (readyState 2, mute) even for
// direct-play, so WebKit plays natively. Non-WebKit (Chrome/Firefox/Edge — incl.
// macOS, which isSafariBrowser() correctly excludes) is never blocked → EQ mounts.
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

    it('blocks ALL WebKit — HLS and direct-play alike (createMediaElementSource freezes the element)', () => {
      expect(webAudioBlocked(true)).toBe(true)  // HLS on Safari → blocked
      expect(webAudioBlocked(false)).toBe(true) // direct-play on Safari → ALSO blocked (freeze)
    })

    it('remounts only the audio HLS element (so it never inherits a tapped source)', () => {
      expect(audioElementKey(true, true)).toBe('audio-hls') // audio + HLS on WebKit
      expect(audioElementKey(true, false)).toBe('media') // audio direct-play
      expect(audioElementKey(false, true)).toBe('media') // video mode untouched
    })
  })
})
