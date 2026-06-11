import { vi, describe, it, expect, beforeEach } from 'vitest'

const getMock = vi.fn()
vi.mock('./http', () => ({
  default: { get: (...a: unknown[]) => getMock(...a) },
  api: { get: (...a: unknown[]) => getMock(...a) },
  withToken: (u: string) => `${u}${u.includes('?') ? '&' : '?'}token=tok`,
  fetchMediaToken: vi.fn(),
  MAGNET_PREFIX: 'magnet:?xt=urn:btih:',
}))

import { buildLocalHash, setLocalViewAsUser } from './client'
import { previewParams, previewRawURL, previewArchiveEntryURL, previewComicPageURL, previewArchiveList } from './preview'

const HASH = 'a'.repeat(40)

describe('previewParams', () => {
  beforeEach(() => {
    setLocalViewAsUser('')
    getMock.mockReset()
  })

  it('builds hash+idx for torrent sources', () => {
    expect(previewParams(HASH, 3).toString()).toBe(`hash=${HASH}&idx=3`)
  })

  it('builds mount+path for local pseudo-hashes', () => {
    const p = previewParams(buildLocalHash('Movies', 'dir/comic.cbz'), 0)
    expect(p.get('mount')).toBe('Movies')
    expect(p.get('path')).toBe('dir/comic.cbz')
    expect(p.get('hash')).toBeNull()
  })

  it('carries the admin view-as user on local sources', () => {
    setLocalViewAsUser('alice')
    const p = previewParams(buildLocalHash('M', 'x.zip'), 0)
    expect(p.get('user')).toBe('alice')
    setLocalViewAsUser('')
  })
})

describe('URL builders', () => {
  it('previewRawURL routes torrent vs local', () => {
    expect(previewRawURL(HASH, 2)).toBe(`/api/stream/${HASH}/2?token=tok`)
    const local = previewRawURL(buildLocalHash('M', 'a/b.pdf'), 0)
    expect(local).toContain('/api/local/file?')
    expect(local).toContain('mount=M')
    expect(local).toContain('token=tok')
  })

  it('entry/page URLs include the inner name and token', () => {
    const u = previewArchiveEntryURL(HASH, 1, 'sub/readme.nfo')
    expect(u).toContain('/api/preview/archive/entry?')
    expect(u).toContain('name=sub%2Freadme.nfo')
    expect(u).toContain('token=tok')

    const p = previewComicPageURL(buildLocalHash('M', 'c.cbz'), 0, 'p01.jpg')
    expect(p).toContain('/api/preview/comic/page?')
    expect(p).toContain('mount=M')
    expect(p).toContain('name=p01.jpg')
  })
})

describe('previewArchiveList', () => {
  it('normalizes the listing payload', async () => {
    getMock.mockResolvedValueOnce({ data: { format: 'zip', entries: [{ name: 'a.txt', size: 5 }], truncated: true } })
    const l = await previewArchiveList(HASH, 0)
    expect(l).toEqual({ format: 'zip', entries: [{ name: 'a.txt', size: 5 }], truncated: true })
    expect(getMock).toHaveBeenCalledWith(`/preview/archive?hash=${HASH}&idx=0`)
  })

  it('tolerates missing fields', async () => {
    getMock.mockResolvedValueOnce({ data: {} })
    const l = await previewArchiveList(HASH, 0)
    expect(l).toEqual({ format: '', entries: [], truncated: false })
  })
})
