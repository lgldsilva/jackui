import { useEffect, useRef, useState } from 'react'
import {
  Download as DownloadIcon, Loader2, Pause, Play, Trash2, CheckCircle2, AlertCircle, Clock,
  Activity, Gauge, Users, Zap,
} from 'lucide-react'
import NavHeader from '../components/NavHeader'
import {
  DownloadEntry, downloadsList, downloadDelete, downloadPause, downloadResume,
  TorrentInfo, streamActive, streamPause, streamResume, streamSetPriority,
  streamPauseAll, streamResumeAll, streamGetLimits, streamSetLimits, StreamPriority, streamDrop,
} from '../api/client'
import { formatBytes, formatRate } from '../lib/format'

/**
 * Page-level view of every background download (one row per file).
 * Auto-refreshes every 2 s while the page is mounted — matches the worker
 * tick so progress feels live but the server isn't hammered with smaller
 * intervals. Stops polling cleanly on unmount.
 */
export default function DownloadsPage() {
  const [items, setItems] = useState<DownloadEntry[]>([])
  const [loading, setLoading] = useState(true)
  const [busyID, setBusyID] = useState<number | null>(null)
  const mountedRef = useRef(true)

  type TorrentFilter = 'all' | 'downloading' | 'paused' | 'done'
  const [torrentFilter, setTorrentFilter] = useState<TorrentFilter>('all')

  // Streamer-active torrents (Transmission-style). Separate state from the
  // background-download queue above so a slow `/stream/active` call never
  // delays the existing UI section.
  const [torrents, setTorrents] = useState<TorrentInfo[]>([])
  const [torrentsLoaded, setTorrentsLoaded] = useState(false)
  const [busyHash, setBusyHash] = useState<string | null>(null)
  const [bulkBusy, setBulkBusy] = useState(false)
  // Bandwidth caps round-trip through the server in bytes/sec. We expose KB/s
  // in the UI because MB/s inputs would lose precision for typical home links.
  const [limitDownKB, setLimitDownKB] = useState<string>('')
  const [limitUpKB, setLimitUpKB] = useState<string>('')
  const [limitsSaving, setLimitsSaving] = useState(false)
  const [limitsMsg, setLimitsMsg] = useState<string>('')

  const load = async () => {
    try {
      const list = await downloadsList()
      if (mountedRef.current) setItems(list)
    } catch {
      // Silent: a transient failure doesn't justify wiping the current view.
    } finally {
      if (mountedRef.current) setLoading(false)
    }
  }

  // Pull current limits once on mount — the inputs are uncontrolled-after-
  // first-load so the user's typing doesn't get clobbered by the 2s poll.
  const loadLimits = async () => {
    try {
      const cur = await streamGetLimits()
      if (!mountedRef.current) return
      setLimitDownKB(cur.down > 0 ? String(Math.round(cur.down / 1024)) : '')
      setLimitUpKB(cur.up > 0 ? String(Math.round(cur.up / 1024)) : '')
    } catch {
      /* leave inputs empty — server will report current value on next save */
    }
  }

  const loadTorrents = async () => {
    try {
      const list = await streamActive()
      if (mountedRef.current) setTorrents(list)
    } catch {
      /* keep last known list on transient failure */
    } finally {
      if (mountedRef.current) setTorrentsLoaded(true)
    }
  }

  useEffect(() => {
    mountedRef.current = true
    load()
    loadTorrents()
    loadLimits()
    const t = setInterval(() => { load(); loadTorrents() }, 2000)
    return () => {
      mountedRef.current = false
      clearInterval(t)
    }
  }, [])

  const onPause = async (id: number) => {
    setBusyID(id)
    try { await downloadPause(id); await load() } finally { setBusyID(null) }
  }
  const onResume = async (id: number) => {
    setBusyID(id)
    try { await downloadResume(id); await load() } finally { setBusyID(null) }
  }
  const onDelete = async (id: number) => {
    if (!confirm('Cancelar e remover este download? Os bytes já baixados podem ser apagados pelo cache LRU.')) return
    setBusyID(id)
    try { await downloadDelete(id); await load() } finally { setBusyID(null) }
  }

  // Torrent-level handlers. We refresh the active list immediately after each
  // mutation so the row reflects its new state without waiting for the next
  // poll tick (better-feeling UI for a single click).
  const onTorrentPause = async (hash: string) => {
    setBusyHash(hash)
    try { await streamPause(hash); await loadTorrents() } finally { setBusyHash(null) }
  }
  const onTorrentResume = async (hash: string) => {
    setBusyHash(hash)
    try { await streamResume(hash); await loadTorrents() } finally { setBusyHash(null) }
  }
  const onTorrentPriority = async (hash: string, priority: StreamPriority) => {
    setBusyHash(hash)
    try { await streamSetPriority(hash, priority); await loadTorrents() } finally { setBusyHash(null) }
  }
  const onTorrentDelete = async (hash: string) => {
    if (!confirm('Parar e remover este torrent da fila de streaming?')) return
    setBusyHash(hash)
    try { await streamDrop(hash); await loadTorrents() } finally { setBusyHash(null) }
  }
  const onPauseAll = async () => {
    setBulkBusy(true)
    try { await streamPauseAll(); await loadTorrents() } finally { setBulkBusy(false) }
  }
  const onResumeAll = async () => {
    setBulkBusy(true)
    try { await streamResumeAll(); await loadTorrents() } finally { setBulkBusy(false) }
  }

  const onSaveLimits = async () => {
    setLimitsSaving(true)
    setLimitsMsg('')
    try {
      const down = limitDownKB.trim() === '' ? 0 : Math.max(0, Math.round(Number(limitDownKB) * 1024))
      const up = limitUpKB.trim() === '' ? 0 : Math.max(0, Math.round(Number(limitUpKB) * 1024))
      if (!isFinite(down) || !isFinite(up)) {
        setLimitsMsg('Valores inválidos')
        return
      }
      await streamSetLimits({ down, up })
      setLimitsMsg('Limites aplicados')
      // Re-pull from server so the displayed value matches what was persisted
      // (catches server-side rounding/clamping if any).
      await loadLimits()
      window.setTimeout(() => { if (mountedRef.current) setLimitsMsg('') }, 2500)
    } catch (err) {
      setLimitsMsg('Falha ao salvar')
    } finally {
      setLimitsSaving(false)
    }
  }

  // Torrents being background-downloaded are shown in the Downloads section;
  // exclude them from "Torrents ativos" to avoid showing the same item twice.
  const bgHashes = new Set(
    items.filter(d => d.status === 'downloading' || d.status === 'queued').map(d => d.infoHash)
  )
  const displayTorrents = torrents.filter(t => !bgHashes.has(t.infoHash))

  const filteredTorrents = displayTorrents.filter(t => {
    if (torrentFilter === 'all') return true
    const status = t.status || ((t.progress || 0) >= 1 ? 'complete' : 'downloading')
    if (torrentFilter === 'downloading') return status === 'downloading' || status === 'seeding'
    if (torrentFilter === 'paused') return status === 'paused'
    if (torrentFilter === 'done') return status === 'complete'
    return true
  })

  return (
    <div className="min-h-screen bg-gray-900">
      <NavHeader />
      <main className="max-w-5xl mx-auto px-4 py-6 flex flex-col gap-10">
        {/* ───────────────── Active torrents (Transmission-style) ───────────────── */}
        <section>
          <header className="flex items-center justify-between mb-4 flex-wrap gap-2">
            <h1 className="text-2xl font-bold text-gray-100 flex items-center gap-2">
              <Activity className="w-6 h-6 text-emerald-400" />
              Torrents ativos
              {displayTorrents.length > 0 && (
                <span className="text-sm font-normal text-gray-400">
                  ({filteredTorrents.length}{torrentFilter !== 'all' ? `/${displayTorrents.length}` : ''})
                </span>
              )}
            </h1>
            <div className="flex items-center gap-1 text-xs flex-wrap">
              {(['all', 'downloading', 'paused', 'done'] as const).map(f => {
                const labels: Record<typeof f, string> = { all: 'Todos', downloading: 'Em andamento', paused: 'Pausados', done: 'Concluídos' }
                return (
                  <button
                    key={f}
                    onClick={() => setTorrentFilter(f)}
                    className={torrentFilter === f ? 'btn-primary' : 'btn-secondary'}
                  >{labels[f]}</button>
                )
              })}
            </div>
          </header>

          {/* Bandwidth caps row */}
          <div className="bg-gray-800/60 border border-gray-700 rounded-lg p-3 flex items-center gap-3 flex-wrap mb-4">
            <Gauge className="w-4 h-4 text-emerald-400 flex-shrink-0" />
            <span className="text-xs text-gray-400">Limites globais (KB/s, 0 = ilimitado):</span>
            <label className="flex items-center gap-1.5 text-xs text-gray-300">
              <span className="text-gray-500">↓ Down</span>
              <input
                type="number"
                min={0}
                placeholder="0"
                value={limitDownKB}
                onChange={e => setLimitDownKB(e.target.value)}
                className="w-24 bg-gray-900 border border-gray-700 rounded px-2 py-1 text-gray-100 focus:outline-none focus:border-emerald-500"
              />
            </label>
            <label className="flex items-center gap-1.5 text-xs text-gray-300">
              <span className="text-gray-500">↑ Up</span>
              <input
                type="number"
                min={0}
                placeholder="0"
                value={limitUpKB}
                onChange={e => setLimitUpKB(e.target.value)}
                className="w-24 bg-gray-900 border border-gray-700 rounded px-2 py-1 text-gray-100 focus:outline-none focus:border-emerald-500"
              />
            </label>
            <button
              onClick={onSaveLimits}
              disabled={limitsSaving}
              className="text-xs bg-emerald-500/20 hover:bg-emerald-500/30 disabled:opacity-50 text-emerald-300 border border-emerald-500/30 px-3 py-1 rounded transition-colors flex items-center gap-1"
            >
              {limitsSaving && <Loader2 className="w-3 h-3 animate-spin" />}
              Aplicar
            </button>
            {limitsMsg && (
              <span className="text-xs text-gray-400">{limitsMsg}</span>
            )}

            {torrents.length > 0 && (
              <div className="ml-auto flex items-center gap-2">
                <button
                  onClick={onPauseAll}
                  disabled={bulkBusy}
                  className="text-xs bg-gray-700 hover:bg-gray-600 disabled:opacity-50 text-gray-200 px-3 py-1 rounded transition-colors flex items-center gap-1"
                >
                  <Pause className="w-3 h-3" />
                  Pausar todos
                </button>
                <button
                  onClick={onResumeAll}
                  disabled={bulkBusy}
                  className="text-xs bg-blue-500/20 hover:bg-blue-500/30 disabled:opacity-50 text-blue-300 border border-blue-500/30 px-3 py-1 rounded transition-colors flex items-center gap-1"
                >
                  <Play className="w-3 h-3" />
                  Retomar todos
                </button>
              </div>
            )}
          </div>

          {!torrentsLoaded ? (
            <div className="flex items-center gap-2 text-gray-400 py-6 justify-center">
              <Loader2 className="w-4 h-4 animate-spin" />
              <span className="text-sm">Carregando torrents ativos...</span>
            </div>
          ) : displayTorrents.length === 0 ? (
            <div className="text-center py-10 text-gray-500 bg-gray-800/40 border border-gray-700/50 rounded-lg">
              <Activity className="w-10 h-10 mx-auto mb-2 opacity-30" />
              <p className="text-sm">Nenhum torrent ativo no momento</p>
              <p className="text-xs mt-1 text-gray-600">
                Inicie um streaming na busca para ver os controles aqui.
              </p>
            </div>
          ) : filteredTorrents.length === 0 ? (
            <div className="text-center py-10 text-gray-500 bg-gray-800/40 border border-gray-700/50 rounded-lg">
              <Activity className="w-10 h-10 mx-auto mb-2 opacity-30" />
              <p className="text-sm">Nenhum torrent nesse filtro</p>
            </div>
          ) : (
            <div className="flex flex-col gap-3">
              {filteredTorrents.map(t => (
                <TorrentCard
                  key={t.infoHash}
                  t={t}
                  busy={busyHash === t.infoHash}
                  onPause={() => onTorrentPause(t.infoHash)}
                  onResume={() => onTorrentResume(t.infoHash)}
                  onPriority={(p) => onTorrentPriority(t.infoHash, p)}
                  onDelete={() => onTorrentDelete(t.infoHash)}
                />
              ))}
            </div>
          )}
        </section>

        {/* ───────────────── Background downloads (file queue) ───────────────── */}
        <section>
          <header className="flex items-center justify-between mb-4">
            <h2 className="text-xl font-bold text-gray-100 flex items-center gap-2">
              <DownloadIcon className="w-5 h-5 text-cyan-400" />
              Downloads em background
            </h2>
            <span className="text-xs text-gray-500">Atualiza a cada 2s</span>
          </header>

          {loading && items.length === 0 ? (
            <div className="flex items-center gap-2 text-gray-400 py-12 justify-center">
              <Loader2 className="w-5 h-5 animate-spin" />
              Carregando...
            </div>
          ) : items.length === 0 ? (
            <div className="text-center py-16 text-gray-500">
              <DownloadIcon className="w-12 h-12 mx-auto mb-3 opacity-30" />
              <p className="text-lg">Nenhum download ainda</p>
              <p className="text-sm mt-1">
                Use o botão "Background" no player para enfileirar o arquivo completo aqui.
              </p>
            </div>
          ) : (
            <div className="flex flex-col gap-3">
              {items.map(d => (
                <DownloadCard
                  key={d.id}
                  d={d}
                  busy={busyID === d.id}
                  onPause={() => onPause(d.id)}
                  onResume={() => onResume(d.id)}
                  onDelete={() => onDelete(d.id)}
                />
              ))}
            </div>
          )}
        </section>
      </main>
    </div>
  )
}

