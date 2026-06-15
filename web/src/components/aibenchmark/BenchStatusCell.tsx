import { CheckCircle2, XCircle, AlertTriangle, MinusCircle, type LucideIcon } from 'lucide-react'
import type { AISlotScore } from '../../api/client'
import { needsRerun } from './benchSort'
import { runStatus, lastSuccessLabel, persistenceLabel, absoluteDateTime, type RunStatus } from './benchHistory'

const STATUS_META: Record<RunStatus, { label: string; cls: string; Icon: LucideIcon }> = {
  ok: { label: 'OK', cls: 'text-green-400', Icon: CheckCircle2 },
  error: { label: 'erro', cls: 'text-red-400', Icon: XCircle },
  incomplete: { label: 'incompleto', cls: 'text-amber-400', Icon: AlertTriangle },
  unknown: { label: '—', cls: 'text-text-muted', Icon: MinusCircle },
}

// BenchStatusCell answers, at a glance, the three things the run history adds:
// did the last run succeed or error (colored status), did the error persist
// ("erro persiste: N falhas desde …"), and when did it last succeed ("último OK:
// …"). Shared by the desktop table row and the mobile card so both stay in sync.
export default function BenchStatusCell({ s }: Readonly<{ s: AISlotScore }>) {
  const status = runStatus(s)
  const { label, cls, Icon } = STATUS_META[status]
  const persist = persistenceLabel(s)
  const lastOK = lastSuccessLabel(s)
  // Prefer the live failure (current results row); fall back to the durable
  // last_error from history, which survives the SaveResults re-baseline.
  const errText = s.failureReason || s.lastError
  // "faltante" is the re-runnable hint; show it next to the status unless the
  // status already IS incomplete (which means the same thing).
  const showFaltante = needsRerun(s) && status !== 'incomplete'
  return (
    <div className="flex flex-col gap-0.5 text-xs">
      <span className={`inline-flex items-center gap-1 ${cls}`} title={absoluteDateTime(s.lastRunAt)}>
        <Icon className="w-3.5 h-3.5 shrink-0" />
        <span>{label}</span>
        {showFaltante && <span className="text-amber-400">· faltante</span>}
      </span>
      {persist && (
        <span className="text-red-400/90 break-words" title={absoluteDateTime(s.firstFailureAt)}>{persist}</span>
      )}
      {lastOK && (
        <span className="text-text-muted" title={absoluteDateTime(s.lastSuccessAt)}>{lastOK}</span>
      )}
      {errText && (
        <span className="text-text-muted break-words" title={errText}>{errText}</span>
      )}
    </div>
  )
}
