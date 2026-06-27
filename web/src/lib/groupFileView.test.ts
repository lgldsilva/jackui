import { describe, it, expect } from 'vitest'
import { viewGroupFiles, groupStatusCounts } from './groupFileView'
import type { DownloadEntry } from '../api/downloads'

// Minimal factory — only the fields viewGroupFiles/groupStatusCounts read.
function mk(over: Partial<DownloadEntry>): DownloadEntry {
  return {
    id: 0, userId: 0, infoHash: 'h', fileIndex: 0, filePath: '', fileSize: 0,
    name: '', magnet: '', status: 'downloading', bytesDownloaded: 0, progress: 0,
    createdAt: '',
    ...over,
  } as DownloadEntry
}

describe('viewGroupFiles', () => {
  const files = [
    mk({ id: 1, filePath: 'S01E10.mkv', fileSize: 300, status: 'completed' }),
    mk({ id: 2, filePath: 'S01E02.mkv', fileSize: 100, status: 'downloading', bytesDownloaded: 50 }),
    mk({ id: 3, filePath: 'S01E01.mkv', fileSize: 200, status: 'queued', bytesDownloaded: 0 }),
  ]

  it('filtra só concluídos', () => {
    const r = viewGroupFiles(files, 'completed', 'name', 'asc')
    expect(r.map((d) => d.id)).toEqual([1])
  })

  it('filtra só ativos (não-concluídos)', () => {
    const r = viewGroupFiles(files, 'active', 'name', 'asc')
    expect(r.map((d) => d.id)).toEqual([3, 2]) // E01, E02 em ordem natural
  })

  it('ordena por nome natural (asc)', () => {
    const r = viewGroupFiles(files, 'all', 'name', 'asc')
    expect(r.map((d) => d.id)).toEqual([3, 2, 1]) // E01, E02, E10
  })

  it('ordena por nome desc', () => {
    const r = viewGroupFiles(files, 'all', 'name', 'desc')
    expect(r.map((d) => d.id)).toEqual([1, 2, 3])
  })

  it('ordena por tamanho asc', () => {
    const r = viewGroupFiles(files, 'all', 'size', 'asc')
    expect(r.map((d) => d.fileSize)).toEqual([100, 200, 300])
  })

  it('ordena por progresso (concluído=1 no topo em desc)', () => {
    const r = viewGroupFiles(files, 'all', 'progress', 'desc')
    expect(r[0].id).toBe(1) // completed = progress 1
  })

  it('é estável nos empates de status (preserva ordem de entrada)', () => {
    const same = [
      mk({ id: 10, filePath: 'b', fileSize: 0, status: 'downloading' }),
      mk({ id: 11, filePath: 'a', fileSize: 0, status: 'downloading' }),
    ]
    // ordenando por size (todos 0 → empate) mantém a ordem original
    expect(viewGroupFiles(same, 'all', 'size', 'asc').map((d) => d.id)).toEqual([10, 11])
  })

  it('não muta o array de entrada', () => {
    const copy = [...files]
    viewGroupFiles(files, 'all', 'size', 'desc')
    expect(files).toEqual(copy)
  })
})

describe('groupStatusCounts', () => {
  it('conta total, ativos e concluídos', () => {
    const files = [
      mk({ status: 'completed' }),
      mk({ status: 'downloading' }),
      mk({ status: 'queued' }),
      mk({ status: 'completed' }),
    ]
    expect(groupStatusCounts(files)).toEqual({ all: 4, active: 2, completed: 2 })
  })

  it('lista vazia → tudo zero', () => {
    expect(groupStatusCounts([])).toEqual({ all: 0, active: 0, completed: 0 })
  })
})
