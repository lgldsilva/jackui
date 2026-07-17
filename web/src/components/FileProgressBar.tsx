import { CheckCircle2, AlertCircle, Loader2, X, Clock } from 'lucide-react'
import { formatBytes, formatRate, formatDurationShort, formatBytesPair } from '../lib/format'

// FileProgressBar — the ONE reusable widget for any file move/copy progress:
// label, X/Y files, bytes done/total, transfer rate and ETA, plus a bar. Used by
// the global Transfers dock and reusable in upload banners / move modals so every
// flow shows the same thing. Pure/presentational — feed it numbers.

export type FileProgressBarProps = {
  readonly label: string
  readonly status?: 'queued' | 'running' | 'done' | 'failed' | 'canceled'
  readonly filesDone?: number
  readonly filesTotal?: number
  readonly bytesDone?: number
  readonly bytesTotal?: number
  readonly ratePerSec?: number
  readonly etaSeconds?: number
  readonly progress?: number // 0..1; derived from bytes/files when omitted
  readonly error?: string
  readonly onCancel?: () => void
  readonly className?: string
}

function gradientFor(status: string): string {
  if (status === 'done') return 'from-green-500 to-emerald-400'
  if (status === 'failed' || status === 'canceled') return 'from-red-500 to-rose-400'
  if (status === 'queued') return 'from-gray-500 to-gray-400'
  return 'from-amber-500 to-yellow-400'
}

function deriveProgress(p: FileProgressBarProps): number {
  if (typeof p.progress === 'number') return Math.max(0, Math.min(1, p.progress))
  if (p.bytesTotal && p.bytesTotal > 0) return Math.min(1, (p.bytesDone ?? 0) / p.bytesTotal)
  if (p.filesTotal && p.filesTotal > 0) return Math.min(1, (p.filesDone ?? 0) / p.filesTotal)
  return 0
}

function StatusIcon({ status }: { readonly status: string }) {
  if (status === 'done') return <CheckCircle2 className="w-3.5 h-3.5 text-emerald-400 flex-shrink-0" />
  if (status === 'failed') return <AlertCircle className="w-3.5 h-3.5 text-red-400 flex-shrink-0" />
  if (status === 'queued') return <Clock className="w-3.5 h-3.5 text-text-muted flex-shrink-0" />
  return <Loader2 className="w-3.5 h-3.5 text-amber-400 animate-spin flex-shrink-0" />
}

function StatusDetail({
  status,
  error,
  ratePerSec,
  eta,
}: {
  readonly status: string
  readonly error?: string
  readonly ratePerSec: number
  readonly eta: string
}) {
  if (status === 'failed' && error) {
    return <span className="text-red-400 truncate ml-2" title={error}>{error}</span>
  }
  if (status === 'queued') {
    return <span className="ml-2">Na fila…</span>
  }
  return (
    <span className="flex items-center gap-2">
      {status === 'running' && ratePerSec > 0 && <span>{formatRate(ratePerSec)}</span>}
      {eta && <span>· {eta}</span>}
    </span>
  )
}

export default function FileProgressBar(props: FileProgressBarProps) {
  const { label, status = 'running', filesDone = 0, filesTotal = 0, bytesDone = 0, bytesTotal = 0, ratePerSec = 0, etaSeconds = 0, error, onCancel, className = '' } = props
  const pct = deriveProgress(props) * 100
  const eta = status === 'running' ? formatDurationShort(etaSeconds) : ''

  return (
    <div className={`flex flex-col gap-1.5 ${className}`}>
      <div className="flex items-center gap-2">
        <StatusIcon status={status} />
        <span className="text-xs font-medium text-text-primary truncate flex-1" title={label}>{label}</span>
        {filesTotal > 0 && (
          <span className="text-[10px] text-text-muted tabular-nums flex-shrink-0">{filesDone}/{filesTotal}</span>
        )}
        {onCancel && (status === 'running' || status === 'queued') && (
          <button onClick={onCancel} title="Cancelar" className="text-text-muted hover:text-red-400 transition-colors flex-shrink-0">
            <X className="w-3.5 h-3.5" />
          </button>
        )}
      </div>
      <div className="h-1.5 bg-surface-tertiary dark:bg-surface/80 rounded-full overflow-hidden">
        <div
          className={`h-full rounded-full bg-gradient-to-r ${gradientFor(status)} transition-all duration-500 ease-out`}
          style={{ width: `${pct.toFixed(1)}%` }}
        />
      </div>
      <div className="flex items-center justify-between text-[10px] text-text-muted tabular-nums">
        <span>
          {bytesTotal > 0 ? formatBytesPair(bytesDone, bytesTotal) : formatBytes(bytesDone)}
          <span className="ml-1">({pct.toFixed(0)}%)</span>
        </span>
        <StatusDetail status={status} error={error} ratePerSec={ratePerSec} eta={eta} />
      </div>
    </div>
  )
}
