// Pure folder-tree model for the player's file picker.
//
// Why this exists: torrents routinely ship season packs, discographies and
// "complete collection" releases with thousands of files nested several
// folders deep. The player's sidebar used to render a FLAT list (last two path
// segments per file), which is unusable once a release has real structure.
// This module turns the flat `path` list into a collapsible tree WITHOUT
// touching React — so it stays unit-testable and the sidebar component doesn't
// grow a parser.
//
// Ordering inside every level mirrors filterAndSortFiles (the player's single
// source of display order): dirs first, then files in episode/extra order.

import { filterAndSortFiles, type FileType, type SortableFile } from '../components/player/playerFormat'

export type FileTreeFile = SortableFile

// Nodes are generic over the file type so the original StreamFile (with its
// downloaded/progress/priority fields) survives into the rendered leaf —
// buildFileTree<T> threads T through. Defaults to SortableFile for callers
// (and tests) that only care about the path/size/index.
export type FileNode<T extends FileTreeFile = FileTreeFile> = {
  readonly type: 'file'
  // Display label (the file's basename).
  readonly name: string
  // Full original path, unique per file (used as React key + selection match).
  readonly path: string
  readonly file: T
}

export type DirNode<T extends FileTreeFile = FileTreeFile> = {
  readonly type: 'dir'
  // Display label. May contain " / " when single-child pass-through folders are
  // collapsed (e.g. "Season 1 / Disc 1").
  readonly name: string
  // Slash-joined directory path (no trailing slash). Stable id for expand state.
  readonly path: string
  readonly children: TreeNode<T>[]
}

export type TreeNode<T extends FileTreeFile = FileTreeFile> = DirNode<T> | FileNode<T>

export type BuildTreeOpts = {
  // Type filter applied BEFORE building, so folders left empty disappear.
  readonly typeFilter?: FileType
  // Free-text filter (path or SxxEyy tag). Folders with no surviving file are
  // pruned; the folders on the path to a match stay.
  readonly filter?: string
  // Collapse single-child pass-through folders ("a/b" with one dir child →
  // "a / b") to reduce depth. On by default.
  readonly collapseSingleChild?: boolean
}

// countFilesUnder returns the number of file leaves under a node (1 for a file).
export function countFilesUnder(node: TreeNode): number {
  if (node.type === 'file') return 1
  let n = 0
  for (const c of node.children) n += countFilesUnder(c)
  return n
}

type MutableDir<T extends FileTreeFile> = {
  type: 'dir'
  name: string
  path: string
  // Insertion-order child-dir lookup while building.
  dirs: Map<string, MutableDir<T>>
  files: T[]
}

function newDir<T extends FileTreeFile>(name: string, path: string): MutableDir<T> {
  return { type: 'dir', name, path, dirs: new Map(), files: [] }
}

// Splits "a/b/c.mkv" → ["a","b"] (dirs) + "c.mkv" (basename). Empty segments
// (leading slash, double slash) are dropped so paths normalise cleanly.
function splitPath(path: string): { dirs: string[]; base: string } {
  const parts = path.split('/').filter(Boolean)
  if (parts.length === 0) return { dirs: [], base: path }
  const base = parts[parts.length - 1]
  return { dirs: parts.slice(0, -1), base }
}

// Builds the immutable children of a mutable dir: subdirs first (recursively,
// in insertion order = the order their first file appeared after the sort),
// then files in display order.
function finalizeChildren<T extends FileTreeFile>(dir: MutableDir<T>, collapse: boolean): TreeNode<T>[] {
  const childDirs: TreeNode<T>[] = []
  for (const sub of dir.dirs.values()) {
    childDirs.push(finalize(sub, collapse))
  }
  const childFiles: FileNode<T>[] = dir.files.map(f => ({
    type: 'file' as const,
    name: splitPath(f.path).base,
    path: f.path,
    file: f,
  }))
  return [...childDirs, ...childFiles]
}

// Finalises a (non-root) mutable dir. Single-child pass-through dirs are folded
// into their label when requested ("a/b" with one dir child → "a / b") to cut
// depth. The synthetic root is NOT passed here — it never collapses into its
// only child (see buildFileTree).
function finalize<T extends FileTreeFile>(dir: MutableDir<T>, collapse: boolean): DirNode<T> {
  const children = finalizeChildren(dir, collapse)
  if (collapse && children.length === 1 && children[0].type === 'dir') {
    const only = children[0]
    return { type: 'dir', name: `${dir.name} / ${only.name}`, path: only.path, children: only.children }
  }
  return { type: 'dir', name: dir.name, path: dir.path, children }
}

