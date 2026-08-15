import { useCallback, useEffect, useState } from 'react'
import { useNavigate, useSearchParams } from 'react-router-dom'
import { useTranslation } from 'react-i18next'
import { AlertTriangle, CheckCircle2, Clock, Download, Flame, Music2, Play, Search, Sparkles } from 'lucide-react'
import NavHeader from '../components/NavHeader'
import { AsyncState } from '../components/AsyncState'
import { HomeRail, homeCardClass } from '../components/home/HomeRail'
import { usePlayer } from '../components/PlayerProvider'
import {
  type DownloadEntry,
  type LibraryEntry,
  type SearchResult,
  type TmdbMatch,
  type TmdbRecommendation,
  downloadsListFiltered,
  getHealth,
  libraryList,
  type RuntimeHealth,
  streamArtURL,
  tmdbRecommendations,
  tmdbTrending,
} from '../api/client'
import { newTabProps, playHref, searchHref } from '../lib/cardNav'
import { formatDuration } from '../lib/format'
import { homeIsEmpty, homePlayFileIndex, pickContinueWatching, pickRecentlyCompleted } from '../lib/homeHub'
import { allHomeSectionsFailed, failedHomeSections, preserveOnFailure, type HomeSection, type HomeSectionResult } from '../lib/homeHealth'
import { useMediaMode } from '../lib/mediaMode'
import { musicTrending, type MusicAlbum } from '../api/music'

function libraryResult(e: LibraryEntry): SearchResult {
  return {
    title: e.name, tracker: '', categoryId: 0, category: '', size: e.totalSize,
    seeders: 0, leechers: 0, age: '', magnetUri: e.magnet, link: '',
    infoHash: e.infoHash, publishDate: '',
  }
}

function downloadResult(d: DownloadEntry): SearchResult {
  return {
    title: d.name, tracker: d.tracker ?? '', categoryId: 0, category: d.category ?? '',
    size: d.fileSize, seeders: 0, leechers: 0, age: '', magnetUri: d.magnet,
    link: '', infoHash: d.infoHash, publishDate: '',
  }
}

function tmdbQuery(m: TmdbMatch): string {
  const name = m.originalTitle?.trim() || m.title
  return m.year ? `${name} ${m.year}` : name
}

