import { ArrowUpCircle } from 'lucide-react'
import { DownloadPriority } from '../../api/client'

// PriorityBadge shows the queue priority on a download card. Hidden for the
// default (normal) so it only draws attention when the user has tuned it.
export function PriorityBadge({ priority }: { readonly priority?: DownloadPriority }) {
  if (!priority || priority === 'normal') return null
  const cls = priority === 'high'
    ? 'bg-orange-500/15 text-orange-700 dark:text-orange-300 border-orange-500/30'
    : 'bg-blue-500/15 text-blue-700 dark:text-blue-300 border-blue-500/30'
  return (
    <span className={`inline-flex items-center gap-1 text-[10px] px-2 py-0.5 rounded-md border font-medium ${cls}`} title="Prioridade na fila">
      <ArrowUpCircle className="w-3 h-3" />{priority === 'high' ? 'Alta' : 'Baixa'}
    </span>
  )
}
