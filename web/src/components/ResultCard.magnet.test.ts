// Security regression: handleOpenMagnet assigns globalThis.location.href =
// magnet where `magnet` comes from the API (result.magnetUri, indexer-
// influenced). Without a scheme allow-list a `javascript:` URI executes in the
// app origin — and the JWTs live in localStorage, so that is an account-theft
// chain. isSafeMagnetUri is the exported gate the navigation site must consult.
import { describe, it, expect } from 'vitest'
import { isSafeMagnetUri } from './ResultCard'

describe('isSafeMagnetUri', () => {
  it('accepts a magnet:? URI', () => {
    expect(isSafeMagnetUri('magnet:?xt=urn:btih:deadbeef&dn=Test')).toBe(true)
  })

  it('accepts a minimal magnet:? URI', () => {
    expect(isSafeMagnetUri('magnet:?xt=urn:btih:' + 'a'.repeat(40))).toBe(true)
  })

  it('rejects javascript: URIs', () => {
    expect(isSafeMagnetUri('javascript:alert(1)')).toBe(false)
  })

  it('rejects javascript: URIs with mixed case (JaVaScRiPt:)', () => {
    expect(isSafeMagnetUri('JaVaScRiPt:alert(1)')).toBe(false)
  })

  it('rejects data: URIs', () => {
    expect(isSafeMagnetUri('data:text/html,<script>alert(1)</script>')).toBe(false)
  })

  it('rejects http(s) URLs with padding whitespace', () => {
    expect(isSafeMagnetUri(' https://x ')).toBe(false)
    expect(isSafeMagnetUri('\thttps://evil.example/payload')).toBe(false)
  })

  it('rejects empty and whitespace-only input', () => {
    expect(isSafeMagnetUri('')).toBe(false)
    expect(isSafeMagnetUri('   ')).toBe(false)
  })

  it('rejects scheme-lookalikes that do not start with magnet:?', () => {
    expect(isSafeMagnetUri('magnet:xt=urn:btih:deadbeef')).toBe(false) // missing '?'
    expect(isSafeMagnetUri('xmagnet:?xt=urn:btih:deadbeef')).toBe(false)
    expect(isSafeMagnetUri('vbscript:msgbox(1)')).toBe(false)
  })
})
