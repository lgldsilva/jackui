import { describe, it, expect } from 'vitest'
import { shouldBlockHiddenDeepLink } from './deepLinkGate'

const e = (h: string) => ({ infoHash: h })
const HASH = 'badcde2d97743cd1220f2f05d311770d4832a2f6'

describe('shouldBlockHiddenDeepLink', () => {
  it('blocks an item that exists ONLY behind the curtain (hidden)', () => {
    expect(shouldBlockHiddenDeepLink(HASH, [], [e(HASH)])).toBe(true)
  })

  it('allows an item visible without the curtain', () => {
    expect(shouldBlockHiddenDeepLink(HASH, [e(HASH)], [e(HASH)])).toBe(false)
  })

  it('allows a genuine non-library magnet (absent from both lists)', () => {
    expect(shouldBlockHiddenDeepLink(HASH, [e('other')], [e('other')])).toBe(false)
  })

  it('allows when both lists are empty', () => {
    expect(shouldBlockHiddenDeepLink(HASH, [], [])).toBe(false)
  })

  it('does not block an empty hash', () => {
    expect(shouldBlockHiddenDeepLink('', [], [e('')])).toBe(false)
  })
})
