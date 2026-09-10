import { describe, it, expect, vi, beforeEach } from 'vitest'

const getMock = vi.fn()
const postMock = vi.fn()
vi.mock('./http', () => ({
  api: { get: (...a: unknown[]) => getMock(...a), post: (...a: unknown[]) => postMock(...a) },
}))

import { oauthExchange, oauthProviders } from './auth'

const bundle = {
  access: 'acc',
  refresh: 'ref',
  expiresAt: '2099-01-01T00:00:00Z',
  user: { id: 1, username: 'luiz', role: 'user' as const, createdAt: '2026-01-01T00:00:00Z' },
}

describe('oauthProviders', () => {
  beforeEach(() => getMock.mockReset())

  it('returns the public feature-detect payload', async () => {
    getMock.mockResolvedValue({ data: { google: true } })
    await expect(oauthProviders()).resolves.toEqual({ google: true })
    expect(getMock).toHaveBeenCalledWith('/auth/oauth/providers')
  })
})

describe('oauthExchange', () => {
  beforeEach(() => postMock.mockReset())

  it('returns the session bundle', async () => {
    postMock.mockResolvedValue({ data: bundle })
    await expect(oauthExchange('ex-1')).resolves.toEqual(bundle)
    expect(postMock).toHaveBeenCalledWith('/auth/oauth/exchange', { code: 'ex-1', totp: '' })
  })

  it('forwards a TOTP on the MFA retry', async () => {
    postMock.mockResolvedValue({ data: bundle })
    await oauthExchange('ex-1', '123456')
    expect(postMock).toHaveBeenCalledWith('/auth/oauth/exchange', { code: 'ex-1', totp: '123456' })
  })

  it('throws mfaRequired in the same shape as password login', async () => {
    postMock.mockResolvedValue({ data: { mfaRequired: true } })
    await expect(oauthExchange('ex-1')).rejects.toMatchObject({
      message: 'mfa required',
      response: { data: { mfaRequired: true } },
    })
  })
})
