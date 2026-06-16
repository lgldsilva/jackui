import { useEffect, useMemo, useState } from 'react'
import { FileVideo, ChevronRight, ChevronDown, ArrowDownWideNarrow, ArrowUpWideNarrow, List, FolderTree } from 'lucide-react'
import { useTranslation } from 'react-i18next'
import { TorrentInfo } from '../../api/client'
import { useHoverThumb } from '../FileThumbHover'
import { usePersistedState } from '../../lib/storage'
import { buildFileTree, pathsToExpand, hasSubdirs } from '../../lib/fileTree'
import { fileType, filterAndSortFiles, type FileType } from './playerFormat'
import { FileRow } from './FileRow'
import { FileTree } from './FileTree'
import { useIncrementalReveal } from './useIncrementalReveal'

type FilePickerSidebarProps = {
  readonly info: TorrentInfo
  readonly videoFiles: TorrentInfo['files']
  readonly selectedFile: number
  readonly selectedFileRef: React.RefObject<HTMLButtonElement>
  readonly fileFilter: string
  readonly fileTypeFilter: FileType
  readonly fileSortBySize: boolean
  readonly fileSizeDesc: boolean
  readonly hoverThumb: ReturnType<typeof useHoverThumb>
  readonly parseEpisode: (path: string) => string | null
  readonly playFile: (idx: number) => void
  readonly setFileFilter: (v: string) => void
  readonly setFileTypeFilter: (v: FileType) => void
  readonly setFileSortBySize: (v: boolean) => void
  readonly setFileSizeDesc: (v: boolean) => void
  readonly setSidebarOpen: (v: boolean) => void
  readonly setPreviewFileIdx: (v: number | null) => void
}

type FileView = 'list' | 'tree'

