import { describe, it, expect } from 'vitest'
import {
  buildFileTree, pathsToExpand, countFilesUnder, allDirPaths, hasSubdirs,
  flattenTree, parentRowIndex, keyNavAction,
  type TreeNode, type DirNode,
} from './fileTree'
import type { SortableFile } from '../components/player/playerFormat'

const mk = (index: number, path: string, size = 100, isVideo = true): SortableFile =>
  ({ index, path, size, isVideo })

// Helper: child names of a dir node (dirs come out before files).
const childNames = (n: DirNode) => n.children.map(c => c.name)
const dirChild = (n: DirNode, name: string): DirNode => {
  const c = n.children.find(x => x.type === 'dir' && x.name === name)
  if (!c || c.type !== 'dir') throw new Error(`no dir ${name}`)
  return c
}
const fileNames = (n: DirNode) => n.children.filter(c => c.type === 'file').map(c => c.name)

describe('buildFileTree — flat (no folders)', () => {
  it('files at the root become direct children, in display order', () => {
    const files = [
      mk(0, 'S01E03.mkv', 300),
      mk(1, 'S01E01.mkv', 100),
      mk(2, 'S01E02.mkv', 200),
    ]
    const root = buildFileTree(files)
    expect(root.children.every(c => c.type === 'file')).toBe(true)
    expect(fileNames(root)).toEqual(['S01E01.mkv', 'S01E02.mkv', 'S01E03.mkv'])
  })
})

describe('buildFileTree — nested', () => {
  const files = [
    mk(0, 'Show/Season 1/S01E03.mkv', 300),
    mk(1, 'Show/Season 1/S01E01.mkv', 100),
    mk(2, 'Show/Season 1/extras/Making of.mkv', 50),
    mk(3, 'Show/Season 1/S01E02.mkv', 200),
    mk(4, 'Show/poster.jpg', 1, false),
  ]

  it('collapses single-child pass-through (Show / Season 1 not folded because Show also has poster.jpg)', () => {
    // Show has TWO children (Season 1 dir + poster.jpg) → NOT collapsed.
    const root = buildFileTree(files)
    expect(childNames(root)).toEqual(['Show'])
    const show = dirChild(root, 'Show')
    // dir before file inside Show
    expect(childNames(show)).toEqual(['Season 1', 'poster.jpg'])
  })

  it('orders files within a folder by episode, extras last (mirrors filterAndSortFiles)', () => {
    const root = buildFileTree(files)
    const season = dirChild(dirChild(root, 'Show'), 'Season 1')
    // extras/ is a subdir (dir first), then the episodes E01 E02 E03
    expect(childNames(season)).toEqual(['extras', 'S01E01.mkv', 'S01E02.mkv', 'S01E03.mkv'])
  })

  it('dirs always come before files at every level', () => {
    const root = buildFileTree(files)
    const show = dirChild(root, 'Show')
    const idxFirstFile = show.children.findIndex(c => c.type === 'file')
    const idxLastDir = show.children.map(c => c.type).lastIndexOf('dir')
    expect(idxLastDir).toBeLessThan(idxFirstFile)
  })
})

describe('buildFileTree — single-child collapse', () => {
  it('folds a chain of single-child dirs into one label', () => {
    const files = [mk(0, 'a/b/c/movie.mkv', 100)]
    const root = buildFileTree(files)
    // a → b → c each have a single dir child → collapse to "a / b / c"
    expect(root.children).toHaveLength(1)
    const node = root.children[0] as DirNode
    expect(node.type).toBe('dir')
    expect(node.name).toBe('a / b / c')
    expect(node.path).toBe('a/b/c')
    expect(fileNames(node)).toEqual(['movie.mkv'])
  })

  it('does NOT collapse when collapseSingleChild is false', () => {
    const files = [mk(0, 'a/b/movie.mkv', 100)]
    const root = buildFileTree(files, { collapseSingleChild: false })
    const a = dirChild(root, 'a')
    expect(a.name).toBe('a')
    const b = dirChild(a, 'b')
    expect(fileNames(b)).toEqual(['movie.mkv'])
  })

  it('does NOT collapse a dir whose single child is a FILE', () => {
    const files = [mk(0, 'a/movie.mkv', 100)]
    const root = buildFileTree(files)
    const a = dirChild(root, 'a')
    expect(a.name).toBe('a')
    expect(fileNames(a)).toEqual(['movie.mkv'])
  })
})

