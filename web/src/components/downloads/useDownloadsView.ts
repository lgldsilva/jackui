import { useMemo } from 'react'
import { useTranslation } from 'react-i18next'
import { DownloadEntry, TorrentInfo } from '../../api/client'
import { applyDownloadSort } from '../../lib/downloadSort'
import { countTorrents, completedViewCounts } from '../../lib/downloadGroups'
import type { CompletedFilterKey } from './CompletedFilterChips'
import type { Tab, TabDownloads, TabTorrents } from './tabs'

// useDownloadsView — derives every read-only view model the page renders from the
// raw download rows + live torrents: per-status groups (memoized), summary
// totals, tab badge counts, per-tab lists and the completed-view filter counts.
// Pure computation — no mutations live here.
export function useDownloadsView(params: {
  readonly items: DownloadEntry[]
  readonly torrents: TorrentInfo[]
  readonly sortCol: string
  readonly sortDir: string
  readonly maxActive: number
  readonly activeTab: Tab
  readonly completedFilter: CompletedFilterKey
}) {
  const { items, torrents, sortCol, sortDir, maxActive, activeTab, completedFilter } = params
  const { t } = useTranslation()

  // Esconde o card de STREAMING quando existe QUALQUER download row pro mesmo
  // hash — incluindo `completed`. Antes só filtrávamos `downloading|queued`, e
  // ao terminar o download a streaming card voltava a aparecer ao lado da
  // download card (ambas dizendo 4GB/4GB) — duplicata óbvia. Agora a download
  // row é a fonte canônica e a streaming card só aparece pra torrents que NÃO
  // foram enfileirados como background download (puro stream).
  const bgHashes = new Set(items.map(d => d.infoHash))
  const displayTorrents = torrents.filter(t => !bgHashes.has(t.infoHash))

  // Torrent status helpers
  const torrentStatus = (t: TorrentInfo) =>
    t.status || ((t.progress || 0) >= 1 ? 'complete' : 'downloading')

  const activeTorrents = displayTorrents.filter(t => {
    const s = torrentStatus(t)
    return s === 'downloading' || s === 'paused'
  })
  const seedingTorrents = displayTorrents.filter(t => {
    const s = torrentStatus(t)
    return s === 'seeding' || s === 'complete'
  })

  // Ordenação por métrica AO VIVO (velocidade ↓/↑, seeds) é client-side: esses
  // valores não são persistidos, então o backend não os ordena (ORDER BY). As
  // demais chaves (data/nome/...) seguem server-side; aqui a ordem é preservada.
  // As seções/grupos derivam de sortedItems para herdar a ordem escolhida.
  const sortedItems = useMemo(
    () => applyDownloadSort(items, sortCol, sortDir),
    [items, sortCol, sortDir],
  )

  // Per-status download groups (memoized — poll every 2s would otherwise rebuild)
  const downloadsByStatus = useMemo(() => ({
    downloading: sortedItems.filter(d => d.status === 'downloading' || d.status === 'queued'),
    paused:      sortedItems.filter(d => d.status === 'paused'),
    completed:   sortedItems.filter(d => d.status === 'completed'),
    failed:      sortedItems.filter(d => d.status === 'failed'),
  }), [sortedItems])
  const completedDownloads = downloadsByStatus.completed

  const queuedDownloads = items.filter(d => d.status === 'queued')

  // Stalled: downloading but no progress (downRate === 0 or null)
  const stalledCount = items.filter(
    d => d.status === 'downloading' && (d.downRate ?? 0) === 0 && d.bytesDownloaded < d.fileSize
  ).length

  // Summary stats: Calculated solely from the active torrents list (`torrents`).
  // Since `items` contains individual file rows of the same torrent (which all
  // share the torrent's aggregate down/up rate and peers), summing from `items` or
  // mixing both would cause double-counting. Using `torrents` ensures each active
  // torrent is counted exactly once.
  const totalDown = torrents.reduce((sum, t) => sum + (t.downRate || 0), 0)
  const totalUp = torrents.reduce((sum, t) => sum + (t.upRate || 0), 0)
  const totalPeers = torrents.reduce((sum, t) => sum + (t.peers || 0), 0)
  // Counts are by TORRENT, not by file row (a 778-file pack is 1 torrent) — see
  // countTorrents. Keeps the badges/indicators aligned with the grouped cards.
  const activeCount = activeTorrents.length + countTorrents(downloadsByStatus.downloading)
  const seedingCount = seedingTorrents.length
  // Background downloads actually running vs waiting (downloadsByStatus.downloading
  // groups both for tab counts, so split them here for the "X/N active" indicator).
  const downloadingNowCount = countTorrents(items.filter(d => d.status === 'downloading'))
  const queuedCount = countTorrents(items.filter(d => d.status === 'queued'))
  let queueSubtitle: string | undefined
  if (queuedCount > 0) queueSubtitle = t('downloads.page.queuedCount', { count: queuedCount })
  else if (seedingCount > 0) queueSubtitle = t('downloads.page.seedingCount', { count: seedingCount })
  const activeValue = maxActive > 0
    ? t('downloads.page.activeOfMax', { current: downloadingNowCount, max: maxActive })
    : t('downloads.page.activeCount', { count: activeCount })

  // Tab badge counts
  const tabCounts: Record<Tab, number> = {
    all:         displayTorrents.length + countTorrents(items),
    downloading: activeTorrents.length + countTorrents(downloadsByStatus.downloading),
    paused:      countTorrents(downloadsByStatus.paused),
    completed:   seedingTorrents.length + countTorrents(completedDownloads),
    failed:      countTorrents(downloadsByStatus.failed),
    network:     0,
  }

  // Items for the currently-selected status tab
  const tabDownloads: TabDownloads = {
    all:         sortedItems,
    downloading: [...downloadsByStatus.downloading, ...downloadsByStatus.paused, ...downloadsByStatus.failed],
    paused:      downloadsByStatus.paused,
    completed:   completedDownloads,
    failed:      downloadsByStatus.failed,
    network:     [],
  }
  const tabTorrents: TabTorrents = {
    all:         displayTorrents,
    downloading: activeTorrents,
    paused:      [],
    completed:   seedingTorrents,
    failed:      [],
    network:     [],
  }

  // Counts for the completed-view filter chips, computed at page level (so the
  // chips can sit at the TOP, above the active cards). Seeding = live torrents
  // not yet on disk + completed groups still seeding; on-disk = completed groups
  // whose torrent is no longer live.
  const { seeding: seedingCountForTab, onDisk: onDiskCountForTab } =
    completedViewCounts(tabDownloads[activeTab], tabTorrents[activeTab])
  const hasCompletedForTab = seedingCountForTab > 0 || onDiskCountForTab > 0
  const effectiveCompletedFilter: CompletedFilterKey = hasCompletedForTab ? completedFilter : 'all'

  return {
    downloadsByStatus,
    completedDownloads,
    queuedDownloads,
    stalledCount,
    totalDown,
    totalUp,
    totalPeers,
    queueSubtitle,
    activeValue,
    tabCounts,
    tabDownloads,
    tabTorrents,
    seedingCountForTab,
    onDiskCountForTab,
    hasCompletedForTab,
    effectiveCompletedFilter,
  }
}
