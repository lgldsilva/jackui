import { describe, it, expect } from 'vitest'
import { countTorrents } from './DownloadsPage'
import type { DownloadEntry } from '../api/client'

const dl = (over: Partial<DownloadEntry>): DownloadEntry =>
  ({
    id: 1, status: 'downloading', infoHash: '', fileIndex: 0, name: 'x', filePath: '',
    fileSize: 0, bytesDownloaded: 0, progress: 0, magnet: '', createdAt: '', userId: 1,
    ...over,
  } as DownloadEntry)

describe('countTorrents', () => {
  it('conta UM torrent mesmo com N linhas por-arquivo (pack tipo Morgpie)', () => {
    const rows = Array.from({ length: 778 }, (_, i) =>
      dl({ id: i + 1, infoHash: 'a7587f', fileIndex: i }))
    expect(countTorrents(rows)).toBe(1)
  })

  it('conta torrents distintos por infoHash', () => {
    const rows = [
      dl({ id: 1, infoHash: 'aaa' }), dl({ id: 2, infoHash: 'aaa' }),
      dl({ id: 3, infoHash: 'bbb' }),
      dl({ id: 4, infoHash: 'ccc' }), dl({ id: 5, infoHash: 'ccc' }), dl({ id: 6, infoHash: 'ccc' }),
    ]
    expect(countTorrents(rows)).toBe(3)
  })

  it('linhas sem infoHash (pré-metadata) contam individualmente', () => {
    const rows = [dl({ id: 1, infoHash: '' }), dl({ id: 2, infoHash: '' }), dl({ id: 3, infoHash: 'x' })]
    expect(countTorrents(rows)).toBe(3) // 2 hashless distintas + 1 com hash
  })

  it('lista vazia = 0', () => {
    expect(countTorrents([])).toBe(0)
  })
})
