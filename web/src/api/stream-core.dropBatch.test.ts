import { vi, describe, it, expect, beforeEach } from 'vitest'

const postMock = vi.fn()
const deleteMock = vi.fn()
vi.mock('./http', () => ({
  api: {
    get: vi.fn(),
    post: (...a: unknown[]) => postMock(...a),
    delete: (...a: unknown[]) => deleteMock(...a),
  },
  withToken: (u: string) => u,
  fetchMediaToken: vi.fn(),
  MAGNET_PREFIX: 'magnet:?xt=urn:btih:',
}))

import { streamDropBatch } from './stream-core'

describe('streamDropBatch', () => {
  beforeEach(() => {
    postMock.mockReset()
    postMock.mockResolvedValue({ status: 200, data: { dropped: 2, total: 2, failed: [] } })
  })

  it('no-ops without a network call when hashes is empty', async () => {
    const res = await streamDropBatch([])
    expect(postMock).not.toHaveBeenCalled()
    expect(res).toEqual({ dropped: 0, total: 0, failed: [] })
  })

  it('posts hashes once to /stream/drop/batch', async () => {
    const res = await streamDropBatch(['aaa', 'bbb'])
    expect(postMock).toHaveBeenCalledTimes(1)
    const [url, body] = postMock.mock.calls[0] as [string, Record<string, unknown>]
    expect(url).toBe('/stream/drop/batch')
    expect(body).toEqual({ hashes: ['aaa', 'bbb'] })
    expect(res.dropped).toBe(2)
  })
})
