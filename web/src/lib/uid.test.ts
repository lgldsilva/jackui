import { describe, it, expect } from 'vitest'
import { uid } from './uid'

const UUID_V4 = /^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$/i

describe('uid', () => {
  it('generates a well-formed UUID v4', () => {
    expect(uid()).toMatch(UUID_V4)
  })

  it('successive calls differ (no collision)', () => {
    const ids = new Set(Array.from({ length: 100 }, () => uid()))
    expect(ids.size).toBe(100)
  })

  it('getRandomValues fallback (no randomUUID) still produces a valid UUID v4', () => {
    const c = globalThis.crypto as Crypto & { randomUUID?: unknown }
    const orig = c.randomUUID
    try {
      // Simulates an insecure context (LAN HTTP): randomUUID unavailable.
      Object.defineProperty(c, 'randomUUID', { value: undefined, configurable: true })
      const a = uid()
      const b = uid()
      expect(a).toMatch(UUID_V4)
      expect(b).toMatch(UUID_V4)
      expect(a).not.toBe(b)
    } finally {
      Object.defineProperty(c, 'randomUUID', { value: orig, configurable: true })
    }
  })
})
