import { Play, FileVideo, ChevronRight, ArrowDownWideNarrow, ArrowUpWideNarrow, Eye } from 'lucide-react'
import { TorrentInfo, streamThumbnailURL } from '../../api/client'
import { detectViewerKind } from '../viewer/viewerKind'
import { useHoverThumb } from '../FileThumbHover'
import { fileType, formatSize, filterAndSortFiles, FILE_EXTRA_RE, FILE_AUDIO_RE, type FileType } from './playerFormat'

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

// File picker — right sidebar on lg+, stacked panel below on mobile.
// Series-aware: detects S/E in filenames and labels them. Filter matches both
// the path AND the parsed S/E tag so "s04e03" finds the episode without typing
// the show name. Extras (featurettes, bonus, behind-the-scenes) sort to the
// bottom with an EXTRA badge.
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
  const filterLower = fileFilter.trim().toLowerCase()
  const isExtra = (path: string) => FILE_EXTRA_RE.test(path)
  const typeCounts = { video: 0, audio: 0, other: 0 }
  for (const f of info.files) typeCounts[fileType(f)]++
  // Shared with useMediaQueue (PlayerModal) — the prev/next buttons follow
  // exactly this display order.
  const filteredFiles = filterAndSortFiles(info.files, {
    filter: fileFilter, typeFilter: fileTypeFilter,
    sortBySize: fileSortBySize, sizeDesc: fileSizeDesc,
  })
  const fileBtnClass = (fIdx: number, isPlayable: boolean, canPreview: boolean, ext: boolean): string => {
    if (selectedFile === fIdx) return 'bg-green-500/20 text-green-400 border border-green-500/30'
    if (isPlayable) {
      if (ext) return 'bg-surface-secondary/40 text-text-muted hover:bg-surface-tertiary/80 border border-transparent'
      return 'bg-surface-tertiary/50 text-text-primary hover:bg-surface-tertiary border border-transparent'
    }
    if (canPreview) return 'bg-blue-500/5 text-blue-700/80 dark:text-blue-200/80 hover:bg-blue-500/15 border border-blue-500/20'
    return 'bg-surface-secondary/50 text-text-muted hover:bg-surface-tertiary border border-transparent'
  }
  const cycleSizeSort = () => {
    // Cicla: Padrão → Tamanho (maior) → Tamanho (menor) → Padrão
    if (!fileSortBySize) setFileSortBySize(true)
    else if (fileSizeDesc) setFileSizeDesc(false)
    else { setFileSortBySize(false); setFileSizeDesc(true) }
  }
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
      </div>
      <div className="flex flex-col gap-1.5 px-2 py-2 overflow-y-auto min-h-0 flex-1 lg:flex-none lg:max-h-[60vh]">
        {filteredFiles.length === 0 && (
          <p className="text-xs text-text-muted text-center py-3">
            {fileFilter ? `Nenhum arquivo bate com "${fileFilter}"` : 'Nenhum arquivo com esse filtro'}
          </p>
        )}
        {filteredFiles.slice(0, 100).map(f => {
          const ep = parseEpisode(f.path)
          const extra = isExtra(f.path)
          // Compact name for sidebar: drop the long shared prefix
          // (everything before the last "/") so paths fit in 320px.
          const shortName = f.path.split('/').slice(-2).join('/')
          const isPlayable = f.isVideo || FILE_AUDIO_RE.test(f.path)
          const previewKind = isPlayable ? 'unknown' : detectViewerKind(f.path)
          const canPreview = previewKind !== 'unknown'
          const previewBadge = canPreview ? previewKind.toUpperCase() : null
          // Hover frame-preview only for video files.
          const thumbUrl = fileType(f) === 'video' && info.infoHash
            ? streamThumbnailURL(info.infoHash, f.index, 10)
            : null
          return (
            <button
              key={f.index}
              ref={selectedFile === f.index ? selectedFileRef : null}
              onClick={() => {
                hoverThumb.hide()
                if (isPlayable) playFile(f.index)
                else if (canPreview) setPreviewFileIdx(f.index)
                // else: dead row, click does nothing (download via long-press / context menu still available)
              }}
              onMouseEnter={e => hoverThumb.show(thumbUrl, e, f.path)}
              onMouseMove={hoverThumb.move}
              onMouseLeave={hoverThumb.hide}
              title={f.path}
              className={`flex flex-col flex-shrink-0 gap-1 px-3 py-2.5 sm:py-2 min-h-[48px] sm:min-h-0 rounded-lg text-sm sm:text-xs transition-colors text-left ${fileBtnClass(f.index, isPlayable, canPreview, extra)}`}
            >
              <span className="flex items-center gap-1.5 min-w-0">
                {ep && (
                  <span className="text-[10px] font-mono bg-blue-500/15 text-blue-700 dark:text-blue-300 border border-blue-500/30 px-1.5 py-0.5 rounded flex-shrink-0">
                    {ep}
                  </span>
                )}
                {extra && (
                  <span className="text-[10px] font-mono bg-surface-tertiary/60 text-text-secondary border border-strong/40 px-1.5 py-0.5 rounded flex-shrink-0">
                    EXTRA
                  </span>
                )}
                {previewBadge && (
                  <span className="text-[10px] font-mono bg-blue-500/15 text-blue-700 dark:text-blue-300 border border-blue-500/30 px-1.5 py-0.5 rounded flex-shrink-0 inline-flex items-center gap-1" title="Visualizar inline">
                    <Eye className="w-3 h-3" />
                    {previewBadge}
                  </span>
                )}
                {selectedFile === f.index && <Play className="w-3 h-3 flex-shrink-0" />}
              </span>
              <span className="flex items-center justify-between gap-2 min-w-0">
                <span className="truncate">{shortName}</span>
                <span className="text-text-muted flex-shrink-0 text-[10px] tabular-nums">{formatSize(f.size)}</span>
              </span>
            </button>
          )
        })}
        {filteredFiles.length > 100 && (
          <p className="text-[11px] text-text-muted text-center py-3 px-2 leading-snug">
            Mostrando 100 de {filteredFiles.length}. Use o filtro acima
            (ex: <span className="font-mono text-text-secondary">s04e03</span> ou
            parte do nome) pra achar o resto.
          </p>
        )}
      </div>
    </aside>
  )
}