describe('buildFileTree — type filter empties folders', () => {
  const files = [
    mk(0, 'Music/track1.flac', 100, false),
    mk(1, 'Music/track2.flac', 100, false),
    mk(2, 'Video/clip.mkv', 200, true),
  ]

  it('video filter drops the audio-only folder entirely', () => {
    const root = buildFileTree(files, { typeFilter: 'video' })
    expect(childNames(root)).toEqual(['Video'])
  })

  it('audio filter drops the video folder', () => {
    const root = buildFileTree(files, { typeFilter: 'audio' })
    expect(childNames(root)).toEqual(['Music'])
  })
})

describe('buildFileTree — text search keeps only matching files and their path', () => {
  const files = [
    mk(0, 'Show/Season 1/S01E01.mkv', 100),
    mk(1, 'Show/Season 1/S01E02.mkv', 200),
    mk(2, 'Show/Season 2/S02E01.mkv', 300),
  ]

  it('matching by SxxEyy tag keeps only that file and the folders on its path', () => {
    const root = buildFileTree(files, { filter: 's02e01' })
    // Show / Season 2 collapses (single child chain) → "Show / Season 2"
    const node = root.children[0] as DirNode
    expect(node.type).toBe('dir')
    expect(node.name).toBe('Show / Season 2')
    expect(fileNames(node)).toEqual(['S02E01.mkv'])
  })

  it('no match → empty tree', () => {
    const root = buildFileTree(files, { filter: 'nonexistent' })
    expect(root.children).toEqual([])
  })
})

describe('countFilesUnder', () => {
  const files = [
    mk(0, 'A/x.mkv'),
    mk(1, 'A/B/y.mkv'),
    mk(2, 'A/B/z.mkv'),
    mk(3, 'C/w.mkv'),
  ]
  it('counts leaves recursively per folder', () => {
    const root = buildFileTree(files)
    const a = dirChild(root, 'A')
    expect(countFilesUnder(a)).toBe(3)
    const b = dirChild(a, 'B')
    expect(countFilesUnder(b)).toBe(2)
    const c = dirChild(root, 'C')
    expect(countFilesUnder(c)).toBe(1)
  })
  it('a file node counts as 1', () => {
    const f: TreeNode = { type: 'file', name: 'x', path: 'x', file: mk(0, 'x') }
    expect(countFilesUnder(f)).toBe(1)
  })
})

describe('pathsToExpand', () => {
  const files = [
    mk(0, 'Show/Season 1/S01E01.mkv', 100),
    mk(1, 'Show/Season 1/S01E02.mkv', 200),
    mk(2, 'Show/Season 2/S02E05.mkv', 300),
    mk(3, 'Show/extras/blooper.mkv', 50),
  ]

  it('reveals the selected file: all folders on its path are expanded', () => {
    const root = buildFileTree(files)
    const want = pathsToExpand(root, 'Show/Season 2/S02E05.mkv')
    // "Show" is NOT collapsed (3 children); "Show/Season 2" exposes path Show/Season 2.
    expect(want.has('Show')).toBe(true)
    expect(want.has('Show/Season 2')).toBe(true)
    // sibling folders stay closed
    expect(want.has('Show/Season 1')).toBe(false)
    expect(want.has('Show/extras')).toBe(false)
  })

  it('falls back to the FIRST file (display order) when nothing is selected', () => {
    const root = buildFileTree(files)
    const want = pathsToExpand(root, null)
    // First in display order is Season 1 / E01 (episodes before extras).
    expect(want.has('Show')).toBe(true)
    expect(want.has('Show/Season 1')).toBe(true)
    expect(want.has('Show/Season 2')).toBe(false)
  })

  it('falls back to first file when the selected path is not in the tree', () => {
    const root = buildFileTree(files)
    const want = pathsToExpand(root, 'does/not/exist.mkv')
    expect(want.has('Show/Season 1')).toBe(true)
  })

  it('empty tree → empty set', () => {
    const root = buildFileTree([] as SortableFile[])
    expect(pathsToExpand(root, null).size).toBe(0)
  })

  it('expands the collapsed combined path for a single-child chain', () => {
    const root = buildFileTree([mk(0, 'a/b/c/movie.mkv')])
    const want = pathsToExpand(root, 'a/b/c/movie.mkv')
    // collapsed node has path "a/b/c" only
    expect([...want]).toEqual(['a/b/c'])
  })
})

describe('allDirPaths', () => {
  it('returns every dir path in the tree', () => {
    const root = buildFileTree([
      mk(0, 'A/x.mkv'),
      mk(1, 'A/B/y.mkv'),
      mk(2, 'C/z.mkv'),
    ])
    expect(allDirPaths(root)).toEqual(new Set(['A', 'A/B', 'C']))
  })
})

