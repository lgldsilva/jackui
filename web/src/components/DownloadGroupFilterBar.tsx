import { ArrowDownWideNarrow, ArrowUpWideNarrow } from 'lucide-react'
import type {
  GroupFileStatusFilter,
  GroupFileSortKey,
  GroupFileSortDir,
} from '../lib/groupFileView'

// Barra de filtro/ordenação DENTRO de um torrent multi-arquivo expandido. Só é
// montada para grupos com 2+ arquivos (um arquivo único não tem o que ordenar).
// Lógica pura mora em lib/groupFileView; aqui é só a UI dos controles.
export default function DownloadGroupFilterBar({
  counts, statusFilter, onStatusFilter, sortKey, sortDir, onSort,
}: Readonly<{
  counts: { all: number; active: number; completed: number }
  statusFilter: GroupFileStatusFilter
  onStatusFilter: (f: GroupFileStatusFilter) => void
  sortKey: GroupFileSortKey
  sortDir: GroupFileSortDir
  onSort: (key: GroupFileSortKey) => void
}>) {
  const chips: { key: GroupFileStatusFilter; label: string; n: number }[] = [
    { key: 'all', label: 'Todos', n: counts.all },
    { key: 'active', label: 'Baixando', n: counts.active },
    { key: 'completed', label: 'Concluídos', n: counts.completed },
  ]
  const sorts: { key: GroupFileSortKey; label: string }[] = [
    { key: 'name', label: 'Nome' },
    { key: 'size', label: 'Tamanho' },
    { key: 'progress', label: 'Progresso' },
  ]
  const DirIcon = sortDir === 'asc' ? ArrowUpWideNarrow : ArrowDownWideNarrow

  return (
    <div className="flex items-center gap-2 flex-wrap pb-2 mb-1 border-b border-default/40 text-[11px]">
      {/* Filtro de status */}
      <div className="flex items-center gap-1">
        {chips.map((c) => (
          <button
            key={c.key}
            onClick={() => onStatusFilter(c.key)}
            className={`px-2 py-0.5 rounded-full font-medium transition-colors ${
              statusFilter === c.key
                ? 'bg-cyan-500/20 text-cyan-700 dark:text-cyan-300 border border-cyan-500/30'
                : 'bg-surface-tertiary text-text-secondary border border-transparent hover:border-strong'
            }`}
          >
            {c.label} <span className="opacity-70 tabular-nums">{c.n}</span>
          </button>
        ))}
      </div>

      <span className="text-text-muted ml-auto">Ordenar:</span>
      {/* Ordenação: clicar na chave ativa alterna a direção */}
      <div className="flex items-center gap-1">
        {sorts.map((s) => (
          <button
            key={s.key}
            onClick={() => onSort(s.key)}
            title={sortKey === s.key ? 'Inverter ordem' : `Ordenar por ${s.label}`}
            className={`inline-flex items-center gap-0.5 px-2 py-0.5 rounded-full font-medium transition-colors ${
              sortKey === s.key
                ? 'bg-cyan-500/20 text-cyan-700 dark:text-cyan-300 border border-cyan-500/30'
                : 'bg-surface-tertiary text-text-secondary border border-transparent hover:border-strong'
            }`}
          >
            {s.label}
            {sortKey === s.key && <DirIcon className="w-3 h-3" />}
          </button>
        ))}
      </div>
    </div>
  )
}
