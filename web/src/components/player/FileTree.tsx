import { useMemo, useRef, useState, useCallback, useEffect } from 'react'
import { ChevronRight, ChevronDown, Folder, FolderOpen, FolderDown } from 'lucide-react'
import { useTranslation } from 'react-i18next'
import { TorrentInfo } from '../../api/client'
import { useHoverThumb } from '../FileThumbHover'
import { FileRow } from './FileRow'
import { buildFileTree, countFilesUnder, flattenTreeCapped, keyNavAction } from '../../lib/fileTree'
import { useIncrementalReveal } from './useIncrementalReveal'
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
  readonly onDownloadFolder?: (file: TorrentInfo['files'][number]) => void
  readonly onDownloadDir?: (dirPath: string) => void
}

const INDENT_PX = 14

// Renderizar todos os FILE rows de uma vez (season pack / discografia c/ milhares)
// congela o modal — então revelamos em lotes via useIncrementalReveal (mais ao
// rolar/clicar), em vez do antigo teto fixo que escondia o resto atrás do filtro.
// Dir rows nunca são capadas (esqueleto de navegação).

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
  onDownloadFolder,
  onDownloadDir,
}: FileTreeProps) {
  const { t } = useTranslation()
  // Key no infoHash (não info.files): a estrutura de arquivos é estável por
  // torrent, mas o poll de progresso de 2s recria info.files — depender dele
  // rebuildava a árvore inteira a cada tick (re-render desnecessário).
  const root = useMemo(
    () => buildFileTree(info.files, { filter: fileFilter, typeFilter: fileTypeFilter }),
    [info.infoHash, fileFilter, fileTypeFilter],
  )
  // Total de arquivos elegíveis (nas pastas abertas), sem cap — só pra CONTAR
  // (não renderiza): montar o array é barato; o que pesa é montar os <button>.
  const totalFiles = useMemo(
    () => flattenTreeCapped(root, expanded, Number.MAX_SAFE_INTEGER).rows.filter((r) => r.kind === 'file').length,
    [root, expanded],
  )
  // Reseta o lote ao trocar torrent/filtro; abrir uma pasta NÃO reseta (só muda o total).
  const reveal = useIncrementalReveal(totalFiles, `${info.infoHash}|${fileFilter}|${fileTypeFilter}`)
  const { rows } = useMemo(
    () => flattenTreeCapped(root, expanded, reveal.visible),
    [root, expanded, reveal.visible],
  )
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
        {fileFilter ? t('player.files.noMatch', { filter: fileFilter }) : t('player.files.noneWithFilter')}
      </p>
    )
  }

  return (
    <div
      ref={containerRef}
      role="tree"
      aria-label={t('player.files.treeLabel')}
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
              aria-selected={false}
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
              {onDownloadDir && (
                // Span (não button) pra não aninhar <button> dentro da row-button.
                // stopPropagation pra baixar em vez de expandir/recolher a pasta.
                <span
                  role="button"
                  tabIndex={-1}
                  title={t('player.files.downloadFolder')}
                  aria-label={t('player.files.downloadFolder')}
                  onClick={e => { e.stopPropagation(); onDownloadDir(row.node.path) }}
                  onKeyDown={e => { if (e.key === 'Enter' || e.key === ' ') { e.preventDefault(); e.stopPropagation(); onDownloadDir(row.node.path) } }}
                  className="p-1 -m-1 rounded hover:bg-surface-tertiary text-text-muted hover:text-blue-400 transition-colors flex-shrink-0"
                >
                  <FolderDown className="w-3.5 h-3.5" />
                </span>
              )}
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
            onDownloadFolder={onDownloadFolder}
            treeItemProps={{
              role: 'treeitem',
              'aria-selected': selected,
              'data-row-idx': i,
              tabIndex: i === focusIdx ? 0 : -1,
              onKeyDown: e => onKeyDown(e, i),
              onFocus: () => setFocusIdx(i),
            } as React.HTMLAttributes<HTMLButtonElement>}
          />
        )
      })}
      {reveal.hasMore && (
        <div ref={reveal.sentinelRef} className="px-2 pt-1 pb-2 flex-shrink-0">
          <button
            type="button"
            onClick={reveal.showMore}
            className="w-full flex items-center justify-center gap-1.5 rounded-lg bg-surface-2 py-2 text-xs text-text-secondary hover:text-text-primary"
          >
            <ChevronDown className="w-3.5 h-3.5" />
            {t('player.files.showMore', { count: reveal.remaining, total: totalFiles })}
          </button>
        </div>
      )}
    </div>
  )
}
