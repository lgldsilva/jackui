import { useState, useEffect, useCallback } from 'react'
import {
  X, Info, Files, Copy, Check, RefreshCw, FileVideo, FileAudio, FileText,
  Loader2, AlertCircle, Trash2, Square,
  ArrowUpCircle, Activity, Globe, Play
} from 'lucide-react'
import {
  DownloadEntry, DownloadDetails, StreamFile,
  downloadDetails, downloadRecheck, downloadDelete, downloadStopSeed,
} from '../api/client'
import { formatBytes, formatRate } from '../lib/format'
import { useScrollLock } from '../lib/useScrollLock'

type Props = {
  readonly download: DownloadEntry | null
  readonly onClose: () => void
  readonly onMutated?: (d: DownloadEntry) => void
  readonly onDeleted?: (id: number) => void
  readonly onPromote?: (d: DownloadEntry) => void
  readonly onPlay?: (d: DownloadEntry) => void
}

type Tab = 'overview' | 'files' | 'trackers' | 'actions'

// Distingue o file que o download representa (highlight verde) dos outros
// arquivos do torrent (listados em cinza). Em torrents single-file os dois
// coincidem; em multi-file isso ajuda o user a ver o que tinha junto.
function fileIcon(f: StreamFile, primary: boolean) {
  const color = primary ? 'text-green-400' : 'text-gray-500'
  if (f.isVideo) return <FileVideo className={`w-4 h-4 ${color} flex-shrink-0`} />
  if (/\.(mp3|flac|ogg|wav|m4a|aac|opus)$/i.test(f.path)) {
    return <FileAudio className={`w-4 h-4 ${color} flex-shrink-0`} />
  }
  return <FileText className={`w-4 h-4 ${color} flex-shrink-0`} />
}

