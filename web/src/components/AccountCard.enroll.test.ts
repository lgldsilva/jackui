// Security guard: the MFA enroll URI lands in an <a href>. It is API data, so
// only otpauth:// (TOTP app deep link) and https:// may render the anchor —
// javascript:/data:/custom schemes must not produce a clickable link.
import { afterEach, describe, expect, it } from 'vitest'
import { isSafeEnrollUri } from './AccountCard'

afterEach(() => { localStorage.clear() })

describe('isSafeEnrollUri', () => {
  it('accepts otpauth:// TOTP URIs', () => {
    expect(isSafeEnrollUri('otpauth://totp/JackUI:user?secret=ABC234&issuer=JackUI')).toBe(true)
  })

  it('accepts https:// URIs', () => {
    expect(isSafeEnrollUri('https://example.com/mfa/setup')).toBe(true)
  })

  it('rejects javascript:, data: and unknown schemes', () => {
    expect(isSafeEnrollUri('javascript:alert(1)')).toBe(false)
    expect(isSafeEnrollUri('JaVaScRiPt:alert(1)')).toBe(false)
    expect(isSafeEnrollUri('data:text/html,<script>1</script>')).toBe(false)
    expect(isSafeEnrollUri('file:///etc/passwd')).toBe(false)
    expect(isSafeEnrollUri('http://insecure.example/x')).toBe(false)
  })

  it('rejects empty and whitespace-padded input', () => {
    expect(isSafeEnrollUri('')).toBe(false)
    expect(isSafeEnrollUri(' https://x ')).toBe(false)
  })
})
