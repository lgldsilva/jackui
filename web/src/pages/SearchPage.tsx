import { useState, useEffect } from 'react'
import { Link } from 'react-router-dom'
import { Settings, SearchX } from 'lucide-react'
import SearchBar from '../components/SearchBar'
import ResultCard from '../components/ResultCard'
import DownloadModal from '../components/DownloadModal'
import { SearchResult, Indexer, getIndexers, searchTorrents } from '../api/client'

function SkeletonCard() {
  return (
    <div className="card animate-pulse flex flex-col gap-3">
      <div className="h-4 bg-gray-700 rounded w-3/4" />
      <div className="h-3 bg-gray-700 rounded w-1/4" />
      <div className="grid grid-cols-2 gap-2">
        <div className="h-3 bg-gray-700 rounded" />
        <div className="h-3 bg-gray-700 rounded" />
        <div className="h-3 bg-gray-700 rounded" />
        <div className="h-3 bg-gray-700 rounded" />
      </div>
      <div className="flex gap-2 pt-1 border-t border-gray-700">
        <div className="h-7 bg-gray-700 rounded flex-1" />
        <div className="h-7 bg-gray-700 rounded flex-1" />
      </div>
    </div>
  )
}

export default function SearchPage() {
  const [query, setQuery] = useState('')
  const [results, setResults] = useState<SearchResult[]>([])
  const [indexers, setIndexers] = useState<Indexer[]>([])
  const [selectedIndexers, setSelectedIndexers] = useState<string[]>([])
  const [selectedCategory, setSelectedCategory] = useState('all')
  const [loading, setLoading] = useState(false)
  const [searched, setSearched] = useState(false)
  const [error, setError] = useState('')
  const [downloadTarget, setDownloadTarget] = useState<SearchResult | null>(null)

  useEffect(() => {
    getIndexers()
      .then(setIndexers)
      .catch(() => {
        // Silently fail — indexers list is optional
      })
  }, [])

  const handleSearch = async () => {
    if (!query.trim()) return

    setLoading(true)
    setError('')
    setSearched(true)

    try {
      const data = await searchTorrents(query, selectedIndexers, selectedCategory)
      setResults(data || [])
    } catch (err: unknown) {
      const errorMessage = err instanceof Error ? err.message : 'Erro ao buscar torrents'
      setError(errorMessage)
      setResults([])
    } finally {
      setLoading(false)
    }
  }

  return (
    <div className="min-h-screen bg-gray-900 flex flex-col">
      {/* Header */}
      <header className="bg-gray-800 border-b border-gray-700 px-4 py-3">
        <div className="max-w-7xl mx-auto flex items-center justify-between">
          <div className="flex items-center gap-2">
            <span className="text-2xl font-bold text-green-500">Jack</span>
            <span className="text-2xl font-bold text-gray-100">UI</span>
            <span className="text-xs text-gray-500 ml-1 bg-gray-700 px-2 py-0.5 rounded-full">
              Jackett UI
            </span>
          </div>
          <Link
            to="/settings"
            className="flex items-center gap-2 text-gray-400 hover:text-gray-100 transition-colors text-sm"
          >
            <Settings className="w-4 h-4" />
            Configuracoes
          </Link>
        </div>
      </header>

      {/* Main content */}
      <main className="flex-1 max-w-7xl mx-auto w-full px-4 py-6 flex flex-col gap-6">
        {/* Search bar */}
        <SearchBar
          query={query}
          onQueryChange={setQuery}
          selectedIndexers={selectedIndexers}
          onIndexersChange={setSelectedIndexers}
          selectedCategory={selectedCategory}
          onCategoryChange={setSelectedCategory}
          indexers={indexers}
          onSearch={handleSearch}
          loading={loading}
        />

        {/* Results count */}
        {searched && !loading && results.length > 0 && (
          <p className="text-sm text-gray-400">
            {results.length} resultado{results.length !== 1 ? 's' : ''} para{' '}
            <span className="text-gray-200 font-medium">"{query}"</span>
          </p>
        )}

        {/* Error */}
        {error && (
          <div className="bg-red-500/10 border border-red-500/30 text-red-400 rounded-xl p-4">
            <p className="font-medium">Erro na busca</p>
            <p className="text-sm mt-1">{error}</p>
          </div>
        )}

        {/* Loading skeletons */}
        {loading && (
          <div className="grid grid-cols-1 md:grid-cols-2 lg:grid-cols-3 gap-4">
            {Array.from({ length: 9 }).map((_, i) => (
              <SkeletonCard key={i} />
            ))}
          </div>
        )}

        {/* Results grid */}
        {!loading && results.length > 0 && (
          <div className="grid grid-cols-1 md:grid-cols-2 lg:grid-cols-3 gap-4">
            {results.map((result, i) => (
              <ResultCard
                key={`${result.infoHash || result.link}-${i}`}
                result={result}
                onDownload={setDownloadTarget}
              />
            ))}
          </div>
        )}

        {/* Empty state */}
        {!loading && searched && results.length === 0 && !error && (
          <div className="flex flex-col items-center justify-center py-20 text-gray-500">
            <SearchX className="w-16 h-16 mb-4 opacity-30" />
            <p className="text-xl font-medium">Nenhum resultado encontrado</p>
            <p className="text-sm mt-2">Tente termos diferentes ou outros indexers</p>
          </div>
        )}

        {/* Initial state */}
        {!loading && !searched && (
          <div className="flex flex-col items-center justify-center py-20 text-gray-600">
            <p className="text-lg">Digite algo para buscar torrents</p>
          </div>
        )}
      </main>

      {/* Download modal */}
      <DownloadModal
        result={downloadTarget}
        onClose={() => setDownloadTarget(null)}
      />
    </div>
  )
}
