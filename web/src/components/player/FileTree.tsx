import { useMemo, useRef, useState, useCallback, useEffect } from 'react'
import { ChevronRight, Folder, FolderOpen } from 'lucide-react'
import { TorrentInfo } from '../../api/client'
import { useHoverThumb } from '../FileThumbHover'
import { FileRow } from './FileRow'
import { buildFileTree, countFilesUnder, flattenTree, keyNavAction } from '../../lib/fileTree'
import type { FileType } from './playerFormat'

type FileTreeProps = {
  readonly info: TorrentInfo
  readonly selectedFile: number
  readonly selectedFileRef: React.RefObject<HTMLButtonElement>
  readonly fileFilter: string
  readonly fileTypeFilter: FileType
  readonly expanded: Set<string>
  readonly setExpanded: (next: Set<string>) => void
  readonly hoverThumb: ReturnType<typeof useHoverThumb>
  readonly parseEpisode: (path: string) => string | null
  readonly playFile: (idx: number) => void
  readonly setPreviewFileIdx: (v: number | null) => void
}

const INDENT_PX = 14

// Keys we intercept (preventDefault) so the page doesn't scroll under us.
const NAV_KEYS = new Set(['ArrowDown', 'ArrowUp', 'ArrowRight', 'ArrowLeft', 'Enter', ' '])

// FileTree — collapsible folder view of the torrent's files for the player
// sidebar. Pure layout lives in lib/fileTree (buildFileTree/countFilesUnder);
// this component only renders + handles expand/collapse + keyboard nav.
//
// Accessibility: role="tree" with roving tabindex over the visible rows.
// ArrowUp/Down move focus; ArrowRight expands a closed folder (or descends);
// ArrowLeft collapses an open folder (or ascends to the parent); Enter/Space
// activate (toggle folder / play|preview file).
export function FileTree({
  info,
  selectedFile,
  selectedFileRef,
  fileFilter,
  fileTypeFilter,
  expanded,
  setExpanded,
  hoverThumb,
  parseEpisode,
  playFile,
  setPreviewFileIdx,
}: FileTreeProps) {
  const root = useMemo(
    () => buildFileTree(info.files, { filter: fileFilter, typeFilter: fileTypeFilter }),
    [info.files, fileFilter, fileTypeFilter],
  )
  const rows = useMemo(() => flattenTree(root, expanded), [root, expanded])
  const [focusIdx, setFocusIdx] = useState(0)
  const containerRef = useRef<HTMLDivElement>(null)

  // Keep the roving focus index in range as rows expand/collapse/filter.
  useEffect(() => {
    if (focusIdx >= rows.length) setFocusIdx(Math.max(0, rows.length - 1))
  }, [rows.length, focusIdx])

  const toggle = useCallback((path: string) => {
    const next = new Set(expanded)
    if (next.has(path)) next.delete(path)
    else next.add(path)
    setExpanded(next)
  }, [expanded, setExpanded])

  const focusRowAt = useCallback((i: number) => {
    setFocusIdx(i)
    const el = containerRef.current?.querySelector<HTMLElement>(`[data-row-idx="${i}"]`)
    el?.focus()
  }, [])

  const onKeyDown = useCallback((e: React.KeyboardEvent, i: number) => {
    const action = keyNavAction(rows, i, e.key)
    if (action.type === 'none') return
    // Enter/Space on a FILE row resolves to 'none' above, so it falls through to
    // the native <button> click. Everything else we own → stop page scroll.
    if (NAV_KEYS.has(e.key)) e.preventDefault()
    if (action.type === 'focus') focusRowAt(action.index)
    else toggle(action.path)
  }, [rows, focusRowAt, toggle])

  if (rows.length === 0) {
    return (
      <p className="text-xs text-text-muted text-center py-3">
        {fileFilter ? `Nenhum arquivo bate com "${fileFilter}"` : 'Nenhum arquivo com esse filtro'}
      </p>
    )
  }

  return (
    <div
      ref={containerRef}
      role="tree"
      aria-label="Árvore de arquivos"
      className="flex flex-col gap-1 px-1 py-2 overflow-y-auto min-h-0 flex-1 lg:flex-none lg:max-h-[60vh]"
    >
      {rows.map((row, i) => {
        const indent = { paddingLeft: `${row.depth * INDENT_PX + 4}px` }
        if (row.kind === 'dir') {
          const count = countFilesUnder(row.node)
          return (
            <button
              key={`d:${row.node.path}`}
              type="button"
              role="treeitem"
              aria-expanded={row.expanded}
              data-row-idx={i}
              tabIndex={i === focusIdx ? 0 : -1}
              style={indent}
              onClick={() => toggle(row.node.path)}
              onKeyDown={e => onKeyDown(e, i)}
              onFocus={() => setFocusIdx(i)}
              title={row.node.path}
              className="flex items-center gap-1.5 px-2 py-2 sm:py-1.5 min-h-[40px] sm:min-h-0 rounded-lg text-sm sm:text-xs text-text-secondary hover:bg-surface-tertiary/50 transition-colors text-left w-full flex-shrink-0"
            >
              <ChevronRight className={`w-3.5 h-3.5 flex-shrink-0 transition-transform ${row.expanded ? 'rotate-90' : ''}`} />
              {row.expanded
                ? <FolderOpen className="w-3.5 h-3.5 flex-shrink-0 text-amber-500/80" />
                : <Folder className="w-3.5 h-3.5 flex-shrink-0 text-amber-500/80" />}
              <span className="truncate flex-1 font-medium">{row.node.name}</span>
              <span className="text-text-muted flex-shrink-0 text-[10px] tabular-nums">{count}</span>
            </button>
          )
        }
        const f = row.node.file
        const selected = selectedFile === f.index
        return (
          <FileRow
            key={`f:${f.index}`}
            ref={selected ? selectedFileRef : undefined}
            file={f}
            infoHash={info.infoHash}
            selected={selected}
            displayName={row.node.name}
            indentStyle={indent}
            hoverThumb={hoverThumb}
            parseEpisode={parseEpisode}
            playFile={playFile}
            setPreviewFileIdx={setPreviewFileIdx}
            treeItemProps={{
              role: 'treeitem',
              'data-row-idx': i,
              tabIndex: i === focusIdx ? 0 : -1,
              onKeyDown: e => onKeyDown(e, i),
              onFocus: () => setFocusIdx(i),
            } as React.HTMLAttributes<HTMLButtonElement>}
          />
        )
      })}
    </div>
  )
}
