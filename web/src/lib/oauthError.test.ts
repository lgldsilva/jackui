import { describe, expect, it } from 'vitest'
import { resolveOAuthError } from './oauthError'

const catalog: Record<string, string> = {
  'login.oauth_error_state': 'expired',
  'login.oauth_error_generic': 'generic',
}

const t = (key: string) => catalog[key] ?? key

describe('resolveOAuthError', () => {
  it('maps a known slug to its login message', () => {
    expect(resolveOAuthError('state', t)).toBe('expired')
  })

  it('falls back for an unknown slug', () => {
    expect(resolveOAuthError('not_a_real_slug', t)).toBe('generic')
  })
})
