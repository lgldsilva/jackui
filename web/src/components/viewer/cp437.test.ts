import { describe, it, expect } from 'vitest'
import { decodeCP437, decodeText } from './cp437'

describe('decodeCP437', () => {
  it('decodes DOS box-drawing art (the NFO use case)', () => {
    // ╔══╗ in CP437: C9 CD CD BB
    const bytes = new Uint8Array([0xc9, 0xcd, 0xcd, 0xbb])
    expect(decodeCP437(bytes)).toBe('╔══╗')
  })

  it('decodes shade blocks and accented letters', () => {
    // ░▒▓ = B0 B1 B2; Ç = 80; é = 82
    expect(decodeCP437(new Uint8Array([0xb0, 0xb1, 0xb2]))).toBe('░▒▓')
    expect(decodeCP437(new Uint8Array([0x80, 0x82]))).toBe('Çé')
  })

  it('passes ASCII through unchanged', () => {
    const ascii = 'Hello, NFO! 123\r\n'
    const bytes = new Uint8Array([...ascii].map(c => c.charCodeAt(0)))
    expect(decodeCP437(bytes)).toBe(ascii)
  })

  it('maps 0xFF to NBSP (full table coverage)', () => {
    expect(decodeCP437(new Uint8Array([0xff]))).toBe(' ')
  })
})

describe('decodeText', () => {
  const enc = (s: string) => new TextEncoder().encode(s).buffer as ArrayBuffer

  it('prefers strict UTF-8 when valid', () => {
    expect(decodeText(enc('conteúdo válido'), true)).toBe('conteúdo válido')
  })

  it('falls back to CP437 for NFO-like files with high bytes', () => {
    const buf = new Uint8Array([0xc9, 0xcd, 0xbb]).buffer // invalid UTF-8
    expect(decodeText(buf, true)).toBe('╔═╗')
  })

  it('falls back to lossy UTF-8 for non-NFO files', () => {
    const buf = new Uint8Array([0x41, 0xff, 0x42]).buffer
    expect(decodeText(buf, false)).toBe('A�B')
  })
})
