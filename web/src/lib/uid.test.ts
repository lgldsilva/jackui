import { describe, it, expect } from 'vitest'
import { uid } from './uid'

const UUID_V4 = /^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$/i

describe('uid', () => {
  it('gera um UUID v4 bem formado', () => {
    expect(uid()).toMatch(UUID_V4)
  })

  it('chamadas sucessivas diferem (sem colisão)', () => {
    const ids = new Set(Array.from({ length: 100 }, () => uid()))
    expect(ids.size).toBe(100)
  })

  it('fallback getRandomValues (sem randomUUID) ainda produz UUID v4 válido', () => {
    const c = globalThis.crypto as Crypto & { randomUUID?: unknown }
    const orig = c.randomUUID
    try {
      // Simula contexto inseguro (HTTP de LAN): randomUUID indisponível.
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
