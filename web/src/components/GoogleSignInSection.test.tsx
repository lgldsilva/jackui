import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { cleanup, render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { MemoryRouter } from 'react-router-dom'
import '../test-setup'

const oauthProviders = vi.fn()
vi.mock('../api/client', () => ({
  oauthProviders: (...a: unknown[]) => oauthProviders(...a),
}))

import GoogleSignInSection from './GoogleSignInSection'

afterEach(() => {
  cleanup()
  oauthProviders.mockReset()
})

function renderSection(path = '/login', remember = true, onError = vi.fn()) {
  return {
    onError,
    ...render(
      <MemoryRouter initialEntries={[path]}>
        <GoogleSignInSection remember={remember} onError={onError} />
      </MemoryRouter>,
    ),
  }
}

describe('GoogleSignInSection', () => {
  it('renders nothing when Google is not configured', async () => {
    oauthProviders.mockResolvedValue({ google: false })
    renderSection()
    await waitFor(() => expect(oauthProviders).toHaveBeenCalled())
    expect(screen.queryByRole('button', { name: 'Sign in with Google' })).toBeNull()
  })

  it('renders nothing when the feature-detect fails', async () => {
    oauthProviders.mockRejectedValue(new Error('network'))
    renderSection()
    await waitFor(() => expect(oauthProviders).toHaveBeenCalled())
    expect(screen.queryByRole('button', { name: 'Sign in with Google' })).toBeNull()
  })

  it('surfaces a localized oauthError from the query string', async () => {
    oauthProviders.mockResolvedValue({ google: false })
    const { onError } = renderSection('/login?oauthError=state')
    await waitFor(() => expect(onError).toHaveBeenCalledWith('Login session expired — please try again.'))
  })

  it('falls back to the generic message for an unknown slug', async () => {
    oauthProviders.mockResolvedValue({ google: false })
    const { onError } = renderSection('/login?oauthError=not_a_real_slug')
    await waitFor(() => expect(onError).toHaveBeenCalledWith('Could not sign in with Google.'))
  })

  describe('when Google is enabled', () => {
    beforeEach(() => {
      oauthProviders.mockResolvedValue({ google: true })
      vi.stubGlobal('location', { href: 'http://localhost/login' })
    })
    afterEach(() => {
      vi.unstubAllGlobals()
    })

    it('starts the OAuth round-trip with remember=1', async () => {
      renderSection('/login', true)
      await userEvent.click(await screen.findByRole('button', { name: 'Sign in with Google' }))
      expect(window.location.href).toBe('/api/auth/oauth/google/start?remember=1')
    })

    it('starts the OAuth round-trip with remember=0', async () => {
      renderSection('/login', false)
      await userEvent.click(await screen.findByRole('button', { name: 'Sign in with Google' }))
      expect(window.location.href).toBe('/api/auth/oauth/google/start?remember=0')
    })
  })
})
