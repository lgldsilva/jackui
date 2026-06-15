import { ArrowUpNarrowWide, ArrowDownWideNarrow } from 'lucide-react'
import { SortKey, SortDir, SORT_LABELS } from '../lib/favSort'

type Props = {
  readonly sortBy: SortKey
  readonly sortDir: SortDir
  readonly onSortBy: (k: SortKey) => void
  readonly onToggleDir: () => void
}

// Sort criterion (date/name/seeds/size) + direction toggle for the favorites
// list. Kept out of FavoritesPage to avoid fattening that god-component.
export default function FavoritesSortControl({ sortBy, sortDir, onSortBy, onToggleDir }: Props) {
  return (
    <>
      <select
        value={sortBy}
        onChange={e => onSortBy(e.target.value as SortKey)}
        title="Ordenar por"
        className="text-xs bg-surface-secondary border border-default rounded-lg px-2 py-2 text-text-primary focus:outline-none focus:border-pink-500 cursor-pointer flex-shrink-0"
      >
        {(Object.keys(SORT_LABELS) as SortKey[]).map(k => (
          <option key={k} value={k}>{SORT_LABELS[k]}</option>
        ))}
      </select>
      <button
        onClick={onToggleDir}
        title={sortDir === 'asc' ? 'Crescente — clique p/ decrescente' : 'Decrescente — clique p/ crescente'}
        className="flex items-center justify-center text-xs bg-surface-tertiary hover:bg-surface-tertiary text-text-primary px-2.5 py-2 rounded-lg transition-colors flex-shrink-0"
      >
        {sortDir === 'asc' ? <ArrowUpNarrowWide className="w-4 h-4" /> : <ArrowDownWideNarrow className="w-4 h-4" />}
      </button>
    </>
  )
}
