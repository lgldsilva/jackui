import { useEffect, useState } from 'react'
import { useNavigate } from 'react-router-dom'
import { Flame, Loader2, Search, Star, Film, Tv, X, TrendingUp, TrendingDown, Sparkles, Wand2 } from 'lucide-react'
import NavHeader from '../components/NavHeader'
import { tmdbTrending, tmdbGenres, tmdbRecommendations, TmdbMatch, TmdbGenre, TmdbRecommendation } from '../api/client'

// DiscoverPage surfaces TMDB's weekly trending movies + shows so the user has a
// starting point when they don't know what to search. Clicking a poster seeds a
// search (title + year) — the existing pipeline takes it from there. Read-only
// and best-effort: with no TMDB key it shows a hint instead of erroring.

type Filter = 'all' | 'movie' | 'tv'

// DirectionBadge shows how a title moved in this week's ranking vs last week.
function DirectionBadge({ m }: { readonly m: TmdbMatch }) {
  if (m.direction === 'new') {
    return (
      <span className="flex items-center gap-0.5 text-[10px] px-1.5 py-0.5 rounded bg-blue-500/80 text-white" title="Novo no ranking">
        <Sparkles className="w-3 h-3" />novo
      </span>
    )
  }
  if (m.direction === 'up') {
    return (
      <span className="flex items-center gap-0.5 text-[10px] px-1.5 py-0.5 rounded bg-emerald-500/80 text-white" title={`Subiu ${m.rankDelta ?? 0} posições`}>
        <TrendingUp className="w-3 h-3" />{m.rankDelta ?? ''}
      </span>
    )
  }
  if (m.direction === 'down') {
    return (
      <span className="flex items-center gap-0.5 text-[10px] px-1.5 py-0.5 rounded bg-red-500/80 text-white" title={`Caiu ${m.rankDelta ?? 0} posições`}>
        <TrendingDown className="w-3 h-3" />{m.rankDelta ?? ''}
      </span>
    )
  }
  return null
}

// PosterCard renders one TMDB title as a clickable poster. Shared by the
// trending grid and the recommendations grid so both look identical. `badge`
// goes bottom-left (trending direction); `caption` replaces the year line (the
// "porque você assistiu X" attribution on recommendations).
function PosterCard({ m, onClick, badge, caption }: {
  readonly m: TmdbMatch
  readonly onClick: () => void
  readonly badge?: React.ReactNode
  readonly caption?: string
}) {
  return (
    <button
      onClick={onClick}
      title={`Buscar "${m.title}"`}
      className="group relative flex flex-col text-left rounded-lg overflow-hidden bg-surface-secondary border border-default hover:border-green-500/50 transition-colors"
    >
      <div className="aspect-[2/3] bg-surface relative">
        <img src={m.posterUrl} alt={m.title} loading="lazy" className="w-full h-full object-cover" />
        <span className="absolute top-1 left-1 flex items-center gap-1 text-[10px] px-1.5 py-0.5 rounded bg-black/70 text-text-primary">
          {m.kind === 'tv' ? <Tv className="w-3 h-3" /> : <Film className="w-3 h-3" />}
          {m.kind === 'tv' ? 'Série' : 'Filme'}
        </span>
        {m.voteAverage > 0 && (
          <span className="absolute top-1 right-1 flex items-center gap-0.5 text-[10px] px-1.5 py-0.5 rounded bg-black/70 text-yellow-300">
            <Star className="w-3 h-3 fill-current" />{m.voteAverage.toFixed(1)}
          </span>
        )}
        <div className="absolute inset-0 flex items-center justify-center bg-black/50 opacity-0 group-hover:opacity-100 group-active:opacity-100 transition-opacity">
          <Search className="w-7 h-7 text-green-400" />
        </div>
        {badge && <span className="absolute bottom-1 left-1">{badge}</span>}
      </div>
      <div className="p-2">
        <p className="text-xs text-text-primary line-clamp-2" title={m.title}>{m.title}</p>
        {caption
          ? <p className="text-[10px] text-green-400/90 line-clamp-1" title={caption}>{caption}</p>
          : (m.year > 0 && <p className="text-[10px] text-text-muted">{m.year}</p>)}
      </div>
    </button>
  )
}

// YEARS lists selectable years from the current one back to 1970 (computed once).
const YEARS = (() => {
  const now = new Date().getFullYear()
  const out: number[] = []
  for (let y = now; y >= 1970; y--) out.push(y)
  return out
})()

