import { memo } from 'react'
import { useTranslation } from 'react-i18next'
import {
  Loader2, Pause, Play, Trash2, CheckCircle2, AlertCircle, Clock, Users,
  ArrowDownCircle, ArrowUpCircle, Info, HardDrive, AlertTriangle, Folder,
} from 'lucide-react'
import type { DownloadEntry, TorrentInfo, DownloadPriority } from '../../api/client'
import { WHOLE_TORRENT_FILE_INDEX } from '../../api/client'
import { formatRate, formatDurationShort, formatBytesPair, formatBytes } from '../../lib/format'
import { useAuth } from '../../auth/AuthContext'
import { KindBadge } from './KindBadge'
import { DownloadStatusBadge } from './DownloadStatusBadge'
import { PriorityBadge } from './PriorityBadge'
import { ActionButton } from './ActionButton'

function downloadBorderClass(completed: boolean, failed: boolean, paused: boolean, moving = false): string {
  if (completed) return 'border-green-500/30 hover:border-green-500/50'
  if (failed) return 'border-red-500/30 hover:border-red-500/50'
  if (moving) return 'border-amber-500/30 hover:border-amber-500/50'
  if (paused) return 'border-strong/50 hover:border-strong/60'
  return 'border-cyan-500/30 hover:border-cyan-500/50'
}

function downloadBarGradient(completed: boolean, failed: boolean, paused: boolean, moving = false): string {
  if (completed) return 'from-green-500 to-emerald-400'
  if (failed) return 'from-red-500 to-rose-400'
  if (moving) return 'from-amber-500 to-yellow-400'
  if (paused) return 'from-gray-600 to-gray-500'
  return 'from-cyan-500 to-blue-400'
}

function computeETA(d: DownloadEntry, t: (key: string, opts?: Record<string, unknown>) => string): string {
  // Prefer backend-computed ETA (more accurate — uses live swarm rate)
  if (d.eta && d.eta > 0) {
    return t('downloads.page.etaRemaining', { time: formatDurationShort(d.eta) })
  }
  if (!d.startedAt || d.fileSize <= 0 || d.bytesDownloaded <= 0) return ''
  if (d.bytesDownloaded >= d.fileSize) return ''
  const startMs = new Date(d.startedAt).getTime()
  if (!Number.isFinite(startMs) || startMs <= 0) return ''
  const elapsedSec = (Date.now() - startMs) / 1000
  if (elapsedSec < 2) return ''
  const rate = d.bytesDownloaded / elapsedSec
  if (rate <= 0) return ''
  const remainingSec = (d.fileSize - d.bytesDownloaded) / rate
  if (!Number.isFinite(remainingSec) || remainingSec <= 0) return ''
  return t('downloads.page.etaRemaining', { time: formatDurationShort(remainingSec) })
}

type DownloadCardProps = {
  readonly d: DownloadEntry
  readonly live?: TorrentInfo
  readonly busy: boolean
  readonly selected?: boolean
  /** True quando o download é UM arquivo de um torrent multi-arquivo (há irmãos
      com o mesmo infoHash). Aí o título mostra o NOME DO ARQUIVO, não o nome do
      torrent — senão todos os episódios de "Euphoria" aparecem idênticos. */
  readonly multiFile?: boolean
  readonly onToggleSelected?: () => void
  readonly onPause: () => void
  readonly onResume: () => void
  readonly onDelete: () => void
  readonly onPromote?: () => void
  readonly onStopSeed?: () => void
  readonly onPlay?: () => void
  readonly onInspect?: () => void
  readonly onSetPriority?: (priority: DownloadPriority) => void
  /** Opens the file in the local browser; undefined when it isn't under a mount. */
  readonly onOpenLocal?: () => void
}

