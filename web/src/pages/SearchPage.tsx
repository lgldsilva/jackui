import { useState, useEffect, useRef, useCallback, useMemo } from 'react'
import { useSearchParams } from 'react-router-dom'
import {
  SearchX, Wifi, WifiOff, Loader2,
  Plus, X, Filter, SortAsc, SortDesc, Play,
} from 'lucide-react'
import SearchBar from '../components/SearchBar'
import ResultCard, { refreshFavoritesCache } from '../components/ResultCard'
import DownloadModal from '../components/DownloadModal'
import { usePlayer } from '../components/PlayerProvider'
import PlaylistPickerModal from '../components/PlaylistPickerModal'
import TorrentContentsModal from '../components/TorrentContentsModal'
import NavHeader from '../components/NavHeader'
import { SearchResult, Indexer, getIndexers, favoritesList, withToken } from '../api/client'
import { load, save } from '../lib/storage'
import { useFilteredResults } from '../lib/useFilteredResults'
import { isIncognito } from '../lib/incognito'

const TABS_KEY = 'searchTabs'
const ACTIVE_KEY = 'activeTabId'
// Last-used filter preferences, applied to every NEW tab/search so a setting
// like "min 10 seeders" sticks instead of resetting to 0 on each fresh search.
const FILTER_DEFAULTS_KEY = 'searchFilterDefaults'
// One-shot flag: corrige filtros antigos persistidos no browser que escondiam
// resultados — `onlyPlayable` ligado matava todo torrent sem magnet (trackers
// privados como o amigos-share só expõem o .torrent), e `minSeeders=0` deixava
// passar torrents mortos. Migra uma vez para os novos defaults.
const FILTER_MIGRATION_KEY = 'searchFiltersMigratedV1'

type FilterDefaults = {
  trackerFilter: string
  minSeeders: number
  minLeechers: number
  maxSizeGb: string
  resultSort: ResultSortKey
  resultSortAsc: boolean
  onlyPlayable: boolean
}

const FALLBACK_FILTERS: FilterDefaults = {
  // minSeeders=1 é o único filtro ligado por padrão: esconde torrents mortos
  // (0 seeds) sem mexer em mais nada. onlyPlayable nasce sempre desligado e não
  // é persistido — antes ele escondia silenciosamente conteúdo sem magnet.
  trackerFilter: 'all', minSeeders: 1, minLeechers: 0, maxSizeGb: '',
  resultSort: 'seeders', resultSortAsc: false, onlyPlayable: false,
}

// What we persist (NOT the live SSE results — those re-fetch when the user re-searches)
type PersistedTab = {
  id: string
  query: string
  selectedIndexers: string[]
  selectedCategory: string
  titleFilter: string
  trackerFilter: string
  minSeeders: number
  minLeechers: number
  maxSizeGb: string
  resultSort: ResultSortKey
  resultSortAsc: boolean
  onlyPlayable: boolean
}

type SearchPhase = 'idle' | 'cache' | 'live' | 'done' | 'error'
type ResultSortKey = 'seeders' | 'leechers' | 'size' | 'title' | 'age'

type TabState = {
  id: string
  query: string
  results: SearchResult[]
  phase: SearchPhase
  error: string
  summary: { total: number; live: number; cached: number } | null
  selectedIndexers: string[]
  selectedCategory: string
  // Filters (per-tab, persisted across tab switches)
  titleFilter: string
  trackerFilter: string
  minSeeders: number
  minLeechers: number
  maxSizeGb: string
  resultSort: ResultSortKey
  resultSortAsc: boolean
  onlyPlayable: boolean
}

function newTab(id: string): TabState {
  // Seed filters from the user's last-used preferences so a new search keeps
  // e.g. the "min 10 seeders" threshold instead of starting at zero.
  const d = load<FilterDefaults>(FILTER_DEFAULTS_KEY, FALLBACK_FILTERS)
  return {
    id, query: '', results: [], phase: 'idle', error: '', summary: null,
    selectedIndexers: [], selectedCategory: 'all',
    titleFilter: '',
    trackerFilter: d.trackerFilter,
    minSeeders: d.minSeeders, minLeechers: d.minLeechers, maxSizeGb: d.maxSizeGb,
    resultSort: d.resultSort, resultSortAsc: d.resultSortAsc,
    // Nunca herdado/persistido: o toggle vale só para a sessão atual.
    onlyPlayable: false,
  }
}

