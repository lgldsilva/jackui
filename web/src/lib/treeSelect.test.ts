import { describe, it, expect } from 'vitest'
import { buildFileTree, type DirNode } from './fileTree'
import {
  getFileIndicesUnder, dirTriState, toggleDirSelection, toggleFileSelection,
  allIndicesUnderRoot, parentDir, filesUnderDir,
} from './treeSelect'
import { isWholeTorrentSelection } from '../api/downloads'
import type { StreamFile } from '../api/client'

const mk = (index: number, path: string, size = 100, isVideo = true): StreamFile =>
  ({ index, path, size, isVideo, downloaded: 0, progress: 0, priority: 'normal' })

const dirChild = (n: DirNode, name: string): DirNode => {
  const c = n.children.find(x => x.type === 'dir' && x.name === name)
  if (!c || c.type !== 'dir') throw new Error(`no dir ${name}`)
  return c
}

describe('getFileIndicesUnder', () => {
  it('a file leaf yields its own index', () => {
    const root = buildFileTree([mk(7, 'a/movie.mkv')])
    const a = dirChild(root, 'a')
    expect(getFileIndicesUnder(a.children[0])).toEqual([7])
  })

  it('a nested folder yields every descendant index, no dirs', () => {
    const root = buildFileTree([
      mk(0, 'Show/Season 1/S01E01.mkv'),
      mk(1, 'Show/Season 1/S01E02.mkv'),
      mk(2, 'Show/Season 1/extras/Making of.mkv'),
      mk(3, 'Show/poster.jpg', 1, false),
    ])
    const show = dirChild(root, 'Show')
    expect(getFileIndicesUnder(show).sort((a, b) => a - b)).toEqual([0, 1, 2, 3])
    const season = dirChild(show, 'Season 1')
    expect(getFileIndicesUnder(season).sort((a, b) => a - b)).toEqual([0, 1, 2])
  })

  it('root with loose files + subfolders yields the full union', () => {
    const root = buildFileTree([mk(0, 'top.mkv'), mk(1, 'A/x.mkv'), mk(2, 'A/B/y.mkv')])
    expect(getFileIndicesUnder(root).sort((a, b) => a - b)).toEqual([0, 1, 2])
  })
})

describe('dirTriState', () => {
  const files = [
    mk(0, 'S/E01.mkv'),
    mk(1, 'S/E02.mkv'),
    mk(2, 'S/E03.mkv'),
  ]
  const root = buildFileTree(files)
  const s = dirChild(root, 'S')

  it('all descendants selected → all', () => {
    expect(dirTriState(s, new Set([0, 1, 2]))).toBe('all')
  })
  it('none selected → none', () => {
    expect(dirTriState(s, new Set())).toBe('none')
  })
  it('partial selection → some (central case)', () => {
    expect(dirTriState(s, new Set([1]))).toBe('some')
  })

  it('empty/filtered folder → none, not all', () => {
    const empty = buildFileTree(files, { filter: 'nomatch' })
    expect(empty.children).toEqual([])
    // the synthetic root with no children
    expect(dirTriState(empty, new Set([0, 1, 2]))).toBe('none')
  })

  it('a fully-selected subfolder leaves the parent at some', () => {
    const r = buildFileTree([
      mk(0, 'Show/Season 1/E01.mkv'),
      mk(1, 'Show/Season 1/E02.mkv'),
      mk(2, 'Show/Season 2/E01.mkv'),
    ])
    const show = dirChild(r, 'Show')
    const season1 = dirChild(show, 'Season 1')
    expect(dirTriState(season1, new Set([0, 1]))).toBe('all')
    expect(dirTriState(show, new Set([0, 1]))).toBe('some')
  })
})

