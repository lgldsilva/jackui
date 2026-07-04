import { useTranslation } from 'react-i18next'
import { Loader2, Pause, Play, Trash2, Clock, Users, ArrowDownCircle, ArrowUpCircle } from 'lucide-react'
import type { TorrentInfo, StreamPriority } from '../../api/client'
import { formatRate, formatBytesPair, formatDurationShort } from '../../lib/format'
import { KindBadge } from './KindBadge'
import { TorrentStatusBadge } from './TorrentStatusBadge'
import { ActionButton } from './ActionButton'

function computeTorrentETA(t: TorrentInfo): string {
  if (!t.totalSize || !t.downRate || t.downRate <= 0) return ''
  const done = (t.progress || 0) * t.totalSize
  const remaining = t.totalSize - done
  if (remaining <= 0) return ''
  const sec = remaining / t.downRate
  if (!Number.isFinite(sec) || sec <= 0) return ''
  return `~${formatDurationShort(sec)}`
}

type TorrentCardProps = {
  readonly t: TorrentInfo
  readonly busy: boolean
  readonly onPause: () => void
  readonly onResume: () => void
  readonly onPriority: (p: StreamPriority) => void
  readonly onDelete: () => void
  readonly onPlay?: () => void
}

// TorrentCard — Premium redesigned streaming torrent card.
export function TorrentCard({ t: torrent, busy, onPause, onResume, onPriority, onDelete, onPlay }: TorrentCardProps) {
  const { t } = useTranslation()
  const pct = Math.max(0, Math.min(1, torrent.progress || 0)) * 100
  const status = torrent.status || (pct >= 100 ? 'complete' : 'downloading')
  const isPaused = status === 'paused'
  const isSeeding = status === 'seeding'
  const isComplete = status === 'complete'

  const eta = computeTorrentETA(torrent)
  const priority: StreamPriority = (torrent.priority as StreamPriority) || 'normal'

  // Card border/glow color based on state
  let borderClass: string
  if (isSeeding) {
    borderClass = 'border-violet-500/30 hover:border-violet-500/50'
  } else if (isPaused) {
    borderClass = 'border-strong/50 hover:border-strong/60'
  } else if (isComplete) {
    borderClass = 'border-green-500/30 hover:border-green-500/50'
  } else {
    borderClass = 'border-emerald-500/30 hover:border-emerald-500/50'
  }

  // Gradient bar colors
  let barGradient: string
  if (isComplete) {
    barGradient = 'from-green-500 to-emerald-400'
  } else if (isPaused) {
    barGradient = 'from-gray-600 to-gray-500'
  } else if (isSeeding) {
    barGradient = 'from-violet-500 to-indigo-400'
  } else {
    barGradient = 'from-emerald-500 to-teal-400'
  }

  return (
    <div className={`
      relative overflow-hidden rounded-xl border ${borderClass}
      bg-card dark:bg-gradient-to-br dark:from-gray-800/80 dark:to-gray-900/60 backdrop-blur-sm
      p-4 flex flex-col gap-3 transition-all duration-300
    `}>
      {/* Top row: name + badges */}
      <div className="min-w-0">
        <h3 className="font-semibold text-text-primary text-sm leading-snug [overflow-wrap:anywhere]" title={torrent.name}>
          {torrent.name || torrent.infoHash}
        </h3>
        <div className="flex items-center gap-1.5 mt-1.5 flex-wrap">
          <KindBadge kind="streaming" />
          <TorrentStatusBadge status={status} />
        </div>
        <p className="text-[11px] text-text-muted truncate mt-0.5 font-mono" title={torrent.infoHash}>{torrent.infoHash}</p>
      </div>

      {/* Live rate chips — destaque pro Down/Up/Peers de CADA torrent. Eram
          mostrados em text-xs no rodapé, passavam batido (sintoma "não sei a
          velocidade de cada um"). Agora ficam em fonte maior + chip dedicado.
          Quando tudo zerado (ex.: pausado), só os peers ainda aparecem. */}
      <div className="flex items-center gap-2 flex-wrap text-sm">
        <span
          className={`flex items-center gap-1 px-2 py-0.5 rounded-full font-mono tabular-nums ${
            torrent.downRate > 0 ? 'bg-emerald-500/15 text-emerald-700 dark:text-emerald-300 border border-emerald-500/30' : 'text-text-muted'
          }`}
          title={t('downloads.page.torrentDownSpeed')}
        >
          <ArrowDownCircle className="w-3.5 h-3.5" />
          {formatRate(torrent.downRate)}
        </span>
        <span
          className={`flex items-center gap-1 px-2 py-0.5 rounded-full font-mono tabular-nums ${
            torrent.upRate > 0 ? 'bg-violet-500/15 text-violet-700 dark:text-violet-300 border border-violet-500/30' : 'text-text-muted'
          }`}
          title={t('downloads.page.torrentUpSpeed')}
        >
          <ArrowUpCircle className="w-3.5 h-3.5" />
          {formatRate(torrent.upRate)}
        </span>
        <span
          className="flex items-center gap-1 px-2 py-0.5 rounded-full bg-blue-500/10 text-blue-700 dark:text-blue-300 border border-blue-500/20 font-mono tabular-nums"
          title={t('downloads.page.peersSeedersSwarm')}
        >
          <Users className="w-3.5 h-3.5" />
          {torrent.peers}{(torrent.seeders ?? 0) > 0 && <span className="text-text-muted"> / {torrent.seeders}</span>}
        </span>
      </div>

      {/* Progress bar */}
      <div>
        <div className="h-2 bg-surface-tertiary dark:bg-surface/80 rounded-full overflow-hidden">
          <div
            className={`h-full rounded-full bg-gradient-to-r ${barGradient} transition-all duration-500 ease-out`}
            style={{ width: `${pct.toFixed(1)}%` }}
          />
        </div>
        {/* Stats row — bytes/% + ETA. Velocidades subiram para os chips acima. */}
        <div className="flex items-center justify-between mt-2 text-xs text-text-secondary gap-3 flex-wrap">
          <span className="text-text-primary font-medium">
            {formatBytesPair(Math.round((torrent.totalSize || 0) * (torrent.progress || 0)), torrent.totalSize)}
            <span className="text-text-muted ml-1">({pct.toFixed(1)}%)</span>
          </span>
          {eta && (
            <span className="flex items-center gap-1 text-text-muted" title="ETA">
              <Clock className="w-3 h-3" /> {eta}
            </span>
          )}
        </div>
      </div>

      {/* Action bar */}
      <div className="flex items-center gap-2 flex-wrap pt-1">
        {/* Ver arquivos / tocar: abre o player pelo info_hash (resolve o arquivo
            principal e lista os demais). Disponível assim que há algo no cache —
            inclusive quando completo/semeando, que antes só tinha pausar/parar. */}
        {onPlay && torrent.progress > 0 && (
          <ActionButton onClick={onPlay} disabled={busy} variant="success" icon={<Play className="w-3.5 h-3.5 fill-current" />} label={t('downloads.page.viewFiles')} title={t('downloads.page.viewFilesTitle')} />
        )}
        {isPaused ? (
          <ActionButton onClick={onResume} disabled={busy} variant="success" icon={<Play className="w-3.5 h-3.5" />} label={t('downloads.page.resumeAction')} title={t('downloads.page.resumeTorrentTitle')} />
        ) : (
          <ActionButton onClick={onPause} disabled={busy || isComplete} variant="neutral" icon={<Pause className="w-3.5 h-3.5" />} label={t('downloads.page.pause')} title={t('downloads.page.pauseTorrentTitle')} />
        )}

        <label className="flex items-center gap-1.5 text-xs text-text-secondary">
          <span className="text-text-muted">{t('downloads.page.priority')}</span>
          <select
            value={priority}
            onChange={e => onPriority(e.target.value as StreamPriority)}
            disabled={busy}
            className="bg-surface-secondary border border-default rounded-lg px-2 py-1 text-text-primary text-xs disabled:opacity-50 focus:outline-none focus:border-emerald-500 transition-colors cursor-pointer"
          >
            <option value="low">{t('downloads.page.priorityLow')}</option>
            <option value="normal">{t('downloads.page.priorityNormal')}</option>
            <option value="high">{t('downloads.page.priorityHigh')}</option>
          </select>
        </label>

        {busy && <Loader2 className="w-3.5 h-3.5 animate-spin text-text-muted" />}
        <ActionButton onClick={onDelete} disabled={busy} variant="danger" icon={<Trash2 className="w-3.5 h-3.5" />} label={t('downloads.page.stop')} title={t('downloads.page.stopStreamingTitle')} className="ml-auto" />
      </div>
    </div>
  )
}
