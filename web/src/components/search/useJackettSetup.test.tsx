import { afterEach, describe, expect, it, vi } from 'vitest'
import { renderHook, waitFor, cleanup } from '@testing-library/react'
import { useJackettSetup } from './useJackettSetup'

// ─── Mock do cliente HTTP ────────────────────────────────────────────────────
// O probe de first-run do Jackett DEVE passar pelo cliente `api` autenticado.
// fetch() puro não envia Authorization — com auth ligada os dois endpoints
// 401'avam a cada mount da SearchPage e o wizard nunca aparecia. Estes testes
// travam essa regressão: o mock espia `api.get` e vigia o fetch global.
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

describe('useJackettSetup — probe autenticado', () => {
  it('proba /status e /config via api.get, nunca via fetch() puro', async () => {
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

  it('validateStatus aceita 200 e 503 (degraded carrega o campo jackett), rejeita 401', () => {
    apiGet.mockImplementation((url: string) => {
      if (url === '/status') return ok({ jackett: 'ok' })
      return ok({})
    })

    res()
    expect(apiGet).toHaveBeenCalledWith('/status', expect.anything())
    const { validateStatus } = apiGet.mock.calls[0][1] as { validateStatus: (s: number) => boolean }
    expect(validateStatus(200)).toBe(true)
    expect(validateStatus(503)).toBe(true)
    // 401 precisa seguir o caminho de rejeição para o interceptor fazer refresh
    expect(validateStatus(401)).toBe(false)
  })
})

describe('useJackettSetup — decisão do prompt', () => {
  it('jackett ok → sem prompt e sem probe de /config', async () => {
    apiGet.mockImplementation((url: string) => {
      if (url === '/status') return ok({ jackett: 'ok' })
      return ok({})
    })

    const { result } = res()
    // dá tempo do efeito resolver antes de negar a chamada
    await new Promise(r => setTimeout(r, 20))
    expect(result.current.showJackettSetup).toBe(false)
    expect(apiGet).not.toHaveBeenCalledWith('/config')
  })

  it('jackett down + config vazio/default → mostra o prompt', async () => {
    apiGet.mockImplementation((url: string) => {
      if (url === '/status') return ok({ jackett: 'down: connection refused' })
      if (url === '/config') return ok({ jackett: { url: 'http://localhost:9117', apiKeySet: false } })
      return ok({})
    })

    const { result } = res()
    await waitFor(() => expect(result.current.showJackettSetup).toBe(true))
  })

  it('jackett down mas apiKeySet=true (já configurado) → sem prompt', async () => {
    apiGet.mockImplementation((url: string) => {
      if (url === '/status') return ok({ jackett: 'down: connection refused' })
      if (url === '/config') return ok({ jackett: { url: 'http://localhost:9117', apiKeySet: true } })
      return ok({})
    })

    const { result } = res()
    await new Promise(r => setTimeout(r, 20))
    expect(result.current.showJackettSetup).toBe(false)
  })

  it('/config ilegível (403 de não-admin rejeita) → sem prompt', async () => {
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
