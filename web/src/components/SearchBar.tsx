import { forwardRef } from 'react'
import { Search } from 'lucide-react'
import { Indexer } from '../api/client'
import IndexerMultiSelect from './IndexerMultiSelect'

interface SearchBarProps {
  readonly query: string
  readonly onQueryChange: (q: string) => void
  readonly selectedIndexers: string[]
  readonly onIndexersChange: (indexers: string[]) => void
  readonly selectedCategory: string
  readonly onCategoryChange: (category: string) => void
  readonly indexers: Indexer[]
  readonly onSearch: () => void
  readonly loading: boolean
}

const CATEGORIES = [
  { value: 'all', label: 'Todos' },
  { value: '2000', label: 'Filmes' },
  { value: '5000', label: 'Series' },
  { value: '3000', label: 'Musica' },
  { value: '4000', label: 'Jogos' },
  { value: '4500', label: 'Software' },
  { value: '6000', label: 'Adulto' },
  { value: '7000', label: 'Outros' },
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
  loading,
}, ref) {
  const handleKeyDown = (e: React.KeyboardEvent) => {
    if (e.key === 'Enter') {
      onSearch()
    }
  }

  return (
    <div className="flex flex-col gap-3 w-full">
      <div className="flex gap-2">
        <div className="relative flex-1">
          <Search className="absolute left-3 top-1/2 -translate-y-1/2 text-gray-400 w-5 h-5" />
          <input
            ref={ref}
            type="text"
            value={query}
            onChange={(e) => onQueryChange(e.target.value)}
            onKeyDown={handleKeyDown}
            placeholder="Buscar torrents... (pressione / para focar)"
            className="input-field pl-10 text-lg py-3"
            autoFocus
          />
        </div>
        <button
          onClick={onSearch}
          disabled={loading || !query.trim()}
          className="btn-primary px-6 py-3 text-lg disabled:opacity-50 disabled:cursor-not-allowed flex items-center gap-2"
        >
          {loading ? (
            <div className="w-5 h-5 border-2 border-white border-t-transparent rounded-full animate-spin" />
          ) : (
            <Search className="w-5 h-5" />
          )}
          Buscar
        </button>
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
                {cat.label}
              </option>
            ))}
          </select>
        </div>
      </div>
    </div>
  )
})

export default SearchBar
