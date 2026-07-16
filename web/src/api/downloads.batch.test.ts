import { vi, describe, it, expect, beforeEach } from 'vitest'

const postMock = vi.fn()
vi.mock('./http', () => ({
  api: { get: vi.fn(), post: (...a: unknown[]) => postMock(...a), delete: vi.fn() },
  withToken: (u: string) => u,
  fetchMediaToken: vi.fn(),
  MAGNET_PREFIX: 'magnet:?xt=urn:btih:',
}))

import {
  buildBatchFiles,
  downloadBatchCreate,
  downloadBatchStopSeed,
  isWholeTorrentSelection,
} from './downloads'
import type { StreamFile } from './client'

const sf = (over: Partial<StreamFile>): StreamFile =>
  ({ index: 0, path: 'f.mkv', size: 0, isVideo: false, downloaded: 0, progress: 0, priority: 'normal', ...over } as StreamFile)

describe('buildBatchFiles', () => {
  it('maps picks to the per-file batch shape', () => {
    const picks = [
      sf({ index: 3, path: 'S01/E01.mkv', size: 100 }),
      sf({ index: 5, path: 'S01/E02.mkv', size: 200 }),
    ]
    expect(buildBatchFiles(picks)).toEqual([
      { fileIndex: 3, filePath: 'S01/E01.mkv', fileSize: 100 },
      { fileIndex: 5, filePath: 'S01/E02.mkv', fileSize: 200 },
    ])
  })

  it('returns an empty array for no picks', () => {
    expect(buildBatchFiles([])).toEqual([])
  })
})

describe('isWholeTorrentSelection', () => {
  const files = [sf({ index: 0 }), sf({ index: 1 }), sf({ index: 2 })]

  it('true quando TODOS os arquivos estão marcados (→ enfileira -2, 1 linha)', () => {
    expect(isWholeTorrentSelection(files, new Set([0, 1, 2]))).toBe(true)
  })

  it('false p/ subconjunto (→ batch granular)', () => {
    expect(isWholeTorrentSelection(files, new Set([0, 2]))).toBe(false)
    expect(isWholeTorrentSelection(files, new Set([1]))).toBe(false)
  })

  it('false p/ seleção vazia e p/ lista vazia', () => {
    expect(isWholeTorrentSelection(files, new Set())).toBe(false)
    expect(isWholeTorrentSelection([], new Set())).toBe(false)
  })

  it('pack tipo Morgpie: 778 arquivos todos marcados = whole torrent', () => {
    const pack = Array.from({ length: 778 }, (_, i) => sf({ index: i }))
    expect(isWholeTorrentSelection(pack, new Set(pack.map(f => f.index)))).toBe(true)
  })
})

describe('downloadBatchCreate', () => {
  beforeEach(() => {
    postMock.mockReset()
    postMock.mockResolvedValue({ status: 200, data: { created: [{ id: 1 }, { id: 2 }], requeued: 0 } })
  })

  it('enqueues every file in ONE POST to /downloads/batch with files[] correct', async () => {
    const res = await downloadBatchCreate({
      infoHash: 'abc', magnet: 'magnet:?xt=urn:btih:abc', name: 'Pack',
      tracker: 'trk', category: 'cat',
      files: buildBatchFiles([
        sf({ index: 0, path: 'E01.mkv', size: 10 }),
        sf({ index: 1, path: 'E02.mkv', size: 20 }),
      ]),
    })

    expect(postMock).toHaveBeenCalledTimes(1)
    const [url, body] = postMock.mock.calls[0] as [string, Record<string, unknown>]
    expect(url).toBe('/downloads/batch')
    expect(body.infoHash).toBe('abc')
    expect(body.tracker).toBe('trk')
    expect(body.files).toEqual([
      { fileIndex: 0, filePath: 'E01.mkv', fileSize: 10 },
      { fileIndex: 1, filePath: 'E02.mkv', fileSize: 20 },
    ])
    expect(res.created).toHaveLength(2)
    expect(res.requeued).toBe(0)
  })
})

describe('downloadBatchStopSeed', () => {
  beforeEach(() => {
    postMock.mockReset()
    postMock.mockResolvedValue({ status: 200, data: { affected: 2, total: 2, failed: [], hashes: 1 } })
  })

  it('no-ops without a network call when ids is empty', async () => {
    const res = await downloadBatchStopSeed([])
    expect(postMock).not.toHaveBeenCalled()
    expect(res).toEqual({ affected: 0, total: 0, failed: [] })
  })

  it('posts ids once to /downloads/batch/stop-seed', async () => {
    const res = await downloadBatchStopSeed([10, 11])
    expect(postMock).toHaveBeenCalledTimes(1)
    const [url, body] = postMock.mock.calls[0] as [string, Record<string, unknown>]
    expect(url).toBe('/downloads/batch/stop-seed')
    expect(body).toEqual({ ids: [10, 11] })
    expect(res.affected).toBe(2)
    expect(res.hashes).toBe(1)
  })
})