describe('hasSubdirs', () => {
  it('true when any file lives in a folder', () => {
    expect(hasSubdirs([{ path: 'a/b.mkv' }, { path: 'c.mkv' }])).toBe(true)
  })
  it('false for a flat torrent', () => {
    expect(hasSubdirs([{ path: 'a.mkv' }, { path: 'b.mkv' }])).toBe(false)
  })
})

describe('flattenTree', () => {
  const files = [
    mk(0, 'Show/Season 1/S01E01.mkv'),
    mk(1, 'Show/Season 2/S02E01.mkv'),
  ]
  const root = buildFileTree(files)

  it('hides children of collapsed folders', () => {
    // Nothing expanded → only top-level "Show" row is visible.
    const rows = flattenTree(root, new Set())
    expect(rows).toHaveLength(1)
    expect(rows[0].kind).toBe('dir')
    expect(rows[0].depth).toBe(0)
  })

  it('reveals children of expanded folders with growing depth', () => {
    const rows = flattenTree(root, new Set(['Show', 'Show/Season 1', 'Show/Season 2']))
    // Show(0) → Season 1(1) → E01(2) → Season 2(1) → E01(2)
    expect(rows.map(r => r.depth)).toEqual([0, 1, 2, 1, 2])
    expect(rows.filter(r => r.kind === 'file')).toHaveLength(2)
  })
})

describe('parentRowIndex', () => {
  const rows = [{ depth: 0 }, { depth: 1 }, { depth: 2 }, { depth: 1 }]
  it('finds the nearest shallower row above', () => {
    expect(parentRowIndex(rows, 2)).toBe(1)
    expect(parentRowIndex(rows, 3)).toBe(0)
    expect(parentRowIndex(rows, 1)).toBe(0)
  })
  it('-1 at the root', () => {
    expect(parentRowIndex(rows, 0)).toBe(-1)
  })
})

describe('keyNavAction', () => {
  // Show(0, open) → Season 1(1, closed) → Season 2(1, closed)
  const files = [
    mk(0, 'Show/Season 1/S01E01.mkv'),
    mk(1, 'Show/Season 2/S02E01.mkv'),
  ]
  const root = buildFileTree(files)
  const rows = flattenTree(root, new Set(['Show']))
  // rows: [0]=Show(open), [1]=Season 1(closed), [2]=Season 2(closed)

  it('ArrowDown/ArrowUp move roving focus, clamped at the ends', () => {
    expect(keyNavAction(rows, 0, 'ArrowDown')).toEqual({ type: 'focus', index: 1 })
    expect(keyNavAction(rows, 2, 'ArrowDown')).toEqual({ type: 'none' })
    expect(keyNavAction(rows, 1, 'ArrowUp')).toEqual({ type: 'focus', index: 0 })
    expect(keyNavAction(rows, 0, 'ArrowUp')).toEqual({ type: 'none' })
  })

  it('ArrowRight on a closed dir expands it; on an open dir descends', () => {
    expect(keyNavAction(rows, 1, 'ArrowRight')).toEqual({ type: 'toggle', path: 'Show/Season 1' })
    expect(keyNavAction(rows, 0, 'ArrowRight')).toEqual({ type: 'focus', index: 1 })
  })

  it('ArrowLeft on an open dir collapses it; on a child ascends to parent', () => {
    expect(keyNavAction(rows, 0, 'ArrowLeft')).toEqual({ type: 'toggle', path: 'Show' })
    expect(keyNavAction(rows, 1, 'ArrowLeft')).toEqual({ type: 'focus', index: 0 })
  })

  it('Enter/Space toggle a folder, no-op on a file (falls through to its button)', () => {
    expect(keyNavAction(rows, 1, 'Enter')).toEqual({ type: 'toggle', path: 'Show/Season 1' })
    expect(keyNavAction(rows, 1, ' ')).toEqual({ type: 'toggle', path: 'Show/Season 1' })
    const withFiles = flattenTree(root, new Set(['Show', 'Show/Season 1']))
    const fileRow = withFiles.findIndex(r => r.kind === 'file')
    expect(keyNavAction(withFiles, fileRow, 'Enter')).toEqual({ type: 'none' })
  })

  it('unknown key and out-of-range index → none', () => {
    expect(keyNavAction(rows, 0, 'a')).toEqual({ type: 'none' })
    expect(keyNavAction(rows, 99, 'ArrowDown')).toEqual({ type: 'none' })
  })
})