export default function DownloadInspectModal({ download, onClose, onMutated, onDeleted, onPromote, onPlay }: Props) {
  useScrollLock(!!download)
  const [tab, setTab] = useState<Tab>('overview')
  const [details, setDetails] = useState<DownloadDetails | null>(null)
  const [loading, setLoading] = useState(false)
  const [error, setError] = useState('')
  const [copiedMagnet, setCopiedMagnet] = useState(false)
  const [busy, setBusy] = useState<string | null>(null) // 'recheck' | 'delete' | 'stopSeed'

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
  }, [download])

  useEffect(() => {
    if (!download) {
      setDetails(null)
      setTab('overview')
      return
    }
    refresh()
  }, [download, refresh])

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

  let filesTabContent: React.ReactNode
  if (!torrent && !syntheticFile) {
    filesTabContent = (
      <p className="text-xs text-gray-500 italic py-2">
        Torrent não está ativo agora — lista de arquivos não disponível. Tente fazer um recheck pra re-attach.
      </p>
    )
  } else if (!torrent && syntheticFile) {
    // Completed download — torrent was dropped but we know the primary file
    filesTabContent = (
      <ul className="bg-gray-900 border border-gray-700 rounded-lg divide-y divide-gray-800 overflow-hidden">
        <li className="px-3 py-2 flex items-center gap-2.5 bg-green-500/5">
          {fileIcon(syntheticFile, true)}
          <div className="flex-1 min-w-0">
            <p className="text-sm truncate text-green-300 font-medium" title={syntheticFile.path}>
              {syntheticFile.path}
            </p>
            {d.filePath && (
              <p className="text-[10px] text-gray-500 font-mono truncate mt-0.5" title={d.filePath}>
                {d.filePath}
              </p>
            )}
          </div>
          <div className="text-right flex-shrink-0">
            <p className="text-xs text-gray-400">{formatBytes(syntheticFile.size)}</p>
            <p className="text-[10px] text-green-400 uppercase tracking-wide">este download</p>
          </div>
        </li>
      </ul>
    )
  } else if (!torrent || torrent.files.length === 0) {
    filesTabContent = (
      <p className="text-xs text-gray-500 italic">Sem arquivos.</p>
    )
  } else {
    filesTabContent = (
      <ul className="bg-gray-900 border border-gray-700 rounded-lg divide-y divide-gray-800 overflow-hidden">
        {torrent.files.map(f => {
          const isPrimary = f.index === d.fileIndex
          return (
            <li
              key={f.index}
              className={`px-3 py-2 flex items-center gap-2.5 ${isPrimary ? 'bg-green-500/5' : ''}`}
            >
              {fileIcon(f, isPrimary)}
              <div className="flex-1 min-w-0">
                <p className={`text-sm truncate ${isPrimary ? 'text-green-300 font-medium' : 'text-gray-200'}`} title={f.path}>
                  {f.path}
                </p>
                {f.progress > 0 && f.progress < 1 && (
                  <div className="mt-1 h-1 bg-gray-700 rounded overflow-hidden">
                    <div className="h-full bg-cyan-500" style={{ width: `${Math.round(f.progress * 100)}%` }} />
                  </div>
                )}
              </div>
              {f.size > 0 && (
                <span className="text-xs text-gray-500 tabular-nums flex-shrink-0">{formatBytes(f.size)}</span>
              )}
            </li>
          )
        })}
      </ul>
    )
  }

  return (
    <dialog
      className="fixed inset-0 bg-black/60 backdrop-blur-sm flex items-center justify-center z-50 p-4 open:flex"
      onClick={e => e.target === e.currentTarget && onClose()}
      onKeyDown={e => e.key === 'Escape' && onClose()}
      onClose={onClose}
      open
    >
      <div className="bg-gray-800 rounded-2xl border border-gray-700 w-full max-w-2xl shadow-2xl max-h-[90vh] flex flex-col">
        <header className="flex items-center justify-between p-4 border-b border-gray-700">
          <h2 className="text-base font-semibold text-gray-100 flex items-center gap-2 truncate">
            <Info className="w-5 h-5 text-cyan-400 flex-shrink-0" />
            <span className="truncate" title={d.name}>{d.name}</span>
          </h2>
          <button onClick={onClose} className="text-gray-400 hover:text-gray-100 flex-shrink-0">
            <X className="w-5 h-5" />
          </button>
        </header>

        {/* Tabs */}
        <div className="flex border-b border-gray-700 px-2 bg-gray-900/40">
          {[
            { id: 'overview' as Tab, label: 'Detalhes', icon: Info },
            { id: 'files' as Tab, label: `Arquivos${torrent ? ` (${torrent.files.length})` : ''}`, icon: Files },
            { id: 'trackers' as Tab, label: `Trackers${displayTrackers.length > 0 ? ` (${displayTrackers.length})` : ''}`, icon: Globe },
            { id: 'actions' as Tab, label: 'Ações', icon: Activity },
          ].map(({ id, label, icon: Icon }) => (
            <button
              key={id}
              onClick={() => setTab(id)}
              className={`flex items-center gap-1.5 px-3 py-2 text-sm border-b-2 -mb-px transition-colors ${
                tab === id
                  ? 'border-cyan-400 text-cyan-300'
                  : 'border-transparent text-gray-400 hover:text-gray-200'
              }`}
            >
              <Icon className="w-3.5 h-3.5" />
              {label}
            </button>
          ))}
        </div>

        <div className="flex-1 overflow-y-auto p-4 text-sm">
          {loading && !details && (
            <div className="flex items-center justify-center py-8 text-gray-500">
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
                <code className="text-xs text-gray-300 font-mono break-all">{d.infoHash}</code>
              </Field>
              <Field label="file_index">
                <code className="text-xs text-gray-300 font-mono">{d.fileIndex}</code>
              </Field>
              <Field label="file_path">
                <code className="text-xs text-gray-300 font-mono break-all">{d.filePath || '—'}</code>
              </Field>
              <Field label="Tamanho">
                <span className="text-gray-300">{formatBytes(d.fileSize)}</span>
                {fileStat?.exists && (
                  <span className="text-xs text-gray-500 ml-2">
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
                <span className="text-gray-300">
                  {formatBytes(d.bytesDownloaded)} ({d.fileSize > 0 ? Math.round((d.bytesDownloaded / d.fileSize) * 100) : 0}%)
                </span>
              </Field>
              {torrent && (
                <>
                  <Field label="Swarm">
                    <span className="text-gray-300">
                      {torrent.seeders} seeders / {torrent.peers} peers
                    </span>
                  </Field>
                  <Field label="Velocidade">
                    <span className="text-gray-300">
                      ↓ {formatRate(torrent.downRate)} · ↑ {formatRate(torrent.upRate)}
                    </span>
                  </Field>
                </>
              )}
              {!torrent && (
                <p className="text-xs text-gray-500 italic">
                  Torrent não está ativo no streamer agora (foi dropado pós-completed ou ainda não foi resolvido). Dados de swarm/velocidade indisponíveis.
                </p>
              )}
              <Field label="Magnet">
                <button
                  onClick={copyMagnet}
                  className="flex items-center gap-1.5 text-xs text-cyan-400 hover:text-cyan-300"
                >
                  {copiedMagnet ? <Check className="w-3.5 h-3.5" /> : <Copy className="w-3.5 h-3.5" />}
                  {copiedMagnet ? 'copiado' : 'copiar magnet'}
                </button>
              </Field>
              <Field label="Criado em">
                <span className="text-gray-300 text-xs">{d.createdAt || '—'}</span>
              </Field>
              {d.completedAt && (
                <Field label="Concluído em">
                  <span className="text-gray-300 text-xs">{d.completedAt}</span>
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
                <p className="text-xs text-gray-500 italic py-2 text-center">
                  Nenhum tracker encontrado. O torrent pode ter sido adicionado via .torrent sem &tr= no magnet.
                </p>
              ) : (
                <div className="space-y-2">
                  <p className="text-xs text-gray-400 mb-2">
                    Servidores de tracker configurados para este torrent:
                  </p>
                  <ul className="bg-gray-900 border border-gray-700 rounded-lg divide-y divide-gray-800 overflow-hidden font-mono text-xs max-h-[50vh] overflow-y-auto">
                    {displayTrackers.map(trackerUrl => (
                      <li key={trackerUrl} className="px-3 py-2 flex items-center justify-between gap-3 text-gray-300 hover:bg-gray-800/40">
                        <span className="truncate flex-1" title={trackerUrl}>{trackerUrl}</span>
                        <button
                          onClick={async () => {
                            try {
                              await navigator.clipboard.writeText(trackerUrl)
                              alert('Tracker copiado para a área de transferência!')
                            } catch {}
                          }}
                          className="text-[10px] text-cyan-400 hover:text-cyan-300 px-2 py-0.5 border border-cyan-500/20 hover:border-cyan-500/40 rounded transition-colors flex-shrink-0"
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
      </div>
    </dialog>
  )
}

function Field({ label, children }: { readonly label: string; readonly children: React.ReactNode }) {
  return (
    <div className="grid grid-cols-[120px_1fr] gap-3 items-start">
      <span className="text-xs text-gray-500 uppercase tracking-wide pt-0.5">{label}</span>
      <div className="flex items-center flex-wrap gap-1">{children}</div>
    </div>
  )
}

function StatusPill({ status }: { readonly status: string }) {
  const cls: Record<string, string> = {
    queued: 'bg-gray-700 text-gray-300',
    downloading: 'bg-cyan-500/20 text-cyan-300 border border-cyan-500/30',
    completed: 'bg-green-500/20 text-green-300 border border-green-500/30',
    failed: 'bg-red-500/20 text-red-300 border border-red-500/30',
    paused: 'bg-amber-500/20 text-amber-300 border border-amber-500/30',
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
    primary: 'bg-cyan-500/20 hover:bg-cyan-500/30 text-cyan-300 border-cyan-500/30',
    danger: 'bg-red-500/15 hover:bg-red-500/25 text-red-300 border-red-500/30',
    success: 'bg-emerald-500/20 hover:bg-emerald-500/30 text-emerald-300 border-emerald-500/30',
    default: 'bg-gray-700/40 hover:bg-gray-700/60 text-gray-200 border-gray-600',
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
