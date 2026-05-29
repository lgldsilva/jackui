import { useState, useEffect, useMemo, useRef } from 'react'
import { Link, useNavigate } from 'react-router-dom'
import { History, Trash2, Search, ArrowUpDown, Calendar, Database, Filter, X, SortAsc, SortDesc, Globe, FolderOpen, Loader2, RefreshCw } from 'lucide-react'
import { getHistory, getHistoryResults, clearHistory, deleteHistoryEntry, searchCache, historyRefresh, searchTorrents, SearchResult, HistoryEntry, CachedSearchResult } from '../api/client'
import ResultCard from '../components/ResultCard'
import DownloadModal from '../components/DownloadModal'
import { usePersistedState } from '../lib/storage'
import { usePlayer } from '../components/PlayerProvider'
import NavHeader from '../components/NavHeader'
import PullToRefreshIndicator from '../components/PullToRefreshIndicator'
import TorrentContentsModal from '../components/TorrentContentsModal'
import PlaylistPickerModal from '../components/PlaylistPickerModal'
import { useFilteredResults } from '../lib/useFilteredResults'
import { usePullToRefresh } from '../lib/usePullToRefresh'
import { formatDate } from '../lib/format'

type Mode = 'browse' | 'global'

type EntrySortKey = 'recent' | 'oldest' | 'most' | 'alpha'
type ResultSortKey = 'seeders' | 'size' | 'date' | 'title'