export default function DiscoverPage() {
  const [items, setItems] = useState<TmdbMatch[] | null>(null)
  const [filter, setFilter] = useState<Filter>('all')
  const [query, setQuery] = useState('')
  const [year, setYear] = useState(0)   // 0 = sem filtro de ano
  const [genre, setGenre] = useState(0) // 0 = sem filtro de gênero
  const [genres, setGenres] = useState<TmdbGenre[]>([])
  const [recs, setRecs] = useState<TmdbRecommendation[]>([])
  const navigate = useNavigate()

  // Genre list for the dropdown (loaded once).
  useEffect(() => {
    tmdbGenres().then(setGenres).catch(() => setGenres([]))
  }, [])

  // Personalized recommendations from the watched library (loaded once, best-
  // effort). Empty → the section just doesn't render.
  useEffect(() => {
    tmdbRecommendations().then(setRecs).catch(() => setRecs([]))
  }, [])

  // Trending / discover list — refetched whenever the year/genre filter changes.
  useEffect(() => {
    setItems(null)
    tmdbTrending({ year, genre }).then(setItems).catch(() => setItems([]))
  }, [year, genre])

  const openSearch = (m: TmdbMatch) => {
    const q = m.year ? `${m.title} ${m.year}` : m.title
    navigate(`/?q=${encodeURIComponent(q)}`)
  }

  const q = query.trim().toLowerCase()
  const shown = (items || []).filter(
    m => (filter === 'all' || m.kind === filter) && (!q || m.title.toLowerCase().includes(q)),
  )

  return (
    <div className="min-h-screen bg-surface flex flex-col">
      <NavHeader />
      <main className="flex-1 max-w-7xl 2xl:max-w-[min(95vw,1600px)] mx-auto w-full px-4 py-6 flex flex-col gap-4">
        {/* Personalized recommendations — rendered only when the watched library
            yielded any (additive; absent for new users or with TMDB off). */}
        {recs.length > 0 && (
          <section className="flex flex-col gap-3">
            <h2 className="text-xl font-semibold text-text-primary flex items-center gap-2">
              <Wand2 className="w-5 h-5 text-green-400" /> Recomendado pra você
            </h2>
            <div className="grid grid-cols-2 sm:grid-cols-4 md:grid-cols-5 lg:grid-cols-6 xl:grid-cols-8 gap-3">
              {recs.map(r => (
                <PosterCard
                  key={`rec-${r.kind}-${r.tmdbId}`}
                  m={r}
                  onClick={() => openSearch(r)}
                  caption={r.becauseOf ? `Porque você viu ${r.becauseOf}` : undefined}
                />
              ))}
            </div>
            <div className="h-px bg-strong/30" />
          </section>
        )}

        <div className="flex items-center justify-between flex-wrap gap-2">
          <h1 className="text-xl font-semibold text-text-primary flex items-center gap-2">
            <Flame className="w-5 h-5 text-orange-400" /> Em alta
          </h1>
          <div className="flex items-center gap-2 text-xs flex-wrap">
            <div className="flex items-center gap-1">
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
            <select
              value={year}
              onChange={e => setYear(Number(e.target.value))}
              className="bg-surface-secondary border border-default rounded-lg px-2 py-1.5 text-text-primary focus:outline-none focus:border-green-500/50"
              title="Filtrar por ano"
            >
              <option value={0}>Qualquer ano</option>
              {YEARS.map(y => <option key={y} value={y}>{y}</option>)}
            </select>
            {genres.length > 0 && (
              <select
                value={genre}
                onChange={e => setGenre(Number(e.target.value))}
                className="bg-surface-secondary border border-default rounded-lg px-2 py-1.5 text-text-primary focus:outline-none focus:border-green-500/50"
                title="Filtrar por gênero"
              >
                <option value={0}>Qualquer gênero</option>
                {genres.map(g => <option key={g.id} value={g.id}>{g.name}</option>)}
              </select>
            )}
          </div>
        </div>

        {/* Busca por título dentro da grade trending */}
        {items !== null && items.length > 0 && (
          <div className="relative w-full sm:max-w-xs">
            <Search className="absolute left-3 top-1/2 -translate-y-1/2 w-4 h-4 text-text-muted pointer-events-none" />
            <input
              type="text"
              value={query}
              onChange={e => setQuery(e.target.value)}
              placeholder="Filtrar por título..."
              className="w-full bg-surface-secondary border border-default rounded-lg pl-9 pr-8 py-2 text-base sm:text-sm text-text-primary placeholder-gray-500 focus:outline-none focus:border-green-500/50"
            />
            {query && (
              <button
                onClick={() => setQuery('')}
                aria-label="Limpar"
                className="absolute right-2 top-1/2 -translate-y-1/2 text-text-muted hover:text-text-primary p-1"
              >
                <X className="w-3.5 h-3.5" />
              </button>
            )}
          </div>
        )}

{(() => {
          if (items === null) return <div className="flex justify-center py-20"><Loader2 className="w-8 h-8 animate-spin text-text-muted" /></div>
          if (items.length === 0) return <div className="text-center py-20 text-text-muted"><Flame className="w-16 h-16 mx-auto mb-4 opacity-30" /><p>Nada pra mostrar</p><p className="text-xs mt-2">Configure a <code className="text-text-secondary">tmdb.api_key</code> (ou <code className="text-text-secondary">TMDB_API_KEY</code>) pra ver os títulos em alta.</p></div>
          return (
          <div className="grid grid-cols-2 sm:grid-cols-4 md:grid-cols-5 lg:grid-cols-6 xl:grid-cols-8 gap-3">
            {shown.map(m => (
              <PosterCard key={`${m.kind}-${m.tmdbId}`} m={m} onClick={() => openSearch(m)} badge={<DirectionBadge m={m} />} />
            ))}
          </div>
        )})()}
      </main>
    </div>
  )
}
