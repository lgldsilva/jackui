import { useEffect, useState } from 'react'
import { useNavigate } from 'react-router-dom'
import { Flame, Loader2, Search, Star, Film, Tv } from 'lucide-react'
import NavHeader from '../components/NavHeader'
import { tmdbTrending, TmdbMatch } from '../api/client'

// DiscoverPage surfaces TMDB's weekly trending movies + shows so the user has a
// starting point when they don't know what to search. Clicking a poster seeds a
// search (title + year) — the existing pipeline takes it from there. Read-only
// and best-effort: with no TMDB key it shows a hint instead of erroring.

type Filter = 'all' | 'movie' | 'tv'

export default function DiscoverPage() {
  const [items, setItems] = useState<TmdbMatch[] | null>(null)
  const [filter, setFilter] = useState<Filter>('all')
  const navigate = useNavigate()

  useEffect(() => {
    tmdbTrending().then(setItems).catch(() => setItems([]))
  }, [])

  const openSearch = (m: TmdbMatch) => {
    const q = m.year ? `${m.title} ${m.year}` : m.title
    navigate(`/?q=${encodeURIComponent(q)}`)
  }

  const shown = (items || []).filter(m => filter === 'all' || m.kind === filter)

  return (
    <div className="min-h-screen bg-gray-900 flex flex-col">
      <NavHeader />
      <main className="flex-1 max-w-7xl 2xl:max-w-[min(95vw,1600px)] mx-auto w-full px-4 py-6 flex flex-col gap-4">
        <div className="flex items-center justify-between flex-wrap gap-2">
          <h1 className="text-xl font-semibold text-gray-100 flex items-center gap-2">
            <Flame className="w-5 h-5 text-orange-400" /> Em alta
          </h1>
          <div className="flex items-center gap-1 text-xs">
            {(['all', 'movie', 'tv'] as Filter[]).map(f => (
              <button
                key={f}
                onClick={() => setFilter(f)}
                className={filter === f ? 'btn-primary' : 'btn-secondary'}
              >
                {{ all: 'Tudo', movie: 'Filmes', tv: 'Séries' }[f]}
              </button>
            ))}
          </div>
        </div>

        {items === null ? (
          <div className="flex justify-center py-20"><Loader2 className="w-8 h-8 animate-spin text-gray-500" /></div>
        ) : items.length === 0 ? (
          <div className="text-center py-20 text-gray-500">
            <Flame className="w-16 h-16 mx-auto mb-4 opacity-30" />
            <p>Nada pra mostrar</p>
            <p className="text-xs mt-2">Configure a <code className="text-gray-400">tmdb.api_key</code> (ou <code className="text-gray-400">TMDB_API_KEY</code>) pra ver os títulos em alta.</p>
          </div>
        ) : (
          <div className="grid grid-cols-3 sm:grid-cols-4 md:grid-cols-5 lg:grid-cols-6 xl:grid-cols-8 gap-3">
            {shown.map(m => (
              <button
                key={`${m.kind}-${m.tmdbId}`}
                onClick={() => openSearch(m)}
                title={`Buscar "${m.title}"`}
                className="group relative flex flex-col text-left rounded-lg overflow-hidden bg-gray-800 border border-gray-700 hover:border-green-500/50 transition-colors"
              >
                <div className="aspect-[2/3] bg-gray-900 relative">
                  <img src={m.posterUrl} alt={m.title} loading="lazy" className="w-full h-full object-cover" />
                  <span className="absolute top-1 left-1 flex items-center gap-1 text-[10px] px-1.5 py-0.5 rounded bg-black/70 text-gray-200">
                    {m.kind === 'tv' ? <Tv className="w-3 h-3" /> : <Film className="w-3 h-3" />}
                    {m.kind === 'tv' ? 'Série' : 'Filme'}
                  </span>
                  {m.voteAverage > 0 && (
                    <span className="absolute top-1 right-1 flex items-center gap-0.5 text-[10px] px-1.5 py-0.5 rounded bg-black/70 text-yellow-300">
                      <Star className="w-3 h-3 fill-current" />{m.voteAverage.toFixed(1)}
                    </span>
                  )}
                  {/* Hover overlay hints the click action */}
                  <div className="absolute inset-0 flex items-center justify-center bg-black/50 opacity-0 group-hover:opacity-100 transition-opacity">
                    <Search className="w-7 h-7 text-green-400" />
                  </div>
                </div>
                <div className="p-2">
                  <p className="text-xs text-gray-200 line-clamp-2" title={m.title}>{m.title}</p>
                  {m.year > 0 && <p className="text-[10px] text-gray-500">{m.year}</p>}
                </div>
              </button>
            ))}
          </div>
        )}
      </main>
    </div>
  )
}