// ─────────────────────────────────────────────────────────────────────────
// TorrentCard — one row in the Transmission-style active list. Receives a
// snapshot (`t`) and emits intent callbacks; never mutates state directly.

interface TorrentCardProps {
  t: TorrentInfo
  busy: boolean
  onPause: () => void
  onResume: () => void
  onPriority: (p: StreamPriority) => void
  onDelete: () => void
}

function TorrentCard({ t, busy, onPause, onResume, onPriority, onDelete }: TorrentCardProps) {
  const pct = Math.max(0, Math.min(1, t.progress || 0)) * 100
  // Status defaults to "downloading" when the server hasn't set it explicitly.
  // The four states map to distinct colors so the user reads state at a glance.
  const status = t.status || (pct >= 100 ? 'complete' : 'downloading')
  const isPaused = status === 'paused'
  const isSeeding = status === 'seeding'
  const isComplete = status === 'complete'

  const barColor = isComplete
    ? 'bg-green-500'
    : isPaused
      ? 'bg-gray-500'
      : isSeeding
        ? 'bg-purple-500'
        : 'bg-emerald-500'

  // Linear ETA: bytes remaining / current down rate. Hidden when we have no
  // rate sample yet (first 1-2s after add) or when the torrent is already done.
  const eta = computeTorrentETA(t)

  const priority: StreamPriority = (t.priority as StreamPriority) || 'normal'

  return (
    <div className="bg-gray-800 border border-gray-700 rounded-lg p-4 flex flex-col gap-3">
      <div className="flex items-start justify-between gap-4">
        <div className="min-w-0 flex-1">
          <h3 className="font-semibold text-gray-100 truncate" title={t.name}>{t.name || t.infoHash}</h3>
          <p className="text-xs text-gray-500 truncate mt-0.5 font-mono" title={t.infoHash}>{t.infoHash}</p>
        </div>
        <TorrentStatusBadge status={status} />
      </div>

      <div>
        <div className="h-2 bg-gray-900 rounded overflow-hidden">
          <div
            className={`h-full transition-all duration-300 ${barColor}`}
            style={{ width: `${pct.toFixed(1)}%` }}
          />
        </div>
        <div className="flex items-center justify-between mt-1.5 text-xs text-gray-400 gap-3 flex-wrap">
          <span>
            {formatBytes(Math.round((t.totalSize || 0) * (t.progress || 0)))} / {formatBytes(t.totalSize)}
            {' '}({pct.toFixed(1)}%)
          </span>
          <span className="flex items-center gap-3 text-gray-400">
            <span className="flex items-center gap-1" title="Velocidade de download">
              <Zap className="w-3 h-3 text-blue-400" />
              {formatRate(t.downRate)}
            </span>
            <span className="flex items-center gap-1" title="Velocidade de upload">
              <Zap className="w-3 h-3 text-purple-400 rotate-180" />
              {formatRate(t.upRate)}
            </span>
            <span className="flex items-center gap-1" title="Peers conectados">
              <Users className="w-3 h-3" />
              {t.peers}
            </span>
            {eta && (
              <span className="flex items-center gap-1 text-gray-500" title="Tempo estimado">
                <Clock className="w-3 h-3" />
                {eta}
              </span>
            )}
          </span>
        </div>
      </div>

      <div className="flex items-center gap-2 flex-wrap">
        {isPaused ? (
          <button
            onClick={onResume}
            disabled={busy}
            className="flex items-center gap-1.5 text-xs bg-blue-500/20 hover:bg-blue-500/30 disabled:opacity-50 text-blue-300 border border-blue-500/30 px-3 py-1.5 rounded transition-colors"
          >
            <Play className="w-3.5 h-3.5" />
            Retomar
          </button>
        ) : (
          <button
            onClick={onPause}
            disabled={busy || isComplete}
            className="flex items-center gap-1.5 text-xs bg-gray-700 hover:bg-gray-600 disabled:opacity-50 text-gray-200 px-3 py-1.5 rounded transition-colors"
          >
            <Pause className="w-3.5 h-3.5" />
            Pausar
          </button>
        )}

        <label className="flex items-center gap-1.5 text-xs text-gray-400">
          <span>Prioridade:</span>
          <select
            value={priority}
            onChange={e => onPriority(e.target.value as StreamPriority)}
            disabled={busy}
            className="bg-gray-700 border border-gray-600 rounded px-2 py-1 text-gray-100 disabled:opacity-50 focus:outline-none focus:border-emerald-500"
          >
            <option value="low">Baixa</option>
            <option value="normal">Normal</option>
            <option value="high">Alta</option>
          </select>
        </label>

        {busy && <Loader2 className="w-3.5 h-3.5 animate-spin text-gray-500" />}
        <button
          onClick={onDelete}
          disabled={busy}
          className="flex items-center gap-1.5 text-xs bg-red-500/20 hover:bg-red-500/30 disabled:opacity-50 text-red-300 border border-red-500/30 px-3 py-1.5 rounded transition-colors ml-auto"
        >
          <Trash2 className="w-3.5 h-3.5" />
          Parar
        </button>
      </div>
    </div>
  )
}

