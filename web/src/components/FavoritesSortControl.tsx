import { ArrowUpNarrowWide, ArrowDownWideNarrow } from 'lucide-react'
import { useTranslation } from 'react-i18next'
import { SortKey, SortDir } from '../lib/favSort'

// i18n keys per sort criterion — the labels live in the locale files (see
// favorites.sort*), SORT_LABELS in lib/favSort is only the canonical key order.
const SORT_LABEL_KEYS: Record<SortKey, string> = {
  date: 'favorites.sortDate',
  name: 'favorites.sortName',
  seeds: 'favorites.sortSeeds',
  size: 'favorites.sortSize',
}

type Props = {
  readonly sortBy: SortKey
  readonly sortDir: SortDir
  readonly onSortBy: (k: SortKey) => void
  readonly onToggleDir: () => void
}

// Sort criterion (date/name/seeds/size) + direction toggle for the favorites
// list. Kept out of FavoritesPage to avoid fattening that god-component.
export default function FavoritesSortControl({ sortBy, sortDir, onSortBy, onToggleDir }: Props) {
  const { t } = useTranslation()
  return (
    <>
      <select
        value={sortBy}
        onChange={e => onSortBy(e.target.value as SortKey)}
        title={t('favorites.sortBy')}
        className="text-xs bg-surface-secondary border border-default rounded-lg px-2 py-2 text-text-primary focus:outline-none focus:border-pink-500 cursor-pointer flex-shrink-0"
      >
        {(Object.keys(SORT_LABEL_KEYS) as SortKey[]).map(k => (
          <option key={k} value={k}>{t(SORT_LABEL_KEYS[k])}</option>
        ))}
      </select>
      <button
        onClick={onToggleDir}
        title={sortDir === 'asc' ? t('favorites.sortDirAscTitle') : t('favorites.sortDirDescTitle')}
        className="flex items-center justify-center text-xs bg-surface-tertiary hover:bg-surface-tertiary text-text-primary px-2.5 py-2 rounded-lg transition-colors flex-shrink-0"
      >
        {sortDir === 'asc' ? <ArrowUpNarrowWide className="w-4 h-4" /> : <ArrowDownWideNarrow className="w-4 h-4" />}
      </button>
    </>
  )
}