describe('toggleDirSelection', () => {
  const files = [mk(0, 'S/E01.mkv'), mk(1, 'S/E02.mkv'), mk(2, 'S/E03.mkv')]
  const root = buildFileTree(files)
  const s = dirChild(root, 'S')

  it('from none → selects all descendants', () => {
    expect([...toggleDirSelection(s, new Set())].sort((a, b) => a - b)).toEqual([0, 1, 2])
  })
  it('from some → selects all descendants', () => {
    expect([...toggleDirSelection(s, new Set([1]))].sort((a, b) => a - b)).toEqual([0, 1, 2])
  })
  it('from all → removes all descendants', () => {
    expect([...toggleDirSelection(s, new Set([0, 1, 2]))]).toEqual([])
  })
  it('does not mutate the input set (immutable)', () => {
    const input = new Set([1])
    const out = toggleDirSelection(s, input)
    expect(input).toEqual(new Set([1]))
    expect(out).not.toBe(input)
  })
  it('preserves indices outside the folder when toggling', () => {
    const r = buildFileTree([mk(0, 'A/x.mkv'), mk(1, 'B/y.mkv')])
    const a = dirChild(r, 'A')
    // selecting A keeps B's index 1
    expect([...toggleDirSelection(a, new Set([1]))].sort((x, y) => x - y)).toEqual([0, 1])
  })
  it('a single-child collapsed chain still covers its leaf', () => {
    const r = buildFileTree([mk(0, 'a/b/c/movie.mkv')])
    const node = r.children[0] as DirNode
    expect(node.name).toBe('a / b / c')
    expect([...toggleDirSelection(node, new Set())]).toEqual([0])
  })
})

describe('toggleFileSelection', () => {
  it('adds then removes an index, immutably', () => {
    const a = toggleFileSelection(5, new Set())
    expect([...a]).toEqual([5])
    const b = toggleFileSelection(5, a)
    expect([...b]).toEqual([])
    expect([...a]).toEqual([5]) // a untouched
  })
})

describe('allIndicesUnderRoot + isWholeTorrentSelection (778-file pack)', () => {
  const files = Array.from({ length: 778 }, (_, i) =>
    mk(i, `Pack/Disc ${Math.floor(i / 100)}/track ${i}.flac`, 1000, false))
  const root = buildFileTree(files)

  it('toggling the pack folder selects all 778', () => {
    const pack = root.children[0] as DirNode
    expect(getFileIndicesUnder(pack)).toHaveLength(778)
    expect(toggleDirSelection(pack, new Set()).size).toBe(778)
  })

  it('allIndicesUnderRoot selects everything and trips whole-torrent', () => {
    const all = allIndicesUnderRoot(root)
    expect(all.size).toBe(778)
    expect(isWholeTorrentSelection(files, all)).toBe(true)
  })

  it('777/778 selected → root tri-state is some, not whole-torrent', () => {
    const partial = new Set(Array.from({ length: 777 }, (_, i) => i))
    expect(dirTriState(root, partial)).toBe('some')
    expect(isWholeTorrentSelection(files, partial)).toBe(false)
  })
})

describe('allIndicesUnderRoot covers root-level loose files', () => {
  it('includes files outside any folder', () => {
    const root = buildFileTree([mk(0, 'loose.mkv'), mk(1, 'A/x.mkv')])
    expect(allIndicesUnderRoot(root)).toEqual(new Set([0, 1]))
  })
})

describe('parentDir', () => {
  it('nested path → its folder', () => {
    expect(parentDir('Show/Season 1/E01.mkv')).toBe('Show/Season 1')
  })
  it('one folder deep', () => {
    expect(parentDir('A/x.mkv')).toBe('A')
  })
  it('root-level file → null (no folder to download)', () => {
    expect(parentDir('movie.mkv')).toBeNull()
  })
})

describe('filesUnderDir (recursive, prefix-safe)', () => {
  const files = [
    mk(0, 'Show/S1/E01.mkv'),
    mk(1, 'Show/S1/extras/blooper.mkv'),
    mk(2, 'Show/S10/E01.mkv'),
    mk(3, 'top.mkv'),
  ]
  it('includes subfolders recursively', () => {
    expect(filesUnderDir(files, 'Show/S1').map(f => f.index).sort((a, b) => a - b)).toEqual([0, 1])
  })
  it('does not match a sibling sharing a prefix (S1 vs S10)', () => {
    expect(filesUnderDir(files, 'Show/S1').some(f => f.index === 2)).toBe(false)
  })
  it('the whole Show folder grabs every descendant', () => {
    expect(filesUnderDir(files, 'Show').map(f => f.index).sort((a, b) => a - b)).toEqual([0, 1, 2])
  })
})