function TorrentStatusBadge({ status }: { status: NonNullable<TorrentInfo['status']> }) {
  const map: Record<NonNullable<TorrentInfo['status']>, { label: string; cls: string; icon: React.ReactNode }> = {
    downloading: { label: 'Baixando',  cls: 'bg-emerald-500/20 text-emerald-300 border-emerald-500/30 border', icon: <Loader2 className="w-3 h-3 animate-spin" /> },
    paused:      { label: 'Pausado',   cls: 'bg-gray-500/20 text-gray-300 border-gray-500/30 border',          icon: <Pause className="w-3 h-3" /> },
    seeding:     { label: 'Semeando',  cls: 'bg-purple-500/20 text-purple-300 border-purple-500/30 border',    icon: <Activity className="w-3 h-3" /> },
    complete:    { label: 'Completo',  cls: 'bg-green-500/20 text-green-300 border-green-500/30 border',       icon: <CheckCircle2 className="w-3 h-3" /> },
  }
  const s = map[status]
  return (
    <span className={`inline-flex items-center gap-1 text-xs px-2 py-0.5 rounded flex-shrink-0 ${s.cls}`}>
      {s.icon}
      {s.label}
    </span>
  )
}

function computeTorrentETA(t: TorrentInfo): string {
  if (!t.totalSize || !t.downRate || t.downRate <= 0) return ''
  const done = (t.progress || 0) * t.totalSize
  const remaining = t.totalSize - done
  if (remaining <= 0) return ''
  const sec = remaining / t.downRate
  if (!isFinite(sec) || sec <= 0) return ''
  return `~${formatDurationShort(sec)}`
}