// File picker — right sidebar on lg+, stacked panel below on mobile.
// Two views (persisted): a flat LIST (series-aware sort, extras last) and a
// collapsible folder TREE (season packs / discographies / huge torrents). The
// tree auto-expands to reveal the currently selected file when opened. Both
// views share the same file-row visual (FileRow), filters and type counts.
export function FilePickerSidebar({
  info,
  videoFiles,
  selectedFile,
  selectedFileRef,
  fileFilter,
  fileTypeFilter,
  fileSortBySize,
  fileSizeDesc,
  hoverThumb,
  parseEpisode,
  playFile,
  setFileFilter,
  setFileTypeFilter,
  setFileSortBySize,
  setFileSizeDesc,
  setSidebarOpen,
  setPreviewFileIdx,
}: FilePickerSidebarProps) {
  const { t } = useTranslation()
  // Default: remember the last choice; first time = Lista (don't surprise
  // existing users). Tree is opt-in even when the torrent has subfolders.
  const [view, setView] = usePersistedState<FileView>('player.fileView', 'list')
  const treeable = hasSubdirs(info.files)
  const [expanded, setExpanded] = useState<Set<string>>(new Set())

  const filterLower = fileFilter.trim().toLowerCase()
  const typeCounts = { video: 0, audio: 0, other: 0 }
  for (const f of info.files) typeCounts[fileType(f)]++
  // Shared with useMediaQueue (PlayerModal) — the prev/next buttons follow
  // exactly this display order.
  const filteredFiles = filterAndSortFiles(info.files, {
    filter: fileFilter, typeFilter: fileTypeFilter,
    sortBySize: fileSortBySize, sizeDesc: fileSizeDesc,
  })
  // Renderiza em lotes (revela mais ao rolar/clicar) — antes cortava em 100 e
  // escondia o resto atrás do filtro. Reseta o lote quando torrent/filtro/ordem muda.
  const reveal = useIncrementalReveal(
    filteredFiles.length,
    `${info.infoHash}|${fileFilter}|${fileTypeFilter}|${fileSortBySize}|${fileSizeDesc}`,
  )

  const selectedPath = useMemo(() => {
    const f = info.files.find(x => x.index === selectedFile)
    return f?.path ?? null
  }, [info.files, selectedFile])

  // When the tree is the active view, auto-expand the path to the selected file
  // (or the first file when none is selected). Filter changes rebuild the tree,
  // so re-derive the reveal set against the CURRENT filter too.
  useEffect(() => {
    if (view !== 'tree' || !treeable) return
    const tree = buildFileTree(info.files, { filter: fileFilter, typeFilter: fileTypeFilter })
    const reveal = pathsToExpand(tree, selectedPath)
    setExpanded(prev => {
      const next = new Set(prev)
      for (const p of reveal) next.add(p)
      return next
    })
  }, [view, treeable, info.files, fileFilter, fileTypeFilter, selectedPath])

  const cycleSizeSort = () => {
    // Cicla: Padrão → Tamanho (maior) → Tamanho (menor) → Padrão
    if (!fileSortBySize) setFileSortBySize(true)
    else if (fileSizeDesc) setFileSizeDesc(false)
    else { setFileSortBySize(false); setFileSizeDesc(true) }
  }

  const viewBtnClass = (active: boolean) =>
    `flex items-center gap-1 px-2 py-1 rounded text-[11px] border transition-colors ${
      active
        ? 'bg-green-500/20 text-green-700 dark:text-green-300 border-green-500/40'
        : 'bg-surface text-text-secondary border-default hover:bg-surface-tertiary/60'
    }`

  return (
    <aside className="flex flex-col flex-1 lg:flex-initial lg:flex-shrink-0 lg:w-80 xl:w-96 border-t lg:border-t-0 lg:border-l border-default bg-surface-elevated/50 min-h-0 lg:overflow-hidden">
      {/* A barra inteira retrai a lista — clicar em qualquer parte funciona,
          não só no chevron (o botão vira só indicador via pointer-events-none). */}
      <button
        type="button"
        onClick={() => setSidebarOpen(false)}
        title="Esconder lista de arquivos"
        className="w-full flex items-center justify-between gap-2 px-3 py-2 border-b border-default flex-shrink-0 text-left cursor-pointer hover:bg-surface-tertiary/40 transition-colors"
      >
        <p className="text-xs text-text-secondary flex items-center gap-2 min-w-0">
          <FileVideo className="w-3.5 h-3.5 text-text-muted flex-shrink-0" />
          <span className="truncate">
            {filteredFiles.length}{filterLower ? ` / ${info.files.length}` : ''} arquivo{filteredFiles.length === 1 ? '' : 's'}
            {videoFiles.length > 0 && <span className="text-blue-400"> · {videoFiles.length} vídeo{videoFiles.length === 1 ? '' : 's'}</span>}
          </span>
        </p>
        <span className="text-text-muted p-1 flex-shrink-0 pointer-events-none">
          <ChevronRight className="w-4 h-4" />
        </span>
      </button>
      {info.files.length > 6 && (
        <div className="px-3 py-2 border-b border-default flex-shrink-0">
          <input
            type="text"
            value={fileFilter}
            onChange={e => setFileFilter(e.target.value)}
            placeholder="Filtrar (ex: s04e03)"
            className="w-full bg-surface border border-default rounded px-3 py-2 sm:py-1 text-sm sm:text-xs text-text-primary placeholder-gray-500 focus:outline-none focus:border-green-500"
          />
        </div>
      )}
      <div className="px-3 py-2 border-b border-default flex-shrink-0 flex items-center gap-1.5 flex-wrap">
        {([
          { key: 'all' as const, label: 'Todos', count: info.files.length },
          { key: 'video' as const, label: 'Vídeo', count: typeCounts.video },
          { key: 'audio' as const, label: 'Áudio', count: typeCounts.audio },
          { key: 'other' as const, label: 'Outros', count: typeCounts.other },
        ])
          .filter(o => o.key === 'all' || o.count > 0)
          .map(o => (
            <button
              key={o.key}
              onClick={() => setFileTypeFilter(o.key)}
              className={`px-2 py-1 rounded text-[11px] border transition-colors ${
                fileTypeFilter === o.key
                  ? 'bg-green-500/20 text-green-700 dark:text-green-300 border-green-500/40'
                  : 'bg-surface text-text-secondary border-default hover:bg-surface-tertiary/60'
              }`}
            >
              {o.label} <span className="tabular-nums opacity-70">{o.count}</span>
            </button>
          ))}
        <div className="flex-1" />
        {/* List ⇄ Tree toggle — only worth showing when the torrent has folders.
            Size sort is meaningless in the tree (order is hierarchical), so it
            hides in tree mode. */}
        {treeable && (
          <div className="flex items-center gap-1">
            <button
              onClick={() => setView('list')}
              title={t('player.view_list')}
              aria-label={t('player.view_list')}
              aria-pressed={view === 'list'}
              className={viewBtnClass(view === 'list')}
            >
              <List className="w-3.5 h-3.5" />
            </button>
            <button
              onClick={() => setView('tree')}
              title={t('player.view_tree')}
              aria-label={t('player.view_tree')}
              aria-pressed={view === 'tree'}
              className={viewBtnClass(view === 'tree')}
            >
              <FolderTree className="w-3.5 h-3.5" />
            </button>
          </div>
        )}
        {(view === 'list' || !treeable) && (
          <button
            onClick={cycleSizeSort}
            title="Ordenar por tamanho"
            className={`flex items-center gap-1 px-2 py-1 rounded text-[11px] border transition-colors ${
              fileSortBySize
                ? 'bg-green-500/20 text-green-700 dark:text-green-300 border-green-500/40'
                : 'bg-surface text-text-secondary border-default hover:bg-surface-tertiary/60'
            }`}
          >
            {fileSortBySize && !fileSizeDesc
              ? <ArrowUpWideNarrow className="w-3.5 h-3.5" />
              : <ArrowDownWideNarrow className={`w-3.5 h-3.5 ${fileSortBySize ? '' : 'opacity-50'}`} />}
            Tamanho
          </button>
        )}
      </div>
      {view === 'tree' && treeable ? (
        <FileTree
          info={info}
          selectedFile={selectedFile}
          selectedFileRef={selectedFileRef}
          fileFilter={fileFilter}
          fileTypeFilter={fileTypeFilter}
          expanded={expanded}
          setExpanded={setExpanded}
          hoverThumb={hoverThumb}
          parseEpisode={parseEpisode}
          playFile={playFile}
          setPreviewFileIdx={setPreviewFileIdx}
        />
      ) : (
        <div className="flex flex-col gap-1.5 px-2 py-2 overflow-y-auto min-h-0 flex-1 lg:flex-none lg:max-h-[60vh]">
          {filteredFiles.length === 0 && (
            <p className="text-xs text-text-muted text-center py-3">
              {fileFilter ? `Nenhum arquivo bate com "${fileFilter}"` : 'Nenhum arquivo com esse filtro'}
            </p>
          )}
          {filteredFiles.slice(0, reveal.visible).map(f => (
            <FileRow
              key={f.index}
              ref={selectedFile === f.index ? selectedFileRef : undefined}
              file={f}
              infoHash={info.infoHash}
              selected={selectedFile === f.index}
              // Compact name for the flat list: last two path segments fit 320px.
              displayName={f.path.split('/').slice(-2).join('/')}
              hoverThumb={hoverThumb}
              parseEpisode={parseEpisode}
              playFile={playFile}
              setPreviewFileIdx={setPreviewFileIdx}
            />
          ))}
          {reveal.hasMore && (
            <div ref={reveal.sentinelRef} className="px-2 pt-1 pb-2">
              <button
                onClick={reveal.showMore}
                className="w-full flex items-center justify-center gap-1.5 rounded-lg bg-surface-2 py-2 text-xs text-text-secondary hover:text-text-primary"
              >
                <ChevronDown className="w-3.5 h-3.5" />
                Mostrar mais ({reveal.remaining} de {filteredFiles.length})
              </button>
            </div>
          )}
        </div>
      )}
    </aside>
  )
}
