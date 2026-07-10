import { SortAsc, SortDesc } from 'lucide-react'
import { ResultSortKey, SortDef } from './types'

export function ResultSortButtons({
  sort, sortAsc, onChange, defs, className,
}: {
  readonly sort: ResultSortKey
  readonly sortAsc: boolean
  readonly onChange: (key: ResultSortKey, asc: boolean) => void
  readonly defs: readonly SortDef[]
  readonly className?: string
}) {
  return (
    <div className={className ?? 'flex items-center gap-1 bg-surface-tertiary border border-strong rounded-lg p-1'}>
      {defs.map(({ key, label }) => (
        <button
          key={key}
          onClick={() => {
            if (sort === key) onChange(key, !sortAsc)
            else onChange(key, false)
          }}
          className={`flex items-center gap-1 text-xs px-2.5 py-1 rounded-md transition-colors ${
            sort === key ? 'bg-green-500/20 text-green-400' : 'text-text-secondary hover:text-text-primary'
          }`}
        >
          {label}{sort === key && (sortAsc ? <SortAsc className="w-3 h-3" /> : <SortDesc className="w-3 h-3" />)}
        </button>
      ))}
    </div>
  )
}
