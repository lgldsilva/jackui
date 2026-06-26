import { useState, useEffect, useCallback } from 'react'
import {
  Info, Files, Copy, Check, RefreshCw, FileVideo, FileAudio, FileText,
  Loader2, AlertCircle, Trash2, Square,
  ArrowUpCircle, Activity, Globe, Play, Share2, Download, Users,
} from 'lucide-react'
import PeersTab from './downloadInspect/PeersTab'
import {
  DownloadEntry, DownloadDetails, StreamFile, TorrentInfo, DownloadSource,
  downloadDetails, downloadRecheck, downloadDelete, downloadStopSeed, downloadSources, downloadCreate,
} from '../api/client'
import { formatBytes, formatRate, formatBytesPair } from '../lib/format'
import { Sheet } from './Sheet'

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

// sourceStatusBadge maps a source's lifecycle to a label + color for the list.
function sourceStatusBadge(status: DownloadSource['status']): { label: string; cls: string } {
  switch (status) {
    case 'active': return { label: 'ativa', cls: 'bg-emerald-500/15 text-emerald-700 dark:text-emerald-300 border-emerald-500/30' }
    case 'cooldown': return { label: 'aguardando', cls: 'bg-amber-500/15 text-amber-700 dark:text-amber-300 border-amber-500/30' }
    case 'failed': return { label: 'falhou', cls: 'bg-red-500/15 text-red-700 dark:text-red-300 border-red-500/30' }
    default: return { label: 'candidata', cls: 'bg-gray-600/30 text-text-primary border-strong/50' }
  }
}

function renderSourcesTab(sources: DownloadSource[], loading: boolean): React.ReactNode {
  if (loading) {
    return <div className="flex items-center gap-2 text-text-secondary py-8 justify-center"><Loader2 className="w-4 h-4 animate-spin" />Carregando fontes...</div>
  }
  if (sources.length === 0) {
    return (
      <p className="text-sm text-text-muted py-6 text-center">
        Nenhuma fonte alternativa ainda. Quando a rotação automática estiver ligada e o download ficar sem seed,
        outras fontes do mesmo conteúdo aparecerão aqui.
      </p>
    )
  }
  return (
    <ul className="flex flex-col gap-2">
      {sources.map((s) => {
        const badge = sourceStatusBadge(s.status)
        return (
          <li key={s.id} className="flex items-center gap-3 bg-surface/60 rounded-lg px-3 py-2">
            <span className={`text-[10px] px-1.5 py-0.5 rounded-md border font-medium whitespace-nowrap ${badge.cls}`}>{badge.label}</span>
            <div className="min-w-0 flex-1">
              <div className="text-sm text-text-primary truncate" title={s.title}>{s.title || s.infoHash}</div>
              <div className="text-[11px] text-text-muted flex items-center gap-2 flex-wrap">
                <span className="flex items-center gap-1"><Globe className="w-3 h-3" />{s.tracker || '—'}</span>
                <span className="text-green-400">{s.seeders} seed</span>
                {s.tries > 0 && <span>· {s.tries}× tentada</span>}
              </div>
            </div>
          </li>
        )
      })}
    </ul>
  )
}

// Distingue o file que o download representa (highlight verde) dos outros
// arquivos do torrent (listados em cinza). Em torrents single-file os dois
// coincidem; em multi-file isso ajuda o user a ver o que tinha junto.
function fileIcon(f: StreamFile, primary: boolean) {
  const color = primary ? 'text-green-400' : 'text-text-muted'
  if (f.isVideo) return <FileVideo className={`w-4 h-4 ${color} flex-shrink-0`} />
  if (/\.(mp3|flac|ogg|wav|m4a|aac|opus)$/i.test(f.path)) {
    return <FileAudio className={`w-4 h-4 ${color} flex-shrink-0`} />
  }
  return <FileText className={`w-4 h-4 ${color} flex-shrink-0`} />
}

function filesTabLabel(torrent: TorrentInfo | null | undefined): string {
  if (!torrent) return 'Arquivos'
  return `Arquivos (${torrent.files.length})`
}

