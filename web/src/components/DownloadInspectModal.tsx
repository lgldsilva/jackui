import { useState, useEffect, useCallback } from 'react'
import { useTranslation } from 'react-i18next'
import {
  Info, Files, Copy, Check, RefreshCw,
  Loader2, AlertCircle, Trash2, Square,
  ArrowUpCircle, Activity, Globe, Play, Share2, Users,
} from 'lucide-react'
import PeersTab from './downloadInspect/PeersTab'
import { fileIcon, renderFilesTab } from './downloadInspect/FilesTab'
import {
  DownloadEntry, DownloadDetails, StreamFile, TorrentInfo, DownloadSource,
  downloadDetails, downloadRecheck, downloadDelete, downloadStopSeed, downloadSources, downloadCreate,
} from '../api/client'
import { formatBytes, formatRate, formatBytesPair } from '../lib/format'
import { errMessage } from '../lib/errMessage'
import { Sheet } from './Sheet'
import { useConfirm } from './ConfirmDialog'
import { useToast } from './Toast'

type Props = {
  readonly download: DownloadEntry | null
  readonly onClose: () => void
  readonly onMutated?: (d: DownloadEntry) => void
  readonly onDeleted?: (id: number) => void
  readonly onPromote?: (d: DownloadEntry) => void
  readonly onPlay?: (d: DownloadEntry) => void
  /** Outros registros de download do MESMO torrent (mesmo info_hash). Usado pra
      saber quais arquivos do torrent JÁ são download e quais estão só em
      streaming (sem registro) → estes ganham botão "Baixar". */
  readonly siblings?: readonly DownloadEntry[]
  /** Chamado após adotar um arquivo só-streaming como download (pra recarregar). */
  readonly onAdopted?: () => void
}

type Tab = 'overview' | 'files' | 'trackers' | 'peers' | 'sources' | 'actions'

// Minimal translate-fn signature so the module-level render helpers can receive
// the component's `t` without pulling the full i18next type surface.
type TFn = (key: string, opts?: Record<string, unknown>) => string

// sourceStatusBadge maps a source's lifecycle to a label + color for the list.
function sourceStatusBadge(status: DownloadSource['status'], t: TFn): { label: string; cls: string } {
  switch (status) {
    case 'active': return { label: t('downloads.inspect.sourceStatus.active'), cls: 'bg-emerald-500/15 text-emerald-700 dark:text-emerald-300 border-emerald-500/30' }
    case 'cooldown': return { label: t('downloads.inspect.sourceStatus.cooldown'), cls: 'bg-amber-500/15 text-amber-700 dark:text-amber-300 border-amber-500/30' }
    case 'failed': return { label: t('downloads.inspect.sourceStatus.failed'), cls: 'bg-red-500/15 text-red-700 dark:text-red-300 border-red-500/30' }
    default: return { label: t('downloads.inspect.sourceStatus.candidate'), cls: 'bg-gray-600/30 text-text-primary border-strong/50' }
  }
}

function renderSourcesTab(sources: DownloadSource[], loading: boolean, t: TFn): React.ReactNode {
  if (loading) {
    return <div className="flex items-center gap-2 text-text-secondary py-8 justify-center"><Loader2 className="w-4 h-4 animate-spin" />{t('downloads.inspect.sourcesLoading')}</div>
  }
  if (sources.length === 0) {
    return (
      <p className="text-sm text-text-muted py-6 text-center">
        {t('downloads.inspect.sourcesEmpty')}
      </p>
    )
  }
  return (
    <ul className="flex flex-col gap-2">
      {sources.map((s) => {
        const badge = sourceStatusBadge(s.status, t)
        return (
          <li key={s.id} className="flex items-center gap-3 bg-surface/60 rounded-lg px-3 py-2">
            <span className={`text-[10px] px-1.5 py-0.5 rounded-md border font-medium whitespace-nowrap ${badge.cls}`}>{badge.label}</span>
            <div className="min-w-0 flex-1">
              <div className="text-sm text-text-primary truncate" title={s.title}>{s.title || s.infoHash}</div>
              <div className="text-[11px] text-text-muted flex items-center gap-2 flex-wrap">
                <span className="flex items-center gap-1"><Globe className="w-3 h-3" />{s.tracker || '—'}</span>
                <span className="text-green-400">{t('downloads.inspect.sourceSeed', { count: s.seeders })}</span>
                {s.tries > 0 && <span>{t('downloads.inspect.sourceTries', { count: s.tries })}</span>}
              </div>
            </div>
          </li>
        )
      })}
    </ul>
  )
}

