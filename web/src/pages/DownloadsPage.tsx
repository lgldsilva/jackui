import { useEffect, useRef, useState } from 'react'
import {
  Download as DownloadIcon, Loader2, Pause, Play, Trash2, CheckCircle2, AlertCircle, Clock,
} from 'lucide-react'
import NavHeader from '../components/NavHeader'
import {
  DownloadEntry, downloadsList, downloadDelete, downloadPause, downloadResume,
} from '../api/client'
import { formatBytes } from '../lib/format'

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

  useEffect(() => {
    mountedRef.current = true
    load()
    const t = setInterval(load, 2000)
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

  return (
    <div className="min-h-screen bg-gray-900">
      <NavHeader />
      <main className="max-w-5xl mx-auto px-4 py-6">
        <header className="flex items-center justify-between mb-6">
          <h1 className="text-2xl font-bold text-gray-100 flex items-center gap-2">
            <DownloadIcon className="w-6 h-6 text-cyan-400" />
            Downloads em background
          </h1>
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
      </main>
    </div>
  )
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