function trackersTabLabel(trackers: readonly string[]): string {
  if (trackers.length === 0) return 'Trackers'
  return `Trackers (${trackers.length})`
}

function renderFilesTab(
  torrent: TorrentInfo | null | undefined,
  syntheticFile: StreamFile | null,
  filePath: string,
  fileIndex: number,
  fileIcon: (f: StreamFile, primary: boolean) => React.ReactNode,
  siblings: readonly DownloadEntry[],
  adopting: number | null,
  onAdopt: (f: StreamFile) => void,
): React.ReactNode {
  if (!torrent && !syntheticFile) {
    return (
      <p className="text-xs text-text-muted italic py-2">
        Torrent não está ativo agora — lista de arquivos não disponível. Tente fazer um recheck pra re-attach.
      </p>
    )
  }
  if (!torrent && syntheticFile) {
    return (
      <ul className="bg-surface border border-default rounded-lg divide-y divide-default overflow-hidden">
        <li className="px-3 py-2 flex items-center gap-2.5 bg-green-500/5">
          {fileIcon(syntheticFile, true)}
          <div className="flex-1 min-w-0">
            <p className="text-sm truncate text-green-700 dark:text-green-300 font-medium" title={syntheticFile.path}>{syntheticFile.path}</p>
            {filePath && <p className="text-[10px] text-text-muted font-mono truncate mt-0.5" title={filePath}>{filePath}</p>}
          </div>
          <div className="text-right flex-shrink-0">
            <p className="text-xs text-text-secondary">{formatBytes(syntheticFile.size)}</p>
            <p className="text-[10px] text-green-400 uppercase tracking-wide">este download</p>
          </div>
        </li>
      </ul>
    )
  }
  if (!torrent || torrent.files.length === 0) {
    return <p className="text-xs text-text-muted italic">Sem arquivos.</p>
  }
  const hasRow = (idx: number) => siblings.some(s => s.fileIndex === idx)
  const missing = torrent.files.filter(f => !hasRow(f.index))
  return (
    <div className="flex flex-col gap-2">
      {missing.length > 1 && (
        <button
          onClick={() => { for (const f of missing) onAdopt(f) }}
          disabled={adopting !== null}
          className="self-start flex items-center gap-1.5 text-xs bg-cyan-500/15 hover:bg-cyan-500/25 disabled:opacity-50 text-cyan-700 dark:text-cyan-300 border border-cyan-500/30 px-3 py-1.5 rounded-lg transition-colors"
          title="Baixa (e move pra downloads ao concluir) todos os arquivos que ainda estão só em streaming"
        >
          <Download className="w-3.5 h-3.5" /> Baixar os {missing.length} que faltam
        </button>
      )}
      <ul className="bg-surface border border-default rounded-lg divide-y divide-default overflow-hidden">
        {torrent.files.map(f => {
          const isPrimary = f.index === fileIndex
          // Marca claramente o que falta baixar: completo (>=99.9%) vs incompleto
          // (mostra o % em âmbar + barra, mesmo a 0%) — assim dá pra ver qual
          // arquivo do torrent ficou pra trás.
          const hasProgress = typeof f.progress === 'number'
          const done = hasProgress && f.progress >= 0.999
          const pct = hasProgress ? Math.round(f.progress * 100) : null
          const tracked = hasRow(f.index)
          return (
            <li key={f.index} className={`px-3 py-2 flex items-center gap-2.5 ${isPrimary ? 'bg-green-500/5' : ''}`}>
              {fileIcon(f, isPrimary)}
              <div className="flex-1 min-w-0">
                <p className={`text-sm truncate ${isPrimary ? 'text-green-700 dark:text-green-300 font-medium' : 'text-text-primary'}`} title={f.path}>{f.path}</p>
                {hasProgress && !done && (
                  <div className="mt-1 h-1 bg-surface-tertiary rounded overflow-hidden">
                    <div className="h-full bg-amber-500" style={{ width: `${Math.max(2, pct ?? 0)}%` }} />
                  </div>
                )}
              </div>
              {/* Arquivo sem registro de download = está só em streaming (cache).
                  Botão adota como download: reusa o cache e move ao concluir. */}
              {!tracked ? (
                <button
                  onClick={() => onAdopt(f)}
                  disabled={adopting !== null}
                  title="Baixar este arquivo (move pra downloads ao concluir; reaproveita o que já está em cache)"
                  className="flex-shrink-0 inline-flex items-center gap-1 text-[10px] bg-cyan-500/15 hover:bg-cyan-500/25 disabled:opacity-50 text-cyan-700 dark:text-cyan-300 border border-cyan-500/30 px-2 py-1 rounded-md transition-colors"
                >
                  {adopting === f.index ? <Loader2 className="w-3 h-3 animate-spin" /> : <Download className="w-3 h-3" />}
                  Baixar
                </button>
              ) : pct !== null && (
                done
                  ? <span className="text-[10px] text-emerald-400 flex-shrink-0 inline-flex items-center gap-0.5" title="Arquivo completo"><Check className="w-3 h-3" />ok</span>
                  : <span className="text-[10px] text-amber-400 tabular-nums flex-shrink-0" title="Ainda não baixado por completo">{pct}%</span>
              )}
              {f.size > 0 && <span className="text-xs text-text-muted tabular-nums flex-shrink-0">{formatBytes(f.size)}</span>}
            </li>
          )
        })}
      </ul>
    </div>
  )
}