// DownloadCard — Premium redesigned background download card.
export const DownloadCard = memo(function DownloadCard({ d, live, busy, selected, multiFile, onToggleSelected, onPause, onResume, onDelete, onPromote, onStopSeed, onPlay, onInspect, onSetPriority, onOpenLocal }: DownloadCardProps) {
  const { isGuest } = useAuth()
  const { t } = useTranslation()
  // Item de torrent INTEIRO (sentinel): UM card com progresso agregado.
  const isWholeTorrent = d.fileIndex === WHOLE_TORRENT_FILE_INDEX
  const wholeFileCount = live?.files?.length ?? 0
  // Em torrent multi-arquivo o `name` é o nome do torrent (igual pra todos os
  // arquivos), então o que distingue é o basename do filePath (ex: o episódio).
  const fileBase = d.filePath ? d.filePath.split('/').pop() || '' : ''
  const titleText = multiFile && fileBase ? fileBase : (d.name || d.filePath)
  // Subtítulo: no multi-arquivo mostra o torrent (contexto, já que o título virou
  // o arquivo); no single-arquivo mantém o caminho como antes.
  const subtitleText = multiFile && fileBase ? d.name : d.filePath
  const pct = Math.max(0, Math.min(1, d.progress || 0)) * 100
  const isCompleted = d.status === 'completed'
  const isFailed = d.status === 'failed'
  const isPaused = d.status === 'paused'
  const isMoving = d.status === 'moving'
  const isActive = d.status === 'downloading' || d.status === 'queued'
  const isStalled = d.status === 'downloading' && (d.downRate ?? 0) === 0 && d.bytesDownloaded < d.fileSize

  const etaText = computeETA(d, t)
  const borderClass = downloadBorderClass(isCompleted, isFailed, isPaused, isMoving)
  const barGradient = downloadBarGradient(isCompleted, isFailed, isPaused, isMoving)

  return (
    <div className={`
      relative overflow-hidden rounded-xl border ${borderClass}
      bg-card dark:bg-gradient-to-br dark:from-gray-800/80 dark:to-gray-900/60 backdrop-blur-sm
      p-4 flex flex-col gap-3 transition-all duration-300
    `}>
      {/* Top row */}
      <div className="flex items-start gap-3">
        {onToggleSelected && (
          <input
            type="checkbox"
            checked={!!selected}
            onChange={onToggleSelected}
            className="mt-1 accent-cyan-500 flex-shrink-0"
            title={t('downloads.page.selectForBatch')}
          />
        )}
        <div className="min-w-0 flex-1">
          <h3 className="font-semibold text-text-primary text-sm leading-snug [overflow-wrap:anywhere]" title={titleText}>
            {titleText}
          </h3>
          <div className="flex items-center gap-1.5 mt-1.5 flex-wrap">
            <KindBadge kind="server" />
            <DownloadStatusBadge status={d.status} />
            {isWholeTorrent && (
              <span className="inline-flex items-center gap-1 text-[10px] px-2 py-0.5 rounded-md border font-medium bg-cyan-500/15 text-cyan-700 dark:text-cyan-300 border-cyan-500/30" title={t('downloads.whole_torrent_badge')}>
                <Folder className="w-3 h-3" />
                {t('downloads.whole_torrent_badge')}
                {wholeFileCount > 0 && <> · {t('downloads.whole_torrent_files', { count: wholeFileCount })}</>}
              </span>
            )}
            {d.status === 'queued' && (d.queuePosition ?? 0) > 0 && (
              <span className="text-[10px] px-1.5 py-0.5 rounded-md bg-surface-tertiary/50 text-text-secondary border border-strong/50 font-medium" title={t('downloads.page.queuePositionTitle')}>
                {t('downloads.page.queuePosition', { n: d.queuePosition })}
              </span>
            )}
            <PriorityBadge priority={d.priority} />
            {isStalled && (
              <span className="inline-flex items-center gap-1 text-[10px] px-2 py-0.5 rounded-md border font-medium bg-amber-500/15 text-amber-700 dark:text-amber-300 border-amber-500/30" title={t('downloads.page.stalledTitle')}>
                <AlertTriangle className="w-3 h-3" /> {(d.stalls ?? 0) > 0 ? t('downloads.page.noSeedCount', { count: d.stalls }) : t('downloads.page.noSeed')}
              </span>
            )}
            {d.status === 'completed' && (
              <span className="inline-flex items-center gap-1 text-[10px] px-2 py-0.5 rounded-md border font-medium bg-emerald-500/15 text-emerald-700 dark:text-emerald-300 border-emerald-500/30">
                <HardDrive className="w-3 h-3 text-emerald-400" /> {t('downloads.page.onDiskBadge')}
              </span>
            )}
            {d.username && (
              <span className="text-[10px] px-1.5 py-0.5 rounded-md bg-violet-500/15 text-violet-700 dark:text-violet-300 border border-violet-500/30 font-medium">
                {d.username}
              </span>
            )}
          </div>
          {subtitleText && <p className="text-[11px] text-text-muted truncate mt-0.5" title={subtitleText}>{subtitleText}</p>}
        </div>
      </div>

      {/* Live activity chips — só quando o anacrolix tem o torrent ativo (ou
          baixando, ou seedando depois de concluído). Mesmo formato visual do
          TorrentCard pra consistência. */}
      {live && (live.downRate > 0 || live.upRate > 0 || live.peers > 0) && (
        <div className="flex items-center gap-2 flex-wrap text-sm">
          <span
            className={`flex items-center gap-1 px-2 py-0.5 rounded-full font-mono tabular-nums ${
              live.downRate > 0 ? 'bg-emerald-500/15 text-emerald-700 dark:text-emerald-300 border border-emerald-500/30' : 'text-text-muted'
            }`}
            title={t('downloads.page.torrentDownload')}
          >
            <ArrowDownCircle className="w-3.5 h-3.5" />
            {formatRate(live.downRate)}
          </span>
          <span
            className={`flex items-center gap-1 px-2 py-0.5 rounded-full font-mono tabular-nums ${
              live.upRate > 0 ? 'bg-violet-500/15 text-violet-700 dark:text-violet-300 border border-violet-500/30' : 'text-text-muted'
            }`}
            title={t('downloads.page.torrentUploadSeeding')}
          >
            <ArrowUpCircle className="w-3.5 h-3.5" />
            {formatRate(live.upRate)}
          </span>
          <span
            className="flex items-center gap-1 px-2 py-0.5 rounded-full bg-blue-500/10 text-blue-700 dark:text-blue-300 border border-blue-500/20 font-mono tabular-nums"
            title={t('downloads.page.peersSeedersSwarm')}
          >
            <Users className="w-3.5 h-3.5" />
            {live.peers}{(live.seeders ?? 0) > 0 && <span className="text-text-muted"> / {live.seeders}</span>}
          </span>
        </div>
      )}

      {/* Progress bar */}
      <div>
        <div className="h-2 bg-surface-tertiary dark:bg-surface/80 rounded-full overflow-hidden">
          <div
            className={`h-full rounded-full bg-gradient-to-r ${barGradient} transition-all duration-500 ease-out`}
            style={{ width: `${pct.toFixed(1)}%` }}
          />
        </div>
        <div className="flex items-center justify-between mt-2 text-xs text-text-secondary">
          <span className="text-text-primary font-medium">
            {formatBytesPair(d.bytesDownloaded, d.fileSize)}
            <span className="text-text-muted ml-1">({pct.toFixed(1)}%)</span>
            {(d.bytesUploaded ?? 0) > 0 && (
              <span className="text-text-muted ml-2" title={t('downloads.page.uploadedThisSession')}>↑ {formatBytes(d.bytesUploaded ?? 0)}</span>
            )}
          </span>
          {!isCompleted && !isFailed && etaText && (
            <span className="flex items-center gap-1 text-text-muted">
              <Clock className="w-3 h-3" /> {etaText}
            </span>
          )}
          {isMoving && (
            <span className="flex items-center gap-1 text-amber-600 dark:text-amber-300 text-xs font-medium" title={t('downloads.page.movingTitle')}>
              <Loader2 className="w-3 h-3 animate-spin" /> {t('downloads.page.movingFiles')}
            </span>
          )}
          {isCompleted && (
            <span className="flex items-center gap-1 text-green-400 text-xs font-medium">
              <CheckCircle2 className="w-3 h-3" /> {t('downloads.page.completed')}
            </span>
          )}
        </div>
      </div>

      {/* Error banner */}
      {isFailed && d.error && (
        <div className="flex items-start gap-2 text-xs text-red-700 dark:text-red-300 bg-red-500/10 border border-red-500/20 rounded-lg px-3 py-2">
          <AlertCircle className="w-3.5 h-3.5 flex-shrink-0 mt-0.5" />
          <span className="break-all">{d.error}</span>
        </div>
      )}

      {/* Action bar */}
      <div className="flex items-center gap-2 pt-1 flex-wrap">
        {onPlay && !isFailed && d.bytesDownloaded > 0 && (
          <ActionButton
            onClick={onPlay}
            disabled={busy}
            variant="success"
            icon={<Play className="w-3.5 h-3.5 fill-current" />}
            label={t('downloads.page.play')}
          />
        )}
        {!isGuest && isActive && (
          <ActionButton onClick={onPause} disabled={busy} variant="neutral" icon={<Pause className="w-3.5 h-3.5" />} label={t('downloads.page.pause')} title={t('downloads.page.pauseDownloadTitle')} />
        )}
        {!isGuest && (isPaused || isFailed) && (
          <ActionButton onClick={onResume} disabled={busy} variant="info" icon={<Play className="w-3.5 h-3.5" />} label={isFailed ? t('downloads.page.retry') : t('downloads.page.resumeAction')} />
        )}
        {!isGuest && isActive && onSetPriority && (
          <label className="flex items-center gap-1.5 text-xs text-text-secondary">
            <span className="text-text-muted">{t('downloads.page.priority')}</span>
            <select
              value={d.priority || 'normal'}
              onChange={e => onSetPriority(e.target.value as DownloadPriority)}
              disabled={busy}
              className="bg-surface-secondary border border-default rounded-lg px-2 py-1 text-text-primary text-xs disabled:opacity-50 focus:outline-none focus:border-cyan-500 transition-colors cursor-pointer"
            >
              <option value="low">{t('downloads.page.priorityLow')}</option>
              <option value="normal">{t('downloads.page.priorityNormal')}</option>
              <option value="high">{t('downloads.page.priorityHigh')}</option>
            </select>
          </label>
        )}
        {!isGuest && isCompleted && onPromote && (
          <ActionButton
            onClick={onPromote}
            disabled={busy}
            variant="info"
            icon={<ArrowUpCircle className="w-3.5 h-3.5" />}
            label={t('downloads.page.promote')}
          />
        )}
        {!isGuest && isCompleted && onStopSeed && (
          <ActionButton
            onClick={onStopSeed}
            disabled={busy}
            variant="neutral"
            icon={<Pause className="w-3.5 h-3.5" />}
            label={t('downloads.page.stopSeed')}
          />
        )}
        {isCompleted && onOpenLocal && (
          <ActionButton
            onClick={onOpenLocal}
            disabled={busy}
            variant="info"
            icon={<Folder className="w-3.5 h-3.5" />}
            label={t('downloads.page.openLocal')}
            title={t('downloads.page.openLocalTitle')}
          />
        )}
        {onInspect && (
          <ActionButton
            onClick={onInspect}
            disabled={busy}
            variant="neutral"
            icon={<Info className="w-3.5 h-3.5" />}
            label={t('downloads.page.details')}
          />
        )}
        {!isGuest && (
          <ActionButton
            onClick={onDelete}
            disabled={busy}
            variant="danger"
            icon={<Trash2 className="w-3.5 h-3.5" />}
            label={isCompleted ? t('downloads.page.removeFromList') : t('downloads.page.cancel')}
            className="ml-auto"
          />
        )}
      </div>
    </div>
  )
})
