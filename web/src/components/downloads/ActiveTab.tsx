import { useTranslation } from 'react-i18next'
import { Loader2, Zap } from 'lucide-react'
import type { TorrentInfo, DownloadEntry, StreamPriority } from '../../api/client'
import { EmptyState } from '../EmptyState'
import { TorrentCard } from './TorrentCard'
import { DownloadCard } from './DownloadCard'

// ActiveTab — downloading/queued torrents + background downloads.
export function ActiveTab({ torrents, downloads, torrentsLoaded, loading, busyHash, busyID,
  onTorrentPause, onTorrentResume, onTorrentPriority, onTorrentDelete, onTorrentPlay,
  onPause, onResume, onDelete, onPlay, onInspect, openLocalFor,
}: {
  readonly torrents: TorrentInfo[]
  readonly downloads: DownloadEntry[]
  readonly torrentsLoaded: boolean
  readonly loading: boolean
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
  readonly onPlay: (d: DownloadEntry) => void
  readonly onInspect: (d: DownloadEntry) => void
  readonly openLocalFor: (d: DownloadEntry) => (() => void) | undefined
}) {
  const { t } = useTranslation()
  const empty = torrents.length === 0 && downloads.length === 0 && torrentsLoaded && !loading
  const isLoading = (!torrentsLoaded || (loading && downloads.length === 0)) && torrents.length === 0 && downloads.length === 0

  return (
    <div className="flex flex-col gap-4">
      {isLoading && (
        <div className="flex items-center gap-2 text-text-secondary py-12 justify-center">
          <Loader2 className="w-5 h-5 animate-spin" />
          <span className="text-sm">{t('downloads.page.loading')}</span>
        </div>
      )}

      {empty && (
        <EmptyState
          icon={<Zap className="w-12 h-12" />}
          title={t('downloads.page.noActiveTransfer')}
          description={t('downloads.page.noActiveTransferDesc')}
        />
      )}

      {/* Streaming torrents */}
      {torrents.map(t => (
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

      {/* Background downloads */}
      {downloads.map(d => (
        <DownloadCard
          key={d.id}
          d={d}
          live={torrents.find(t => t.infoHash === d.infoHash)}
          busy={busyID === d.id}
          onPause={() => onPause(d.id)}
          onResume={() => onResume(d.id)}
          onDelete={() => onDelete(d.id)}
          onPlay={() => onPlay(d)}
          onInspect={() => onInspect(d)}
          onOpenLocal={openLocalFor(d)}
        />
      ))}
    </div>
  )
}
