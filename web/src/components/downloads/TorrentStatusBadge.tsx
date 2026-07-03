import { ArrowUpCircle, CheckCircle2, Loader2, Pause } from 'lucide-react'
import { TorrentInfo } from '../../api/client'

// TorrentStatusBadge — pílula de status de um torrent de streaming (baixando/
// pausado/semeando/completo).
export function TorrentStatusBadge({ status }: { readonly status: NonNullable<TorrentInfo['status']> }) {
  const map: Record<NonNullable<TorrentInfo['status']>, { label: string; cls: string; icon: React.ReactNode }> = {
    downloading: { label: 'Baixando',  cls: 'bg-emerald-500/15 text-emerald-700 dark:text-emerald-300 border-emerald-500/30', icon: <Loader2 className="w-3 h-3 animate-spin" /> },
    paused:      { label: 'Pausado',   cls: 'bg-gray-500/15 text-text-primary border-strong/30',          icon: <Pause className="w-3 h-3" /> },
    seeding:     { label: 'Semeando',  cls: 'bg-violet-500/15 text-violet-700 dark:text-violet-300 border-violet-500/30',    icon: <ArrowUpCircle className="w-3 h-3" /> },
    complete:    { label: 'Completo',  cls: 'bg-green-500/15 text-green-700 dark:text-green-300 border-green-500/30',       icon: <CheckCircle2 className="w-3 h-3" /> },
  }
  const s = map[status]
  return (
    <span className={`inline-flex items-center gap-1 text-[10px] px-2 py-0.5 rounded-md border font-medium ${s.cls}`}>
      {s.icon} {s.label}
    </span>
  )
}
