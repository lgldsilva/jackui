import { vi, describe, it, expect, beforeEach } from 'vitest'

// Mock the HTTP core so client.ts (and the domain modules it barrels) get a
// fake axios instance. We only need `get` for the local-file flow.
const getMock = vi.fn()
vi.mock('./http', () => ({
  api: { get: (...a: unknown[]) => getMock(...a), post: vi.fn(), delete: vi.fn() },
  withToken: (u: string) => u,
  fetchMediaToken: vi.fn(),
  MAGNET_PREFIX: 'magnet:?xt=urn:btih:',
}))

import { streamInfo, buildLocalHash } from './client'

describe('streamInfo for local files', () => {
  beforeEach(() => {
    getMock.mockReset()
    getMock.mockImplementation((url: string) => {
      if (url.startsWith('/local/play')) {
        return Promise.resolve({ status: 200, data: { kind: 'hls', url: '/api/local/hls/index.m3u8?mount=M&path=p' } })
      }
      if (url.startsWith('/local/transfer-status')) {
        return Promise.resolve({
          status: 200,
          data: { bytesRead: 5_000_000, ratePerSec: 1_048_576, size: 10_000_000, active: true, stalled: false },
        })
      }
      return Promise.resolve({ status: 404, data: {} })
    })
  })

  it('maps transfer-status onto downRate / totalSize / progress', async () => {
    const hash = buildLocalHash('Movies', 'film/movie.mkv')

    // First call resolves the playable URL (caches it) via /local/play.
    await streamInfo(hash)
    // Second call takes the cheap transfer-status path and maps throughput.
    const info = await streamInfo(hash)

    expect(info.downRate).toBe(1_048_576)
    expect(info.totalSize).toBe(10_000_000)
    expect(info.progress).toBeCloseTo(0.5, 5)
    expect(info.files[0].downloaded).toBe(5_000_000)
    expect(info.stalled).toBe(false)
    // The poll path must NOT re-run ffprobe via /local/play on every tick.
    const playCalls = getMock.mock.calls.filter(([u]) => String(u).startsWith('/local/play'))
    expect(playCalls).toHaveLength(1)
  })

  it('reports stalled when no bytes flow and rate is zero', async () => {
    const hash = buildLocalHash('Drive', 'slow.mkv')
    await streamInfo(hash) // prime the URL cache

    getMock.mockImplementation((url: string) => {
      if (url.startsWith('/local/transfer-status')) {
        return Promise.resolve({ status: 200, data: { bytesRead: 1024, ratePerSec: 0, size: 999999, active: false, stalled: true } })
      }
      return Promise.resolve({ status: 200, data: { kind: 'hls', url: '/x' } })
    })

    const info = await streamInfo(hash)
    expect(info.downRate).toBe(0)
    expect(info.stalled).toBe(true)
  })
})
