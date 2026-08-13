import { useCallback, useEffect, useState } from 'react'
import { useNavigate, useSearchParams } from 'react-router-dom'
import { useTranslation } from 'react-i18next'
import { Clock, Download, Flame, Music2, Play, Search, Sparkles } from 'lucide-react'
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
  libraryList,
  streamArtURL,
  tmdbRecommendations,
  tmdbTrending,
} from '../api/client'
import { newTabProps, playHref, searchHref } from '../lib/cardNav'
import { formatDuration } from '../lib/format'
import { pickContinueWatching, pickRecentlyCompleted } from '../lib/homeHub'
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
    Promise.all([
      libraryList({ limit: 40 }).catch(() => [] as LibraryEntry[]),
      downloadsListFiltered({ status: 'completed' }).catch(() => [] as DownloadEntry[]),
      tmdbRecommendations(),
      tmdbTrending(),
      musicTrending({ limit: 16 }),
    ])
      .then(([lib, dones, r, tr, al]) => {
        setLibrary(lib)
        setCompleted(dones)
        setRecs(r)
        setTrending(tr)
        setAlbums(al)
      })
      .catch(err => setError(err instanceof Error ? err.message : 'failed'))
      .finally(() => setLoading(false))
  }, [])

  useEffect(() => { reload() }, [reload])

  const continueWatching = pickContinueWatching(library)
  const recent = pickRecentlyCompleted(completed)
  const recSlice = recs.slice(0, 16)
  const trendSlice = trending.slice(0, 16)
  const empty = !loading && !error
    && continueWatching.length === 0 && recent.length === 0
    && recSlice.length === 0 && trendSlice.length === 0

  const goSearch = (query: string) => {
    const trimmed = query.trim()
    navigate(trimmed ? searchHref(trimmed) : '/search')
  }

  return (
    <div className="min-h-screen bg-surface flex flex-col">
      <NavHeader />
      <main className="flex-1 max-w-7xl 2xl:max-w-[min(95vw,1600px)] mx-auto w-full px-4 py-6 flex flex-col gap-8">
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

        <AsyncState
          loading={loading}
          error={error}
          empty={empty}
          onRetry={reload}
          emptyConfig={{
            icon: <Flame className="w-16 h-16 opacity-30" />,
            title: t('home.emptyTitle'),
            description: t('home.emptyHint'),
          }}
        >
          <div className="flex flex-col gap-8">
            <HomeRail title={t('home.continue')} href="/library" seeAllLabel={t('home.seeAll')} empty={continueWatching.length === 0}>
              {continueWatching.map(e => {
                const fileIdx = e.lastFileIndex >= 0 ? e.lastFileIndex : e.primaryFileIndex
                const href = playHref(e.infoHash, fileIdx, e.resumeSeconds)
                const ratio = e.durationSeconds > 0 ? Math.min(1, e.resumeSeconds / e.durationSeconds) : 0
                return (
                  <button
                    key={e.id}
                    type="button"
                    {...newTabProps(href, () => playSingle(libraryResult(e), fileIdx > 0 ? fileIdx : undefined))}
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