let tabCounter = 1

function hydrateTabs(): { tabs: TabState[]; activeId: string } {
  // Migração one-shot dos defaults: floor de minSeeders em 1 (não derrubamos um
  // valor que o usuário tenha subido) e onlyPlayable desligado.
  const migrated = load<boolean>(FILTER_MIGRATION_KEY, false)
  if (!migrated) {
    const d = load<FilterDefaults>(FILTER_DEFAULTS_KEY, FALLBACK_FILTERS)
    if (d.minSeeders < 1 || d.onlyPlayable) {
      save<FilterDefaults>(FILTER_DEFAULTS_KEY, { ...d, minSeeders: Math.max(1, d.minSeeders), onlyPlayable: false })
    }
  }

  const persisted = load<PersistedTab[]>(TABS_KEY, [])
  if (persisted.length === 0) {
    if (!migrated) save(FILTER_MIGRATION_KEY, true)
    const id = String(tabCounter++)
    return { tabs: [newTab(id)], activeId: id }
  }
  // Restore counter so new tabs get unique IDs beyond persisted ones
  const maxId = persisted.reduce((m, t) => Math.max(m, Number.parseInt(t.id) || 0), 0)
  tabCounter = maxId + 1
  // onlyPlayable nunca é restaurado (deixou de esconder sem-magnet); na migração
  // inicial, abas que estavam em 0 seeds passam a 1 — sem mexer em valores >0.
  const tabs = persisted.map(p => {
    const t = { ...newTab(p.id), ...p, onlyPlayable: false }
    if (!migrated && t.minSeeders < 1) t.minSeeders = 1
    return t
  })
  if (!migrated) save(FILTER_MIGRATION_KEY, true)
  const savedActive = load<string>(ACTIVE_KEY, '')
  const activeId = tabs.some(t => t.id === savedActive) ? savedActive : tabs[0].id
  return { tabs, activeId }
}

function persistTabs(tabs: TabState[], activeId: string) {
  // Incognito must not leave a trace in the browser either — skip persisting
  // the search tabs/queries to localStorage while it's active (the backend
  // already skips history/library writes).
  if (isIncognito()) return
  const stripped: PersistedTab[] = tabs.map(t => ({
    id: t.id,
    query: t.query,
    selectedIndexers: t.selectedIndexers,
    selectedCategory: t.selectedCategory,
    titleFilter: t.titleFilter,
    trackerFilter: t.trackerFilter,
    minSeeders: t.minSeeders,
    minLeechers: t.minLeechers,
    maxSizeGb: t.maxSizeGb,
    resultSort: t.resultSort,
    resultSortAsc: t.resultSortAsc,
    onlyPlayable: t.onlyPlayable,
  }))
  save(TABS_KEY, stripped)
  save(ACTIVE_KEY, activeId)
}

function appendResult(prev: TabState[], tabId: string, result: SearchResult): TabState[] {
  return prev.map(t => t.id === tabId ? { ...t, results: [...t.results, result] } : t)
}

function setErrorMsg(prev: TabState[], tabId: string, message: string): TabState[] {
  return prev.map(t => t.id === tabId ? { ...t, error: message } : t)
}

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

function PhaseIndicator({ phase }: { readonly phase: SearchPhase }) {
  if (phase === 'idle') return null
  if (phase === 'cache' || phase === 'live')
    return <span className="w-2 h-2 rounded-full bg-yellow-400 animate-pulse flex-shrink-0" />
  if (phase === 'done')
    return <span className="w-2 h-2 rounded-full bg-green-400 flex-shrink-0" />
  return <span className="w-2 h-2 rounded-full bg-red-400 flex-shrink-0" />
}

const SORT_OPTIONS: { key: ResultSortKey; label: string }[] = [
  { key: 'seeders',  label: 'Seeds'    },
  { key: 'leechers', label: 'Leechers' },
  { key: 'size',     label: 'Tamanho'  },
  { key: 'title',    label: 'Nome'     },
  { key: 'age',      label: 'Data'     },
]

