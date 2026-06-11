import { vi, describe, it, expect, beforeEach } from 'vitest'

// Mock the HTTP core so client.ts (and the domain modules it barrels) get a
// fake axios instance. `get` drives the local-file flow; `post` the download
// queue flow.
const getMock = vi.fn()
const postMock = vi.fn()
vi.mock('./http', () => ({
  api: { get: (...a: unknown[]) => getMock(...a), post: (...a: unknown[]) => postMock(...a), delete: vi.fn() },
  withToken: (u: string) => u,
  fetchMediaToken: vi.fn(),
  MAGNET_PREFIX: 'magnet:?xt=urn:btih:',
}))

import { streamInfo, buildLocalHash, queueAllTorrentFiles, WHOLE_TORRENT_FILE_INDEX, type TorrentInfo } from './client'

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

describe('queueAllTorrentFiles', () => {
  beforeEach(() => {
    postMock.mockReset()
    postMock.mockResolvedValue({ status: 200, data: { id: 7, status: 'queued' } })
  })

  it('queues the WHOLE torrent as ONE request with the sentinel fileIndex', async () => {
    // 5699 arquivos — a versão antiga fazia 5699 POSTs (1 por arquivo) e
    // estourava o navegador; agora tem que ser exatamente UMA chamada.
    const files = Array.from({ length: 5699 }, (_, i) => ({
      index: i, path: `dir/f${i}.bin`, size: 1000, isVideo: false,
    }))
    const info = {
      infoHash: 'abc123', name: 'Big', files, totalSize: 5_699_000,
    } as unknown as TorrentInfo

    const entry = await queueAllTorrentFiles(info, 'magnet:?xt=urn:btih:abc123', 'Big', 'trk', 'cat')

    expect(postMock).toHaveBeenCalledTimes(1)
    const [url, body] = postMock.mock.calls[0] as [string, Record<string, unknown>]
    expect(url).toBe('/downloads')
    expect(body.fileIndex).toBe(WHOLE_TORRENT_FILE_INDEX)
    expect(body.infoHash).toBe('abc123')
    expect(body.fileSize).toBe(5_699_000)
    expect(body.tracker).toBe('trk')
    expect(body.category).toBe('cat')
    expect(entry.id).toBe(7)
  })

  it('propagates a rejected create (no allSettled swallowing)', async () => {
    postMock.mockRejectedValue(new Error('boom'))
    const info = { infoHash: 'x', name: 'N', files: [], totalSize: 0 } as unknown as TorrentInfo
    await expect(queueAllTorrentFiles(info, 'magnet:?xt=urn:btih:x', 'N')).rejects.toThrow('boom')
  })
})
