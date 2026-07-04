import { AlertCircle, CheckCircle2, Clock, Loader2, Pause } from 'lucide-react'
import { DownloadEntry } from '../../api/client'

// DownloadStatusBadge — pílula de status de um background download (fila/baixando/
// movendo/concluído/falhou/pausado).
export function DownloadStatusBadge({ status }: { readonly status: DownloadEntry['status'] }) {
  const map: Record<DownloadEntry['status'], { label: string; cls: string; icon: React.ReactNode }> = {
    queued:      { label: 'Na fila',     cls: 'bg-surface-tertiary/50 text-text-primary border-strong/50',         icon: <Clock className="w-3 h-3" /> },
    downloading: { label: 'Baixando',    cls: 'bg-cyan-500/15 text-cyan-700 dark:text-cyan-300 border-cyan-500/30',         icon: <Loader2 className="w-3 h-3 animate-spin" /> },
    moving:      { label: 'Movendo',     cls: 'bg-amber-500/15 text-amber-700 dark:text-amber-300 border-amber-500/30',      icon: <Loader2 className="w-3 h-3 animate-spin" /> },
    completed:   { label: 'Concluído',   cls: 'bg-green-500/15 text-green-700 dark:text-green-300 border-green-500/30',      icon: <CheckCircle2 className="w-3 h-3" /> },
    failed:      { label: 'Falhou',      cls: 'bg-red-500/15 text-red-700 dark:text-red-300 border-red-500/30',            icon: <AlertCircle className="w-3 h-3" /> },
    paused:      { label: 'Pausado',     cls: 'bg-gray-500/15 text-text-primary border-strong/30',         icon: <Pause className="w-3 h-3" /> },
  }
  const s = map[status]
  return (
    <span className={`inline-flex items-center gap-1 text-[10px] px-2 py-0.5 rounded-md border font-medium ${s.cls}`}>
      {s.icon} {s.label}
    </span>
  )
}
