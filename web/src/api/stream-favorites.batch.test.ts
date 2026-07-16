import { vi, describe, it, expect, beforeEach } from 'vitest'

const postMock = vi.fn()
const deleteMock = vi.fn()
const patchMock = vi.fn()
vi.mock('./http', () => ({
  api: {
    get: vi.fn(),
    post: (...a: unknown[]) => postMock(...a),
    delete: (...a: unknown[]) => deleteMock(...a),
    patch: (...a: unknown[]) => patchMock(...a),
  },
  withToken: (u: string) => u,
  fetchMediaToken: vi.fn(),
  MAGNET_PREFIX: 'magnet:?xt=urn:btih:',
}))

import { favoriteRemoveBatch, favoriteSetFolderBatch } from './stream-favorites'

describe('favoriteRemoveBatch', () => {
  beforeEach(() => {
    postMock.mockReset()
    postMock.mockResolvedValue({ status: 200, data: { affected: 2, total: 2, failed: [] } })
  })

  it('no-ops without a network call when names is empty', async () => {
    const res = await favoriteRemoveBatch([])
    expect(postMock).not.toHaveBeenCalled()
    expect(res).toEqual({ affected: 0, total: 0, failed: [] })
  })

  it('posts names once to /stream/favorites/batch/remove', async () => {
    const res = await favoriteRemoveBatch(['Alpha', 'Beta'])
    expect(postMock).toHaveBeenCalledTimes(1)
    const [url, body] = postMock.mock.calls[0] as [string, Record<string, unknown>]
    expect(url).toBe('/stream/favorites/batch/remove')
    expect(body).toEqual({ names: ['Alpha', 'Beta'] })
    expect(res.affected).toBe(2)
  })
})

describe('favoriteSetFolderBatch', () => {
  beforeEach(() => {
    postMock.mockReset()
    postMock.mockResolvedValue({ status: 200, data: { affected: 2, total: 2, failed: [] } })
  })

  it('no-ops without a network call when names is empty', async () => {
    const res = await favoriteSetFolderBatch([], 3)
    expect(postMock).not.toHaveBeenCalled()
    expect(res).toEqual({ affected: 0, total: 0, failed: [] })
  })

  it('posts names + folderId once to /stream/favorites/batch/folder', async () => {
    const res = await favoriteSetFolderBatch(['Alpha', 'Beta'], 7)
    expect(postMock).toHaveBeenCalledTimes(1)
    const [url, body] = postMock.mock.calls[0] as [string, Record<string, unknown>]
    expect(url).toBe('/stream/favorites/batch/folder')
    expect(body).toEqual({ names: ['Alpha', 'Beta'], folderId: 7 })
    expect(res.affected).toBe(2)
  })

  it('uses toRoot when folderId is null', async () => {
    await favoriteSetFolderBatch(['Alpha'], null)
    const [, body] = postMock.mock.calls[0] as [string, Record<string, unknown>]
    expect(body).toEqual({ names: ['Alpha'], toRoot: true })
  })
})