export default function HomePage() {
  const { t } = useTranslation()
  const navigate = useNavigate()
  const [params] = useSearchParams()
  const { playSingle } = usePlayer()
  const [mediaMode] = useMediaMode()
  const [q, setQ] = useState('')
  const [library, setLibrary] = useState<LibraryEntry[]>([])
  const [completed, setCompleted] = useState<DownloadEntry[]>([])
  const [recs, setRecs] = useState<TmdbRecommendation[]>([])
  const [trending, setTrending] = useState<TmdbMatch[]>([])
  const [albums, setAlbums] = useState<MusicAlbum[]>([])
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState<string | null>(null)
  const [sectionErrors, setSectionErrors] = useState<HomeSection[]>([])
  const [health, setHealth] = useState<RuntimeHealth | null>(null)
  const [healthError, setHealthError] = useState(false)

  // Old bookmarks were `/?q=` on Search-as-home. Preserve them.
  useEffect(() => {
    const legacy = params.get('q')
    if (legacy && !params.get('play')) {
      navigate(`/search?q=${encodeURIComponent(legacy)}`, { replace: true })
    }
  }, [params, navigate])

  const reload = useCallback(() => {
    setLoading(true)
    setError(null)
    setHealthError(false)
    void Promise.allSettled([
      libraryList({ limit: 40 }),
      downloadsListFiltered({ status: 'completed' }),
      tmdbRecommendations(),
      tmdbTrending(),
      musicTrending({ limit: 16 }),
      getHealth(),
    ])
      .then(([libResult, doneResult, recResult, trendResult, albumResult, healthResult]) => {
        const sectionResults: HomeSectionResult[] = [
          { section: 'continue', status: libResult.status },
          { section: 'recent', status: doneResult.status },
          { section: 'recommended', status: recResult.status },
          { section: 'trending', status: trendResult.status },
          { section: 'music', status: albumResult.status },
        ]
        const failed = failedHomeSections(sectionResults)
        setSectionErrors(failed)
        setError(allHomeSectionsFailed(sectionResults) ? t('home.loadFailed') : null)
        // Preserve the last known rail when a retry loses one dependency. A
        // transient outage must not erase usable content already on screen.
        setLibrary(previous => preserveOnFailure(previous, libResult))
        setCompleted(previous => preserveOnFailure(previous, doneResult))
        setRecs(previous => preserveOnFailure(previous, recResult))
        setTrending(previous => preserveOnFailure(previous, trendResult))
        setAlbums(previous => preserveOnFailure(previous, albumResult))
        setHealthError(healthResult.status === 'rejected')
        setHealth(healthResult.status === 'fulfilled' ? healthResult.value : null)
      })
      .finally(() => setLoading(false))
  }, [t])

  useEffect(() => { reload() }, [reload])

  const continueWatching = pickContinueWatching(library)
  const recent = pickRecentlyCompleted(completed)
  const recSlice = recs.slice(0, 16)
  const trendSlice = trending.slice(0, 16)
  const hasRailContent = continueWatching.length > 0 || recent.length > 0 || recSlice.length > 0 || trendSlice.length > 0 || albums.length > 0
  // Keep the last usable rails visible when a retry fails after content was
  // already rendered. Only the initial/all-empty failure should replace the
  // page with the blocking error state.
  const healthDegraded = Boolean(health && health.status !== 'ok')
  const healthAttention = healthDegraded || healthError
  const contentError = error && !hasRailContent ? error : null
  const showIssueBanner = (sectionErrors.length > 0 || healthAttention) && (!error || hasRailContent)
  const empty = !loading && !error && sectionErrors.length === 0 && homeIsEmpty({
    continueCount: continueWatching.length,
    recentCount: recent.length,
    recCount: recSlice.length,
    trendCount: trendSlice.length,
    albumCount: albums.length,
  })
  const sectionLabels: Record<HomeSection, string> = {
    continue: t('home.continue'),
    recent: t('home.recentlyAdded'),
    recommended: t('home.recommended'),
    trending: t('home.trending'),
    music: t('home.trendingAlbums'),
  }
  let healthMessage = t('home.partialHint')
  if (healthDegraded) healthMessage = t('home.healthHint')
  if (healthError) healthMessage = t('home.healthUnavailable')

  const goSearch = (query: string) => {
    const trimmed = query.trim()
    navigate(trimmed ? searchHref(trimmed) : '/search')
  }

  return (
    <div className="min-h-screen bg-surface flex flex-col">
      <NavHeader />
      <main id="main-content" tabIndex={-1} className="flex-1 max-w-7xl 2xl:max-w-[min(95vw,1600px)] mx-auto w-full px-4 py-6 flex flex-col gap-8">
        <header className="flex flex-col gap-4">
          <div>
            <p className="text-xs uppercase tracking-[0.2em] text-green-400/80">{t('home.kicker')}</p>
            <h1 className="text-2xl sm:text-3xl font-semibold text-text-primary mt-1">{t('home.title')}</h1>
            <p className="text-sm text-text-secondary mt-1 max-w-xl">{t('home.subtitle')}</p>
          </div>
          <form
            className="flex gap-2 max-w-xl"
            onSubmit={e => { e.preventDefault(); goSearch(q) }}
          >
            <label className="flex-1 relative">
              <Search className="absolute left-3 top-1/2 -translate-y-1/2 w-4 h-4 text-text-muted" />
              <input
                value={q}
                onChange={e => setQ(e.target.value)}
                placeholder={t('home.searchPlaceholder')}
                className="w-full pl-9 pr-3 py-2.5 rounded-lg bg-surface-secondary border border-default text-text-primary placeholder:text-text-muted focus:outline-none focus:border-green-500"
              />
            </label>
            <button type="submit" className="btn-primary">{t('home.search')}</button>
          </form>
        </header>

        {showIssueBanner && (
          <section
            role="status"
            aria-live="polite"
            className="rounded-xl border border-amber-400/30 bg-amber-500/10 p-4 flex flex-col gap-3 sm:flex-row sm:items-start sm:justify-between"
          >
            <div className="flex items-start gap-3 min-w-0">
              <AlertTriangle className="w-5 h-5 mt-0.5 text-amber-400 flex-shrink-0" aria-hidden />
              <div className="min-w-0">
                <h2 className="font-medium text-text-primary">
                  {error ? t('home.loadFailed') : healthDegraded || healthError ? t('home.healthTitle') : t('home.partialTitle')}
                </h2>
                <p className="text-sm text-text-secondary mt-1">
                  {healthMessage}
                </p>
                {sectionErrors.length > 0 && (
                  <p className="text-xs text-amber-800 dark:text-amber-200 mt-2">
                    {sectionErrors.map(section => sectionLabels[section]).join(' · ')}
                  </p>
                )}
                {healthDegraded && health && (
                  <div className="flex flex-wrap gap-2 mt-3 text-xs">
                    <HealthChip label={t('home.healthDb')} value={health.db} />
                    <HealthChip label={t('home.healthStreamer')} value={health.streamer} />
                  </div>
                )}
              </div>
            </div>
            <div className="flex flex-wrap gap-2 sm:flex-shrink-0 sm:pt-0.5">
              <button type="button" className="btn-secondary text-sm" onClick={reload}>{t('common.retry')}</button>
              <button type="button" className="btn-secondary text-sm" onClick={() => navigate('/settings')}>{t('home.openSettings')}</button>
            </div>
          </section>
        )}

        <AsyncState
          loading={loading && !hasRailContent}
          error={contentError}
          empty={empty}
          onRetry={reload}
          emptyConfig={{
            icon: <Flame className="w-16 h-16 opacity-30" />,
            title: t('home.emptyTitle'),
            description: t('home.emptyHint'),
            action: (
              <div className="flex flex-wrap justify-center gap-2">
                <button type="button" className="btn-primary" onClick={() => navigate('/search')}>{t('home.startSearch')}</button>
                <button type="button" className="btn-secondary" onClick={() => navigate('/discover')}>{t('home.openTrending')}</button>
              </div>
            ),
          }}
        >
          <div className="flex flex-col gap-8">
            <HomeRail title={t('home.continue')} href="/library" seeAllLabel={t('home.seeAll')} empty={continueWatching.length === 0}>
              {continueWatching.map(e => {
                const fileIdx = homePlayFileIndex(e)
                const href = playHref(e.infoHash, fileIdx, e.resumeSeconds)
                const ratio = e.durationSeconds > 0 ? Math.min(1, e.resumeSeconds / e.durationSeconds) : 0
                return (
                  <button
                    key={e.id}
                    type="button"
                    {...newTabProps(href, () => playSingle(libraryResult(e), fileIdx))}
                    className={`${homeCardClass()} card text-left p-0 overflow-hidden group`}
                  >
                    <div className="aspect-[2/3] bg-surface-tertiary relative">
                      <img src={streamArtURL(e.infoHash)} alt="" className="w-full h-full object-cover" onError={ev => { ev.currentTarget.style.display = 'none' }} />
                      <span className="absolute inset-0 flex items-center justify-center bg-black/40 opacity-0 group-hover:opacity-100 transition-opacity">
                        <Play className="w-8 h-8 text-white fill-white" />
                      </span>
                      <span className="absolute bottom-0 left-0 right-0 h-1 bg-black/40">
                        <span className="block h-full bg-green-500" style={{ width: `${ratio * 100}%` }} />
                      </span>
                    </div>
                    <div className="p-2">
                      <p className="text-xs text-text-primary line-clamp-2">{e.name}</p>
                      {e.durationSeconds > 0 && (
                        <p className="text-[10px] text-text-muted flex items-center gap-1 mt-0.5">
                          <Clock className="w-3 h-3" />
                          {t('home.left', { time: formatDuration(Math.max(0, e.durationSeconds - e.resumeSeconds)) })}
                        </p>
                      )}
                    </div>
                  </button>
                )
              })}
            </HomeRail>

            <HomeRail title={t('home.recentlyAdded')} href="/downloads" seeAllLabel={t('home.seeAll')} empty={recent.length === 0}>
              {recent.map(d => (
                <button
                  key={d.id}
                  type="button"
                  {...newTabProps(playHref(d.infoHash, d.fileIndex >= 0 ? d.fileIndex : undefined), () => playSingle(downloadResult(d), d.fileIndex >= 0 ? d.fileIndex : undefined))}
                  className={`${homeCardClass()} card text-left p-0 overflow-hidden group`}
                >
                  <div className="aspect-[2/3] bg-surface-tertiary relative">
                    <img src={streamArtURL(d.infoHash)} alt="" className="w-full h-full object-cover" onError={ev => { ev.currentTarget.style.display = 'none' }} />
                    <span className="absolute top-1 left-1 text-[10px] px-1.5 py-0.5 rounded bg-green-600/90 text-white flex items-center gap-0.5">
                      <Download className="w-3 h-3" />{t('home.onDisk')}
                    </span>
                    <span className="absolute inset-0 flex items-center justify-center bg-black/40 opacity-0 group-hover:opacity-100 transition-opacity">
                      <Play className="w-8 h-8 text-white fill-white" />
                    </span>
                  </div>
                  <div className="p-2">
                    <p className="text-xs text-text-primary line-clamp-2">{d.name}</p>
                  </div>
                </button>
              ))}
            </HomeRail>

            {mediaMode === 'audio' ? (
              <HomeRail title={t('home.trendingAlbums')} href="/discover" seeAllLabel={t('home.seeAll')} empty={albums.length === 0}>
                {albums.map((a, i) => {
                  const q = `${a.artist} ${a.name}`.trim()
                  return (
                    <button
                      key={`${a.artist}-${a.name}-${i}`}
                      type="button"
                      {...newTabProps(searchHref(q), () => navigate(searchHref(q)))}
                      className={`${homeCardClass()} card text-left p-0 overflow-hidden group`}
                    >
                      <div className="aspect-square bg-surface-tertiary relative">
                        {a.artwork
                          ? <img src={a.artwork} alt="" className="w-full h-full object-cover" />
                          : <Music2 className="w-10 h-10 m-auto text-text-muted" />}
                      </div>
                      <div className="p-2">
                        <p className="text-xs text-text-primary line-clamp-2">{a.name}</p>
                        <p className="text-[10px] text-text-muted line-clamp-1">{a.artist}</p>
                      </div>
                    </button>
                  )
                })}
              </HomeRail>
            ) : (
              <>
                <HomeRail title={t('home.recommended')} href="/discover" seeAllLabel={t('home.seeAll')} empty={recSlice.length === 0}>
                  {recSlice.map(m => (
                    <TmdbPoster key={`${m.kind}-${m.tmdbId}`} m={m} because={m.becauseOf} />
                  ))}
                </HomeRail>
                <HomeRail title={t('home.trending')} href="/discover" seeAllLabel={t('home.seeAll')} empty={trendSlice.length === 0}>
                  {trendSlice.map(m => (
                    <TmdbPoster key={`${m.kind}-${m.tmdbId}`} m={m} />
                  ))}
                </HomeRail>
              </>
            )}
          </div>
        </AsyncState>
      </main>
    </div>
  )
}

