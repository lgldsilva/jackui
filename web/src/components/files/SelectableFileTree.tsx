import { useMemo, useRef, useState, useCallback, useEffect } from 'react'
import { ChevronRight, ChevronDown, Folder, FolderOpen } from 'lucide-react'
import { StreamFile } from '../../api/client'
import {
  buildFileTree, countFilesUnder, flattenTreeCapped, keyNavAction, allDirPaths,
  type DirNode, type FileNode,
} from '../../lib/fileTree'
import { dirTriState, toggleDirSelection, toggleFileSelection, type TriState } from '../../lib/treeSelect'
import { useIncrementalReveal } from '../player/useIncrementalReveal'
import { formatBytes } from '../../lib/format'
import { fileIcon } from './fileIcon'

type SelectableFileTreeProps = {
  readonly files: readonly StreamFile[]
  readonly selected: ReadonlySet<number>
  readonly onChange: (next: Set<number>) => void
  readonly filter?: string
}

const INDENT_PX = 14
const NAV_KEYS = new Set(['ArrowDown', 'ArrowUp', 'ArrowRight', 'ArrowLeft', 'Enter', ' '])

// Checkbox with the third "indeterminate" state — not a JSX attribute, so it's
// set imperatively on the DOM node via a ref/effect.
function TriCheckbox({ state, onToggle }: { readonly state: TriState; readonly onToggle: () => void }) {
  const ref = useRef<HTMLInputElement>(null)
  useEffect(() => {
    if (ref.current) ref.current.indeterminate = state === 'some'
  }, [state])
  return (
    <input
      ref={ref}
      type="checkbox"
      checked={state === 'all'}
      onChange={onToggle}
      onClick={e => e.stopPropagation()}
      className="accent-cyan-500 flex-shrink-0"
    />
  )
}

// SelectableFileTree — the folder TREE used in the download modals' file picker.
// Marking a folder selects/deselects every descendant recursively (tri-state).
// Pure tree math lives in lib/fileTree + lib/treeSelect; this component renders
// + handles expand/collapse + keyboard nav (WAI-ARIA tree, roving tabindex).
//
// Selection is keyed on StreamFile.index (a Set<number>) — the same contract
// the flat list and the submit path (isWholeTorrentSelection/buildBatchFiles)
// use. The render is capped (flattenTreeCapped) so a pack with thousands of
// files can't mount thousands of rows, but a folder TOGGLE walks the full model
// so it still flips every (even unrendered) descendant.
export function SelectableFileTree({ files, selected, onChange, filter = '' }: SelectableFileTreeProps) {
  const root = useMemo(() => buildFileTree(files, { filter }), [files, filter])

  // Selection wants everything visible: start fully expanded, re-init when the
  // tree identity changes (new file set / filter). User collapses persist until
  // then.
  const [expanded, setExpanded] = useState<Set<string>>(() => allDirPaths(root))
  useEffect(() => { setExpanded(allDirPaths(root)) }, [root])

  const totalFiles = useMemo(
    () => flattenTreeCapped(root, expanded, Number.MAX_SAFE_INTEGER).rows.filter(r => r.kind === 'file').length,
    [root, expanded],
  )
  const reveal = useIncrementalReveal(totalFiles, filter)
  const { rows } = useMemo(() => flattenTreeCapped(root, expanded, reveal.visible), [root, expanded, reveal.visible])

  const [focusIdx, setFocusIdx] = useState(0)
  const containerRef = useRef<HTMLDivElement>(null)

  useEffect(() => {
    if (focusIdx >= rows.length) setFocusIdx(Math.max(0, rows.length - 1))
  }, [rows.length, focusIdx])

  const toggle = useCallback((path: string) => {
    setExpanded(prev => {
      const next = new Set(prev)
      if (next.has(path)) next.delete(path); else next.add(path)
      return next
    })
  }, [])

  const focusRowAt = useCallback((i: number) => {
    setFocusIdx(i)
    containerRef.current?.querySelector<HTMLElement>(`[data-row-idx="${i}"]`)?.focus()
  }, [])

  const onKeyDown = useCallback((e: React.KeyboardEvent, i: number) => {
    const action = keyNavAction(rows, i, e.key)
    if (action.type === 'none') return
    if (NAV_KEYS.has(e.key)) e.preventDefault()
    if (action.type === 'focus') focusRowAt(action.index)
    else toggle(action.path)
  }, [rows, focusRowAt, toggle])

  const onToggleDir = useCallback((node: DirNode<StreamFile>) => {
    onChange(toggleDirSelection(node, selected))
  }, [onChange, selected])

  const onToggleFile = useCallback((index: number) => {
    onChange(toggleFileSelection(index, selected))
  }, [onChange, selected])

  if (rows.length === 0) {
    return <p className="text-xs text-text-muted text-center py-3">Nenhum arquivo com esse filtro</p>
  }

  return (
    <div
      ref={containerRef}
      role="tree"
      aria-label="Seleção de arquivos"
      className="bg-surface border border-default rounded-lg max-h-72 overflow-y-auto flex flex-col gap-0.5 p-1"
    >
      {rows.map((row, i) => {
        const indent = { paddingLeft: `${row.depth * INDENT_PX + 4}px` }
        if (row.kind === 'dir') {
          return (
            <DirSelectRow
              key={`d:${row.node.path}`}
              node={row.node}
              expanded={row.expanded}
              indent={indent}
              tri={dirTriState(row.node, selected)}
              rowIdx={i}
              tabbable={i === focusIdx}
              onExpand={() => toggle(row.node.path)}
              onToggleSel={() => onToggleDir(row.node)}
              onKeyDown={e => onKeyDown(e, i)}
              onFocus={() => setFocusIdx(i)}
            />
          )
        }
        return (
          <FileSelectRow
            key={`f:${row.node.file.index}`}
            node={row.node}
            indent={indent}
            checked={selected.has(row.node.file.index)}
            rowIdx={i}
            tabbable={i === focusIdx}
            onToggleSel={() => onToggleFile(row.node.file.index)}
            onKeyDown={e => onKeyDown(e, i)}
            onFocus={() => setFocusIdx(i)}
          />
        )
      })}
      {reveal.hasMore && (
        <div ref={reveal.sentinelRef} className="px-2 pt-1 pb-2">
          <button
            type="button"
            onClick={reveal.showMore}
            className="w-full flex items-center justify-center gap-1.5 rounded-lg bg-surface-2 py-2 text-xs text-text-secondary hover:text-text-primary"
          >
            <ChevronDown className="w-3.5 h-3.5" />
            Mostrar mais ({reveal.remaining} de {totalFiles})
          </button>
        </div>
      )}
    </div>
  )
}