export default function SearchPage() {
  const initial = hydrateTabs()
  const [tabs, setTabs] = useState<TabState[]>(initial.tabs)
  const [activeId, setActiveId] = useState(initial.activeId)
  const [indexers, setIndexers] = useState<Indexer[]>([])
  const [discoveredIndexers, setDiscoveredIndexers] = useState<Indexer[]>([])

  // Carrega indexadores autodescobertos persistidos
  useEffect(() => {
    setDiscoveredIndexers(load<Indexer[]>('discoveredIndexers', []))
  }, [])

  // Coleta novos indexadores a partir dos resultados de busca
  useEffect(() => {
    if (tabs.length === 0) return
    const allResults = tabs.flatMap(t => t.results)
    if (allResults.length === 0) return

    const discoveredMap = new Map<string, Indexer>()
    discoveredIndexers.forEach(idx => discoveredMap.set(idx.id, idx))

    let mutated = false
    allResults.forEach(r => {
      const id = r.trackerId || r.tracker.toLowerCase().replaceAll(/[^a-z0-9]+/g, '-')
      if (id && !discoveredMap.has(id)) {
        discoveredMap.set(id, {
          id,
          name: r.tracker,
          description: `Descoberto via busca (${r.tracker})`,
          configured: true,
          language: '',
          type: ''
        })
        mutated = true
      }
    })

    if (mutated) {
      const nextList = Array.from(discoveredMap.values())
      setDiscoveredIndexers(nextList)
      save('discoveredIndexers', nextList)
    }
  }, [tabs, discoveredIndexers])

  const allIndexers = useMemo(() => {
    const map = new Map<string, Indexer>()
    indexers.forEach(i => map.set(i.id, i))
    discoveredIndexers.forEach(i => map.set(i.id, i))
    return Array.from(map.values())
  }, [indexers, discoveredIndexers])

  const [downloadTarget, setDownloadTarget] = useState<SearchResult | null>(null)
  const { playSingle } = usePlayer()
  const [playlistTarget, setPlaylistTarget] = useState<SearchResult | null>(null)
  const [playlistTargetFile, setPlaylistTargetFile] = useState<{ index: number; title: string } | null>(null)
  const [contentsTarget, setContentsTarget] = useState<SearchResult | null>(null)
  const esMap = useRef<Map<string, EventSource>>(new Map())
  const searchInputRef = useRef<HTMLInputElement>(null)
  // Infinite scroll pagination (grows as user scrolls)
  const PAGE_SIZE = 60
  const [visible, setVisible] = useState(PAGE_SIZE)
  const sentinelRef = useRef<HTMLDivElement>(null)

  useEffect(() => {
    getIndexers().then(setIndexers).catch(() => {})
    // Populate the global favorites cache so cards know which results are starred
    favoritesList()
      .then(list => refreshFavoritesCache(list.map(f => ({ name: f.name, infoHash: f.infoHash }))))
      .catch(() => {})
  }, [])

  useEffect(() => {
    return () => { esMap.current.forEach(es => es.close()) }
  }, [])

  // Persist tabs whenever they change (debounced via React batching)
  useEffect(() => {
    persistTabs(tabs, activeId)
    // Remember the active tab's filter prefs as the global default so the next
    // new search inherits them (the "min 10 seeders sticks" behaviour).
    const a = tabs.find(t => t.id === activeId)
    if (a) {
      save<FilterDefaults>(FILTER_DEFAULTS_KEY, {
        trackerFilter: a.trackerFilter,
        minSeeders: a.minSeeders,
        minLeechers: a.minLeechers,
        maxSizeGb: a.maxSizeGb,
        resultSort: a.resultSort,
        resultSortAsc: a.resultSortAsc,
        onlyPlayable: a.onlyPlayable,
      })
    }
  }, [tabs, activeId])

  // Reset visible count when active tab or its filters change
  useEffect(() => { setVisible(PAGE_SIZE) }, [activeId])

  // IntersectionObserver — load more results as user scrolls near bottom
  useEffect(() => {
    const sentinel = sentinelRef.current
    if (!sentinel) return
    const obs = new IntersectionObserver((entries) => {
      if (entries[0].isIntersecting) setVisible(v => v + PAGE_SIZE)
    }, { rootMargin: '400px' })
    obs.observe(sentinel)
    return () => obs.disconnect()
  }, [activeId, tabs])

  const closeActiveTab = useCallback(() => {
    setTabs(prev => {
      if (prev.length === 1) return prev
      const es = esMap.current.get(activeId)
      if (es) { es.close(); esMap.current.delete(activeId) }
      const next = prev.filter(t => t.id !== activeId)
      const idx = prev.findIndex(t => t.id === activeId)
      setActiveId(next[Math.max(0, idx - 1)].id)
      return next
    })
  }, [activeId])

  // Keyboard shortcuts
  useEffect(() => {
    const onKey = (e: KeyboardEvent) => {
      const target = e.target as HTMLElement
      const inField = target && (target.tagName === 'INPUT' || target.tagName === 'TEXTAREA' || target.tagName === 'SELECT')
      const cmd = e.metaKey || e.ctrlKey

      // Cmd+T → new tab
      if (cmd && e.key === 't') {
        e.preventDefault()
        addTabAndFocus()
        return
      }
      // Cmd+W → close active tab
      if (cmd && e.key === 'w') {
        e.preventDefault()
        closeActiveTab()
        return
      }
      // Cmd+1..9 → switch tab by index
      if (cmd && /^[1-9]$/.test(e.key)) {
        const idx = Number.parseInt(e.key) - 1
        if (idx < tabs.length) {
          e.preventDefault()
          setActiveId(tabs[idx].id)
        }
        return
      }
      // "/" → focus search input (only when not in a field)
      if (!inField && e.key === '/') {
        e.preventDefault()
        searchInputRef.current?.focus()
        searchInputRef.current?.select()
      }
    }
    globalThis.addEventListener('keydown', onKey)
    return () => globalThis.removeEventListener('keydown', onKey)
  // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [tabs, activeId])

  const updateTab = useCallback((id: string, patch: Partial<TabState>) => {
    setTabs(prev => prev.map(t => t.id === id ? { ...t, ...patch } : t))
  }, [])

  const handleSearch = useCallback((tabId: string, queryOverride?: string) => {
    const tab = tabs.find(t => t.id === tabId)
    // queryOverride lets a caller (e.g. the Discover page seeding via ?q=) run a
    // search before the tab's query state has propagated through a re-render.
    const q = (queryOverride ?? tab?.query ?? '').trim()
    if (!tab || !q) return

    const existing = esMap.current.get(tabId)
    if (existing) { existing.close(); esMap.current.delete(tabId) }

    updateTab(tabId, { results: [], error: '', summary: null, phase: 'cache' })

    const params = new URLSearchParams({ q })
    if (tab.selectedIndexers.length > 0 && tab.selectedIndexers[0] !== 'all')
      params.set('indexers', tab.selectedIndexers.join(','))
    if (tab.selectedCategory && tab.selectedCategory !== 'all')
      params.set('category', tab.selectedCategory)
    if (isIncognito()) params.set('incognito', '1')

    // EventSource can't set Authorization header — inject Bearer as query token instead.
    // The middleware's extractToken() reads ?token= as a fallback.
    const es = new EventSource(withToken(`/api/search/stream?${params}`))
    esMap.current.set(tabId, es)

    // SSE payloads come from the network — a malformed/empty frame must not
    // throw out of the listener (an uncaught exception there would leave the tab
    // stuck "searching" forever). Parse defensively; the generic `error` event
    // isn't even a MessageEvent (no .data), so guard that too.
    const parseSSE = (raw: unknown): any => {
      if (typeof raw !== 'string' || raw === '') return null
      try { return JSON.parse(raw) } catch { return null }
    }

    es.addEventListener('result', (e) => {
      const result = parseSSE(e.data) as SearchResult | null
      if (!result) return
      setTabs(prev => appendResult(prev, tabId, result))
    })

    es.addEventListener('progress', (e) => {
      const data = parseSSE(e.data)
      if (data?.phase === 'live') updateTab(tabId, { phase: 'live' })
    })

    es.addEventListener('done', (e) => {
      const data = parseSSE(e.data)
      updateTab(tabId, { summary: data, phase: 'done' })
      es.close()
      esMap.current.delete(tabId)
    })

    es.addEventListener('error', (e) => {
      const data = parseSSE((e as MessageEvent).data)
      if (data) {
        setTabs(prev => setErrorMsg(prev, tabId, data.message || 'Erro na busca'))
      }
    })

    es.onerror = () => {
      if (esMap.current.has(tabId)) {
        updateTab(tabId, { phase: 'error', error: 'Conexão perdida com o servidor' })
        es.close()
        esMap.current.delete(tabId)
      }
    }
  // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [tabs, updateTab])

  // Seed a search from ?q= (e.g. clicking a poster on the Discover page). Runs
  // once: fills the active tab's query, fires the search via the override (so it
  // doesn't wait for state to propagate), then clears the param so a refresh
  // doesn't re-trigger.
  const [searchParams, setSearchParams] = useSearchParams()
  const seededRef = useRef(false)
  useEffect(() => {
    if (seededRef.current) return
    const q = searchParams.get('q')
    if (!q) return
    seededRef.current = true
    updateTab(activeId, { query: q })
    handleSearch(activeId, q)
    setSearchParams({}, { replace: true })
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [])

  const addTab = () => {
    const id = String(tabCounter++)
    setTabs(prev => [...prev, newTab(id)])
    setActiveId(id)
  }

  const addTabAndFocus = () => {
    addTab()
    setTimeout(() => searchInputRef.current?.focus(), 50)
  }

  const closeTab = (id: string, e?: React.MouseEvent) => {
    e?.stopPropagation()
    const existing = esMap.current.get(id)
    if (existing) { existing.close(); esMap.current.delete(id) }
    setTabs(prev => {
      if (prev.length === 1) return prev
      const next = prev.filter(t => t.id !== id)
      if (id === activeId) {
        const idx = prev.findIndex(t => t.id === id)
        setActiveId(next[Math.max(0, idx - 1)].id)
      }
      return next
    })
  }

  const activeTab = tabs.find(t => t.id === activeId) ?? tabs[0]
  const isSearching = activeTab.phase === 'cache' || activeTab.phase === 'live'

  // All trackers seen in current tab results
  const trackers = useMemo(() => {
    const set = new Set(activeTab.results.map(r => r.tracker).filter(Boolean))
    return ['all', ...Array.from(set).sort((a, b) => a.localeCompare(b))]
  }, [activeTab.results])

  // Filtered + sorted results (after dedup-grouping by infoHash)
  const { filteredResults, groupedCount } = useFilteredResults(activeTab.results, {
    minSeeders: activeTab.minSeeders,
    minLeechers: activeTab.minLeechers,
    maxBytes: activeTab.maxSizeGb ? Number.parseFloat(activeTab.maxSizeGb) * 1024 ** 3 : Infinity,
    trackerFilter: activeTab.trackerFilter,
    titleFilter: activeTab.titleFilter,
    onlyPlayable: activeTab.onlyPlayable,
    sortKey: activeTab.resultSort,
    sortAsc: activeTab.resultSortAsc,
  })

  const hasResults = activeTab.results.length > 0
  // `isFiltered` agora reflete só REDUÇÃO POR FILTRO do usuário, NÃO por
  // deduplicação. groupedCount é o universo já agrupado (mesmo torrent em
  // múltiplos trackers vira 1) — a contagem que faz sentido pro usuário.
  const isFiltered = filteredResults.length !== groupedCount
  const hasDuplicates = groupedCount !== activeTab.results.length

  return (
    <div className="min-h-screen bg-gray-900 flex flex-col">
      <NavHeader />

      {/* Tab strip */}
      <div className="bg-gray-800/60 border-b border-gray-700 px-4">
        <div className="max-w-7xl 2xl:max-w-[min(95vw,1600px)] mx-auto flex items-end gap-0.5 overflow-x-auto">
          {tabs.map(tab => (
            <button
              key={tab.id}
              onClick={() => setActiveId(tab.id)}
              className={`group flex items-center gap-2 px-4 py-2.5 text-sm rounded-t-lg transition-colors min-w-0 max-w-[200px] border-t border-l border-r flex-shrink-0 ${
                tab.id === activeId
                  ? 'bg-gray-900 border-gray-700 text-gray-100'
                  : 'border-transparent text-gray-500 hover:text-gray-300 hover:bg-gray-800'
              }`}
            >
              <PhaseIndicator phase={tab.phase} />
              <span className="truncate flex-1 text-left">
                {tab.query.trim() || 'Nova busca'}
              </span>
              {tabs.length > 1 && (
                <button
                  type="button"
                  onClick={e => closeTab(tab.id, e)}
                  onKeyDown={e => { if (e.key === 'Enter' || e.key === ' ') { e.preventDefault(); closeTab(tab.id) } }}
                  className="opacity-60 sm:opacity-0 sm:group-hover:opacity-100 hover:text-red-400 transition-all flex-shrink-0 cursor-pointer p-0.5"
                >
                  <X className="w-3.5 h-3.5" />
                </button>
              )}
            </button>
          ))}
          <button
            onClick={addTab}
            className="flex items-center justify-center w-8 h-8 mb-0.5 text-gray-500 hover:text-gray-200 hover:bg-gray-700 rounded-lg transition-colors flex-shrink-0"
            title="Nova aba de busca"
          >
            <Plus className="w-4 h-4" />
          </button>
        </div>
      </div>

      <main className="flex-1 max-w-7xl 2xl:max-w-[min(95vw,1600px)] mx-auto w-full px-4 py-6 flex flex-col gap-4">
        {/* Search bar */}
        <SearchBar
          ref={searchInputRef}
          query={activeTab.query}
          onQueryChange={q => updateTab(activeTab.id, { query: q })}
          selectedIndexers={activeTab.selectedIndexers}
          onIndexersChange={sel => updateTab(activeTab.id, { selectedIndexers: sel })}
          selectedCategory={activeTab.selectedCategory}
          onCategoryChange={cat => updateTab(activeTab.id, { selectedCategory: cat })}
          indexers={allIndexers}
          onSearch={() => handleSearch(activeTab.id)}
          loading={isSearching}
        />

        {/* Status bar */}
        {activeTab.phase !== 'idle' && (
          <div className="flex items-center gap-3 text-sm flex-wrap">
            {activeTab.phase === 'cache' && (
              <span className="flex items-center gap-2 text-blue-400">
                <Loader2 className="w-3.5 h-3.5 animate-spin" />Carregando cache...
              </span>
            )}
            {activeTab.phase === 'live' && (
              <span className="flex items-center gap-2 text-yellow-400">
                <Wifi className="w-3.5 h-3.5 animate-pulse" />Buscando ao vivo nos indexadores...
              </span>
            )}
            {activeTab.phase === 'done' && activeTab.summary && (
              <span className="flex items-center gap-2 text-green-400">
                <Wifi className="w-3.5 h-3.5" />
                {isFiltered ? (
                  <><span className="text-gray-200 font-medium">{filteredResults.length}</span> de {groupedCount} únicos</>
                ) : (
                  <>
                    <span className="text-gray-200 font-medium">{groupedCount}</span>
                    {' '}{groupedCount === 1 ? 'único' : 'únicos'}
                    {hasDuplicates && (
                      <span
                        className="text-gray-500"
                        title={`${activeTab.results.length} resultados brutos antes de agrupar duplicatas por hash/título`}
                      >
                        {' '}(de {activeTab.results.length} brutos)
                      </span>
                    )}
                  </>
                )}{' '}para <span className="text-gray-200 font-medium">"{activeTab.query}"</span>
                <span className="text-gray-500">
                  ({activeTab.summary.live} ao vivo, {activeTab.summary.cached} cache)
                </span>
              </span>
            )}
            {activeTab.phase === 'error' && (
              <span className="flex items-center gap-2 text-red-400">
                <WifiOff className="w-3.5 h-3.5" />{activeTab.error || 'Erro na busca'}
              </span>
            )}
            {isSearching && hasResults && (
              <span className="text-gray-500 ml-auto">{activeTab.results.length} até agora</span>
            )}
          </div>
        )}

        {/* Filter + Sort toolbar — shown once results start arriving */}
        {hasResults && (
          <div className="flex flex-wrap items-center gap-2 p-3 bg-gray-800/60 rounded-xl border border-gray-700">
            <Filter className="w-3.5 h-3.5 text-gray-500 flex-shrink-0" />

            {/* Title filter */}
            <input
              type="text"
              placeholder="Filtrar título..."
              value={activeTab.titleFilter}
              onChange={e => updateTab(activeTab.id, { titleFilter: e.target.value })}
              className="bg-gray-700 border border-gray-600 rounded-lg px-3 py-1.5 text-sm text-gray-100 placeholder-gray-500 focus:outline-none focus:border-green-500 w-full sm:w-44"
            />

            {/* Tracker dropdown */}
            <select
              value={activeTab.trackerFilter}
              onChange={e => updateTab(activeTab.id, { trackerFilter: e.target.value })}
              className="bg-gray-700 border border-gray-600 rounded-lg px-3 py-1.5 text-sm text-gray-300 focus:outline-none focus:border-green-500"
            >
              {trackers.map(t => (
                <option key={t} value={t}>{t === 'all' ? 'Todos os servidores' : t}</option>
              ))}
            </select>

            {/* Min seeders */}
            <label className="flex items-center gap-1.5 bg-gray-700 border border-gray-600 rounded-lg px-3 py-1.5">
              <span className="text-xs text-gray-500 whitespace-nowrap">Seeds ≥</span>
              <input
                type="number" min={0}
                value={activeTab.minSeeders || ''}
                placeholder="0"
                onChange={e => updateTab(activeTab.id, { minSeeders: Math.max(0, Number.parseInt(e.target.value) || 0) })}
                className="w-12 bg-transparent text-sm text-gray-200 focus:outline-none"
              />
            </label>

            {/* Min leechers */}
            <label className="flex items-center gap-1.5 bg-gray-700 border border-gray-600 rounded-lg px-3 py-1.5">
              <span className="text-xs text-gray-500 whitespace-nowrap">Leech ≥</span>
              <input
                type="number" min={0}
                value={activeTab.minLeechers || ''}
                placeholder="0"
                onChange={e => updateTab(activeTab.id, { minLeechers: Math.max(0, Number.parseInt(e.target.value) || 0) })}
                className="w-12 bg-transparent text-sm text-gray-200 focus:outline-none"
              />
            </label>

            {/* Max size */}
            <label className="flex items-center gap-1.5 bg-gray-700 border border-gray-600 rounded-lg px-3 py-1.5">
              <span className="text-xs text-gray-500 whitespace-nowrap">Máx GB</span>
              <input
                type="number" min={0} step={0.1}
                value={activeTab.maxSizeGb}
                placeholder="∞"
                onChange={e => updateTab(activeTab.id, { maxSizeGb: e.target.value })}
                className="w-14 bg-transparent text-sm text-gray-200 focus:outline-none"
              />
            </label>

            {/* Only playable toggle */}
            <button
              onClick={() => updateTab(activeTab.id, { onlyPlayable: !activeTab.onlyPlayable })}
              title="Mostrar apenas resultados que podem ser reproduzidos no player (vídeo)"
              className={`flex items-center gap-1.5 text-xs px-3 py-1.5 rounded-lg transition-colors border ${
                activeTab.onlyPlayable
                  ? 'bg-purple-500/20 text-purple-300 border-purple-500/30'
                  : 'bg-gray-700 hover:bg-gray-600 text-gray-300 border-gray-600'
              }`}
            >
              <Play className={`w-3.5 h-3.5 ${activeTab.onlyPlayable ? 'fill-current' : ''}`} />
              Playable
            </button>

            {/* Sort buttons */}
            <div className="flex items-center gap-1 bg-gray-700 border border-gray-600 rounded-lg p-1 ml-auto">
              {SORT_OPTIONS.map(({ key, label }) => (
                <button
                  key={key}
                  onClick={() => {
                    if (activeTab.resultSort === key) {
                      updateTab(activeTab.id, { resultSortAsc: !activeTab.resultSortAsc })
                    } else {
                      updateTab(activeTab.id, { resultSort: key, resultSortAsc: false })
                    }
                  }}
                  className={`flex items-center gap-1 text-xs px-2.5 py-1 rounded-md transition-colors ${
                    activeTab.resultSort === key
                      ? 'bg-green-500/20 text-green-400'
                      : 'text-gray-400 hover:text-gray-200'
                  }`}
                >
                  {label}
                  {activeTab.resultSort === key && (
                    activeTab.resultSortAsc
                      ? <SortAsc className="w-3 h-3" />
                      : <SortDesc className="w-3 h-3" />
                  )}
                </button>
              ))}
            </div>

            {/* Reset filters — only if any active */}
            {(activeTab.titleFilter || activeTab.trackerFilter !== 'all' || activeTab.minSeeders !== 1 || activeTab.minLeechers > 0 || activeTab.maxSizeGb || activeTab.onlyPlayable) && (
              <button
                onClick={() => updateTab(activeTab.id, {
                  titleFilter: '', trackerFilter: 'all',
                  minSeeders: 1, minLeechers: 0, maxSizeGb: '',
                  onlyPlayable: false,
                })}
                className="text-xs text-gray-500 hover:text-red-400 transition-colors flex items-center gap-1"
                title="Limpar filtros"
              >
                <X className="w-3.5 h-3.5" />Limpar
              </button>
            )}
          </div>
        )}

        {/* Soft error with results */}
        {activeTab.error && hasResults && (
          <div className="bg-yellow-500/10 border border-yellow-500/30 text-yellow-400 rounded-xl px-4 py-2 text-sm">
            {activeTab.error}
          </div>
        )}

        {/* Hard error */}
        {activeTab.error && !hasResults && (
          <div className="bg-red-500/10 border border-red-500/30 text-red-400 rounded-xl p-4">
            <p className="font-medium">Erro na busca</p>
            <p className="text-sm mt-1">{activeTab.error}</p>
          </div>
        )}

        {/* Loading skeletons */}
        {isSearching && !hasResults && (
          <div className="grid grid-cols-1 md:grid-cols-2 lg:grid-cols-3 gap-4">
            {Array.from({ length: 9 }, () => crypto.randomUUID()).map(key => <SkeletonCard key={key} />)}
          </div>
        )}

        {/* Results grid (paginated via infinite scroll) */}
        {hasResults && filteredResults.length > 0 && (
          <>
            <div className="grid grid-cols-1 md:grid-cols-2 lg:grid-cols-3 gap-4">
              {filteredResults.slice(0, visible).map((result, i) => (
                <ResultCard
                  key={`${result.infoHash || result.link}-${i}`}
                  result={result}
                  onDownload={setDownloadTarget}
                  onPlay={(r) => playSingle(r)}
                  onAddToPlaylist={(r) => { setPlaylistTargetFile(null); setPlaylistTarget(r) }}
                  onExploreContents={setContentsTarget}
                />
              ))}
            </div>
            {visible < filteredResults.length && (
              <div ref={sentinelRef} className="text-center py-6 text-xs text-gray-500">
                Mostrando {visible} de {filteredResults.length} • role pra ver mais
              </div>
            )}
          </>
        )}

        {/* Empty after filter */}
        {hasResults && filteredResults.length === 0 && (
          <div className="flex flex-col items-center justify-center py-16 text-gray-500">
            <SearchX className="w-12 h-12 mb-3 opacity-30" />
            <p className="font-medium">Nenhum resultado com os filtros aplicados</p>
            <p className="text-sm mt-1">{activeTab.results.length} resultado{(activeTab.results.length === 1 ? '' : 's')} disponíve{(activeTab.results.length === 1 ? 'l' : 'is')} antes dos filtros</p>
          </div>
        )}

        {/* Empty after search */}
        {activeTab.phase === 'done' && !hasResults && !activeTab.error && (
          <div className="flex flex-col items-center justify-center py-20 text-gray-500">
            <SearchX className="w-16 h-16 mb-4 opacity-30" />
            <p className="text-xl font-medium">Nenhum resultado encontrado</p>
            <p className="text-sm mt-2">Tente termos diferentes ou outros indexers</p>
          </div>
        )}

        {/* Initial state */}
        {activeTab.phase === 'idle' && (
          <div className="flex flex-col items-center justify-center py-20 text-gray-600">
            <p className="text-lg">Digite algo para buscar torrents</p>
          </div>
        )}
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