function HealthChip({ label, value }: { readonly label: string; readonly value: string }) {
  const { t } = useTranslation()
  const healthy = value === 'ok'
  return (
    <span className={`inline-flex items-center gap-1.5 rounded-full border px-2.5 py-1 ${healthy ? 'border-green-400/30 text-green-800 dark:text-green-300' : 'border-amber-400/30 text-amber-800 dark:text-amber-200'}`}>
      {healthy ? <CheckCircle2 className="w-3.5 h-3.5" aria-hidden /> : <AlertTriangle className="w-3.5 h-3.5" aria-hidden />}
      <span>{label}: {healthy ? 'OK' : t('home.healthUnknown')}</span>
    </span>
  )
}

function TmdbPoster({ m, because }: { readonly m: TmdbMatch; readonly because?: string }) {
  const { t } = useTranslation()
  const navigate = useNavigate()
  const q = tmdbQuery(m)
  return (
    <button
      type="button"
      {...newTabProps(searchHref(q), () => navigate(searchHref(q)))}
      className={`${homeCardClass()} card text-left p-0 overflow-hidden group`}
      title={because ? t('home.because', { title: because }) : m.title}
    >
      <div className="aspect-[2/3] bg-surface-tertiary relative">
        {m.posterUrl && <img src={m.posterUrl} alt="" className="w-full h-full object-cover" />}
        <span className="absolute inset-0 flex items-center justify-center bg-black/40 opacity-0 group-hover:opacity-100 transition-opacity">
          <Search className="w-7 h-7 text-green-400" />
        </span>
        {because && (
          <span className="absolute top-1 left-1 text-[10px] px-1.5 py-0.5 rounded bg-black/70 text-amber-200 flex items-center gap-0.5">
            <Sparkles className="w-3 h-3" />
          </span>
        )}
      </div>
      <div className="p-2">
        <p className="text-xs text-text-primary line-clamp-2">{m.title}</p>
        {m.year > 0 && <p className="text-[10px] text-text-muted">{m.year}</p>}
      </div>
    </button>
  )
}