type DirRowProps = {
  readonly node: DirNode<StreamFile>
  readonly expanded: boolean
  readonly indent: React.CSSProperties
  readonly tri: TriState
  readonly rowIdx: number
  readonly tabbable: boolean
  readonly onExpand: () => void
  readonly onToggleSel: () => void
  readonly onKeyDown: (e: React.KeyboardEvent) => void
  readonly onFocus: () => void
}

function DirSelectRow({ node, expanded, indent, tri, rowIdx, tabbable, onExpand, onToggleSel, onKeyDown, onFocus }: DirRowProps) {
  const ariaChecked = tri === 'some' ? 'mixed' : tri === 'all'
  return (
    <div
      role="treeitem"
      aria-expanded={expanded}
      aria-checked={ariaChecked}
      aria-selected={tri === 'all'}
      data-row-idx={rowIdx}
      tabIndex={tabbable ? 0 : -1}
      style={indent}
      onKeyDown={onKeyDown}
      onFocus={onFocus}
      className="flex items-center gap-2 px-2 py-1.5 rounded-lg text-sm text-text-secondary hover:bg-surface-secondary/40 cursor-pointer outline-none focus:ring-1 focus:ring-cyan-500/50"
    >
      <TriCheckbox state={tri} onToggle={onToggleSel} />
      <button type="button" onClick={onExpand} title={node.path} className="flex items-center gap-1.5 flex-1 min-w-0 text-left">
        <ChevronRight className={`w-3.5 h-3.5 flex-shrink-0 transition-transform ${expanded ? 'rotate-90' : ''}`} />
        {expanded
          ? <FolderOpen className="w-3.5 h-3.5 flex-shrink-0 text-amber-500/80" />
          : <Folder className="w-3.5 h-3.5 flex-shrink-0 text-amber-500/80" />}
        <span className="truncate flex-1 font-medium">{node.name}</span>
        <span className="text-text-muted flex-shrink-0 text-[10px] tabular-nums">{countFilesUnder(node)}</span>
      </button>
    </div>
  )
}

type FileRowProps = {
  readonly node: FileNode<StreamFile>
  readonly indent: React.CSSProperties
  readonly checked: boolean
  readonly rowIdx: number
  readonly tabbable: boolean
  readonly onToggleSel: () => void
  readonly onKeyDown: (e: React.KeyboardEvent) => void
  readonly onFocus: () => void
}

function FileSelectRow({ node, indent, checked, rowIdx, tabbable, onToggleSel, onKeyDown, onFocus }: FileRowProps) {
  const f = node.file
  return (
    <div
      role="treeitem"
      aria-selected={checked}
      data-row-idx={rowIdx}
      tabIndex={tabbable ? 0 : -1}
      style={indent}
      onClick={onToggleSel}
      onKeyDown={onKeyDown}
      onFocus={onFocus}
      className="flex items-center gap-2.5 px-2 py-1.5 rounded-lg hover:bg-surface-secondary/40 cursor-pointer outline-none focus:ring-1 focus:ring-cyan-500/50"
    >
      <input type="checkbox" checked={checked} readOnly tabIndex={-1} className="accent-cyan-500 flex-shrink-0" />
      {fileIcon(f)}
      <span className="flex-1 min-w-0 text-sm text-text-primary truncate" title={f.path}>{node.name}</span>
      <span className="text-xs text-text-muted flex-shrink-0">{formatBytes(f.size)}</span>
    </div>
  )
}
