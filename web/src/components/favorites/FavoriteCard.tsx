import { useTranslation } from 'react-i18next'
import { Trash2, Play, Clock, FileVideo, Folder, FolderOpen, Download } from 'lucide-react'
import { StreamFavorite, FavoriteFolder } from '../../api/client'
import Thumbnail from '../Thumbnail'
import SeedBadge from '../SeedBadge'
import { newTabProps, playHref } from '../../lib/cardNav'
import { formatDate } from '../../lib/format'

type FavoriteCardProps = {
  readonly fav: StreamFavorite
  readonly selected: boolean
  readonly anySelected: boolean
  readonly folders: FavoriteFolder[]
  readonly seedRefresh: number
  readonly onToggleSelected: () => void
  readonly onDragStart: (e: React.DragEvent) => void
  readonly onPlay: () => void
  readonly onRemove: () => void
  readonly onDownload: () => void
  readonly onOpenContents: () => void
  readonly onMoveToFolder: (folderId: number | null) => void
}

export default function FavoriteCard(p: FavoriteCardProps) {
  const { t } = useTranslation()
  const { fav, folders } = p
  return (
    <div
      key={fav.name}
      role="button"
      tabIndex={0}
      draggable
      onDragStart={e => p.onDragStart(e)}
      {...newTabProps(playHref(fav.infoHash), () => p.onPlay())}
      onKeyDown={e => { if (e.key === 'Enter') p.onPlay() }}
      className={`card flex flex-col gap-2 group cursor-grab active:cursor-grabbing relative w-full text-left ${
        p.selected ? 'ring-2 ring-green-500' : ''
      }`}
    >
      {/* Multi-select checkbox — pick several, then move all to a
          folder via the action bar. Stops propagation so it doesn't
          start a drag/play. */}
      <input
        type="checkbox"
        checked={p.selected}
        onChange={() => p.onToggleSelected()}
        onClick={e => e.stopPropagation()}
        title={t('favorites.select')}
        className={`absolute top-2 left-2 z-10 w-4 h-4 accent-green-500 cursor-pointer ${
          p.anySelected ? 'opacity-100' : 'max-sm:opacity-100 opacity-0 group-hover:opacity-100'
        }`}
      />
      <div className="flex items-start gap-2 pl-6">
        {/* Lazy TMDB poster — falls back to a Film/Music icon when no match. */}
        <Thumbnail title={fav.name} size="md" infoHash={fav.infoHash} />
        <h3 className="text-sm font-medium text-text-primary line-clamp-2 flex-1" title={fav.name}>
          <FileVideo className="w-3.5 h-3.5 inline mr-1.5 text-text-muted" />
          {fav.name}
        </h3>
        <button
          onClick={(e) => { e.stopPropagation(); p.onRemove() }}
          title={t('favorites.removeFromFavorites')}
          className="text-text-muted hover:text-red-400 transition-colors max-sm:opacity-100 opacity-0 group-hover:opacity-100 flex-shrink-0"
        >
          <Trash2 className="w-4 h-4" />
        </button>
      </div>

      <div className="flex items-center gap-3 text-xs text-text-muted flex-wrap">
        <span className="flex items-center gap-1">
          <Clock className="w-3 h-3" />
          {formatDate(fav.favoritedAt)}
        </span>
        <SeedBadge infoHash={fav.infoHash} magnet={fav.magnet} refreshSignal={p.seedRefresh} />
        <span className={`text-[10px] px-1.5 py-0.5 rounded ${
          fav.reason === 'auto-5min'
            ? 'bg-blue-500/20 text-blue-700 dark:text-blue-300 border border-blue-500/30'
            : 'bg-pink-500/20 text-pink-700 dark:text-pink-300 border border-pink-500/30'
        }`}>
          {fav.reason === 'auto-5min' ? t('favorites.autoReason') : t('favorites.manualReason')}
        </span>
        {fav.folderId != null && (
          <span className="text-[10px] px-1.5 py-0.5 rounded bg-surface-secondary text-text-secondary border border-default flex items-center gap-1">
            <Folder className="w-2.5 h-2.5" />
            {folders.find(f => f.id === fav.folderId)?.name || '?'}
          </span>
        )}
      </div>

      <div className="flex gap-1.5 mt-auto pt-2 border-t border-default">
        <button
          onClick={e => { e.stopPropagation(); p.onPlay() }}
          disabled={!fav.magnet}
          title={fav.magnet ? t('favorites.playTooltip') : t('favorites.magnetNotSaved')}
          className={`flex items-center gap-1 text-xs px-2.5 py-1.5 rounded-lg flex-1 justify-center transition-colors ${
            fav.magnet
              ? 'bg-green-500/20 hover:bg-green-500/30 text-green-700 dark:text-green-300 border border-green-500/30'
              : 'bg-surface-tertiary/30 text-text-muted cursor-not-allowed'
          }`}
        >
          <Play className="w-3.5 h-3.5" />
          {t('favorites.play')}
        </button>
        {/* Baixar — abre o modal unificado (destino + seleção de
            arquivos/árvore), igual à busca/histórico. */}
        <button
          onClick={e => { e.stopPropagation(); p.onDownload() }}
          disabled={!fav.magnet}
          title={t('favorites.downloadTooltip')}
          className={`flex items-center justify-center text-xs px-2.5 py-1.5 rounded-lg transition-colors ${
            fav.magnet
              ? 'bg-blue-500/15 hover:bg-blue-500/25 text-blue-700 dark:text-blue-300 border border-blue-500/30'
              : 'bg-surface-tertiary/30 text-text-muted cursor-not-allowed'
          }`}
        >
          <Download className="w-3.5 h-3.5" />
        </button>
        {/* Details/contents — view files + torrent details without
            committing to play (consistent with search/history). */}
        <button
          onClick={e => { e.stopPropagation(); p.onOpenContents() }}
          disabled={!fav.magnet}
          title={t('favorites.contentsTooltip')}
          className={`flex items-center justify-center text-xs px-2.5 py-1.5 rounded-lg transition-colors ${
            fav.magnet
              ? 'bg-surface-tertiary/40 hover:bg-surface-tertiary/70 text-text-primary border border-default'
              : 'bg-surface-tertiary/30 text-text-muted cursor-not-allowed'
          }`}
        >
          <FolderOpen className="w-3.5 h-3.5" />
        </button>
        {/* Move to folder — touch-friendly alternative to drag-and-drop
            (HTML5 DnD doesn't work on touch). Native <select> is fully
            usable on iOS. Only shown when folders exist. */}
        {folders.length > 0 && (
          <select
            value={fav.folderId ?? ''}
            onClick={e => e.stopPropagation()}
            onChange={e => p.onMoveToFolder(e.target.value === '' ? null : Number(e.target.value))}
            title={t('favorites.moveToFolder')}
            className="text-xs px-2 py-1.5 rounded-lg bg-surface-tertiary/40 text-text-primary border border-default focus:outline-none focus:border-green-500 cursor-pointer max-w-[45%]"
          >
            <option value="">{t('favorites.rootNoFolder')}</option>
            {folders.map(f => (
              <option key={f.id} value={f.id}>{f.name}</option>
            ))}
          </select>
        )}
      </div>
    </div>
  )
}