export default function DownloadInspectModal({ download, onClose, onMutated, onDeleted, onPromote, onPlay, siblings, onAdopted }: Props) {
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
      setError((e as Error)?.message || 'Erro carregando detalhes')
    } finally {
      setLoading(false)
    }
    // Key on the id, not the object: the parent now derives `download` from the 2s
    // poll, so its reference changes every tick — depending on `download` would
    // re-fetch details/sources every 2s. The id is what actually identifies the row.
  }, [download?.id])

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
      setError((e as Error)?.message || 'Falha ao baixar arquivo')
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
      setError((e as Error)?.message || 'Recheck falhou')
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
      setError((e as Error)?.message || 'Stop seed falhou')
    } finally {
      setBusy(null)
    }
  }

  const handleDelete = async () => {
    if (busy) return
    if (!confirm(`Apagar download "${d.name}"?\n\nO arquivo no disco NÃO é removido.`)) return
    setBusy('delete')
    setError('')
    try {
      await downloadDelete(d.id)
      onDeleted?.(d.id)
      onClose()
    } catch (e: unknown) {
      setError((e as Error)?.message || 'Delete falhou')
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

  const filesTabContent = renderFilesTab(torrent, syntheticFile, d.filePath, d.fileIndex, fileIcon, siblings ?? [], adopting, adopt)

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
            { id: 'overview' as Tab, label: 'Detalhes', icon: Info },
            { id: 'files' as Tab, label: filesTabLabel(torrent), icon: Files },
            { id: 'trackers' as Tab, label: trackersTabLabel(displayTrackers), icon: Globe },
            { id: 'peers' as Tab, label: 'Peers', icon: Users },
            { id: 'sources' as Tab, label: 'Fontes', icon: Share2 },
            { id: 'actions' as Tab, label: 'Ações', icon: Activity },
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
              <Field label="Status">
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
              <Field label="Tamanho">
                <span className="text-text-primary">{formatBytes(d.fileSize)}</span>
                {fileStat?.exists && (
                  <span className="text-xs text-text-muted ml-2">
                    no disco: {formatBytes(fileStat.onDisk)}
                    {sparseInfo && (
                      <span className="ml-1.5 text-amber-400" title="Arquivo sparse — bytes alocados &lt; tamanho declarado">
                        (sparse)
                      </span>
                    )}
                  </span>
                )}
              </Field>
              <Field label="Progresso">
                <span className="text-text-primary">
                  {formatBytesPair(d.bytesDownloaded, d.fileSize)} ({d.fileSize > 0 ? Math.round((d.bytesDownloaded / d.fileSize) * 100) : 0}%)
                </span>
              </Field>
              {torrent && (
                <>
                  <Field label="Swarm">
                    <span className="text-text-primary">
                      {torrent.seeders} seeders / {torrent.peers} peers
                    </span>
                  </Field>
                  <Field label="Velocidade">
                    <span className="text-text-primary">
                      ↓ {formatRate(torrent.downRate)} · ↑ {formatRate(torrent.upRate)}
                    </span>
                  </Field>
                </>
              )}
              {!torrent && (
                <p className="text-xs text-text-muted italic">
                  Torrent não está ativo no streamer agora (foi dropado pós-completed ou ainda não foi resolvido). Dados de swarm/velocidade indisponíveis.
                </p>
              )}
              <Field label="Magnet">
                <button
                  onClick={copyMagnet}
                  className="flex items-center gap-1.5 text-xs text-cyan-400 hover:text-cyan-500 dark:hover:text-cyan-300"
                >
                  {copiedMagnet ? <Check className="w-3.5 h-3.5" /> : <Copy className="w-3.5 h-3.5" />}
                  {copiedMagnet ? 'copiado' : 'copiar magnet'}
                </button>
              </Field>
              <Field label="Criado em">
                <span className="text-text-primary text-xs">{d.createdAt || '—'}</span>
              </Field>
              {d.completedAt && (
                <Field label="Concluído em">
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
                  Nenhum tracker encontrado. O torrent pode ter sido adicionado via .torrent sem &tr= no magnet.
                </p>
              ) : (
                <div className="space-y-2">
                  <p className="text-xs text-text-secondary mb-2">
                    Servidores de tracker configurados para este torrent:
                  </p>
                  <ul className="bg-surface border border-default rounded-lg divide-y divide-default overflow-hidden font-mono text-xs max-h-[50vh] overflow-y-auto">
                    {displayTrackers.map(trackerUrl => (
                      <li key={trackerUrl} className="px-3 py-2 flex items-center justify-between gap-3 text-text-primary hover:bg-surface-secondary/40">
                        <span className="truncate flex-1 min-w-0" title={trackerUrl}>{trackerUrl}</span>
                        <button
                          onClick={async () => {
                            try {
                              await navigator.clipboard.writeText(trackerUrl)
                              alert('Tracker copiado para a área de transferência!')
                            } catch {}
                          }}
                          className="text-[10px] text-cyan-400 hover:text-cyan-500 dark:hover:text-cyan-300 px-2 py-0.5 border border-cyan-500/20 hover:border-cyan-500/40 rounded transition-colors flex-shrink-0"
                        >
                          copiar
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
              {renderSourcesTab(sources, loadingSources)}
            </div>
          )}
          {tab === 'actions' && (
            <div className="space-y-2">
              {onPlay && fileStat?.exists && fileStat.apparent >= d.fileSize * 0.99 && (
                <ActionRow
                  icon={Play}
                  title="Tocar agora"
                  desc="Assista a este arquivo completo localmente no player integrado."
                  onClick={() => { onPlay(d); onClose() }}
                  busy={false}
                  disabled={!!busy}
                  variant="success"
                />
              )}
              <ActionRow
                icon={RefreshCw}
                title="Recheck (force)"
                desc="Re-hasha todos os pieces e reseta o progresso pro worker reconciliar com o disco. Útil quando você desconfia de corrupção."
                onClick={handleRecheck}
                busy={busy === 'recheck'}
                disabled={!!busy}
              />
              {onPromote && d.status === 'completed' && !d.promoted && (
                <ActionRow
                  icon={ArrowUpCircle}
                  title="Promover"
                  desc="Move o arquivo pra biblioteca compartilhada (JACKUI_SHARED_DIR). Opcional continuar seedando."
                  onClick={() => { onPromote(d); onClose() }}
                  busy={false}
                  disabled={!!busy}
                  variant="primary"
                />
              )}
              <ActionRow
                icon={Square}
                title="Parar de seedar"
                desc="Dropa o torrent do streamer. O arquivo no disco permanece. Linha do download fica no DB."
                onClick={handleStopSeed}
                busy={busy === 'stopSeed'}
                disabled={!!busy || !torrent}
              />
              <ActionRow
                icon={Trash2}
                title="Apagar download"
                desc="Remove a linha do DB. O arquivo no disco NÃO é apagado (use o promove ou apague manualmente). O torrent fica seedando se ainda estiver ativo."
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
