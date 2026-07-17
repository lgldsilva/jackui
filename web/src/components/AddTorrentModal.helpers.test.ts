import { vi, describe, it, expect, beforeEach } from 'vitest'

const downloadCreate = vi.fn()
const downloadBatchCreate = vi.fn()
const downloadTorrent = vi.fn()

// Keep pure helpers (createParamsWhenFilesUnknown, sentinels) real so a
// regression to fileIndex:0 fails this suite — only stub the network layer.
vi.mock('../api/client', async (importOriginal) => {
  const actual = await importOriginal<typeof import('../api/client')>()
  return {
    ...actual,
    downloadCreate: (...a: unknown[]) => downloadCreate(...a),
    downloadBatchCreate: (...a: unknown[]) => downloadBatchCreate(...a),
    downloadTorrent: (...a: unknown[]) => downloadTorrent(...a),
  }
})

import { confirmDownloads, type TorrentItem } from './AddTorrentModal.helpers'
import { AUTO_FILE_INDEX, WHOLE_TORRENT_FILE_INDEX } from '../api/downloads'

const INTERNAL_ID = '__internal__'

function item(over: Partial<TorrentItem> & Pick<TorrentItem, 'id' | 'name'>): TorrentItem {
  return {
    loading: false,
    selectedFiles: new Set(),
    infoHash: 'deadbeef',
    magnet: 'magnet:?xt=urn:btih:deadbeef',
    ...over,
  }
}

describe('confirmDownloads — unresolved file list', () => {
  beforeEach(() => {
    downloadCreate.mockReset().mockResolvedValue({ id: 1 })
    downloadBatchCreate.mockReset()
    downloadTorrent.mockReset()
  })

  // Regression: without a resolved file list we used fileIndex:0 → only .nfo.
  it('enqueues AUTO_FILE_INDEX (-1) when item has no files', async () => {
    const n = await confirmDownloads(
      [item({ id: '1', name: 'Scene.Pack.mp4', files: undefined })],
      INTERNAL_ID,
      '',
    )
    expect(n).toBe(1)
    expect(downloadCreate).toHaveBeenCalledTimes(1)
    const payload = downloadCreate.mock.calls[0][0] as { fileIndex: number }
    expect(payload.fileIndex).toBe(AUTO_FILE_INDEX)
    expect(payload.fileIndex).toBe(-1)
    expect(payload.fileIndex).not.toBe(0)
    expect(downloadBatchCreate).not.toHaveBeenCalled()
  })

  it('enqueues AUTO_FILE_INDEX when files is empty array', async () => {
    await confirmDownloads(
      [item({ id: '1', name: 'Empty', files: [] })],
      INTERNAL_ID,
      '',
    )
    const payload = downloadCreate.mock.calls[0][0] as { fileIndex: number }
    expect(payload.fileIndex).toBe(AUTO_FILE_INDEX)
    expect(payload.fileIndex).not.toBe(0)
  })

  it('uses whole-torrent sentinel when every file is selected', async () => {
    const files = [
      { index: 0, path: 'a.nfo', size: 34, isVideo: false, downloaded: 0, progress: 0, priority: 'normal' as const },
      { index: 1, path: 'b.mp4', size: 1e9, isVideo: true, downloaded: 0, progress: 0, priority: 'normal' as const },
    ]
    await confirmDownloads(
      [item({
        id: '1',
        name: 'Pack',
        files,
        selectedFiles: new Set([0, 1]),
      })],
      INTERNAL_ID,
      '',
    )
    expect(downloadCreate).toHaveBeenCalledWith(
      expect.objectContaining({ fileIndex: WHOLE_TORRENT_FILE_INDEX }),
    )
  })
})
