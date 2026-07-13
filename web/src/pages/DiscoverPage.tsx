import { useEffect, useMemo, useState, useCallback } from 'react'
import { useTranslation, Trans } from 'react-i18next'
import { useNavigate } from 'react-router-dom'
import { Flame, Loader2, Search, Star, Film, Tv, X, TrendingUp, TrendingDown, Sparkles, Wand2, Clapperboard, ChevronDown } from 'lucide-react'
import NavHeader from '../components/NavHeader'
import TrailerModal from '../components/TrailerModal'
import { tmdbTrending, tmdbGenres, tmdbRecommendations, tmdbDismissRecommendation, tmdbVideos, TmdbMatch, TmdbGenre, TmdbRecommendation } from '../api/client'
import { groupRecommendations, removeRec, RecGroup } from '../lib/recsGroup'
import { dedupeMatches } from '../lib/dedupeMatches'
import { usePersistedState } from '../lib/storage'
import { useQueryParam, useEnumQueryParam } from '../lib/useQueryState'
import { useScrollRestoration } from '../lib/useScrollRestoration'
import { newTabProps, searchHref } from '../lib/cardNav'
import { useMediaMode } from '../lib/mediaMode'
import { MusicDiscoverView } from '../components/MusicDiscoverView'
import { AsyncState } from '../components/AsyncState'

// searchQuery builds the seed string a poster click uses (and the href a new tab
// opens). Torrent releases are named by the ORIGINAL title, not the pt-BR one —
// prefer original_title, falling back to the localized title, with the year.
function searchQuery(m: TmdbMatch): string {
  const name = m.originalTitle?.trim() || m.title
  return m.year ? `${name} ${m.year}` : name
}

// DiscoverPage surfaces TMDB's weekly trending movies + shows so the user has a
// starting point when they don't know what to search. Clicking a poster seeds a
// search (title + year) — the existing pipeline takes it from there. Read-only
// and best-effort: with no TMDB key it shows a hint instead of erroring.

type Filter = 'all' | 'movie' | 'tv'
const FILTERS: readonly Filter[] = ['all', 'movie', 'tv']

// DirectionBadge shows how a title moved in this week's ranking vs last week.
function DirectionBadge({ m }: { readonly m: TmdbMatch }) {
  const { t } = useTranslation()
  if (m.direction === 'new') {
    return (
      <span className="flex items-center gap-0.5 text-[10px] px-1.5 py-0.5 rounded bg-blue-500/80 text-white" title={t('discover.new_rank')}>
        <Sparkles className="w-3 h-3" />{t('discover.new_short')}
      </span>
    )
  }
  if (m.direction === 'up') {
    return (
      <span className="flex items-center gap-0.5 text-[10px] px-1.5 py-0.5 rounded bg-emerald-500/80 text-white" title={t('discover.rank_up', { n: m.rankDelta ?? 0 })}>
        <TrendingUp className="w-3 h-3" />{m.rankDelta ?? ''}
      </span>
    )
  }
  if (m.direction === 'down') {
    return (
      <span className="flex items-center gap-0.5 text-[10px] px-1.5 py-0.5 rounded bg-red-500/80 text-white" title={t('discover.rank_down', { n: m.rankDelta ?? 0 })}>
        <TrendingDown className="w-3 h-3" />{m.rankDelta ?? ''}
      </span>
    )
  }
  return null
}

