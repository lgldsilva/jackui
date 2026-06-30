// Pure selection helpers over the folder tree (lib/fileTree).
//
// Why this exists: the download modals select files by `file.index` (a
// Set<number>, consumed by isWholeTorrentSelection/buildBatchFiles). The folder
// TREE view needs to flip a whole folder — and all its nested subfolders — in
// one click, and render a tri-state checkbox (all / none / some) per folder.
// Keeping the math here (no React, no DOM) makes it unit-testable and lets the
// SelectableFileTree component stay thin. Every mutating helper returns a NEW
// Set so it slots into `setSelected(prev => ...)` without aliasing surprises.

import type { TreeNode, DirNode, FileTreeFile } from './fileTree'

// Folder selection relative to the current set: every descendant selected,
// none, or a mix.
export type TriState = 'all' | 'none' | 'some'

// getFileIndicesUnder collects the file.index of every file leaf under a node
// (a single index for a file node). This walks the FULL model — not the
// render-capped flatten — so toggling a folder with thousands of unrendered
// files still flips all of them.
export function getFileIndicesUnder<T extends FileTreeFile>(node: TreeNode<T>): number[] {
  if (node.type === 'file') return [node.file.index]
  const out: number[] = []
  for (const c of node.children) {
    if (c.type === 'file') out.push(c.file.index)
    else out.push(...getFileIndicesUnder(c))
  }
  return out
}

// dirTriState reports whether all / none / some of a folder's descendants are
// selected. An empty folder (e.g. filtered to nothing) is 'none', never 'all',
// so it doesn't render as a checked box with nothing behind it.
export function dirTriState<T extends FileTreeFile>(
  node: DirNode<T>,
  selected: ReadonlySet<number>,
): TriState {
  const indices = getFileIndicesUnder(node)
  if (indices.length === 0) return 'none'
  let hit = 0
  for (const i of indices) if (selected.has(i)) hit++
  if (hit === 0) return 'none'
  if (hit === indices.length) return 'all'
  return 'some'
}

// toggleDirSelection flips a whole folder: if it's fully selected ('all') it
// removes every descendant; otherwise ('none'/'some') it adds every descendant.
// Returns a new Set; `selected` is not mutated.
export function toggleDirSelection<T extends FileTreeFile>(
  node: DirNode<T>,
  selected: ReadonlySet<number>,
): Set<number> {
  const indices = getFileIndicesUnder(node)
  const next = new Set(selected)
  const fullySelected = indices.length > 0 && indices.every(i => next.has(i))
  if (fullySelected) for (const i of indices) next.delete(i)
  else for (const i of indices) next.add(i)
  return next
}

// toggleFileSelection adds or removes a single file index. Returns a new Set.
export function toggleFileSelection(index: number, selected: ReadonlySet<number>): Set<number> {
  const next = new Set(selected)
  if (next.has(index)) next.delete(index)
  else next.add(index)
  return next
}

// allIndicesUnderRoot is every file index in the (possibly filtered) tree — the
// "Todos" button. It mirrors what the tree actually shows, so selecting all
// then submitting still trips isWholeTorrentSelection when nothing is filtered.
export function allIndicesUnderRoot<T extends FileTreeFile>(root: DirNode<T>): Set<number> {
  return new Set(getFileIndicesUnder(root))
}

// --- Folder helpers for "download this file's folder" (player 📁↓) ---

// parentDir returns the directory a path lives in, or null for a root-level
// file (no folder to download). "Show/S1/E01.mkv" → "Show/S1"; "movie.mkv" → null.
export function parentDir(path: string): string | null {
  const parts = path.split('/').filter(Boolean)
  if (parts.length <= 1) return null
  return parts.slice(0, -1).join('/')
}

// filesUnderDir returns every file whose path is inside `dir`, RECURSIVELY
// (subfolders included) — the "download the whole folder" set. Matching is by
// the `dir + '/'` prefix so "Show/S1" doesn't also catch "Show/S10".
export function filesUnderDir<T extends FileTreeFile>(files: readonly T[], dir: string): T[] {
  const prefix = dir.endsWith('/') ? dir : `${dir}/`
  return files.filter(f => f.path.startsWith(prefix))
}
