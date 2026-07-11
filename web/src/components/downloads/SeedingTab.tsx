import { useState } from 'react'
import { useTranslation } from 'react-i18next'
import { Loader2, Pause, Clock, ArrowUpCircle, HardDrive } from 'lucide-react'
import type { TorrentInfo, DownloadEntry, DownloadPriority, StreamPriority } from '../../api/client'
import { groupByHash, groupCompleted, type CompletedGroup } from '../../lib/downloadGroups'
import { type CompletedFilterKey } from './CompletedFilterChips'
import { GroupHeader } from './GroupHeader'
import { EmptyState } from '../EmptyState'
import { TorrentCard } from './TorrentCard'
import { DownloadCard } from './DownloadCard'
import { DownloadGroupCard, CompletedGroupActions, ActiveGroupActions } from './DownloadGroupCard'

// SeedingTab — seeding/complete torrents + completed downloads.
// Sub-divided by lifecycle: Baixando agora / Na fila / Semeando / No disco / Pausados.
export function SeedingTab({ torrents, downloads, completedFilter, torrentsLoaded, busyHash, busyID,
  onTorrentPause, onTorrentResume, onTorrentPriority, onTorrentDelete, onTorrentPlay,
  onPause, onResume, onDelete, onPromote, onStopSeed, onSetPriority,
  onPromoteMany, onDeleteMany, onStopSeedMany, onRetryMany,
  selected, onToggleSelected, onPlay, onInspect, openLocalFor, loading,
}: {
  readonly torrents: TorrentInfo[]
  readonly downloads: DownloadEntry[]
  readonly completedFilter: CompletedFilterKey
  readonly torrentsLoaded: boolean
  readonly busyHash: string | null
  readonly busyID: number | null
  readonly onTorrentPause: (h: string) => void
  readonly onTorrentResume: (h: string) => void
  readonly onTorrentPriority: (h: string, p: StreamPriority) => void
  readonly onTorrentDelete: (h: string) => void
  readonly onTorrentPlay: (t: TorrentInfo) => void
  readonly onPause: (id: number) => void
  readonly onResume: (id: number) => void
  readonly onDelete: (id: number) => void
  readonly onPromote: (d: DownloadEntry) => void
  readonly onStopSeed: (id: number, name: string) => void
  readonly onPromoteMany: (ds: DownloadEntry[]) => void
  readonly onDeleteMany: (ds: DownloadEntry[]) => void
  readonly onStopSeedMany: (ds: DownloadEntry[]) => void
  readonly onRetryMany: (ds: DownloadEntry[]) => void
  readonly onSetPriority: (id: number, p: DownloadPriority) => void
  readonly selected: Set<number>
  readonly onToggleSelected: (id: number) => void
  readonly onPlay: (d: DownloadEntry) => void
  readonly onInspect: (d: DownloadEntry) => void
  readonly openLocalFor: (d: DownloadEntry) => (() => void) | undefined
  readonly loading?: boolean
}) {
  const { t } = useTranslation()
  const [expandedGroups, setExpandedGroups] = useState<Set<string>>(new Set())
  const toggleGroup = (key: string) => setExpandedGroups(prev => {
    const next = new Set(prev)
    if (next.has(key)) next.delete(key); else next.add(key)
    return next
  })
  const empty = torrents.length === 0 && downloads.length === 0 && !loading

  // Quantos downloads compartilham cada infoHash → distingue torrent multi-arquivo
  // (>1) de single-file. Vale pra TODAS as seções (baixando/fila/concluídos/…),
  // então o arquivo "que ficou sem baixar" também aparece com o nome do episódio.
  const dlCountByHash = new Map<string, number>()
  for (const d of downloads) {
    if (d.infoHash) dlCountByHash.set(d.infoHash, (dlCountByHash.get(d.infoHash) ?? 0) + 1)
  }

  const renderDownloadCard = (d: DownloadEntry) => (
    <DownloadCard
      key={d.id}
      d={d}
      live={torrents.find(t => t.infoHash === d.infoHash)}
      busy={busyID === d.id}
      selected={selected.has(d.id)}
      multiFile={!!d.infoHash && (dlCountByHash.get(d.infoHash) ?? 0) > 1}
      onToggleSelected={() => onToggleSelected(d.id)}
      onPause={() => onPause?.(d.id)}
      onResume={() => onResume?.(d.id)}
      onDelete={() => onDelete(d.id)}
      onPromote={() => onPromote(d)}
      onStopSeed={() => onStopSeed(d.id, d.name || d.filePath)}
      onPlay={() => onPlay(d)}
      onInspect={() => onInspect(d)}
      onSetPriority={(p) => onSetPriority(d.id, p)}
      onOpenLocal={openLocalFor(d)}
    />
  )

  // groupShell wraps a multi-file group in the collapsible card; a group of one
  // (single-file OR whole-torrent -2) renders its lone card with no wrapper, so
  // the torrent stays the unit without doubling chrome.
  const groupShell = (g: CompletedGroup, actions: React.ReactNode) => {
    if (g.files.length === 1) return renderDownloadCard(g.files[0])
    return (
      <DownloadGroupCard
        key={g.key}
        group={g}
        expanded={expandedGroups.has(g.key)}
        onToggle={() => toggleGroup(g.key)}
        actions={actions}
        renderFile={renderDownloadCard}
      />
    )
  }

  // Completed/seeding group: promote / stop-seed (only while live) / remove all.
  const renderCompletedGroup = (g: CompletedGroup) => groupShell(g, (
    <CompletedGroupActions
      onPromote={() => onPromoteMany(g.files)}
      onStopSeed={g.seeding ? () => onStopSeedMany(g.files) : undefined}
      onDelete={() => onDeleteMany(g.files)}
      busy={g.files.some(f => busyID === f.id)}
    />
  ))

  // In-progress group (Baixando/Fila/Pausados/Erro): pause/resume/retry the whole
  // torrent; remove drops every file row. onRetryFailed appears only when some files
  // are in the failed state — batch-requeues them in one request.
  const renderActiveGroup = (g: CompletedGroup) => {
    const allPaused = g.files.every(f => f.status === 'paused')
    const hasFailed = g.files.some(f => f.status === 'failed')
    return groupShell(g, (
      <ActiveGroupActions
        paused={allPaused}
        onPause={() => g.files.forEach(f => onPause(f.id))}
        onResume={() => g.files.forEach(f => onResume(f.id))}
        onRetryFailed={hasFailed ? () => onRetryMany(g.files) : undefined}
        onDelete={() => onDeleteMany(g.files)}
        busy={g.files.some(f => busyID === f.id)}
      />
    ))
  }

  // Group downloads by lifecycle so the list is legible. Completed files are
  // grouped per torrent (infoHash) and split into Seeding (live) vs On-disk.
  // Streaming-only torrents (no download row) keep their TorrentCard. Headers
  // show only when more than one group is non-empty.
  // Each lifecycle section is grouped per torrent (infoHash): a multi-file torrent
  // is ONE card, a single-file / whole-torrent (-2) item a card of one. Counts and
  // headers count GROUPS (torrents), not file rows.
  const downloadingGroups = groupByHash(downloads.filter(d => d.status === 'downloading'), torrents)
  const queuedGroups = groupByHash(downloads.filter(d => d.status === 'queued'), torrents)
  const otherGroups = groupByHash(downloads.filter(d => d.status === 'paused' || d.status === 'failed'), torrents)
  const completed = downloads.filter(d => d.status === 'completed')
  const completedGroups = groupCompleted(completed, torrents)
  const seedingGroups = completedGroups.filter(g => g.seeding)
  const onDiskGroups = completedGroups.filter(g => !g.seeding)
  // Streaming torrents that aren't backed by a completed download row.
  const streamingOnly = torrents.filter(t => !completed.some(d => d.infoHash === t.infoHash))
  const seedingCount = streamingOnly.length + seedingGroups.length

  const sectionCount = [downloadingGroups.length, queuedGroups.length, seedingCount, onDiskGroups.length, otherGroups.length]
    .filter(n => n > 0).length
  const showHeaders = sectionCount > 1
  // Filtro Semeando/No disco: 'seeding' isola os que estão semeando ao vivo,
  // 'ondisk' os parados no disco, 'all' mostra tudo (incluindo baixando/fila/pausados).
  const hasCompleted = seedingCount > 0 || onDiskGroups.length > 0
  // Sem concluídos, o filtro não se aplica (não esconde baixando/fila/pausados).
  const cf = hasCompleted ? completedFilter : 'all'
  const showSeeding = cf !== 'ondisk'
  const showOnDisk = cf !== 'seeding'
  const showOthers = cf === 'all'

  return (
    <div className="flex flex-col gap-4">
      {!torrentsLoaded && (
        <div className="flex items-center gap-2 text-text-secondary py-12 justify-center">
          <Loader2 className="w-5 h-5 animate-spin" />
          <span className="text-sm">{t('downloads.page.loading')}</span>
        </div>
      )}

      {torrentsLoaded && empty && (
        <EmptyState
          icon={<ArrowUpCircle className="w-12 h-12" />}
          title={t('downloads.page.nothingSeedingOrComplete')}
          description={t('downloads.page.nothingSeedingOrCompleteDesc')}
        />
      )}

      {/* Baixando agora */}
      {showOthers && downloadingGroups.length > 0 && (
        <>
          {showHeaders && <GroupHeader icon={<Loader2 className="w-3.5 h-3.5" />} label={t('downloads.page.sectionDownloadingNow', { count: downloadingGroups.length })} color="text-cyan-400" />}
          {downloadingGroups.map(renderActiveGroup)}
        </>
      )}

      {/* Na fila */}
      {showOthers && queuedGroups.length > 0 && (
        <>
          {showHeaders && <GroupHeader icon={<Clock className="w-3.5 h-3.5" />} label={t('downloads.page.sectionQueued', { count: queuedGroups.length })} color="text-text-muted" />}
          {queuedGroups.map(renderActiveGroup)}
        </>
      )}

      {/* Semeando (torrents de streaming + grupos completed com torrent live) */}
      {showSeeding && seedingCount > 0 && (
        <>
          {showHeaders && <GroupHeader icon={<ArrowUpCircle className="w-3.5 h-3.5" />} label={t('downloads.page.seeding')} color="text-emerald-400" />}
          {streamingOnly.map(t => (
            <TorrentCard
              key={t.infoHash}
              t={t}
              busy={busyHash === t.infoHash}
              onPause={() => onTorrentPause(t.infoHash)}
              onResume={() => onTorrentResume(t.infoHash)}
              onPriority={(p) => onTorrentPriority(t.infoHash, p)}
              onDelete={() => onTorrentDelete(t.infoHash)}
              onPlay={() => onTorrentPlay(t)}
            />
          ))}
          {seedingGroups.map(renderCompletedGroup)}
        </>
      )}

      {/* No disco (concluído, seed parado) */}
      {showOnDisk && onDiskGroups.length > 0 && (
        <>
          {showHeaders && <GroupHeader icon={<HardDrive className="w-3.5 h-3.5" />} label={t('downloads.page.onDisk')} color="text-text-muted" />}
          {onDiskGroups.map(renderCompletedGroup)}
        </>
      )}

      {/* Pausados / falhos */}
      {showOthers && otherGroups.length > 0 && (
        <>
          {showHeaders && <GroupHeader icon={<Pause className="w-3.5 h-3.5" />} label={t('downloads.page.sectionPausedError', { count: otherGroups.length })} color="text-text-muted" />}
          {otherGroups.map(renderActiveGroup)}
        </>
      )}
    </div>
  )
}
