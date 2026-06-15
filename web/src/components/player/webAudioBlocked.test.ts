import { afterAll, beforeAll, describe, expect, it } from 'vitest'
import { webAudioBlocked, audioElementKey } from './playerFormat'

// webAudioBlocked blocks ALL of WebKit (Safari/iOS): createMediaElementSource
// there makes the graph the only output and a suspended AudioContext stalls the
// element (readyState 2) even for direct-play — so WebKit plays natively, no tap.
// Non-WebKit (Chrome/Firefox) is never blocked → EQ/visualizer mount.
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

    it('blocks ALL WebKit — HLS and direct-play alike (createMediaElementSource stalls)', () => {
      expect(webAudioBlocked(true)).toBe(true)
      expect(webAudioBlocked(false)).toBe(true)
    })

    it('remounts only the audio HLS element (so it never inherits a tapped source)', () => {
      expect(audioElementKey(true, true)).toBe('audio-hls') // audio + HLS on WebKit
      expect(audioElementKey(true, false)).toBe('media') // audio direct-play
      expect(audioElementKey(false, true)).toBe('media') // video mode untouched
    })
  })
})
