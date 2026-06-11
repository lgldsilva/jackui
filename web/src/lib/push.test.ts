import { describe, expect, it } from 'vitest'
import { urlBase64ToUint8Array } from './push'

describe('urlBase64ToUint8Array', () => {
  it('decodes plain base64', () => {
    // "hello" in base64
    expect(Array.from(urlBase64ToUint8Array('aGVsbG8'))).toEqual([104, 101, 108, 108, 111])
  })

  it('handles url-safe alphabet (- and _)', () => {
    // 0xfb 0xff encodes to "-_8" in base64url
    expect(Array.from(urlBase64ToUint8Array('-_8'))).toEqual([251, 255])
  })

  it('pads to a multiple of 4', () => {
    const a = urlBase64ToUint8Array('QQ') // "A"
    expect(Array.from(a)).toEqual([65])
  })

  it('round-trips a realistic VAPID-sized key', () => {
    const bytes = Array.from({ length: 65 }, (_, i) => i % 256)
    const b64 = btoa(String.fromCharCode(...bytes)).replaceAll('+', '-').replaceAll('/', '_').replaceAll('=', '')
    expect(Array.from(urlBase64ToUint8Array(b64))).toEqual(bytes)
  })
})