function filesTabLabel(torrent: TorrentInfo | null | undefined, t: TFn): string {
  if (!torrent) return t('downloads.inspect.tabFiles')
  return t('downloads.inspect.tabFilesCount', { count: torrent.files.length })
}

function trackersTabLabel(trackers: readonly string[], t: TFn): string {
  if (trackers.length === 0) return t('downloads.inspect.tabTrackers')
  return t('downloads.inspect.tabTrackersCount', { count: trackers.length })
}

export default function DownloadInspectModal({ download, onClose, onMutated, onDeleted, onPromote, onPlay, siblings, onAdopted }: Props) {
  const { t } = useTranslation()
  const confirm = useConfirm()
  const { notify } = useToast()
  const [tab, setTab] = useState<Tab>('overview')
  const [details, setDetails] = useState<DownloadDetails | null>(null)
  const [loading, setLoading] = useState(false)
  const [error, setError] = useState('')
  const [copiedMagnet, setCopiedMagnet] = useState(false)
  const [busy, setBusy] = useState<string | null>(null) // 'recheck' | 'delete' | 'stopSeed'
  const [adopting, setAdopting] = useState<number | null>(null) // fileIndex sendo adotado
  const [sources, setSources] = useState<DownloadSource[]>([])
  const [loadingSources, setLoadingSources] = useState(false)

  const refresh = useCallback(async () => {
    if (!download) return
    setLoading(true)
    setError('')
    try {
      const d = await downloadDetails(download.id)
      setDetails(d)
    } catch (e: unknown) {
      setError(errMessage(e) || t('downloads.inspect.errorDetails'))
    } finally {
      setLoading(false)
    }
    // Key on the id, not the object: the parent now derives `download` from the 2s
    // poll, so its reference changes every tick — depending on `download` would
    // re-fetch details/sources every 2s. The id is what actually identifies the row.
  }, [download?.id, t])

  useEffect(() => {
    if (!download) {
      setDetails(null)
      setTab('overview')
      setSources([])
      return
    }
    refresh()
  }, [download?.id, refresh])

  // Lazily load the source catalog the first time the Fontes tab is opened.
  useEffect(() => {
    if (tab !== 'sources' || !download) return
    setLoadingSources(true)
    downloadSources(download.id)
      .then(setSources)
      .catch(() => setSources([]))
      .finally(() => setLoadingSources(false))
  }, [tab, download?.id])

  if (!download) return null

  const d = details?.download ?? download
  const torrent = details?.torrent
  const fileStat = details?.file

  // Extract tracker URLs from the magnet URI as a client-side fallback.
  const magnetQ = d.magnet ?? ''
  const magnetTrackers: string[] = magnetQ
    ? (() => {
        try {
          const q = magnetQ.includes('?') ? magnetQ.split('?')[1] : magnetQ
          return new URLSearchParams(q).getAll('tr')
        } catch { return [] as string[] }
      })()
    : []

  const displayTrackers: string[] = (torrent?.trackers?.length ?? 0) > 0
    ? (torrent?.trackers ?? [])
    : magnetTrackers


  const copyMagnet = async () => {
    if (!d.magnet) return
    try {
      await navigator.clipboard.writeText(d.magnet)
      setCopiedMagnet(true)
      setTimeout(() => setCopiedMagnet(false), 1500)
    } catch {}
  }

  // adopt cria um registro de download pra um arquivo que estava só em streaming.
  // O worker anexa ao torrent já ativo, reaproveita os pedaços em cache e — ao
  // completar — move o arquivo pro diretório de downloads. Se já estava 100% no
  // cache, conclui quase instantâneo e vai pros "baixados".
  const adopt = async (f: StreamFile) => {
    if (adopting !== null) return
    setAdopting(f.index)
    setError('')
    try {
      await downloadCreate({
        infoHash: d.infoHash,
        fileIndex: f.index,
        magnet: d.magnet,
        name: d.name,
        filePath: f.path,
        fileSize: f.size,
        tracker: d.tracker,
        category: d.category,
      })
      onAdopted?.()
      await refresh()
    } catch (e: unknown) {
      setError(errMessage(e) || t('downloads.inspect.errorAdopt'))
    } finally {
      setAdopting(null)
    }
  }

  const handleRecheck = async () => {
    if (busy) return
    setBusy('recheck')
    setError('')
    try {
      const updated = await downloadRecheck(d.id)
      onMutated?.(updated)
      // re-busca details pra refletir reset de bytes
      await refresh()
    } catch (e: unknown) {
      setError(errMessage(e) || t('downloads.inspect.errorRecheck'))
    } finally {
      setBusy(null)
    }
  }

  const handleStopSeed = async () => {
    if (busy) return
    setBusy('stopSeed')
    setError('')
    try {
      await downloadStopSeed(d.id)
      onMutated?.(d)
      await refresh()
    } catch (e: unknown) {
      setError(errMessage(e) || t('downloads.inspect.errorStopSeed'))
    } finally {
      setBusy(null)
    }
  }

  const handleDelete = async () => {
    if (busy) return
    const ok = await confirm({ title: t('downloads.inspect.deleteTitle'), message: t('downloads.inspect.deleteMessage', { name: d.name }), confirmLabel: t('downloads.inspect.deleteConfirm'), destructive: true })
    if (!ok) return
    setBusy('delete')
    setError('')
    try {
      await downloadDelete(d.id)
      onDeleted?.(d.id)
      onClose()
    } catch (e: unknown) {
      setError(errMessage(e) || t('downloads.inspect.errorDelete'))
      setBusy(null)
    }
  }

  const sparseInfo = fileStat?.exists && fileStat.apparent > 0
    ? Math.abs(fileStat.apparent - fileStat.onDisk) / fileStat.apparent > 0.1
    : false

  // When the torrent is not active (dropped post-completion) we can still show
  // the primary file from the download row itself.
  const syntheticFile: StreamFile | null = !torrent && d.fileSize > 0 ? {
    index: d.fileIndex,
    path: d.filePath ? d.filePath.split('/').pop() ?? d.name : d.name,
    size: d.fileSize,
    isVideo: /\.(mp4|mkv|avi|mov|webm|m4v|wmv|ts|m2ts)$/i.test(d.name),
    downloaded: d.bytesDownloaded,
    progress: d.fileSize > 0 ? d.bytesDownloaded / d.fileSize : 0,
    priority: 'normal',
  } : null

  const filesTabContent = renderFilesTab(torrent, syntheticFile, d.filePath, d.fileIndex, fileIcon, siblings ?? [], adopting, adopt, t)

  return (
    <Sheet
      open
      onClose={onClose}
      size="2xl"
      title={<span className="truncate" title={d.name}>{d.name}</span>}
      icon={<Info className="w-4 h-4 text-cyan-400 flex-shrink-0" />}
    >
        {/* Tabs — cola no topo do corpo (compensa o p-4 do Sheet) */}
        <div className="-mx-4 -mt-4 mb-4 flex border-b border-default px-2 bg-surface/40">
          {[
            { id: 'overview' as Tab, label: t('downloads.inspect.tabOverview'), icon: Info },
            { id: 'files' as Tab, label: filesTabLabel(torrent, t), icon: Files },
            { id: 'trackers' as Tab, label: trackersTabLabel(displayTrackers, t), icon: Globe },
            { id: 'peers' as Tab, label: t('downloads.inspect.tabPeers'), icon: Users },
            { id: 'sources' as Tab, label: t('downloads.inspect.tabSources'), icon: Share2 },
            { id: 'actions' as Tab, label: t('downloads.inspect.tabActions'), icon: Activity },
          ].map(({ id, label, icon: Icon }) => (
            <button
              key={id}
              onClick={() => setTab(id)}
              className={`flex items-center gap-1.5 px-3 py-2 text-sm border-b-2 -mb-px transition-colors ${
                tab === id
                  ? 'border-cyan-400 text-cyan-700 dark:text-cyan-300'
                  : 'border-transparent text-text-secondary hover:text-text-primary'
              }`}
            >
              <Icon className="w-3.5 h-3.5" />
              {label}
            </button>
          ))}
        </div>

        <div className="text-sm">
          {loading && !details && (
            <div className="flex items-center justify-center py-8 text-text-muted">
              <Loader2 className="w-5 h-5 animate-spin" />
            </div>
          )}
          {error && (
            <div className="mb-3 flex items-start gap-2 bg-red-500/10 border border-red-500/30 text-red-400 rounded px-3 py-2 text-xs">
              <AlertCircle className="w-4 h-4 mt-0.5 flex-shrink-0" />
              <span>{error}</span>
            </div>
          )}

          {tab === 'overview' && (
            <div className="space-y-3">
              <Field label={t('downloads.inspect.fieldStatus')}>
                <StatusPill status={d.status} />
                {d.error && <span className="text-red-400 text-xs ml-2">{d.error}</span>}
              </Field>
              <Field label="info_hash">
                <code className="text-xs text-text-primary font-mono break-all">{d.infoHash}</code>
              </Field>
              <Field label="file_index">
                <code className="text-xs text-text-primary font-mono">{d.fileIndex}</code>
              </Field>
              <Field label="file_path">
                <code className="text-xs text-text-primary font-mono break-all">{d.filePath || '—'}</code>
              </Field>
              <Field label={t('downloads.inspect.fieldSize')}>
                <span className="text-text-primary">{formatBytes(d.fileSize)}</span>
                {fileStat?.exists && (
                  <span className="text-xs text-text-muted ml-2">
                    {t('downloads.inspect.onDiskLabel', { size: formatBytes(fileStat.onDisk) })}
                    {sparseInfo && (
                      <span className="ml-1.5 text-amber-400" title={t('downloads.inspect.sparseTitle')}>
                        {t('downloads.inspect.sparse')}
                      </span>
                    )}
                  </span>
                )}
              </Field>
              <Field label={t('downloads.inspect.fieldProgress')}>
                <span className="text-text-primary">
                  {formatBytesPair(d.bytesDownloaded, d.fileSize)} ({d.fileSize > 0 ? Math.round((d.bytesDownloaded / d.fileSize) * 100) : 0}%)
                </span>
              </Field>
              {torrent && (
                <>
                  <Field label={t('downloads.inspect.fieldSwarm')}>
                    <span className="text-text-primary">
                      {t('downloads.inspect.swarmValue', { seeders: torrent.seeders, peers: torrent.peers })}
                    </span>
                  </Field>
                  <Field label={t('downloads.inspect.fieldSpeed')}>
                    <span className="text-text-primary">
                      ↓ {formatRate(torrent.downRate)} · ↑ {formatRate(torrent.upRate)}
                    </span>
                  </Field>
                </>
              )}
              {!torrent && (
                <p className="text-xs text-text-muted italic">
                  {t('downloads.inspect.torrentInactive')}
                </p>
              )}
              <Field label={t('downloads.inspect.fieldMagnet')}>
                <button
                  onClick={copyMagnet}
                  className="flex items-center gap-1.5 text-xs text-cyan-400 hover:text-cyan-500 dark:hover:text-cyan-300"
                >
                  {copiedMagnet ? <Check className="w-3.5 h-3.5" /> : <Copy className="w-3.5 h-3.5" />}
                  {copiedMagnet ? t('downloads.inspect.copied') : t('downloads.inspect.copyMagnet')}
                </button>
              </Field>
              <Field label={t('downloads.inspect.fieldCreated')}>
                <span className="text-text-primary text-xs">{d.createdAt || '—'}</span>
              </Field>
              {d.completedAt && (
                <Field label={t('downloads.inspect.fieldCompleted')}>
                  <span className="text-text-primary text-xs">{d.completedAt}</span>
                </Field>
              )}
            </div>
          )}

          {tab === 'files' && (
            <div>
              {filesTabContent}
            </div>
          )}
          {tab === 'trackers' && (
            <div>
              {displayTrackers.length === 0 ? (
                <p className="text-xs text-text-muted italic py-2 text-center">
                  {t('downloads.inspect.noTrackers')}
                </p>
              ) : (
                <div className="space-y-2">
                  <p className="text-xs text-text-secondary mb-2">
                    {t('downloads.inspect.trackersConfigured')}
                  </p>
                  <ul className="bg-surface border border-default rounded-lg divide-y divide-default overflow-hidden font-mono text-xs max-h-[50vh] overflow-y-auto">
                    {displayTrackers.map(trackerUrl => (
                      <li key={trackerUrl} className="px-3 py-2 flex items-center justify-between gap-3 text-text-primary hover:bg-surface-secondary/40">
                        <span className="truncate flex-1 min-w-0" title={trackerUrl}>{trackerUrl}</span>
                        <button
                          onClick={async () => {
                            try {
                              await navigator.clipboard.writeText(trackerUrl)
                              notify(t('downloads.inspect.trackerCopied'), 'success')
                            } catch {}
                          }}
                          className="text-[10px] text-cyan-400 hover:text-cyan-500 dark:hover:text-cyan-300 px-2 py-0.5 border border-cyan-500/20 hover:border-cyan-500/40 rounded transition-colors flex-shrink-0"
                        >
                          {t('downloads.inspect.copy')}
                        </button>
                      </li>
                    ))}
                  </ul>
                </div>
              )}
            </div>
          )}
          {tab === 'peers' && <PeersTab downloadId={download.id} />}
          {tab === 'sources' && (
            <div>
              {renderSourcesTab(sources, loadingSources, t)}
            </div>
          )}
          {tab === 'actions' && (
            <div className="space-y-2">
              {onPlay && fileStat?.exists && fileStat.apparent >= d.fileSize * 0.99 && (
                <ActionRow
                  icon={Play}
                  title={t('downloads.inspect.actionPlayTitle')}
                  desc={t('downloads.inspect.actionPlayDesc')}
                  onClick={() => { onPlay(d); onClose() }}
                  busy={false}
                  disabled={!!busy}
                  variant="success"
                />
              )}
              <ActionRow
                icon={RefreshCw}
                title={t('downloads.inspect.actionRecheckTitle')}
                desc={t('downloads.inspect.actionRecheckDesc')}
                onClick={handleRecheck}
                busy={busy === 'recheck'}
                disabled={!!busy}
              />
              {onPromote && d.status === 'completed' && !d.promoted && (
                <ActionRow
                  icon={ArrowUpCircle}
                  title={t('downloads.inspect.actionPromoteTitle')}
                  desc={t('downloads.inspect.actionPromoteDesc')}
                  onClick={() => { onPromote(d); onClose() }}
                  busy={false}
                  disabled={!!busy}
                  variant="primary"
                />
              )}
              <ActionRow
                icon={Square}
                title={t('downloads.inspect.actionStopSeedTitle')}
                desc={t('downloads.inspect.actionStopSeedDesc')}
                onClick={handleStopSeed}
                busy={busy === 'stopSeed'}
                disabled={!!busy || !torrent}
              />
              <ActionRow
                icon={Trash2}
                title={t('downloads.inspect.actionDeleteTitle')}
                desc={t('downloads.inspect.actionDeleteDesc')}
                onClick={handleDelete}
                busy={busy === 'delete'}
                disabled={!!busy}
                variant="danger"
              />
            </div>
          )}
        </div>
    </Sheet>
  )
}