// PosterCard renders one TMDB title as a clickable poster. Shared by the
// trending grid and the recommendations grid so both look identical. `badge`
// goes bottom-left (trending direction). The root is a div with a full-bleed
// button overlay (not a <button> wrapper) so the trailer mini-button can be a
// sibling — nested buttons are invalid HTML.
function PosterCard({ m, onClick, badge, onTrailer, trailerMuted, onDismiss }: {
  readonly m: TmdbMatch
  readonly onClick: () => void
  readonly badge?: React.ReactNode
  readonly onTrailer?: () => void
  readonly trailerMuted?: boolean // already probed: this title has no trailer
  readonly onDismiss?: () => void // present only on recommendation cards
}) {
  const { t } = useTranslation()
  return (
    <div className="group relative flex flex-col text-left rounded-lg overflow-hidden bg-surface-secondary border border-default hover:border-green-500/50 transition-colors">
      <button
        {...newTabProps(searchHref(searchQuery(m)), onClick)}
        title={t('discover.search_title', { title: m.title })}
        aria-label={t('discover.search_title', { title: m.title })}
        className="absolute inset-0 z-[1]"
      />
      <div className="aspect-[2/3] bg-surface relative">
        <img src={m.posterUrl} alt={m.title} loading="lazy" className="w-full h-full object-cover" />
        <span className="absolute top-1 left-1 flex items-center gap-1 text-[10px] px-1.5 py-0.5 rounded bg-black/70 text-white">
          {m.kind === 'tv' ? <Tv className="w-3 h-3" /> : <Film className="w-3 h-3" />}
          {m.kind === 'tv' ? t('discover.kind_tv') : t('discover.kind_movie')}
        </span>
        {/* On recommendation cards an "ignore" (X) sits top-right. Always visible
            on touch (no hover there — the X was unreachable on mobile); hover-only
            on devices with a real pointer to keep the grid clean. The rating drops
            one row so the two never overlap. Trending cards have no dismiss. */}
        {onDismiss && (
          <button
            onClick={onDismiss}
            title={t('discover.dislike')}
            aria-label={t('discover.dislike')}
            className="absolute top-1 right-1 z-[2] flex items-center justify-center w-7 h-7 rounded-full bg-black/70 text-white opacity-100 [@media(hover:hover)]:opacity-0 group-hover:opacity-100 group-focus-within:opacity-100 hover:text-red-400 transition-opacity"
          >
            <X className="w-4 h-4" />
          </button>
        )}
        {m.voteAverage > 0 && (
          <span className={`absolute right-1 flex items-center gap-0.5 text-[10px] px-1.5 py-0.5 rounded bg-black/70 text-yellow-300 ${onDismiss ? 'top-9' : 'top-1'}`}>
            <Star className="w-3 h-3 fill-current" />{m.voteAverage.toFixed(1)}
          </span>
        )}
        <div className="absolute inset-0 flex items-center justify-center bg-black/50 opacity-0 group-hover:opacity-100 group-active:opacity-100 transition-opacity">
          <Search className="w-7 h-7 text-green-400" />
        </div>
        {badge && <span className="absolute bottom-1 left-1">{badge}</span>}
        {onTrailer && (
          <button
            onClick={onTrailer}
            disabled={trailerMuted}
            title={trailerMuted ? t('discover.no_trailer') : t('discover.watch_trailer')}
            aria-label={trailerMuted ? t('discover.no_trailer') : t('discover.trailer_of', { title: m.title })}
            className={`absolute bottom-1 right-1 z-[2] flex items-center justify-center w-8 h-8 rounded-full bg-black/70 transition-colors ${trailerMuted ? 'text-white/30' : 'text-white hover:text-red-400'}`}
          >
            <Clapperboard className="w-4 h-4" />
          </button>
        )}
      </div>
      <div className="p-2">
        <p className="text-xs text-text-primary line-clamp-2" title={m.title}>{m.title}</p>
        {m.year > 0 && <p className="text-[10px] text-text-muted">{m.year}</p>}
      </div>
    </div>
  )
}