// buildFileTree turns the flat file list into a folder tree. The type/text
// filter is applied first (via filterAndSortFiles, which also gives us the
// canonical episode/extra ordering), then files are bucketed by their dir
// path. Files at the torrent root land directly under the synthetic root.
//
// The returned root is a DirNode with path "" — callers render `root.children`.
export function buildFileTree<T extends FileTreeFile>(files: readonly T[], opts: BuildTreeOpts = {}): DirNode<T> {
  const collapse = opts.collapseSingleChild !== false
  // filterAndSortFiles fixes the ORDER once: files are slotted into the tree in
  // that order, so the first appearance of a dir fixes its position among
  // siblings (episodes/extras keep their sort within each folder too).
  const ordered = filterAndSortFiles(files, {
    filter: opts.filter ?? '',
    typeFilter: opts.typeFilter ?? 'all',
    sortBySize: false,
    sizeDesc: false,
  })
  const root = newDir<T>('', '')
  for (const f of ordered) {
    const { dirs } = splitPath(f.path)
    let cur = root
    let acc = ''
    for (const seg of dirs) {
      acc = acc ? `${acc}/${seg}` : seg
      let next = cur.dirs.get(seg)
      if (!next) {
        next = newDir<T>(seg, acc)
        cur.dirs.set(seg, next)
      }
      cur = next
    }
    cur.files.push(f)
  }
  // Root itself never collapses into its sole child — return it with finalised
  // children directly.
  return { type: 'dir', name: '', path: '', children: finalizeChildren(root, collapse) }
}

// Walks the tree collecting every dir path, so callers can expand-all.
export function allDirPaths(root: DirNode): Set<string> {
  const out = new Set<string>()
  const walk = (n: TreeNode) => {
    if (n.type !== 'dir') return
    if (n.path) out.add(n.path)
    for (const c of n.children) walk(c)
  }
  for (const c of root.children) walk(c)
  return out
}

// pathsToExpand returns the set of dir paths to open so that the selected file
// is revealed (auto-expand to current). When selectedPath is null/absent, it
// reveals the FIRST file in display order instead, so opening the tree always
// shows something useful rather than a wall of collapsed folders.
//
// Note: dir paths here are the COLLAPSED paths actually present in the tree
// (e.g. a folded "a/b" exposes path "a/b", not "a"), so we walk the tree and
// match by whether the target file lives under each dir.
export function pathsToExpand(root: DirNode, selectedPath: string | null): Set<string> {
  const target = resolveTargetPath(root, selectedPath)
  const out = new Set<string>()
  if (!target) return out
  const walk = (n: TreeNode): boolean => {
    if (n.type === 'file') return n.path === target
    let hit = false
    for (const c of n.children) {
      if (walk(c)) hit = true
    }
    if (hit && n.path) out.add(n.path)
    return hit
  }
  for (const c of root.children) walk(c)
  return out
}

// resolveTargetPath: the explicit selection if it exists in the tree, else the
// first file leaf in display order.
function resolveTargetPath(root: DirNode, selectedPath: string | null): string | null {
  if (selectedPath && fileExists(root, selectedPath)) return selectedPath
  return firstFilePath(root)
}

function fileExists(root: DirNode, path: string): boolean {
  let found = false
  const walk = (n: TreeNode) => {
    if (found) return
    if (n.type === 'file') {
      if (n.path === path) found = true
      return
    }
    for (const c of n.children) walk(c)
  }
  walk(root)
  return found
}

// First file leaf in display order (depth-first, dirs already sorted first).
function firstFilePath(dir: DirNode): string | null {
  for (const c of dir.children) {
    if (c.type === 'file') return c.path
    const sub = firstFilePath(c)
    if (sub) return sub
  }
  return null
}

// hasSubdirs reports whether any file sits inside a folder — used to decide
// whether the tree view is worth offering / defaulting to.
export function hasSubdirs(files: readonly { path: string }[]): boolean {
  for (const f of files) {
    if (f.path.includes('/')) return true
  }
  return false
}

// --- Flattening + keyboard navigation (pure, so FileTree.tsx stays thin) ---

// A visible row in keyboard-navigation order: the tree flattened to only the
// rows on screen (collapsed folders hide their children).
export type FlatRow<T extends FileTreeFile = FileTreeFile> =
  | { kind: 'dir'; node: DirNode<T>; depth: number; expanded: boolean }
  | { kind: 'file'; node: FileNode<T>; depth: number }