// ─────────────────────────────────────────────────────────────────────────

interface CardProps {
  d: DownloadEntry
  busy: boolean
  onPause: () => void
  onResume: () => void
  onDelete: () => void
}

function DownloadCard({ d, busy, onPause, onResume, onDelete }: CardProps) {
  const pct = Math.max(0, Math.min(1, d.progress || 0)) * 100
  const isCompleted = d.status === 'completed'
  const isFailed = d.status === 'failed'
  const isPaused = d.status === 'paused'
  const isActive = d.status === 'downloading' || d.status === 'queued'

  const barColor = isCompleted
    ? 'bg-green-500'
    : isFailed
      ? 'bg-red-500'
      : isPaused
        ? 'bg-gray-500'
        : 'bg-blue-500'

  // ETA: very rough — uses average rate since startedAt. The server doesn't
  // track per-download rate (it'd require a second sample on each tick), so
  // we settle for "linear extrapolation since start" which is good enough
  // for the user to know whether to wait or come back later.
  const etaText = computeETA(d)

  return (
    <div className="bg-gray-800 border border-gray-700 rounded-lg p-4 flex flex-col gap-3">
      <div className="flex items-start justify-between gap-4">
        <div className="min-w-0 flex-1">
          <h2 className="font-semibold text-gray-100 truncate" title={d.name}>{d.name || d.filePath}</h2>
          <p className="text-xs text-gray-500 truncate mt-0.5" title={d.filePath}>{d.filePath}</p>
        </div>
        <StatusBadge status={d.status} />
      </div>

      <div>
        <div className="h-2 bg-gray-900 rounded overflow-hidden">
          <div
            className={`h-full transition-all duration-300 ${barColor}`}
            style={{ width: `${pct.toFixed(1)}%` }}
          />
        </div>
        <div className="flex items-center justify-between mt-1.5 text-xs text-gray-400">
          <span>
            {formatBytes(d.bytesDownloaded)} / {formatBytes(d.fileSize)}
            {' '}({pct.toFixed(1)}%)
          </span>
          {!isCompleted && !isFailed && etaText && (
            <span className="flex items-center gap-1 text-gray-500">
              <Clock className="w-3 h-3" />
              {etaText}
            </span>
          )}
          {isCompleted && d.completedAt && (
            <span className="text-green-400 text-xs">Concluído</span>
          )}
        </div>
      </div>

      {isFailed && d.error && (
        <div className="flex items-start gap-2 text-xs text-red-400 bg-red-500/10 border border-red-500/20 rounded px-2 py-1.5">
          <AlertCircle className="w-3.5 h-3.5 flex-shrink-0 mt-0.5" />
          <span className="break-all">{d.error}</span>
        </div>
      )}

      <div className="flex items-center gap-2">
        {isActive && (
          <button
            onClick={onPause}
            disabled={busy}
            className="flex items-center gap-1.5 text-xs bg-gray-700 hover:bg-gray-600 disabled:opacity-50 text-gray-200 px-3 py-1.5 rounded transition-colors"
          >
            <Pause className="w-3.5 h-3.5" />
            Pausar
          </button>
        )}
        {(isPaused || isFailed) && (
          <button
            onClick={onResume}
            disabled={busy}
            className="flex items-center gap-1.5 text-xs bg-blue-500/20 hover:bg-blue-500/30 disabled:opacity-50 text-blue-300 border border-blue-500/30 px-3 py-1.5 rounded transition-colors"
          >
            <Play className="w-3.5 h-3.5" />
            {isFailed ? 'Tentar novamente' : 'Resumir'}
          </button>
        )}
        <button
          onClick={onDelete}
          disabled={busy}
          className="flex items-center gap-1.5 text-xs bg-red-500/20 hover:bg-red-500/30 disabled:opacity-50 text-red-300 border border-red-500/30 px-3 py-1.5 rounded transition-colors ml-auto"
        >
          <Trash2 className="w-3.5 h-3.5" />
          {isCompleted ? 'Remover da lista' : 'Cancelar'}
        </button>
      </div>
    </div>
  )
}

