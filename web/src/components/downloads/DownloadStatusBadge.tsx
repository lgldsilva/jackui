import { AlertCircle, CheckCircle2, Clock, Loader2, Pause } from 'lucide-react'
import { useTranslation } from 'react-i18next'
import { DownloadEntry } from '../../api/client'

// DownloadStatusBadge — pílula de status de um background download (fila/baixando/
// movendo/concluído/falhou/pausado).
export function DownloadStatusBadge({ status }: { readonly status: DownloadEntry['status'] }) {
  const { t } = useTranslation()
  const map: Record<DownloadEntry['status'], { label: string; cls: string; icon: React.ReactNode }> = {
    queued:      { label: t('downloads.page.statusQueued'),      cls: 'bg-surface-tertiary/50 text-text-primary border-strong/50',         icon: <Clock className="w-3 h-3" /> },
    downloading: { label: t('downloads.page.statusDownloading'), cls: 'bg-cyan-500/15 text-cyan-700 dark:text-cyan-300 border-cyan-500/30',         icon: <Loader2 className="w-3 h-3 animate-spin" /> },
    moving:      { label: t('downloads.page.statusMoving'),      cls: 'bg-amber-500/15 text-amber-700 dark:text-amber-300 border-amber-500/30',      icon: <Loader2 className="w-3 h-3 animate-spin" /> },
    completed:   { label: t('downloads.page.statusCompleted'),   cls: 'bg-green-500/15 text-green-700 dark:text-green-300 border-green-500/30',      icon: <CheckCircle2 className="w-3 h-3" /> },
    failed:      { label: t('downloads.page.statusFailed'),      cls: 'bg-red-500/15 text-red-700 dark:text-red-300 border-red-500/30',            icon: <AlertCircle className="w-3 h-3" /> },
    paused:      { label: t('downloads.page.statusPaused'),      cls: 'bg-gray-500/15 text-text-primary border-strong/30',         icon: <Pause className="w-3 h-3" /> },
  }
  const s = map[status]
  return (
    <span className={`inline-flex items-center gap-1 text-[10px] px-2 py-0.5 rounded-md border font-medium ${s.cls}`}>
      {s.icon} {s.label}
    </span>
  )
}
