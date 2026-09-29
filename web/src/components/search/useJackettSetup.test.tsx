import { afterEach, describe, expect, it, vi } from 'vitest'
import { renderHook, waitFor, cleanup } from '@testing-library/react'
import { useJackettSetup } from './useJackettSetup'

// ─── HTTP client mock ────────────────────────────────────────────────────────
// The Jackett first-run probe MUST go through the authenticated `api` client.
// Plain fetch() sends no Authorization — with auth on, both endpoints
// 401'd on every SearchPage mount and the wizard never appeared. These tests
// lock that regression: the mock spies on `api.get` and watches the global fetch.
const apiGet = vi.fn()

vi.mock('../../api/client', () => ({
  api: { get: (...args: unknown[]) => apiGet(...args) },
}))

afterEach(() => {
  cleanup()
  apiGet.mockReset()
  vi.unstubAllGlobals()
})

const ok = (data: unknown) => Promise.resolve({ data })
const res = () => renderHook(() => useJackettSetup())

describe('useJackettSetup — authenticated probe', () => {
  it('probes /status and /config via api.get, never via plain fetch()', async () => {
    const fetchSpy = vi.fn(() => Promise.resolve({ ok: true, json: () => ({}) }))
    vi.stubGlobal('fetch', fetchSpy)
    apiGet.mockImplementation((url: string) => {
      if (url === '/status') return ok({ jackett: 'down: refused' })
      if (url === '/config') return ok({ jackett: { url: '', apiKeySet: false } })
      return ok({})
    })

    const { result } = res()
    await waitFor(() => expect(result.current.showJackettSetup).toBe(true))

    expect(apiGet).toHaveBeenCalledWith('/status', expect.objectContaining({ validateStatus: expect.any(Function) }))
    expect(apiGet).toHaveBeenCalledWith('/config')
    expect(fetchSpy).not.toHaveBeenCalled()
  })

  it('validateStatus accepts 200 and 503 (degraded loads the jackett field), rejects 401', () => {
    apiGet.mockImplementation((url: string) => {
      if (url === '/status') return ok({ jackett: 'ok' })
      return ok({})
    })

    res()
    expect(apiGet).toHaveBeenCalledWith('/status', expect.anything())
    const { validateStatus } = apiGet.mock.calls[0][1] as { validateStatus: (s: number) => boolean }
    expect(validateStatus(200)).toBe(true)
    expect(validateStatus(503)).toBe(true)
    // 401 must follow the rejection path so the interceptor performs a refresh
    expect(validateStatus(401)).toBe(false)
  })
})

describe('useJackettSetup — prompt decision', () => {
  it('jackett ok → no prompt and no /config probe', async () => {
    apiGet.mockImplementation((url: string) => {
      if (url === '/status') return ok({ jackett: 'ok' })
      return ok({})
    })

    const { result } = res()
    // gives the effect time to resolve before denying the call
    await new Promise(r => setTimeout(r, 20))
    expect(result.current.showJackettSetup).toBe(false)
    expect(apiGet).not.toHaveBeenCalledWith('/config')
  })

  it('jackett down + empty/default config → shows the prompt', async () => {
    apiGet.mockImplementation((url: string) => {
      if (url === '/status') return ok({ jackett: 'down: connection refused' })
      if (url === '/config') return ok({ jackett: { url: 'http://localhost:9117', apiKeySet: false } })
      return ok({})
    })

    const { result } = res()
    await waitFor(() => expect(result.current.showJackettSetup).toBe(true))
  })

  it('jackett down but apiKeySet=true (already configured) → no prompt', async () => {
    apiGet.mockImplementation((url: string) => {
      if (url === '/status') return ok({ jackett: 'down: connection refused' })
      if (url === '/config') return ok({ jackett: { url: 'http://localhost:9117', apiKeySet: true } })
      return ok({})
    })

    const { result } = res()
    await new Promise(r => setTimeout(r, 20))
    expect(result.current.showJackettSetup).toBe(false)
  })

  it('/config unreadable (non-admin 403 rejects) → no prompt', async () => {
    apiGet.mockImplementation((url: string) => {
      if (url === '/status') return ok({ jackett: 'down: connection refused' })
      if (url === '/config') return Promise.reject(Object.assign(new Error('403'), { response: { status: 403 } }))
      return ok({})
    })

    const { result } = res()
    await new Promise(r => setTimeout(r, 20))
    expect(result.current.showJackettSetup).toBe(false)
  })

  it('/status fora do ar (rejeita) → sem prompt', async () => {
    apiGet.mockRejectedValue(new Error('Network Error'))

    const { result } = res()
    await new Promise(r => setTimeout(r, 20))
    expect(result.current.showJackettSetup).toBe(false)
  })
})