function StatusBadge({ status }: { status: DownloadEntry['status'] }) {
  const map: Record<DownloadEntry['status'], { label: string; cls: string; icon: React.ReactNode }> = {
    queued:      { label: 'Na fila',     cls: 'bg-gray-700 text-gray-300',                     icon: <Clock className="w-3 h-3" /> },
    downloading: { label: 'Baixando',    cls: 'bg-blue-500/20 text-blue-300 border-blue-500/30 border',   icon: <Loader2 className="w-3 h-3 animate-spin" /> },
    completed:   { label: 'Concluído',   cls: 'bg-green-500/20 text-green-300 border-green-500/30 border', icon: <CheckCircle2 className="w-3 h-3" /> },
    failed:      { label: 'Falhou',      cls: 'bg-red-500/20 text-red-300 border-red-500/30 border',       icon: <AlertCircle className="w-3 h-3" /> },
    paused:      { label: 'Pausado',     cls: 'bg-gray-500/20 text-gray-300 border-gray-500/30 border',    icon: <Pause className="w-3 h-3" /> },
  }
  const s = map[status]
  return (
    <span className={`inline-flex items-center gap-1 text-xs px-2 py-0.5 rounded flex-shrink-0 ${s.cls}`}>
      {s.icon}
      {s.label}
    </span>
  )
}

function computeETA(d: DownloadEntry): string {
  if (!d.startedAt || d.fileSize <= 0 || d.bytesDownloaded <= 0) return ''
  if (d.bytesDownloaded >= d.fileSize) return ''
  const startMs = new Date(d.startedAt).getTime()
  if (!isFinite(startMs) || startMs <= 0) return ''
  const elapsedSec = (Date.now() - startMs) / 1000
  if (elapsedSec < 2) return ''
  const rate = d.bytesDownloaded / elapsedSec // bytes/sec
  if (rate <= 0) return ''
  const remainingSec = (d.fileSize - d.bytesDownloaded) / rate
  if (!isFinite(remainingSec) || remainingSec <= 0) return ''
  return `~${formatDurationShort(remainingSec)} restantes`
}

function formatDurationShort(totalSeconds: number): string {
  if (totalSeconds < 60) return `${Math.ceil(totalSeconds)}s`
  if (totalSeconds < 3600) return `${Math.ceil(totalSeconds / 60)}m`
  const hours = Math.floor(totalSeconds / 3600)
  const mins = Math.floor((totalSeconds % 3600) / 60)
  return mins > 0 ? `${hours}h ${mins}m` : `${hours}h`
}
