import { describe, it, expect } from 'vitest'
import { filterAndSortFiles, parseEpisodeTag } from './playerFormat'
import { buildMediaQueue } from './playerHooks'
import { chapterSeekTargets } from './ChaptersPanel'
import type { MediaChapter, StreamFile } from '../../api/client'

// Torrent com episódios FORA de ordem nos índices — o caso real que fazia o
// botão "Próx." pular episódios: a lista exibida ordena por SxxEyy, mas a fila
// antiga seguia a ordem crua dos files.
const mk = (index: number, path: string, size: number, isVideo: boolean): StreamFile =>
  ({ index, path, size, isVideo, downloaded: 0, progress: 0, priority: 'normal' })

const files = [
  mk(0, 'Show/S01E03.mkv', 300, true),
  mk(1, 'Show/S01E01.mkv', 100, true),
  mk(2, 'Show/extras/Making of.mkv', 50, true),
  mk(3, 'Show/S01E02.mkv', 200, true),
  mk(4, 'Show/poster.jpg', 1, false),
]

describe('filterAndSortFiles', () => {
  it('ordena por episódio com extras no fim (a ordem da lista visível)', () => {
    const out = filterAndSortFiles(files, { filter: '', typeFilter: 'all', sortBySize: false, sizeDesc: true })
    expect(out.map(f => f.index)).toEqual([1, 3, 0, 4, 2])
  })

  it('respeita o sort por tamanho quando ativado', () => {
    const out = filterAndSortFiles(files, { filter: '', typeFilter: 'video', sortBySize: true, sizeDesc: true })
    expect(out.map(f => f.index)).toEqual([0, 3, 1, 2])
  })

  it('filtra por texto e por tag de episódio', () => {
    const out = filterAndSortFiles(files, { filter: 's01e02', typeFilter: 'all', sortBySize: false, sizeDesc: true })
    expect(out.map(f => f.index)).toEqual([3])
  })
})

describe('buildMediaQueue sobre a ordem exibida', () => {
  const ordered = filterAndSortFiles(files, { filter: '', typeFilter: 'all', sortBySize: false, sizeDesc: true })

  it('next segue a lista visível: E01 → E02 → E03 (não a ordem dos índices)', () => {
    const fromE01 = buildMediaQueue(ordered, 1)
    expect(fromE01.nextIdx).toBe(3) // E02
    const fromE02 = buildMediaQueue(ordered, 3)
    expect(fromE02.prevIdx).toBe(1) // E01
    expect(fromE02.nextIdx).toBe(0) // E03
  })

  it('exclui arquivos não-reproduzíveis da fila', () => {
    const q = buildMediaQueue(ordered, 1)
    expect(q.indices).not.toContain(4) // poster.jpg fora
  })

  it('arquivo fora da fila → cursor -1 e sem next/prev', () => {
    const q = buildMediaQueue(ordered, 4)
    expect(q.cursor).toBe(-1)
    expect(q.nextIdx).toBe(-1)
    expect(q.prevIdx).toBe(-1)
  })
})

describe('parseEpisodeTag', () => {
  it('normaliza variações de SxxEyy', () => {
    expect(parseEpisodeTag('Show.s1e2.mkv')).toBe('S01E02')
    expect(parseEpisodeTag('Show S01 E10.mkv')).toBe('S01E10')
    expect(parseEpisodeTag('Filme.2024.mkv')).toBeNull()
  })
})

describe('chapterSeekTargets', () => {
  const chapters: MediaChapter[] = [
    { index: 0, startSec: 0, endSec: 60 },
    { index: 1, startSec: 60, endSec: 180 },
    { index: 2, startSec: 180, endSec: 300 },
  ]

  it('avança para o início do próximo capítulo', () => {
    expect(chapterSeekTargets(chapters, 30).nextSec).toBe(60)
    expect(chapterSeekTargets(chapters, 60).nextSec).toBe(180)
  })

  it('no último capítulo não há próximo', () => {
    expect(chapterSeekTargets(chapters, 200).nextSec).toBeNull()
  })

  it('>3s dentro do capítulo, prev volta ao início DELE', () => {
    expect(chapterSeekTargets(chapters, 70).prevSec).toBe(60)
  })

  it('no início do capítulo, prev vai ao capítulo anterior', () => {
    expect(chapterSeekTargets(chapters, 61).prevSec).toBe(0)
  })

  it('no começo do vídeo não há anterior', () => {
    expect(chapterSeekTargets(chapters, 1).prevSec).toBeNull()
  })

  it('lista vazia desabilita os dois', () => {
    expect(chapterSeekTargets([], 10)).toEqual({ prevSec: null, nextSec: null })
  })
})