function Field({ label, children }: { readonly label: string; readonly children: React.ReactNode }) {
  return (
    <div className="grid grid-cols-[120px_1fr] gap-3 items-start">
      <span className="text-xs text-text-muted uppercase tracking-wide pt-0.5">{label}</span>
      <div className="flex items-center flex-wrap gap-1">{children}</div>
    </div>
  )
}

function StatusPill({ status }: { readonly status: string }) {
  const cls: Record<string, string> = {
    queued: 'bg-surface-tertiary text-text-primary',
    downloading: 'bg-cyan-500/20 text-cyan-700 dark:text-cyan-300 border border-cyan-500/30',
    completed: 'bg-green-500/20 text-green-700 dark:text-green-300 border border-green-500/30',
    failed: 'bg-red-500/20 text-red-700 dark:text-red-300 border border-red-500/30',
    paused: 'bg-amber-500/20 text-amber-700 dark:text-amber-300 border border-amber-500/30',
  }
  return (
    <span className={`text-[11px] px-2 py-0.5 rounded-full ${cls[status] || cls.queued}`}>
      {status}
    </span>
  )
}

type ActionRowProps = {
  readonly icon: typeof RefreshCw
  readonly title: string
  readonly desc: string
  readonly onClick: () => void | Promise<void>
  readonly busy: boolean
  readonly disabled: boolean
  readonly variant?: 'primary' | 'danger' | 'success'
}
function ActionRow({ icon: Icon, title, desc, onClick, busy, disabled, variant }: ActionRowProps) {
  const colorMap = {
    primary: 'bg-cyan-500/20 hover:bg-cyan-500/30 text-cyan-700 dark:text-cyan-300 border-cyan-500/30',
    danger: 'bg-red-500/15 hover:bg-red-500/25 text-red-700 dark:text-red-300 border-red-500/30',
    success: 'bg-emerald-500/20 hover:bg-emerald-500/30 text-emerald-700 dark:text-emerald-300 border-emerald-500/30',
    default: 'bg-surface-tertiary/40 hover:bg-surface-tertiary/60 text-text-primary border-strong',
  }
  return (
    <button
      onClick={() => { void onClick() }}
      disabled={disabled || busy}
      className={`w-full text-left flex items-start gap-3 p-3 rounded-lg border transition-colors disabled:opacity-50 ${
        colorMap[variant || 'default']
      }`}
    >
      {busy ? <Loader2 className="w-4 h-4 animate-spin mt-0.5" /> : <Icon className="w-4 h-4 mt-0.5 flex-shrink-0" />}
      <span className="flex-1">
        <span className="block text-sm font-medium">{title}</span>
        <span className="block text-xs opacity-80 mt-0.5">{desc}</span>
      </span>
    </button>
  )
}
