import { afterEach, describe, expect, it, vi } from 'vitest'
import { cleanup, render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { MemoryRouter, Route, Routes } from 'react-router-dom'
import '../test-setup'

const oauthExchange = vi.fn()
const completeOAuthLogin = vi.fn()
vi.mock('../api/client', () => ({
  oauthExchange: (...a: unknown[]) => oauthExchange(...a),
}))
vi.mock('../auth/AuthContext', () => ({
  useAuth: () => ({ completeOAuthLogin }),
}))

import OAuthCallbackPage from './OAuthCallback'

afterEach(() => {
  cleanup()
  oauthExchange.mockReset()
  completeOAuthLogin.mockReset()
})

const bundle = {
  access: 'acc',
  refresh: 'ref',
  user: { id: 1, username: 'luiz', role: 'user', createdAt: '2026-01-01T00:00:00Z' },
}

function renderAt(path: string) {
  return render(
    <MemoryRouter initialEntries={[path]}>
      <Routes>
        <Route path="/auth/google/callback" element={<OAuthCallbackPage />} />
        <Route path="/login" element={<div>login-page</div>} />
        <Route path="/" element={<div>home-page</div>} />
      </Routes>
    </MemoryRouter>,
  )
}

describe('OAuthCallbackPage', () => {
  it('bounces to login when the exchange code is missing', async () => {
    renderAt('/auth/google/callback')
    expect(await screen.findByText('login-page')).toBeInTheDocument()
    expect(oauthExchange).not.toHaveBeenCalled()
  })

  it('preserves a provider oauthError slug when bouncing without a code', async () => {
    renderAt('/auth/google/callback?oauthError=exchange')
    expect(await screen.findByText('login-page')).toBeInTheDocument()
  })

  it('completes the session and goes home on a successful exchange', async () => {
    oauthExchange.mockResolvedValue(bundle)
    renderAt('/auth/google/callback?code=ex-1')
    expect(await screen.findByText('home-page')).toBeInTheDocument()
    expect(completeOAuthLogin).toHaveBeenCalledWith(bundle)
    expect(oauthExchange).toHaveBeenCalledWith('ex-1')
  })

  it('shows the backend error and a path back to login', async () => {
    oauthExchange.mockRejectedValue({ response: { data: { error: 'expired code' } } })
    renderAt('/auth/google/callback?code=ex-1')
    expect(await screen.findByText('expired code')).toBeInTheDocument()
    await userEvent.click(screen.getByRole('button', { name: 'Go to login' }))
    expect(await screen.findByText('login-page')).toBeInTheDocument()
  })

  it('falls back to the generic login-failed copy', async () => {
    oauthExchange.mockRejectedValue(new Error('network'))
    renderAt('/auth/google/callback?code=ex-1')
    expect(await screen.findByText('Login failed')).toBeInTheDocument()
  })

  it('challenges MFA, retries with TOTP, then completes', async () => {
    oauthExchange
      .mockRejectedValueOnce({ response: { data: { mfaRequired: true } } })
      .mockResolvedValueOnce(bundle)
    renderAt('/auth/google/callback?code=ex-1')
    expect(await screen.findByText('MFA or recovery code')).toBeInTheDocument()
    await userEvent.type(screen.getByPlaceholderText('000000 or xxxx-xxxx'), '123456')
    await userEvent.click(screen.getByRole('button', { name: 'Sign in' }))
    await waitFor(() => expect(completeOAuthLogin).toHaveBeenCalledWith(bundle))
    expect(oauthExchange).toHaveBeenLastCalledWith('ex-1', '123456')
    expect(await screen.findByText('home-page')).toBeInTheDocument()
  })

  it('keeps the MFA prompt after a wrong TOTP', async () => {
    oauthExchange
      .mockRejectedValueOnce({ response: { data: { mfaRequired: true } } })
      .mockRejectedValueOnce({ response: { data: { error: 'bad totp' } } })
    renderAt('/auth/google/callback?code=ex-1')
    await userEvent.type(await screen.findByPlaceholderText('000000 or xxxx-xxxx'), '000000')
    await userEvent.click(screen.getByRole('button', { name: 'Sign in' }))
    expect(await screen.findByText('bad totp')).toBeInTheDocument()
    expect(screen.getByText('MFA or recovery code')).toBeInTheDocument()
  })

  it('falls back to invalid_code when the MFA retry has no message', async () => {
    oauthExchange
      .mockRejectedValueOnce({ response: { data: { mfaRequired: true } } })
      .mockRejectedValueOnce(new Error('network'))
    renderAt('/auth/google/callback?code=ex-1')
    await userEvent.type(await screen.findByPlaceholderText('000000 or xxxx-xxxx'), '000000')
    await userEvent.click(screen.getByRole('button', { name: 'Sign in' }))
    expect(await screen.findByText('Invalid code, try again.')).toBeInTheDocument()
  })
})