// flattenTree turns the tree into the visible rows given the expanded set. Used
// both to render and to drive arrow-key nav, so the two never disagree.
export function flattenTree<T extends FileTreeFile>(root: DirNode<T>, expanded: Set<string>): FlatRow<T>[] {
  const out: FlatRow<T>[] = []
  const walk = (nodes: TreeNode<T>[], depth: number) => {
    for (const n of nodes) {
      if (n.type === 'dir') {
        const isOpen = expanded.has(n.path)
        out.push({ kind: 'dir', node: n, depth, expanded: isOpen })
        if (isOpen) walk(n.children, depth + 1)
      } else {
        out.push({ kind: 'file', node: n, depth })
      }
    }
  }
  walk(root.children, 0)
  return out
}

// flattenTreeCapped is flattenTree with a ceiling on rendered FILE rows, so a
// single auto-expanded folder holding thousands of files can't mount thousands
// of live <button> nodes and freeze the player modal (the flat list caps the
// same way — see FilePickerSidebar's .slice(0, 100)).
//
// Dir rows are never dropped (there are far fewer of them and they're the
// navigation skeleton); only FILE rows past the cap are omitted. `hiddenFiles`
// reports how many files were left out so the UI can show a "mostrando N de M"
// hint that nudges the user to the filter box.
export type CappedRows<T extends FileTreeFile = FileTreeFile> = {
  readonly rows: FlatRow<T>[]
  readonly hiddenFiles: number
}

export function flattenTreeCapped<T extends FileTreeFile>(
  root: DirNode<T>,
  expanded: Set<string>,
  fileCap: number,
): CappedRows<T> {
  const rows: FlatRow<T>[] = []
  let shownFiles = 0
  let hiddenFiles = 0
  const walk = (nodes: TreeNode<T>[], depth: number) => {
    for (const n of nodes) {
      if (n.type === 'dir') {
        const isOpen = expanded.has(n.path)
        rows.push({ kind: 'dir', node: n, depth, expanded: isOpen })
        if (isOpen) walk(n.children, depth + 1)
      } else if (shownFiles < fileCap) {
        rows.push({ kind: 'file', node: n, depth })
        shownFiles++
      } else {
        hiddenFiles++
      }
    }
  }
  walk(root.children, 0)
  return { rows, hiddenFiles }
}

// Index of the parent row (nearest shallower row above), or -1 at the root.
export function parentRowIndex(rows: readonly { depth: number }[], i: number): number {
  const depth = rows[i].depth
  for (let j = i - 1; j >= 0; j--) {
    if (rows[j].depth < depth) return j
  }
  return -1
}

// An intent resolved from a key press — kept data-only so it's trivially
// testable and the React handler that applies it stays flat.
//  - focus N : move roving focus to row N
//  - toggle P: expand/collapse dir at path P
//  - none    : no-op (e.g. Enter on a file → falls through to its button)
export type NavAction =
  | { type: 'focus'; index: number }
  | { type: 'toggle'; path: string }
  | { type: 'none' }

const focusOr = (cond: boolean, index: number): NavAction => (cond ? { type: 'focus', index } : { type: 'none' })

function arrowRightAction<T extends FileTreeFile>(rows: FlatRow<T>[], i: number, row: FlatRow<T>): NavAction {
  if (row.kind !== 'dir') return { type: 'none' }
  if (!row.expanded) return { type: 'toggle', path: row.node.path }
  return focusOr(i < rows.length - 1, i + 1)
}

function arrowLeftAction<T extends FileTreeFile>(rows: FlatRow<T>[], i: number, row: FlatRow<T>): NavAction {
  if (row.kind === 'dir' && row.expanded) return { type: 'toggle', path: row.node.path }
  return focusOr(parentRowIndex(rows, i) >= 0, parentRowIndex(rows, i))
}

// keyNavAction maps an arrow/Enter/Space key to a NavAction over the visible
// rows. WAI-ARIA tree pattern: Up/Down move focus, Right expands-or-descends,
// Left collapses-or-ascends, Enter/Space toggle a folder (files fall through).
export function keyNavAction<T extends FileTreeFile>(rows: FlatRow<T>[], i: number, key: string): NavAction {
  const row = rows[i]
  if (!row) return { type: 'none' }
  switch (key) {
    case 'ArrowDown':
      return focusOr(i < rows.length - 1, i + 1)
    case 'ArrowUp':
      return focusOr(i > 0, i - 1)
    case 'ArrowRight':
      return arrowRightAction(rows, i, row)
    case 'ArrowLeft':
      return arrowLeftAction(rows, i, row)
    case 'Enter':
    case ' ':
      return row.kind === 'dir' ? { type: 'toggle', path: row.node.path } : { type: 'none' }
    default:
      return { type: 'none' }
  }
}
