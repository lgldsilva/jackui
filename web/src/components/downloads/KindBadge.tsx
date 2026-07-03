import { Activity, Server } from 'lucide-react'

// KindBadge — distingue um card de streaming (torrent puro) de um de servidor
// (background download).
export function KindBadge({ kind }: { readonly kind: 'streaming' | 'server' }) {
  if (kind === 'streaming') {
    return (
      <span className="inline-flex items-center gap-1 text-[10px] font-bold uppercase tracking-wider px-2 py-0.5 rounded-md bg-gradient-to-r from-emerald-500/20 to-teal-500/20 text-emerald-700 dark:text-emerald-300 border border-emerald-500/30">
        <Activity className="w-2.5 h-2.5" />
        Streaming
      </span>
    )
  }
  return (
    <span className="inline-flex items-center gap-1 text-[10px] font-bold uppercase tracking-wider px-2 py-0.5 rounded-md bg-gradient-to-r from-cyan-500/20 to-blue-500/20 text-cyan-700 dark:text-cyan-300 border border-cyan-500/30">
      <Server className="w-2.5 h-2.5" />
      Servidor
    </span>
  )
}
