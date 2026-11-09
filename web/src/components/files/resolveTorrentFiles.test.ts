import { describe, expect, it, vi } from 'vitest'
import type { TorrentInfo } from '../../api/client'
import { resolveTorrentFiles } from './resolveTorrentFiles'

const msgs = { notYet: 'not yet', fallbackFailed: 'resolve failed' }
const withFiles = { files: [{ index: 0, path: 'a.mkv', size: 1 }] } as unknown as TorrentInfo
const hash = 'a'.repeat(40)

describe('resolveTorrentFiles', () => {
  it('uses the metadata cache without adding', async () => {
    const add = vi.fn()
    const r = await resolveTorrentFiles({ infoHash: hash }, { metadata: async () => withFiles, add }, msgs)
    expect(r.files).toHaveLength(1)
    expect(add).not.toHaveBeenCalled()
  })

  // Regression: the add error used to be overwritten by the generic
  // "metadata not yet" message (stale closure over filesError).
  it('keeps the streamAdd error message', async () => {
    const r = await resolveTorrentFiles(
      { infoHash: hash },
      { metadata: async () => null, add: async () => { throw new Error('torrent not found on any tracker') } },
      msgs,
    )
    expect(r).toEqual({ error: 'torrent not found on any tracker' })
  })

  it('falls back to the generic failure message when the error has none', async () => {
    const r = await resolveTorrentFiles(
      { infoHash: hash },
      { metadata: async () => null, add: async () => { throw new Error('') } },
      msgs,
    )
    expect(r).toEqual({ error: 'resolve failed' })
  })

  it('reports notYet when the add returns no files', async () => {
    const r = await resolveTorrentFiles(
      { magnetUri: `magnet:?xt=urn:btih:${hash}` },
      { metadata: async () => null, add: async () => ({ files: [] } as unknown as TorrentInfo) },
      msgs,
    )
    expect(r).toEqual({ error: 'not yet' })
  })

  it('never rejects when the metadata lookup throws', async () => {
    const r = await resolveTorrentFiles(
      { infoHash: hash },
      { metadata: async () => { throw new Error('net') }, add: async () => withFiles },
      msgs,
    )
    expect(r.files).toHaveLength(1)
  })
})