// RecTopic renders one collapsible recommendations topic ("Porque você viu X").
// The header is a real <button> (keyboard + aria-expanded + aria-controls) that
// toggles the grid; the collapsed state is owned by the parent so it can persist
// across visits. The grid stays MOUNTED when collapsed (height animation via
// grid-rows) so re-expanding is instant and lazy posters don't re-fetch — but a
// mounted-yet-invisible grid still contains the posters' overlay/trailer
// <button>s. Without removing them from the a11y tree, a keyboard user tabbing
// past a collapsed topic would land on buttons that are clipped off-screen
// (focus disappears) — WCAG 2.4.3/2.4.7. So when collapsed we mark the region
// `inert` (drops descendants from tab order AND from the accessibility tree) and
// add aria-hidden as a redundant signal for AT that doesn't yet honour `inert`.
function RecTopic({ group, collapsed, onToggle, renderCard }: {
  readonly group: RecGroup
  readonly collapsed: boolean
  readonly onToggle: () => void
  readonly renderCard: (r: TmdbRecommendation) => React.ReactNode
}) {
  const { t } = useTranslation()
  // group.key (e.g. "because:the matrix") may carry spaces/colons — not valid in
  // an HTML id token, which would break the aria-controls reference. Slugify it.
  const regionId = `rectopic-${group.key.replace(/[^a-z0-9]+/gi, '-')}`
  // `inert` must be ABSENT (not inert="false") when expanded — React 18 has no
  // typed boolean `inert`, and the DOM treats any present value as truthy. Spread
  // the attribute only while collapsed; the cast lets TS accept the unknown prop.
  const inertWhenCollapsed = (collapsed ? { inert: '' } : {}) as { inert?: '' }
  return (
    <section className="flex flex-col gap-2">
      <button
        onClick={onToggle}
        aria-expanded={!collapsed}
        aria-controls={regionId}
        title={collapsed ? t('discover.expand_topic', { label: group.label }) : t('discover.collapse_topic', { label: group.label })}
        className="flex items-center gap-2 text-left text-sm font-medium text-text-secondary hover:text-text-primary transition-colors w-full"
      >
        <ChevronDown className={`w-4 h-4 shrink-0 transition-transform ${collapsed ? '-rotate-90' : ''}`} />
        <span className="line-clamp-1">{group.label}</span>
        <span className="text-[11px] text-text-muted font-normal">({group.items.length})</span>
      </button>
      <div
        id={regionId}
        aria-hidden={collapsed}
        {...inertWhenCollapsed}
        className={`grid transition-[grid-template-rows] duration-200 ease-out ${collapsed ? 'grid-rows-[0fr]' : 'grid-rows-[1fr]'}`}
      >
        <div className="overflow-hidden">
          <div className="grid grid-cols-2 sm:grid-cols-4 md:grid-cols-5 lg:grid-cols-6 xl:grid-cols-8 gap-3">
            {group.items.map(renderCard)}
          </div>
        </div>
      </div>
    </section>
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
  const { t } = useTranslation()
  const [items, setItems] = useState<TmdbMatch[] | null>(null)
  const [trendingError, setTrendingError] = useState<string | null>(null)
  // Filtros do Discover na URL (sobrevivem a back/forward/reload/reabrir). year e
  // genre são numéricos → glue string<->number ('' = sem filtro). filter/query
  // são filtros client-side; year/genre disparam o fetch de trending.
  const [filter, setFilter] = useEnumQueryParam<Filter>('type', FILTERS, 'all')
  const [query, setQuery] = useQueryParam('q')
  const [yearStr, setYearStr] = useQueryParam('year')
  const year = Number(yearStr) || 0
  const setYear = (n: number) => setYearStr(n ? String(n) : '')
  const [genreStr, setGenreStr] = useQueryParam('genre')
  const genre = Number(genreStr) || 0
  const setGenre = (n: number) => setGenreStr(n ? String(n) : '')
  const [genres, setGenres] = useState<TmdbGenre[]>([])
  const [recs, setRecs] = useState<TmdbRecommendation[]>([])
  const [trailer, setTrailer] = useState<{ videoKey: string; title: string } | null>(null)
  const [noTrailer, setNoTrailer] = useState<Set<string>>(new Set())
  // Collapsed topic keys, persisted as an array (usePersistedState JSON-serializes,
  // so a Set wouldn't survive). Derived into a Set for O(1) lookups in render.
  const [collapsedKeys, setCollapsedKeys] = usePersistedState<string[]>('discover.collapsed', [])
  const [mediaMode] = useMediaMode()
  const navigate = useNavigate()
  // Scroll restaurado quando o trending carrega (chamado antes do early-return).
  useScrollRestoration(items !== null)

  // O toggle Filme/Série filtra a tela TODA — incluindo as recomendações do topo,
  // não só a grade "Em alta". Aplicado antes de agrupar (tópicos que ficam vazios
  // somem naturalmente).
  const filteredRecs = useMemo(
    () => (filter === 'all' ? recs : recs.filter(r => r.kind === filter)),
    [recs, filter],
  )
  // Group recommendations by their "Porque você viu X" source into collapsible
  // topics — client-side over the already-loaded list, so no extra requests.
  const recGroups = useMemo(() => groupRecommendations(filteredRecs), [filteredRecs])
  const collapsedSet = useMemo(() => new Set(collapsedKeys), [collapsedKeys])
  const toggleGroup = (key: string) =>
    setCollapsedKeys(prev => prev.includes(key) ? prev.filter(k => k !== key) : [...prev, key])

  // dismissRec removes a recommendation optimistically (the topic vanishes once
  // its last item goes — recGroups is re-derived from `recs`) and persists the
  // "never again" on the server so a rebuild won't bring it back.
  const dismissRec = (r: TmdbRecommendation) => {
    setRecs(prev => removeRec(prev, r.kind, r.tmdbId))
    void tmdbDismissRecommendation(r.kind, r.tmdbId)
  }

  // openTrailer probes the title's videos on demand (session-cached in the API
  // layer) and either plays the best one or marks the card as trailer-less.
  const openTrailer = async (m: TmdbMatch) => {
    const vids = await tmdbVideos(m.kind, m.tmdbId)
    if (vids.length > 0) {
      setTrailer({ videoKey: vids[0].key, title: `${m.title} — ${vids[0].name}` })
    } else {
      setNoTrailer(prev => new Set(prev).add(`${m.kind}-${m.tmdbId}`))
    }
  }

  // Genre list for the dropdown (loaded once).
  useEffect(() => {
    tmdbGenres().then(setGenres).catch(() => setGenres([]))
  }, [])

  // Personalized recommendations from the watched library (loaded once, best-
  // effort). Empty → the section just doesn't render.
  useEffect(() => {
    // Dedupe by (kind, tmdbId) so no two cards share a React key even if the
    // server (or a response cached before the dedupe fix) returns a repeat.
    tmdbRecommendations().then(l => setRecs(dedupeMatches(l))).catch(() => setRecs([]))
  }, [])

  const loadTrending = useCallback(() => {
    setItems(null)
    setTrendingError(null)
    tmdbTrending({ year, genre })
      .then(l => { setItems(dedupeMatches(l)); setTrendingError(null) })
      .catch(err => {
        setItems([])
        setTrendingError(err instanceof Error ? err.message : t('discover.load_error'))
      })
  }, [year, genre, t])

  // Trending / discover list — refetched whenever the year/genre filter changes.
  useEffect(() => { loadTrending() }, [loadTrending])

  const openSearch = (m: TmdbMatch) => {
    navigate(searchHref(searchQuery(m)))
  }

  const q = query.trim().toLowerCase()
  const shown = (items || []).filter(
    m => (filter === 'all' || m.kind === filter) && (!q || m.title.toLowerCase().includes(q)),
  )

  // Modo Música: troca o Discover de filmes (TMDB) pela grade de álbuns em alta
  // (Apple RSS). Early-return DEPOIS de todos os hooks acima (ordem estável).
  if (mediaMode === 'audio') return <MusicDiscoverView />

  return (
    <div className="min-h-screen bg-surface flex flex-col">
      <NavHeader />
      <main className="flex-1 max-w-7xl 2xl:max-w-[min(95vw,1600px)] mx-auto w-full px-4 py-6 flex flex-col gap-4">
        {/* Personalized recommendations — rendered only when the watched library
            yielded any (additive; absent for new users or with TMDB off). Grouped
            into one collapsible topic per "Porque você viu X" source so the user
            can skip between topics; the collapsed state persists across visits. */}
        {recGroups.length > 0 && (
          <section className="flex flex-col gap-4">
            <h2 className="text-xl font-semibold text-text-primary flex items-center gap-2">
              <Wand2 className="w-5 h-5 text-green-400" /> {t('discover.recommended')}
            </h2>
            {recGroups.map(group => (
              <RecTopic
                key={group.key}
                group={group}
                collapsed={collapsedSet.has(group.key)}
                onToggle={() => toggleGroup(group.key)}
                renderCard={r => (
                  <PosterCard
                    key={`rec-${r.kind}-${r.tmdbId}`}
                    m={r}
                    onClick={() => openSearch(r)}
                    onTrailer={() => openTrailer(r)}
                    trailerMuted={noTrailer.has(`${r.kind}-${r.tmdbId}`)}
                    onDismiss={() => dismissRec(r)}
                  />
                )}
              />
            ))}
            <div className="h-px bg-strong/30" />
          </section>
        )}

        <div className="flex items-center justify-between flex-wrap gap-2">
          <h1 className="text-xl font-semibold text-text-primary flex items-center gap-2">
            <Flame className="w-5 h-5 text-orange-400" /> {t('discover.title_trending')}
          </h1>
          <div className="flex items-center gap-2 text-xs flex-wrap">
            <div className="flex items-center gap-1">
              {(['all', 'movie', 'tv'] as Filter[]).map(f => (
                <button
                  key={f}
                  onClick={() => setFilter(f)}
                  className={filter === f ? 'btn-primary' : 'btn-secondary'}
                >
                  {{ all: t('discover.filter_all'), movie: t('discover.filter_movies'), tv: t('discover.filter_tv') }[f]}
                </button>
              ))}
            </div>
            <select
              value={year}
              onChange={e => setYear(Number(e.target.value))}
              className="bg-surface-secondary border border-default rounded-lg px-2 py-1.5 text-text-primary focus:outline-none focus:border-green-500/50"
              title={t('discover.filter_year')}
            >
              <option value={0}>{t('discover.any_year')}</option>
              {YEARS.map(y => <option key={y} value={y}>{y}</option>)}
            </select>
            {genres.length > 0 && (
              <select
                value={genre}
                onChange={e => setGenre(Number(e.target.value))}
                className="bg-surface-secondary border border-default rounded-lg px-2 py-1.5 text-text-primary focus:outline-none focus:border-green-500/50"
                title={t('discover.filter_genre')}
              >
                <option value={0}>{t('discover.any_genre')}</option>
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
              placeholder={t('discover.filter_title')}
              className="w-full bg-surface-secondary border border-default rounded-lg pl-9 pr-8 py-2 text-base sm:text-sm text-text-primary placeholder-gray-500 focus:outline-none focus:border-green-500/50"
            />
            {query && (
              <button
                onClick={() => setQuery('')}
                aria-label={t('discover.clear')}
                className="absolute right-2 top-1/2 -translate-y-1/2 text-text-muted hover:text-text-primary p-1"
              >
                <X className="w-3.5 h-3.5" />
              </button>
            )}
          </div>
        )}

        <AsyncState
          loading={items === null}
          error={trendingError}
          empty={items !== null && items.length === 0 && !trendingError}
          loadingLabel={t('discover.loading_trending')}
          onRetry={loadTrending}
          emptyConfig={{
            icon: <Flame className="w-16 h-16 opacity-30" />,
            title: t('discover.empty_title'),
            description: <Trans i18nKey="discover.empty_hint" components={{ c: <code className="text-text-secondary" /> }} />,
          }}
        >
          {items !== null && items.length > 0 && (
          <div className="grid grid-cols-2 sm:grid-cols-4 md:grid-cols-5 lg:grid-cols-6 xl:grid-cols-8 gap-3">
            {shown.map(m => (
              <PosterCard
                key={`${m.kind}-${m.tmdbId}`}
                m={m}
                onClick={() => openSearch(m)}
                badge={<DirectionBadge m={m} />}
                onTrailer={() => openTrailer(m)}
                trailerMuted={noTrailer.has(`${m.kind}-${m.tmdbId}`)}
              />
            ))}
          </div>
          )}
        </AsyncState>
      </main>

      {trailer && (
        <TrailerModal videoKey={trailer.videoKey} title={trailer.title} onClose={() => setTrailer(null)} />
      )}
    </div>
  )
}
