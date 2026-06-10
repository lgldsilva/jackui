import { forwardRef } from 'react'
import { Search, X } from 'lucide-react'
import { useTranslation } from 'react-i18next'
import { Indexer } from '../api/client'
import IndexerMultiSelect from './IndexerMultiSelect'

type SearchBarProps = {
  readonly query: string
  readonly onQueryChange: (q: string) => void
  readonly selectedIndexers: string[]
  readonly onIndexersChange: (indexers: string[]) => void
  readonly selectedCategory: string
  readonly onCategoryChange: (category: string) => void
  readonly indexers: Indexer[]
  readonly onSearch: () => void
  // Abort the in-flight search (closes the SSE → backend cancels the indexers).
  readonly onStop: () => void
  readonly loading: boolean
  // Past queries (server-side history) surfaced as native autocomplete.
  readonly suggestions?: readonly string[]
}

const CATEGORIES = [
  { value: 'all', labelKey: 'search.categories.all' },
  { value: '2000', labelKey: 'search.categories.movies' },
  { value: '5000', labelKey: 'search.categories.series' },
  { value: '3000', labelKey: 'search.categories.music' },
  { value: '4000', labelKey: 'search.categories.games' },
  { value: '4500', labelKey: 'search.categories.software' },
  { value: '6000', labelKey: 'search.categories.adult' },
  { value: '7000', labelKey: 'search.categories.others' },
]

const SearchBar = forwardRef<HTMLInputElement, SearchBarProps>(function SearchBar({
  query,
  onQueryChange,
  selectedIndexers,
  onIndexersChange,
  selectedCategory,
  onCategoryChange,
  indexers,
  onSearch,
  onStop,
  loading,
  suggestions,
}, ref) {
  const { t } = useTranslation()

  const handleKeyDown = (e: React.KeyboardEvent) => {
    if (e.key === 'Enter') {
      onSearch()
    }
  }

  return (
    <div className="flex flex-col gap-3 w-full">
      <div className="flex gap-2">
        <div className="relative flex-1">
          <Search className="absolute left-3 top-1/2 -translate-y-1/2 text-text-secondary w-5 h-5" />
          <input
            ref={ref}
            type="text"
            value={query}
            onChange={(e) => onQueryChange(e.target.value)}
            onKeyDown={handleKeyDown}
            placeholder={t('search.placeholder')}
            className="input-field pl-10 text-lg py-3"
            autoFocus
            list={suggestions && suggestions.length > 0 ? 'search-suggestions' : undefined}
          />
          {suggestions && suggestions.length > 0 && (
            <datalist id="search-suggestions">
              {suggestions.map(s => <option key={s} value={s} />)}
            </datalist>
          )}
        </div>
        {loading ? (
          // While searching, the same button becomes "Parar" — clicking closes
          // the SSE (handleSearch.onStop), which cancels the request context on
          // the backend so the indexers stop being polled.
          <button
            onClick={onStop}
            title={t('search.stop_title')}
            className="px-6 py-3 text-lg rounded-lg font-medium flex items-center gap-2 bg-red-600 text-white hover:bg-red-500 transition-colors"
          >
            <X className="w-5 h-5" />
            {t('search.stop')}
          </button>
        ) : (
          <button
            onClick={onSearch}
            disabled={!query.trim()}
            className="btn-primary px-6 py-3 text-lg disabled:opacity-50 disabled:cursor-not-allowed flex items-center gap-2"
          >
            <Search className="w-5 h-5" />
            {t('nav.search')}
          </button>
        )}
      </div>

      <div className="flex gap-3">
        <div className="flex-1">
          <IndexerMultiSelect
            selected={selectedIndexers}
            onChange={onIndexersChange}
            indexers={indexers}
          />
        </div>

        <div className="flex-1">
          <select
            value={selectedCategory}
            onChange={(e) => onCategoryChange(e.target.value)}
            className="input-field"
          >
            {CATEGORIES.map((cat) => (
              <option key={cat.value} value={cat.value}>
                {t(cat.labelKey)}
              </option>
            ))}
          </select>
        </div>
      </div>
    </div>
  )
})

export default SearchBar