export default function HistoryPage() {
  const navigate = useNavigate()
  const [entries, setEntries] = useState<HistoryEntry[]>([])
  const [selected, setSelected] = useState<string | null>(null)
  const [results, setResults] = useState<SearchResult[]>([])
  const [loadingResults, setLoadingResults] = useState(false)
  const [downloadTarget, setDownloadTarget] = useState<SearchResult | null>(null)
  const { playSingle } = usePlayer()
  const [contentsTarget, setContentsTarget] = useState<SearchResult | null>(null)
  const [playlistTarget, setPlaylistTarget] = useState<SearchResult | null>(null)
  const [playlistTargetFile, setPlaylistTargetFile] = useState<{ index: number; title: string } | null>(null)

  // Filters for the query list. Sort persists; the text filter is transient
  // (reopening with a stale text filter would be confusing).
  const [queryFilter, setQueryFilter] = useState('')
  const [entrySort, setEntrySort] = usePersistedState<EntrySortKey>('history.entrySort', 'recent')

  // Filters for the results panel — sort/threshold/tracker persist across
  // sessions (e.g. "always hide < 10 seeders"); the text filter stays transient.
  const [resultFilter, setResultFilter] = useState('')
  const [resultSort, setResultSort] = usePersistedState<ResultSortKey>('history.resultSort', 'seeders')
  const [resultSortAsc, setResultSortAsc] = usePersistedState('history.resultSortAsc', false)
  const [minSeeders, setMinSeeders] = usePersistedState('history.minSeeders', 0)
  const [trackerFilter, setTrackerFilter] = usePersistedState('history.trackerFilter', 'all')

  // Pagination — render incrementally to avoid blocking on 1000s of cards
  const PAGE_SIZE = 60
  const [browseVisible, setBrowseVisible] = useState(PAGE_SIZE)
  const [globalVisible, setGlobalVisible] = useState(PAGE_SIZE)
  const browseSentinelRef = useRef<HTMLDivElement>(null)
  const globalSentinelRef = useRef<HTMLDivElement>(null)

  // Global FTS5 search mode
  const [mode, setMode] = useState<Mode>('browse')
  const [globalQuery, setGlobalQuery] = useState('')
  const [globalResults, setGlobalResults] = useState<CachedSearchResult[]>([])
  const [globalLoading, setGlobalLoading] = useState(false)
  const [globalSearched, setGlobalSearched] = useState(false)
  const debounceRef = useRef<number | null>(null)

  // Per-row refresh state — keyed by results.id. We track:
  //   refreshingIDs:  currently-in-flight POST /api/history/:id/refresh
  //   refreshedLabels: short "agora"/"5min" labels rendered near the seed count.
  // Map is preferred over arrays of objects because lookups are O(1) per card.
  const [refreshingIDs, setRefreshingIDs] = useState<Set<number>>(new Set())
  const [refreshedLabels, setRefreshedLabels] = useState<Map<number, string>>(new Map())

  const loadHistory = async () => {
    try { setEntries(await getHistory()) }
    catch { setEntries([]) }
  }

  useEffect(() => { loadHistory() }, [])

  const ptr = usePullToRefresh({
    onRefresh: async () => {
      await loadHistory()
      if (selected) {
        const data = await getHistoryResults(selected).catch(() => [])
        setResults(data || [])
      }
    },
  })

  const handleSelect = async (q: string) => {
    if (selected === q) return
    setSelected(q)
    setResultFilter('')
    setTrackerFilter('all')
    setMinSeeders(0)
    setBrowseVisible(PAGE_SIZE)
    setLoadingResults(true)
    try {
      const data = await getHistoryResults(q)
      setResults(data || [])
    } catch {
      setResults([])
    } finally {
      setLoadingResults(false)
    }
  }

  // Re-run the selected query live against Jackett (the history view shows the
  // CACHED results from when it was first searched; seeders/sources go stale).
  // Refreshes all results at once, vs the per-row seeders refresh.
  const [refreshingSearch, setRefreshingSearch] = useState(false)
  const handleRefreshSearch = async () => {
    if (!selected || refreshingSearch) return
    setRefreshingSearch(true)
    try {
      const fresh = await searchTorrents(selected, ['all'], 'all')
      if (fresh) setResults(fresh)
    } catch {
      /* keep the cached results on failure */
    } finally {
      setRefreshingSearch(false)
    }
  }

  // Reset visible counts whenever the filter/sort changes (avoid showing stale page)
  useEffect(() => { setBrowseVisible(PAGE_SIZE) }, [resultFilter, trackerFilter, minSeeders, resultSort, resultSortAsc])
  useEffect(() => { setGlobalVisible(PAGE_SIZE) }, [globalResults, resultFilter, trackerFilter, minSeeders, resultSort, resultSortAsc])

  // IntersectionObserver to grow `visible` as user scrolls near the bottom
  useEffect(() => {
    const sentinel = mode === 'global' ? globalSentinelRef.current : browseSentinelRef.current
    if (!sentinel) return
    const obs = new IntersectionObserver((entries) => {
      if (entries[0].isIntersecting) {
        if (mode === 'global') setGlobalVisible(v => v + PAGE_SIZE)
        else setBrowseVisible(v => v + PAGE_SIZE)
      }
    }, { rootMargin: '400px' })
    obs.observe(sentinel)
    return () => obs.disconnect()
  }, [mode, selected, globalResults.length, results.length])

  const handleClear = async () => {
    await clearHistory()
    setEntries([])
    setSelected(null)
    setResults([])
  }

  // Debounced global FTS search — triggers 300ms after user stops typing
  useEffect(() => {
    if (mode !== 'global') return
    if (!globalQuery.trim()) {
      setGlobalResults([])
      setGlobalSearched(false)
      return
    }
    if (debounceRef.current) clearTimeout(debounceRef.current)
    debounceRef.current = globalThis.setTimeout(async () => {
      setGlobalLoading(true)
      try {
        const data = await searchCache(globalQuery)
        setGlobalResults(data || [])
        setGlobalSearched(true)
      } catch {
        setGlobalResults([])
      } finally {
        setGlobalLoading(false)
      }
    }, 300)
    return () => { if (debounceRef.current) clearTimeout(debounceRef.current) }
  }, [globalQuery, mode])

  const handleDeleteEntry = async (q: string, e: React.MouseEvent) => {
    e.stopPropagation()
    await deleteHistoryEntry(q)
    setEntries(prev => prev.filter(en => en.query !== q))
    if (selected === q) {
      setSelected(null)
      setResults([])
    }
  }

  // Per-card refresh: re-polls Jackett for fresh seeders/leechers. We update
  // both the local results array AND the globalResults array so the same row
  // looks right whether it's reached via "Por busca" or "Busca global".
  // Backend implements a 5min TTL cache so spamming is cheap (no Jackett hit).
  const handleRefreshResult = async (result: SearchResult) => {
    if (result.id === undefined) return
    const id = result.id
    setRefreshingIDs(prev => {
      const next = new Set(prev)
      next.add(id)
      return next
    })
    try {
      const fresh = await historyRefresh(id)
      const updater = <T extends SearchResult>(arr: T[]): T[] =>
        arr.map(r => r.id === id ? { ...r, seeders: fresh.seeders, leechers: fresh.leechers } : r)
      setResults(prev => updater(prev))
      setGlobalResults(prev => updater(prev))
      // Show "agora" (5min cache) or "cache" hint right after click.
      setRefreshedLabels(prev => {
        const next = new Map(prev)
        next.set(id, fresh.cached ? 'cache' : 'agora')
        return next
      })
      // Fade the label out after 30s so old marks don't linger across many refreshes.
      globalThis.setTimeout(() => {
        setRefreshedLabels(prev => {
          const next = new Map(prev)
          next.delete(id)
          return next
        })
      }, 30_000)
    } catch {
      /* swallow — the spinner will simply stop without changes */
    } finally {
      setRefreshingIDs(prev => {
        const next = new Set(prev)
        next.delete(id)
        return next
      })
    }
  }

  // Filtered + sorted query list
  const filteredEntries = useMemo(() => {
    let e = entries.filter(en =>
      queryFilter === '' || en.query.toLowerCase().includes(queryFilter.toLowerCase())
    )
    switch (entrySort) {
      case 'recent':  e = [...e].sort((a, b) => b.lastSaved.localeCompare(a.lastSaved)); break
      case 'oldest':  e = [...e].sort((a, b) => a.lastSaved.localeCompare(b.lastSaved)); break
      case 'most':    e = [...e].sort((a, b) => b.resultCount - a.resultCount); break
      case 'alpha':   e = [...e].sort((a, b) => a.query.localeCompare(b.query)); break
    }
    return e
  }, [entries, queryFilter, entrySort])

  // All trackers in current result set
  const trackers = useMemo(() => {
    const set = new Set(results.map(r => r.tracker).filter(Boolean))
    return ['all', ...Array.from(set).sort((a, b) => a.localeCompare(b))]
  }, [results])

  // Filtered + sorted results (with infoHash grouping)
  const { filteredResults } = useFilteredResults(results, {
    minSeeders,
    trackerFilter,
    titleFilter: resultFilter,
    sortKey: resultSort,
    sortAsc: resultSortAsc,
  })

  const totalResults = entries.reduce((s, e) => s + e.resultCount, 0)

  // Global results — same filters/sort apply but operate on globalResults
  const { filteredResults: filteredGlobal } = useFilteredResults(globalResults, {
    minSeeders,
    trackerFilter,
    titleFilter: resultFilter,
    sortKey: resultSort,
    sortAsc: resultSortAsc,
  })

  const globalTrackers = useMemo(() => {
    const set = new Set(globalResults.map(r => r.tracker).filter(Boolean))
    return ['all', ...Array.from(set).sort((a, b) => a.localeCompare(b))]
  }, [globalResults])

  return (
    <div className="min-h-screen bg-gray-900 flex flex-col">
      <PullToRefreshIndicator pull={ptr.pull} progress={ptr.progress} refreshing={ptr.refreshing} />
      <NavHeader />

      <main className="flex-1 max-w-7xl 2xl:max-w-[min(95vw,1600px)] mx-auto w-full px-4 py-6 flex flex-col gap-4">
        {/* Top bar */}
        <div className="flex items-center justify-between flex-wrap gap-3">
          <div className="flex items-center gap-3">
            <History className="w-5 h-5 text-gray-400" />
            <h1 className="text-lg font-semibold text-gray-100">Histórico</h1>
            <div className="flex items-center gap-2 text-xs text-gray-500">
              <span className="bg-gray-800 border border-gray-700 px-2 py-0.5 rounded-full flex items-center gap-1">
                <Search className="w-3 h-3" />{entries.length} buscas
              </span>
              <span className="bg-gray-800 border border-gray-700 px-2 py-0.5 rounded-full flex items-center gap-1">
                <Database className="w-3 h-3" />{totalResults.toLocaleString()} resultados
              </span>
            </div>
          </div>
          <div className="flex items-center gap-3">
            {/* Mode toggle */}
            <div className="flex items-center gap-1 bg-gray-800 border border-gray-700 rounded-lg p-1">
              <button
                onClick={() => setMode('browse')}
                className={`flex items-center gap-1.5 text-xs px-3 py-1.5 rounded-md transition-colors ${
                  mode === 'browse' ? 'bg-green-500/20 text-green-400' : 'text-gray-400 hover:text-gray-200'
                }`}
              >
                <FolderOpen className="w-3.5 h-3.5" />
                Por busca
              </button>
              <button
                onClick={() => setMode('global')}
                className={`flex items-center gap-1.5 text-xs px-3 py-1.5 rounded-md transition-colors ${
                  mode === 'global' ? 'bg-green-500/20 text-green-400' : 'text-gray-400 hover:text-gray-200'
                }`}
              >
                <Globe className="w-3.5 h-3.5" />
                Busca global
              </button>
            </div>
            {entries.length > 0 && (
              <button onClick={handleClear} className="flex items-center gap-1.5 text-xs text-red-400 hover:text-red-300 transition-colors">
                <Trash2 className="w-3.5 h-3.5" />
                Limpar cache
              </button>
            )}
          </div>
        </div>

        {/* Global FTS5 search mode */}
        {mode === 'global' && (
          <div className="flex flex-col gap-4">
            <div className="relative">
              <Search className="absolute left-4 top-1/2 -translate-y-1/2 w-5 h-5 text-gray-400" />
              <input
                type="text"
                autoFocus
                placeholder="Busca full-text em TODOS os resultados em cache (ex: '1080p hevc', 'breaking bad')..."
                value={globalQuery}
                onChange={e => setGlobalQuery(e.target.value)}
                className="w-full bg-gray-800 border border-gray-700 rounded-xl pl-12 pr-12 py-3 text-base text-gray-100 placeholder-gray-500 focus:outline-none focus:border-green-500"
              />
              {globalQuery && (
                <button
                  onClick={() => setGlobalQuery('')}
                  className="absolute right-4 top-1/2 -translate-y-1/2 text-gray-500 hover:text-gray-300"
                >
                  <X className="w-4 h-4" />
                </button>
              )}
            </div>

            {/* Stats row */}
            {globalSearched && (
              <p className="text-sm text-gray-400 flex items-center gap-2">
                {globalLoading
                  ? <><Loader2 className="w-3.5 h-3.5 animate-spin" />Buscando...</>
                  : <><span className="text-gray-200 font-medium">{filteredGlobal.length}</span>
                      {filteredGlobal.length !== globalResults.length && <span>de {globalResults.length}</span>}
                      {' '}resultados em todo o cache para <span className="text-green-400">"{globalQuery}"</span></>
                }
              </p>
            )}

            {/* Filter toolbar (only when there are results) */}
            {globalResults.length > 0 && (
              <div className="flex flex-wrap items-center gap-2 p-3 bg-gray-800/60 rounded-xl border border-gray-700">
                <Filter className="w-3.5 h-3.5 text-gray-500" />
                <input
                  type="text" placeholder="Filtrar título..."
                  value={resultFilter}
                  onChange={e => setResultFilter(e.target.value)}
                  className="bg-gray-700 border border-gray-600 rounded-lg px-3 py-1.5 text-sm text-gray-100 placeholder-gray-500 focus:outline-none focus:border-green-500 w-44"
                />
                <select
                  value={trackerFilter}
                  onChange={e => setTrackerFilter(e.target.value)}
                  className="bg-gray-700 border border-gray-600 rounded-lg px-3 py-1.5 text-sm text-gray-300 focus:outline-none focus:border-green-500"
                >
                  {globalTrackers.map(t => (
                    <option key={t} value={t}>{t === 'all' ? 'Todos os servidores' : t}</option>
                  ))}
                </select>
                <label className="flex items-center gap-1.5 bg-gray-700 border border-gray-600 rounded-lg px-3 py-1.5">
                  <span className="text-xs text-gray-500">Seeds ≥</span>
                  <input
                    type="number" min={0}
                    value={minSeeders || ''}
                    placeholder="0"
                    onChange={e => setMinSeeders(Math.max(0, Number.parseInt(e.target.value) || 0))}
                    className="w-12 bg-transparent text-sm text-gray-200 focus:outline-none"
                  />
                </label>
                <div className="flex items-center gap-1 bg-gray-700 border border-gray-600 rounded-lg p-1 ml-auto">
                  {([['seeders','Seeds'],['size','Tamanho'],['date','Data'],['title','Nome']] as [ResultSortKey,string][]).map(([key, label]) => (
                    <button
                      key={key}
                      onClick={() => {
                        if (resultSort === key) setResultSortAsc(a => !a)
                        else { setResultSort(key); setResultSortAsc(false) }
                      }}
                      className={`flex items-center gap-1 text-xs px-2.5 py-1 rounded-md transition-colors ${
                        resultSort === key ? 'bg-green-500/20 text-green-400' : 'text-gray-400 hover:text-gray-200'
                      }`}
                    >
                      {label}
                      {resultSort === key && (resultSortAsc ? <SortAsc className="w-3 h-3" /> : <SortDesc className="w-3 h-3" />)}
                    </button>
                  ))}
                </div>
              </div>
            )}

            {/* Global results grid */}
            {globalLoading && globalResults.length === 0 && (
              <div className="flex items-center justify-center py-20 text-gray-500">
                <Loader2 className="w-8 h-8 animate-spin" />
              </div>
            )}

            {globalSearched && !globalLoading && filteredGlobal.length === 0 && (
              <div className="flex flex-col items-center justify-center py-16 text-gray-500">
                <Search className="w-12 h-12 mb-3 opacity-30" />
                <p className="font-medium">Nenhum resultado encontrado no cache</p>
                <p className="text-sm mt-1">Tente outros termos ou faça uma nova busca</p>
              </div>
            )}

            {filteredGlobal.length > 0 && (
              <>
                <div className="grid grid-cols-1 md:grid-cols-2 xl:grid-cols-3 gap-4">
                  {filteredGlobal.slice(0, globalVisible).map((result, i) => (
                    <div key={`${result.infoHash || result.link}-${i}`} className="flex flex-col gap-1">
                      <ResultCard
                        result={result}
                        onDownload={setDownloadTarget}
                        onPlay={(r) => playSingle(r)}
                        onAddToPlaylist={(r) => { setPlaylistTargetFile(null); setPlaylistTarget(r) }}
                        onExploreContents={setContentsTarget}
                        onRefresh={handleRefreshResult}
                        refreshing={result.id !== undefined && refreshingIDs.has(result.id)}
                        refreshedAt={result.id === undefined ? null : refreshedLabels.get(result.id) ?? null}
                      />
                      {result.query && (
                        <button
                          onClick={() => { setMode('browse'); handleSelect(result.query!) }}
                          className="text-[10px] text-gray-500 hover:text-green-400 transition-colors flex items-center gap-1 px-2 truncate"
                          title={`Ver todos os resultados da busca "${result.query}"`}
                        >
                          <FolderOpen className="w-2.5 h-2.5 flex-shrink-0" />
                          <span className="truncate">de: {result.query}</span>
                        </button>
                      )}
                    </div>
                  ))}
                </div>
                {globalVisible < filteredGlobal.length && (
                  <div ref={globalSentinelRef} className="text-center py-6 text-xs text-gray-500">
                    Mostrando {globalVisible} de {filteredGlobal.length} • role pra ver mais
                  </div>
                )}
              </>
            )}

            {!globalSearched && !globalQuery && (
              <div className="flex flex-col items-center justify-center py-20 text-gray-600">
                <Globe className="w-16 h-16 mb-4 opacity-30" />
                <p className="text-lg">Busca full-text em todo o cache</p>
                <p className="text-sm mt-2">Digite termos para encontrar resultados de qualquer busca anterior</p>
              </div>
            )}
          </div>
        )}

        {mode === 'browse' && (entries.length === 0 ? (
          <div className="flex flex-col items-center justify-center py-20 text-gray-500">
            <History className="w-16 h-16 mb-4 opacity-30" />
            <p className="text-xl font-medium">Nenhuma busca salva</p>
            <p className="text-sm mt-2">Faça uma busca para começar a acumular o cache</p>
            <Link to="/" className="mt-4 text-green-500 hover:text-green-400 text-sm transition-colors">Ir para busca</Link>
          </div>
        ) : (
          <div className="grid grid-cols-1 lg:grid-cols-[300px_1fr] gap-4 flex-1">
            {/* ── Left: Query list ── */}
            <div className="flex flex-col gap-2">
              {/* Query filter */}
              <div className="relative">
                <Search className="absolute left-3 top-1/2 -translate-y-1/2 w-3.5 h-3.5 text-gray-500" />
                <input
                  type="text"
                  placeholder="Filtrar buscas..."
                  value={queryFilter}
                  onChange={e => setQueryFilter(e.target.value)}
                  className="w-full bg-gray-800 border border-gray-700 rounded-lg pl-9 pr-8 py-2 text-sm text-gray-100 placeholder-gray-500 focus:outline-none focus:border-green-500"
                />
                {queryFilter && (
                  <button onClick={() => setQueryFilter('')} className="absolute right-3 top-1/2 -translate-y-1/2 text-gray-500 hover:text-gray-300">
                    <X className="w-3.5 h-3.5" />
                  </button>
                )}
              </div>

              {/* Sort options */}
              <div className="flex gap-1">
                {([['recent','Recente'],['oldest','Antiga'],['most','+ Resultados'],['alpha','A–Z']] as [EntrySortKey,string][]).map(([key, label]) => (
                  <button
                    key={key}
                    onClick={() => setEntrySort(key)}
                    className={`flex-1 text-xs px-2 py-1.5 rounded-lg transition-colors ${
                      entrySort === key ? 'bg-green-500/20 text-green-400 border border-green-500/30' : 'bg-gray-800 text-gray-400 border border-gray-700 hover:text-gray-200'
                    }`}
                  >
                    {label}
                  </button>
                ))}
              </div>

              {/* Entry list */}
              <div className="bg-gray-800 rounded-xl border border-gray-700 overflow-hidden flex-1 overflow-y-auto max-h-[calc(100vh-280px)]">
                {filteredEntries.length === 0 ? (
                  <p className="text-gray-500 text-sm text-center py-8">Nenhuma busca encontrada</p>
                ) : filteredEntries.map((entry) => (
                  <button
                    key={entry.query}
                    onClick={() => handleSelect(entry.query)}
                    className={`w-full flex items-start justify-between gap-2 px-4 py-3 text-sm transition-colors border-b border-gray-700/50 last:border-b-0 text-left ${
                      selected === entry.query ? 'bg-green-500/10 border-l-2 border-l-green-500' : 'hover:bg-gray-700/50'
                    }`}
                  >
                    <div className="flex-1 min-w-0">
                      <p
                        className={`truncate font-medium ${selected === entry.query ? 'text-green-400' : 'text-gray-200'}`}
                        title={entry.query}
                      >
                        {entry.query}
                      </p>
                      <div className="flex items-center gap-2 mt-0.5">
                        <span className="flex items-center gap-1 text-xs text-gray-500">
                          <Database className="w-2.5 h-2.5" />{entry.resultCount.toLocaleString()}
                        </span>
                        <span className="flex items-center gap-1 text-xs text-gray-500">
                          <Calendar className="w-2.5 h-2.5" />{formatDate(entry.lastSaved)}
                        </span>
                      </div>
                    </div>
                    <div className="flex items-center gap-1.5 flex-shrink-0 mt-0.5">
                      <button
                        onClick={e => { e.stopPropagation(); navigate(`/?q=${encodeURIComponent(entry.query)}`) }}
                        title="Nova busca"
                        className="text-gray-600 hover:text-green-400 transition-colors"
                      >
                        <Search className="w-3.5 h-3.5" />
                      </button>
                      <button
                        onClick={e => handleDeleteEntry(entry.query, e)}
                        title="Remover do cache"
                        className="text-gray-600 hover:text-red-400 transition-colors"
                      >
                        <Trash2 className="w-3.5 h-3.5" />
                      </button>
                    </div>
                  </button>
                ))}
              </div>
            </div>

            {/* ── Right: Results panel ── */}
            <div className="flex flex-col gap-3">
              {!selected && (
                <div className="flex flex-col items-center justify-center py-20 text-gray-600">
                  <ArrowUpDown className="w-10 h-10 mb-3 opacity-30" />
                  <p>Selecione uma busca para ver os resultados em cache</p>
                </div>
              )}

              {selected && (
                <>
                  {/* Results toolbar */}
                  <div className="flex flex-wrap items-center gap-2">
                    {/* Text filter */}
                    <div className="relative flex-1 min-w-[180px]">
                      <Filter className="absolute left-3 top-1/2 -translate-y-1/2 w-3.5 h-3.5 text-gray-500" />
                      <input
                        type="text"
                        placeholder="Filtrar títulos..."
                        value={resultFilter}
                        onChange={e => setResultFilter(e.target.value)}
                        className="w-full bg-gray-800 border border-gray-700 rounded-lg pl-9 pr-8 py-2 text-sm text-gray-100 placeholder-gray-500 focus:outline-none focus:border-green-500"
                      />
                      {resultFilter && (
                        <button onClick={() => setResultFilter('')} className="absolute right-3 top-1/2 -translate-y-1/2 text-gray-500 hover:text-gray-300">
                          <X className="w-3.5 h-3.5" />
                        </button>
                      )}
                    </div>

                    {/* Tracker filter */}
                    <select
                      value={trackerFilter}
                      onChange={e => setTrackerFilter(e.target.value)}
                      className="bg-gray-800 border border-gray-700 rounded-lg px-3 py-2 text-sm text-gray-300 focus:outline-none focus:border-green-500"
                    >
                      {trackers.map(t => (
                        <option key={t} value={t}>{t === 'all' ? 'Todos os trackers' : t}</option>
                      ))}
                    </select>

                    {/* Min seeders */}
                    <div className="flex items-center gap-2 bg-gray-800 border border-gray-700 rounded-lg px-3 py-2">
                      <span className="text-xs text-gray-500">Mín seeds</span>
                      <input
                        type="number"
                        min={0}
                        value={minSeeders}
                onChange={e => setMinSeeders(Math.max(0, Number.parseInt(e.target.value) || 0))}
                        className="w-14 bg-transparent text-sm text-gray-200 focus:outline-none"
                      />
                    </div>

                    {/* Sort */}
                    <div className="flex items-center gap-1 bg-gray-800 border border-gray-700 rounded-lg p-1">
                      {([['seeders','Seeds'],['size','Tamanho'],['date','Data'],['title','Título']] as [ResultSortKey,string][]).map(([key, label]) => (
                        <button
                          key={key}
                          onClick={() => {
                            if (resultSort === key) setResultSortAsc(a => !a)
                            else { setResultSort(key); setResultSortAsc(false) }
                          }}
                          className={`flex items-center gap-1 text-xs px-2.5 py-1 rounded-md transition-colors ${
                            resultSort === key ? 'bg-green-500/20 text-green-400' : 'text-gray-400 hover:text-gray-200'
                          }`}
                        >
                          {label}
                          {resultSort === key && (resultSortAsc ? <SortAsc className="w-3 h-3" /> : <SortDesc className="w-3 h-3" />)}
                        </button>
                      ))}
                    </div>
                  </div>

                  {/* Results count + re-run search live */}
                  <div className="flex items-center justify-between gap-2 flex-wrap">
                    <p className="text-xs text-gray-500">
                      {loadingResults ? 'Carregando...' : (
                        <>
                          <span className="text-gray-300 font-medium">{filteredResults.length}</span>
                          {filteredResults.length !== results.length && <span> de {results.length}</span>}
                          {' '}resultado{filteredResults.length === 1 ? '' : 's'} em cache para{' '}
                          <span className="text-green-400 font-medium">"{selected}"</span>
                        </>
                      )}
                    </p>
                    {selected && !loadingResults && (
                      <button
                        onClick={handleRefreshSearch}
                        disabled={refreshingSearch}
                        title="Buscar de novo no Jackett — atualiza seeders e traz resultados novos"
                        className="flex items-center gap-1.5 text-xs bg-green-500/15 hover:bg-green-500/25 text-green-300 border border-green-500/30 px-3 py-1.5 rounded-lg transition-colors disabled:opacity-50"
                      >
                        {refreshingSearch ? <Loader2 className="w-3.5 h-3.5 animate-spin" /> : <RefreshCw className="w-3.5 h-3.5" />}
                        {refreshingSearch ? 'Atualizando...' : 'Atualizar busca'}
                      </button>
                    )}
                  </div>

                  {/* Skeletons */}
                  {loadingResults && (
                    <div className="grid grid-cols-1 md:grid-cols-2 xl:grid-cols-3 gap-4">
                      {Array.from({ length: 6 }, () => crypto.randomUUID()).map(key => (
                        <div key={key} className="card animate-pulse flex flex-col gap-3">
                          <div className="h-4 bg-gray-700 rounded w-3/4" />
                          <div className="h-3 bg-gray-700 rounded w-1/4" />
                          <div className="grid grid-cols-2 gap-2">
                            <div className="h-3 bg-gray-700 rounded" />
                            <div className="h-3 bg-gray-700 rounded" />
                          </div>
                        </div>
                      ))}
                    </div>
                  )}

                  {/* Empty */}
                  {!loadingResults && filteredResults.length === 0 && (
                    <div className="text-gray-500 text-sm py-10 text-center">
                      {results.length === 0
                        ? `Nenhum resultado em cache para "${selected}"`
                        : 'Nenhum resultado com os filtros aplicados'}
                    </div>
                  )}

                  {/* Grid (paginated via infinite scroll) */}
                  {!loadingResults && filteredResults.length > 0 && (
                    <>
                      <div className="grid grid-cols-1 md:grid-cols-2 xl:grid-cols-3 gap-4">
                        {filteredResults.slice(0, browseVisible).map((result, i) => (
                          <ResultCard
                            key={`${result.infoHash || result.link}-${i}`}
                            result={{ ...result, cached: true }}
                            onDownload={setDownloadTarget}
                            onPlay={(r) => playSingle(r)}
                            onAddToPlaylist={(r) => { setPlaylistTargetFile(null); setPlaylistTarget(r) }}
                            onExploreContents={setContentsTarget}
                            onRefresh={handleRefreshResult}
                            refreshing={result.id !== undefined && refreshingIDs.has(result.id)}
                            refreshedAt={result.id === undefined ? null : refreshedLabels.get(result.id) ?? null}
                          />
                        ))}
                      </div>
                      {browseVisible < filteredResults.length && (
                        <div ref={browseSentinelRef} className="text-center py-6 text-xs text-gray-500">
                          Mostrando {browseVisible} de {filteredResults.length} • role pra ver mais
                        </div>
                      )}
                    </>
                  )}
                </>
              )}
            </div>
          </div>
        ))}
      </main>

      <DownloadModal result={downloadTarget} onClose={() => setDownloadTarget(null)} />
      <PlaylistPickerModal
        result={playlistTarget}
        fileIndex={playlistTargetFile?.index}
        fileTitle={playlistTargetFile?.title}
        onClose={() => { setPlaylistTarget(null); setPlaylistTargetFile(null) }}
      />
      <TorrentContentsModal
        result={contentsTarget}
        onClose={() => setContentsTarget(null)}
        onPlayFile={(r, fileIdx) => {
          setContentsTarget(null)
          playSingle(r, fileIdx)
        }}
        onAddFileToPlaylist={(r, fileIdx, title) => {
          setContentsTarget(null)
          setPlaylistTargetFile({ index: fileIdx, title })
          setPlaylistTarget(r)
        }}
      />
    </div>
  )
}
