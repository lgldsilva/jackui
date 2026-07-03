import { forwardRef, memo } from 'react'
import { Play, Eye, FolderDown } from 'lucide-react'
import { useTranslation } from 'react-i18next'
import { TorrentInfo, streamThumbnailURL } from '../../api/client'
import { detectViewerKind } from '../viewer/viewerKind'
import { useHoverThumb } from '../FileThumbHover'
import { fileType, formatSize, FILE_EXTRA_RE, FILE_AUDIO_RE } from './playerFormat'

// One file entry, shared by the flat list (FilePickerSidebar) and the tree
// (FileTree). Centralising it keeps the badges (episode / EXTRA / viewer
// kind), the hover frame-preview, the selected highlight and the
// play-vs-preview click behaviour identical across both views — change once,
// both follow. The universal viewer (detectViewerKind) decides which
// non-playable files get the eye/preview affordance.

type FileRowProps = {
  readonly file: TorrentInfo['files'][number]
  readonly infoHash: string
  readonly selected: boolean
  readonly hoverThumb: ReturnType<typeof useHoverThumb>
  readonly parseEpisode: (path: string) => string | null
  readonly playFile: (idx: number) => void
  readonly setPreviewFileIdx: (v: number | null) => void
  // The tree passes a short basename; the flat list passes the last two path
  // segments. Both keep the full path in the title tooltip.
  readonly displayName: string
  // Tree rows indent by depth; the flat list passes nothing.
  readonly indentStyle?: React.CSSProperties
  // role="treeitem" wiring for the tree; the flat list leaves it undefined.
  readonly treeItemProps?: React.HTMLAttributes<HTMLButtonElement>
  // "Baixar a pasta inteira deste arquivo" — só aparece quando o pai oferece e o
  // arquivo está dentro de uma pasta (torrents enormes com centenas de arquivos).
  readonly onDownloadFolder?: (file: TorrentInfo['files'][number]) => void
}

function fileBtnClass(selected: boolean, isPlayable: boolean, canPreview: boolean, ext: boolean): string {
  if (selected) return 'bg-green-500/20 text-green-400 border border-green-500/30'
  if (isPlayable) {
    if (ext) return 'bg-surface-secondary/40 text-text-muted hover:bg-surface-tertiary/80 border border-transparent'
    return 'bg-surface-tertiary/50 text-text-primary hover:bg-surface-tertiary border border-transparent'
  }
  if (canPreview) return 'bg-blue-500/5 text-blue-700/80 dark:text-blue-200/80 hover:bg-blue-500/15 border border-blue-500/20'
  return 'bg-surface-secondary/50 text-text-muted hover:bg-surface-tertiary border border-transparent'
}

// memo so flattening a thousands-of-files tree doesn't re-render every row on
// each expand/collapse or focus change — only the rows whose props actually
// change repaint. Parent passes stable callbacks (useCallback) so this holds.
// Props annotated directly on the function param (not via forwardRef's generic)
// so the analyzer sees every field is consumed — the generic form hid the usage
// and tripped S6767 (prop defined but never used).
function FileRowImpl(
  { file: f, infoHash, selected, hoverThumb, parseEpisode, playFile, setPreviewFileIdx, displayName, indentStyle, treeItemProps, onDownloadFolder }: FileRowProps,
  ref: React.ForwardedRef<HTMLButtonElement>,
) {
  const { t } = useTranslation()
  const ep = parseEpisode(f.path)
  const extra = FILE_EXTRA_RE.test(f.path)
  const inFolder = f.path.includes('/')
  const isPlayable = f.isVideo || FILE_AUDIO_RE.test(f.path)
  const previewKind = isPlayable ? 'unknown' : detectViewerKind(f.path)
  const canPreview = previewKind !== 'unknown'
  const previewBadge = canPreview ? previewKind.toUpperCase() : null
  // Hover frame-preview only for video files.
  const thumbUrl = fileType(f) === 'video' && infoHash
    ? streamThumbnailURL(infoHash, f.index, 10)
    : null
  return (
    <button
      ref={ref}
      type="button"
      style={indentStyle}
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
      {...treeItemProps}
      className={`flex flex-col flex-shrink-0 gap-1 px-3 py-2.5 sm:py-2 min-h-[48px] sm:min-h-0 rounded-lg text-sm sm:text-xs transition-colors text-left w-full ${fileBtnClass(selected, isPlayable, canPreview, extra)}`}
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
          <span className="text-[10px] font-mono bg-blue-500/15 text-blue-700 dark:text-blue-300 border border-blue-500/30 px-1.5 py-0.5 rounded flex-shrink-0 inline-flex items-center gap-1" title={t('player.files.previewInline')}>
            <Eye className="w-3 h-3" />
            {previewBadge}
          </span>
        )}
        {selected && <Play className="w-3 h-3 flex-shrink-0" />}
      </span>
      <span className="flex items-center justify-between gap-2 min-w-0">
        <span className="truncate">{displayName}</span>
        <span className="flex items-center gap-1.5 flex-shrink-0">
          {onDownloadFolder && inFolder && (
            // Span (não button) pra não aninhar <button> dentro da row-button.
            <span
              role="button"
              tabIndex={-1}
              title={t('player.files.downloadFolder')}
              aria-label={t('player.files.downloadFolder')}
              onClick={e => { e.stopPropagation(); onDownloadFolder(f) }}
              onKeyDown={e => { if (e.key === 'Enter' || e.key === ' ') { e.preventDefault(); e.stopPropagation(); onDownloadFolder(f) } }}
              className="p-1 -m-1 rounded hover:bg-surface-tertiary text-text-muted hover:text-blue-400 transition-colors"
            >
              <FolderDown className="w-3.5 h-3.5" />
            </span>
          )}
          <span className="text-text-muted text-[10px] tabular-nums">{formatSize(f.size)}</span>
        </span>
      </span>
    </button>
  )
}

export const FileRow = memo(forwardRef(FileRowImpl))
FileRow.displayName = 'FileRow'
