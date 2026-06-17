import { useState, useEffect, useRef, useCallback, useMemo } from 'react'
import { useSearchParams } from 'react-router-dom'
import {
  SearchX, Wifi, WifiOff, Loader2,
  Plus, X, Filter, SortAsc, SortDesc, Play, Sparkles, Layers,
} from 'lucide-react'
import SearchBar from '../components/SearchBar'
import ResultCard, { refreshFavoritesCache } from '../components/ResultCard'
import DownloadModal from '../components/DownloadModal'
import { usePlayer } from '../components/PlayerProvider'
import PlaylistPickerModal from '../components/PlaylistPickerModal'
import TorrentContentsModal from '../components/TorrentContentsModal'
import { useScrollRestoration } from '../lib/useScrollRestoration'
import NavHeader from '../components/NavHeader'
import { Sheet } from '../components/Sheet'
import SavedSearches from '../components/SavedSearches'
import { SearchResult, Indexer, getIndexers, getHistory, favoritesList, withToken, saveConfig, testJackettConnection } from '../api/client'
import { load, save } from '../lib/storage'
import { getTabResults, mergeCachedResults, syncTabsToCache } from '../lib/searchResultsCache'
import type { SearchPhase } from '../lib/searchResultsCache'
import { useRehydratedResults, canApplyRehydrated } from '../lib/useRehydratedResults'
import { useFilteredResults } from '../lib/useFilteredResults'
import { useMediaMode } from '../lib/mediaMode'
import { MusicSearchFilterToggle } from '../components/MusicSearchFilterToggle'
import { buildSeriesLayout } from '../lib/seriesGroup'
import { isIncognito } from '../lib/incognito'
import { useSwipe } from '../lib/useSwipe'
import { uid } from '../lib/uid'
import { shouldPromptJackettSetup } from '../lib/jackettSetup'
import { appendUnique, openSearchStream, type SearchStreamHandle } from '../lib/searchStream'
import { useTranslation } from 'react-i18next'

const TABS_KEY = 'searchTabs'
const ACTIVE_KEY = 'activeTabId'
// Last-used filter preferences, applied to every NEW tab/search so a setting
// like "min 10 seeders" sticks instead of resetting to 0 on each fresh search.
const FILTER_DEFAULTS_KEY = 'searchFilterDefaults'
// One-shot flag: corrige filtros antigos persistidos no browser que escondiam
// resultados — `onlyPlayable` ligado matava qualquer torrent sem magnet (trackers
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
  resolution: string
  hdrOnly: boolean
  codecGroup: string
}

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
  // Quality filters (onda 3). Per-tab, persisted; not part of the global
  // FilterDefaults (quality is per-search, unlike "min seeders").
  resolution: string
  hdrOnly: boolean
  codecGroup: string
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
    resolution: '', hdrOnly: false, codecGroup: '',
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
    // localStorage never stores results — pull them back from the in-memory
    // cache (same tab id + same query) so SPA navigation keeps the search.
    return mergeCachedResults(t, getTabResults(t.id))
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
    resolution: t.resolution,
    hdrOnly: t.hdrOnly,
    codecGroup: t.codecGroup,
  }))
  save(TABS_KEY, stripped)
  save(ACTIVE_KEY, activeId)
}

// appendResult dedupes by infoHash (or tracker|title|size) — an SSE reconnect
// replays the backend's cache phase, so re-received results must be absorbed
// instead of duplicating cards. Returns `prev` untouched on a duplicate so
// React skips the re-render.
function appendResult(prev: TabState[], tabId: string, result: SearchResult): TabState[] {
  const tab = prev.find(t => t.id === tabId)
  if (!tab) return prev
  const next = appendUnique(tab.results, result)
  if (next === tab.results) return prev
  return prev.map(t => t.id === tabId ? { ...t, results: next as SearchResult[] } : t)
}

function setErrorMsg(prev: TabState[], tabId: string, message: string): TabState[] {
  return prev.map(t => t.id === tabId ? { ...t, error: message } : t)
}

function SkeletonCard() {
  return (
    <div className="card animate-pulse flex flex-col gap-3">
      <div className="h-4 bg-surface-tertiary rounded w-3/4" />
      <div className="h-3 bg-surface-tertiary rounded w-1/4" />
      <div className="grid grid-cols-2 gap-2">
        <div className="h-3 bg-surface-tertiary rounded" />
        <div className="h-3 bg-surface-tertiary rounded" />
        <div className="h-3 bg-surface-tertiary rounded" />
        <div className="h-3 bg-surface-tertiary rounded" />
      </div>
      <div className="flex gap-2 pt-1 border-t border-default">
        <div className="h-7 bg-surface-tertiary rounded flex-1" />
        <div className="h-7 bg-surface-tertiary rounded flex-1" />
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
  const { t } = useTranslation()
  const initial = hydrateTabs()
  const [tabs, setTabs] = useState<TabState[]>(initial.tabs)
  const [activeId, setActiveId] = useState(initial.activeId)
  // Restaura o scroll da aba ativa quando ela já tem resultados (best-effort: em
  // re-busca os resultados chegam por SSE, então pode restaurar parcialmente).
  useScrollRestoration((tabs.find(t => t.id === activeId)?.results.length ?? 0) > 0)
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
      if (!id) return
      if (discoveredMap.has(id)) return  // already known by this ID

      // When a real trackerId arrives, evict any stale entry with the same
      // display name but a different (possibly synthetic) ID. Without this,
      // re-discovering "Amigos Share Club" under its real Jackett ID leaves
      // the old synthetic-id entry, causing the same indexer to appear twice.
      if (r.trackerId) {
        const nameLower = r.tracker.toLowerCase()
        for (const [staleId, stale] of discoveredMap) {
          if (stale.name.toLowerCase() === nameLower && staleId !== id) {
            discoveredMap.delete(staleId)
            break
          }
        }
      }

      discoveredMap.set(id, {
        id,
        name: r.tracker,
        description: `Descoberto via busca (${r.tracker})`,
        configured: true,
        language: '',
        type: ''
      })
      mutated = true
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
    // Final dedup by display name: prevents a stale entry (old synthetic ID)
    // from appearing alongside a newer entry with the same name but a real ID.
    const byName = new Map<string, Indexer>()
    for (const idx of map.values()) {
      const key = idx.name.toLowerCase().trim()
      const existing = byName.get(key)
      if (existing) {
        // Prefer the entry whose ID is NOT the synthetic derivation of the name
        const synthetic = idx.name.toLowerCase().replaceAll(/[^a-z0-9]+/g, '-')
        if (existing.id === synthetic && idx.id !== synthetic) {
          byName.set(key, idx)
        }
      } else {
        byName.set(key, idx)
      }
    }
    return Array.from(byName.values())
  }, [indexers, discoveredIndexers])

  const [downloadTarget, setDownloadTarget] = useState<SearchResult | null>(null)
  const { playSingle } = usePlayer()
  const [playlistTarget, setPlaylistTarget] = useState<SearchResult | null>(null)
  const [playlistTargetFile, setPlaylistTargetFile] = useState<{ index: number; title: string } | null>(null)
  const [contentsTarget, setContentsTarget] = useState<SearchResult | null>(null)
  const [filterSheetOpen, setFilterSheetOpen] = useState(false)

  // Jackett connection status — for first-run / config prompt
  const [showJackettSetup, setShowJackettSetup] = useState(false)
  const [setupUrl, setSetupUrl] = useState('')
  const [setupKey, setSetupKey] = useState('')
  const [setupTesting, setSetupTesting] = useState(false)
  const [setupError, setSetupError] = useState('')
  const [setupTestOk, setSetupTestOk] = useState(false)

  useEffect(() => {
    // Check if Jackett is actually configured before showing the setup prompt.
    // If the network request fails (transient Electron GPU crash), don't prompt —
    // the config might already be saved.
    fetch('/api/status')
      .then(r => r.json())
      .then(d => {
        if (d.jackett === 'ok') return
        // Only prompt if there's truly no config saved. /api/config is admin-only,
        // so capture response.ok: an unreadable config (non-admin 403, transient
        // error) must NOT be misread as "unconfigured" — see lib/jackettSetup.ts.
        fetch('/api/config')
          .then(async r => ({ ok: r.ok, body: r.ok ? await r.json() : {} }))
          .then(({ ok, body }) => {
            if (shouldPromptJackettSetup(d.jackett, { ok, jackettUrl: body?.jackett?.url, apiKeySet: body?.jackett?.apiKeySet })) {
              setShowJackettSetup(true)
            }
          })
          .catch(() => {})
      })
      .catch(() => {}) // network error — don't prompt, config might be saved
  }, [])

  // Shared runner for the Jackett setup prompt's "Testar" / "Salvar e Testar"
  // buttons: validates the URL, runs `action` (test-only or save+test) with one
  // transient-network retry, and manages the shared setup* UI state — so the two
  // buttons stay a single skeleton instead of duplicating the retry/try-catch.
  const runJackettSetup = async (
    action: () => Promise<{ success: boolean; error?: string }>,
    onSuccess: () => void,
  ) => {
    if (!setupUrl.trim()) { setSetupError('Informe a URL do Jackett'); return }
    setSetupTesting(true); setSetupError(''); setSetupTestOk(false)
    const isNetErr = (e: unknown) => e instanceof Error && e.message.includes('Network Error')
    try {
      let d
      try { d = await action() }
      catch (err) {
        if (!isNetErr(err)) throw err
        await new Promise(r => setTimeout(r, 3000))
        d = await action()
      }
      if (d.success) onSuccess()
      else setSetupError(d.error || 'Falha ao conectar — verifique a URL e a porta')
    } catch (err) {
      setSetupError(err instanceof Error ? err.message : 'Erro ao testar conexão')
    }
    setSetupTesting(false)
  }
  const esMap = useRef<Map<string, SearchStreamHandle>>(new Map())
  const searchInputRef = useRef<HTMLInputElement>(null)
  // Infinite scroll pagination (grows as user scrolls)
  const PAGE_SIZE = 60
  const [visible, setVisible] = useState(PAGE_SIZE)
  const [historyQueries, setHistoryQueries] = useState<string[]>([])
  // "Group series" view mode (persisted, not per-tab): folds episodes of the
  // same series/season under a header. Additive — off by default.
  const [groupSeries, setGroupSeries] = useState<boolean>(() => load<boolean>('searchGroupSeries', false))
  const sentinelRef = useRef<HTMLDivElement>(null)

  useEffect(() => {
    getIndexers().then(setIndexers).catch(() => {})
    // Past queries → autocomplete suggestions + "recent" chips on the idle screen.
    getHistory().then(h => setHistoryQueries(h.map(e => e.query))).catch(() => {})
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

  // Mirror results into the in-memory cache (survives SPA navigation — the
  // localStorage persistence above intentionally drops them). The sync also
  // evicts entries of closed tabs and of tabs whose results were cleared.
  // Runs in incognito too: it never touches localStorage.
  useEffect(() => { syncTabsToCache(tabs) }, [tabs])

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

    // EventSource can't set Authorization header — inject Bearer as query token
    // instead (the middleware's extractToken() reads ?token= as a fallback).
    // openSearchStream owns the connection: a drop before `done` reconnects
    // with backoff while the tab stays in 'live' (the backend's cache phase
    // re-emits what already arrived; appendResult dedupes the replay). Only
    // after the retry budget is exhausted does the tab go to 'error'.
    const handle = openSearchStream(withToken(`/api/search/stream?${params}`), {
      onResult: (result) => setTabs(prev => appendResult(prev, tabId, result as SearchResult)),
      onLive: () => updateTab(tabId, { phase: 'live' }),
      onServerError: (message) => setTabs(prev => setErrorMsg(prev, tabId, message)),
      onDone: (summary) => {
        esMap.current.delete(tabId)
        updateTab(tabId, { summary: summary as TabState['summary'], phase: 'done' })
      },
      onGiveUp: () => {
        esMap.current.delete(tabId)
        updateTab(tabId, { phase: 'error', error: 'Conexão perdida com o servidor' })
      },
    })
    esMap.current.set(tabId, handle)
  // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [tabs, updateTab])

  // Abort the in-flight search for a tab. Closing the EventSource cancels the
  // request on the backend (the SSE handler watches c.Request.Context()), so the
  // indexers stop being polled. Partial results already received stay on screen;
  // the phase goes to 'done' (not 'error') since the stop was intentional.
  const stopSearch = useCallback((tabId: string) => {
    const es = esMap.current.get(tabId)
    if (es) { es.close(); esMap.current.delete(tabId) }
    updateTab(tabId, { phase: 'done' })
  }, [updateTab])

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

  // Post-reload: the in-memory cache is gone, so a restored tab has a query
  // but no results. Quietly refill from the backend cache of that EXACT query
  // (GET /api/history/results — no live Jackett hit). Only tabs captured at
  // mount are eligible; typing never triggers it. The guard re-checks the tab
  // at apply time so an in-flight search is never overwritten.
  const applyRehydrated = useCallback((tabId: string, query: string, results: SearchResult[]) => {
    setTabs(prev => prev.map(t =>
      canApplyRehydrated(t, tabId, query) ? { ...t, results, phase: 'done' } : t))
  }, [])
  useRehydratedResults(tabs, activeTab.id, applyRehydrated)

  // Mobile gesture: horizontal swipe over the content switches search tabs
  // (left = next, right = previous). Edge band is reserved (ignoreEdgePx) so a
  // right-swipe from the screen edge still opens the nav drawer instead.
  const mainRef = useRef<HTMLElement>(null)
  const stripRef = useRef<HTMLDivElement>(null)
  const activeTabRef = useRef<HTMLButtonElement>(null)
  const switchTab = useCallback((delta: number) => {
    const idx = tabs.findIndex(t => t.id === activeId)
    const next = idx + delta
    if (next >= 0 && next < tabs.length) setActiveId(tabs[next].id)
  }, [tabs, activeId])
  useSwipe(mainRef, { onLeft: () => switchTab(1), onRight: () => switchTab(-1) },
    { enabled: tabs.length > 1, ignoreEdgePx: 28, threshold: 70 })
  // Keep the selected tab visible in the horizontal strip when it changes
  // (e.g. via swipe) — otherwise it can sit off-screen on a narrow phone.
  useEffect(() => {
    activeTabRef.current?.scrollIntoView({ inline: 'center', block: 'nearest', behavior: 'smooth' })
  }, [activeId])

  // All trackers seen in current tab results
  const trackers = useMemo(() => {
    const set = new Set(activeTab.results.map(r => r.tracker).filter(Boolean))
    return ['all', ...Array.from(set).sort((a, b) => a.localeCompare(b))]
  }, [activeTab.results])

  // Modo Música (NavHeader): a busca filtra pra áudio. `showAll` é um escape
  // EFÊMERO (não persiste em TabState) — some ao trocar de modo ou de aba.
  const [mediaMode] = useMediaMode()
  const [showAll, setShowAll] = useState(false)
  useEffect(() => { setShowAll(false) }, [mediaMode, activeTab.id])

  // Filtered + sorted results (after dedup-grouping by infoHash)
  const { filteredResults, groupedCount } = useFilteredResults(activeTab.results, {
    minSeeders: activeTab.minSeeders,
    minLeechers: activeTab.minLeechers,
    maxBytes: activeTab.maxSizeGb ? Number.parseFloat(activeTab.maxSizeGb) * 1024 ** 3 : Infinity,
    trackerFilter: activeTab.trackerFilter,
    titleFilter: activeTab.titleFilter,
    onlyPlayable: activeTab.onlyPlayable,
    audioOnly: mediaMode === 'audio' && !showAll,
    resolution: activeTab.resolution,
    hdrOnly: activeTab.hdrOnly,
    codecGroup: activeTab.codecGroup,
    sortKey: activeTab.resultSort,
    sortAsc: activeTab.resultSortAsc,
  })

  const hasResults = activeTab.results.length > 0
  // `isFiltered` agora reflete só REDUÇÃO POR FILTRO do usuário, NÃO por
  // deduplicação. groupedCount é o universo já agrupado (mesmo torrent em
  // múltiplos trackers vira 1) — a contagem que faz sentido pro usuário.
  const isFiltered = filteredResults.length !== groupedCount
  const hasDuplicates = groupedCount !== activeTab.results.length

  const activeFilterCount = [
    activeTab.titleFilter,
    activeTab.trackerFilter !== 'all',
    activeTab.minSeeders > 1, // 0 ou 1 = permissivo/default; só conta se o user subiu
    activeTab.minLeechers > 0,
    activeTab.maxSizeGb,
    activeTab.onlyPlayable,
    activeTab.resolution,
    activeTab.hdrOnly,
    activeTab.codecGroup,
  ].filter(Boolean).length

  // clearFilters zera TODOS os filtros que podem esconder resultados, do jeito MAIS
  // permissivo possível — inclusive minSeeders=0 (revela torrents com 0 seeders, que
  // o default minSeeders=1 escondia) e os de qualidade (resolução/codec/HDR, que
  // descartam silenciosamente o que não tem aquela metadata, ex.: software/ISO sem
  // resolução) — e levanta a restrição de modo áudio/vídeo (showAll). É o "mostrar
  // tudo" do banner de ocultos: depois dele, filteredResults == groupedCount.
  const clearFilters = () => {
    setShowAll(true)
    updateTab(activeTab.id, {
      titleFilter: '', trackerFilter: 'all',
      minSeeders: 0, minLeechers: 0, maxSizeGb: '',
      onlyPlayable: false,
      resolution: '', hdrOnly: false, codecGroup: '',
    })
  }

  // Shared result-card renderer, reused by the flat list and the grouped layout
  // so both paths get the exact same actions (download/play/playlist/explore).
  const renderResultCard = (result: SearchResult, key: string) => (
    <ResultCard
      key={key}
      result={result}
      onDownload={setDownloadTarget}
      onPlay={(r) => playSingle(r)}
      onAddToPlaylist={(r) => { setPlaylistTargetFile(null); setPlaylistTarget(r) }}
      onExploreContents={setContentsTarget}
    />
  )

  const toggleGroupSeries = () => setGroupSeries(prev => { const next = !prev; save('searchGroupSeries', next); return next })

  // Campos de filtro compartilhados entre a barra inline (desktop) e o Sheet
  // (mobile). `stacked` controla a largura: no desktop os campos fluem no
  // flex-wrap (display:contents nos numéricos); no Sheet ficam full-width.
  const filterFields = (stacked: boolean) => (
    <>
      <input
        type="text"
        placeholder={t('search.filter_title')}
        value={activeTab.titleFilter}
        onChange={e => updateTab(activeTab.id, { titleFilter: e.target.value })}
        className={`bg-surface-tertiary border border-strong rounded-lg px-3 py-1.5 text-base sm:text-sm text-text-primary placeholder-gray-500 focus:outline-none focus:border-green-500 ${stacked ? 'w-full' : 'w-full sm:w-44'}`}
      />
      <select
        value={activeTab.trackerFilter}
        onChange={e => updateTab(activeTab.id, { trackerFilter: e.target.value })}
        className={`bg-surface-tertiary border border-strong rounded-lg px-3 py-1.5 text-base sm:text-sm text-text-primary focus:outline-none focus:border-green-500 ${stacked ? 'w-full' : ''}`}
      >
        {trackers.map(tOption => (
          <option key={tOption} value={tOption}>{tOption === 'all' ? t('search.all_servers') : tOption}</option>
        ))}
      </select>
      <div className={stacked ? 'grid grid-cols-3 gap-2' : 'contents'}>
        <label className={`flex items-center gap-1.5 bg-surface-tertiary border border-strong rounded-lg px-3 py-1.5 ${stacked ? 'w-full justify-between' : ''}`}>
          <span className="text-xs text-text-muted whitespace-nowrap">Seeds ≥</span>
          <input
            type="number" min={0}
            value={activeTab.minSeeders || ''}
            placeholder="0"
            onChange={e => updateTab(activeTab.id, { minSeeders: Math.max(0, Number.parseInt(e.target.value) || 0) })}
            className="w-12 bg-transparent text-base sm:text-sm text-text-primary focus:outline-none"
          />
        </label>
        <label className={`flex items-center gap-1.5 bg-surface-tertiary border border-strong rounded-lg px-3 py-1.5 ${stacked ? 'w-full justify-between' : ''}`}>
          <span className="text-xs text-text-muted whitespace-nowrap">Leech ≥</span>
          <input
            type="number" min={0}
            value={activeTab.minLeechers || ''}
            placeholder="0"
            onChange={e => updateTab(activeTab.id, { minLeechers: Math.max(0, Number.parseInt(e.target.value) || 0) })}
            className="w-12 bg-transparent text-base sm:text-sm text-text-primary focus:outline-none"
          />
        </label>
        <label className={`flex items-center gap-1.5 bg-surface-tertiary border border-strong rounded-lg px-3 py-1.5 ${stacked ? 'w-full justify-between' : ''}`}>
          <span className="text-xs text-text-muted whitespace-nowrap">Máx GB</span>
          <input
            type="number" min={0} step={0.1}
            value={activeTab.maxSizeGb}
            placeholder="∞"
            onChange={e => updateTab(activeTab.id, { maxSizeGb: e.target.value })}
            className="w-14 bg-transparent text-base sm:text-sm text-text-primary focus:outline-none"
          />
        </label>
      </div>
      <button
        onClick={() => updateTab(activeTab.id, { onlyPlayable: !activeTab.onlyPlayable })}
        title="Mostrar apenas resultados que podem ser reproduzidos no player (vídeo)"
        className={`flex items-center gap-1.5 text-sm px-3 py-1.5 rounded-lg transition-colors border ${stacked ? 'w-full justify-center' : ''} ${
          activeTab.onlyPlayable
            ? 'bg-purple-500/20 text-purple-700 dark:text-purple-300 border-purple-500/30'
            : 'bg-surface-tertiary hover:bg-surface-tertiary text-text-primary border-strong'
        }`}
      >
        <Play className={`w-3.5 h-3.5 ${activeTab.onlyPlayable ? 'fill-current' : ''}`} />
        {t('search.playable')}
      </button>
      <select
        value={activeTab.resolution}
        onChange={e => updateTab(activeTab.id, { resolution: e.target.value })}
        title="Filtrar por resolução"
        className={`bg-surface-tertiary border border-strong rounded-lg px-3 py-1.5 text-base sm:text-sm text-text-primary focus:outline-none focus:border-green-500 ${stacked ? 'w-full' : ''}`}
      >
        <option value="">{t('search.resolution')}</option>
        <option value="2160p">4K (2160p)</option>
        <option value="1080p">1080p</option>
        <option value="720p">720p</option>
        <option value="480p">480p</option>
      </select>
      <select
        value={activeTab.codecGroup}
        onChange={e => updateTab(activeTab.id, { codecGroup: e.target.value })}
        title="Filtrar por codec de vídeo"
        className={`bg-surface-tertiary border border-strong rounded-lg px-3 py-1.5 text-base sm:text-sm text-text-primary focus:outline-none focus:border-green-500 ${stacked ? 'w-full' : ''}`}
      >
        <option value="">{t('search.codec')}</option>
        <option value="hevc">H.265 / HEVC</option>
        <option value="h264">H.264</option>
        <option value="av1">AV1</option>
      </select>
      <button
        onClick={() => updateTab(activeTab.id, { hdrOnly: !activeTab.hdrOnly })}
        title="Mostrar apenas HDR ou Dolby Vision"
        className={`flex items-center gap-1.5 text-sm px-3 py-1.5 rounded-lg transition-colors border ${stacked ? 'w-full justify-center' : ''} ${
          activeTab.hdrOnly
            ? 'bg-yellow-500/20 text-yellow-700 dark:text-yellow-300 border-yellow-500/30'
            : 'bg-surface-tertiary hover:bg-surface-tertiary text-text-primary border-strong'
        }`}
      >
        <Sparkles className={`w-3.5 h-3.5 ${activeTab.hdrOnly ? 'fill-current' : ''}`} />
        {t('search.hdr')}
      </button>
      <button
        onClick={toggleGroupSeries}
        title="Agrupar episódios da mesma série por temporada"
        className={`flex items-center gap-1.5 text-sm px-3 py-1.5 rounded-lg transition-colors border ${stacked ? 'w-full justify-center' : ''} ${
          groupSeries
            ? 'bg-green-500/20 text-green-700 dark:text-green-300 border-green-500/30'
            : 'bg-surface-tertiary hover:bg-surface-tertiary text-text-primary border-strong'
        }`}
      >
        <Layers className="w-3.5 h-3.5" />
        {t('search.series')}
      </button>
      {activeFilterCount > 0 && (
        <button
          onClick={clearFilters}
          className={`text-xs text-text-muted hover:text-red-400 transition-colors flex items-center gap-1 ${stacked ? 'w-full justify-center py-2' : ''}`}
          title={t('search.clean_filters')}
        >
          <X className="w-3.5 h-3.5" />{t('search.clean')}
        </button>
      )}
    </>
  )

  // sortControls is the result-ordering control. No celular (sem espaço pro
  // segmented control de 5 opções, que gerava scroll horizontal no header) vira
  // um dropdown compacto + botão asc/desc; no desktop fica o segmented control.
  const toggleSort = (key: ResultSortKey) => {
    if (activeTab.resultSort === key) {
      updateTab(activeTab.id, { resultSortAsc: !activeTab.resultSortAsc })
    } else {
      updateTab(activeTab.id, { resultSort: key, resultSortAsc: false })
    }
  }
  const sortControls = () => (
    <>
      {/* Mobile: dropdown compacto — nunca estoura a largura da tela. */}
      <div className="flex sm:hidden items-center gap-1.5 min-w-0 w-full">
        <span className="text-xs text-text-muted flex-shrink-0">Ordenar:</span>
        <select
          value={activeTab.resultSort}
          onChange={e => updateTab(activeTab.id, { resultSort: e.target.value as ResultSortKey, resultSortAsc: false })}
          className="flex-1 min-w-0 text-xs bg-surface-tertiary border border-strong rounded-lg px-2 py-1.5 text-text-primary"
        >
          {SORT_OPTIONS.map(({ key, label }) => <option key={key} value={key}>{label}</option>)}
        </select>
        <button
          onClick={() => updateTab(activeTab.id, { resultSortAsc: !activeTab.resultSortAsc })}
          title={activeTab.resultSortAsc ? 'Crescente' : 'Decrescente'}
          className="flex-shrink-0 p-1.5 rounded-lg bg-surface-tertiary border border-strong text-text-secondary"
        >
          {activeTab.resultSortAsc ? <SortAsc className="w-3.5 h-3.5" /> : <SortDesc className="w-3.5 h-3.5" />}
        </button>
      </div>
      {/* Desktop: segmented control (wrap em vez de scroll horizontal). */}
      <div className="hidden sm:flex items-center gap-1.5 max-w-full">
        <span className="text-xs text-text-muted flex-shrink-0">Ordenar:</span>
        <div className="flex items-center gap-1 bg-surface-tertiary border border-strong rounded-lg p-1 flex-wrap">
          {SORT_OPTIONS.map(({ key, label }) => (
            <button
              key={key}
              onClick={() => toggleSort(key)}
              className={`flex items-center gap-1 text-xs px-2.5 py-1 rounded-md transition-colors whitespace-nowrap ${
                activeTab.resultSort === key
                  ? 'bg-green-500/20 text-green-400'
                  : 'text-text-secondary hover:text-text-primary'
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
      </div>
    </>
  )

  return (
    <div className="min-h-screen bg-surface flex flex-col">
      <NavHeader />

      {/* Tab strip */}
      <div className="bg-surface-secondary/60 border-b border-default px-4">
        <div ref={stripRef} className="max-w-7xl 2xl:max-w-[min(95vw,1600px)] mx-auto flex items-end gap-0.5 overflow-x-auto scroll-smooth snap-x safe-left">
          {tabs.map(tab => (
            <button
              key={tab.id}
              ref={tab.id === activeId ? activeTabRef : undefined}
              onClick={() => setActiveId(tab.id)}
              className={`group flex items-center gap-2 px-4 py-2.5 text-sm rounded-t-lg transition-colors min-w-0 max-w-[200px] border-t border-l border-r flex-shrink-0 snap-start ${
                tab.id === activeId
                  ? 'bg-surface border-default text-text-primary'
                  : 'border-transparent text-text-muted hover:text-text-primary hover:bg-surface-secondary'
              }`}
            >
              <PhaseIndicator phase={tab.phase} />
              <span className="truncate flex-1 min-w-0 text-left">
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
            className="flex items-center justify-center w-8 h-8 mb-0.5 text-text-muted hover:text-text-primary hover:bg-surface-tertiary rounded-lg transition-colors flex-shrink-0"
            title="Nova aba de busca"
          >
            <Plus className="w-4 h-4" />
          </button>
        </div>
      </div>

      <main ref={mainRef} className="flex-1 max-w-7xl 2xl:max-w-[min(95vw,1600px)] mx-auto w-full px-4 py-6 flex flex-col gap-4">
        {/* Jackett setup prompt */}
        {showJackettSetup && (
          <div className="bg-amber-500/10 border border-amber-500/30 rounded-xl p-4 sm:p-6">
            <div className="flex items-start gap-3">
              <WifiOff className="w-6 h-6 text-amber-400 flex-shrink-0 mt-0.5" />
              <div className="flex-1 min-w-0">
                <h3 className="text-amber-700 dark:text-amber-300 font-medium text-sm mb-1">Jackett não configurado</h3>
                <p className="text-text-secondary text-xs mb-4">
                  Informe a URL e a API key do seu servidor Jackett (local ou remoto)
                  para começar a buscar torrents.
                </p>
                <div className="flex flex-col sm:flex-row gap-2 mb-3">
                  <input
                    className="input-field flex-1 text-sm"
                    placeholder="URL do Jackett (ex: http://localhost:9117)"
                    value={setupUrl}
                    onChange={e => setSetupUrl(e.target.value)}
                  />
                  <input
                    className="input-field flex-1 text-sm"
                    placeholder="API Key"
                    value={setupKey}
                    onChange={e => setSetupKey(e.target.value)}
                  />
                </div>
                {setupError && <p className="text-red-400 text-xs mb-2">{setupError}</p>}
                {setupTestOk && <p className="text-green-400 text-xs mb-2">Conexão OK — porta acessível e Jackett respondeu.</p>}
                <div className="flex gap-2">
                  <button
                    onClick={() => runJackettSetup(
                      // Test-only: validate URL+key WITHOUT saving, so the user can
                      // confirm the port is reachable before committing config.
                      () => testJackettConnection({ url: setupUrl.trim(), apiKey: setupKey.trim() }),
                      () => setSetupTestOk(true),
                    )}
                    disabled={setupTesting}
                    className="btn-secondary text-sm px-4 py-2"
                  >
                    {setupTesting ? 'Testando…' : 'Testar'}
                  </button>
                  <button
                    onClick={() => runJackettSetup(
                      async () => {
                        await saveConfig({ port: 8989, jackett: { url: setupUrl.trim(), apiKey: setupKey.trim() }, downloadClients: [] })
                        return testJackettConnection()
                      },
                      () => { setShowJackettSetup(false); getIndexers().then(setIndexers).catch(() => {}) },
                    )}
                    disabled={setupTesting}
                    className="btn-primary text-sm px-4 py-2"
                  >
                    {setupTesting ? 'Testando…' : 'Salvar e Testar'}
                  </button>
                  <button
                    onClick={() => setShowJackettSetup(false)}
                    className="text-xs text-text-muted hover:text-text-primary px-3 py-2"
                  >
                    Ignorar
                  </button>
                </div>
              </div>
            </div>
          </div>
        )}
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
          onStop={() => stopSearch(activeTab.id)}
          loading={isSearching}
          suggestions={historyQueries}
        />

        {/* Status bar — no mobile a contagem e o controle de ordenação NÃO
            dividem a mesma linha (encavalavam com o `ml-auto`): empilham, com a
            ordenação numa linha própria full-width (scroll horizontal). No
            sm:+ voltam pra mesma linha, com o sort empurrado pra direita. */}
        {activeTab.phase !== 'idle' && (
          <div className="flex flex-col items-start gap-2 text-sm sm:flex-row sm:flex-wrap sm:items-center sm:gap-3">
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
                  <><span className="text-text-primary font-medium">{filteredResults.length}</span> de {groupedCount} únicos</>
                ) : (
                  <>
                    <span className="text-text-primary font-medium">{groupedCount}</span>
                    {' '}{groupedCount === 1 ? 'único' : 'únicos'}
                    {hasDuplicates && (
                      <span
                        className="text-text-muted"
                        title={`${activeTab.results.length} resultados brutos antes de agrupar duplicatas por hash/título`}
                      >
                        {' '}(de {activeTab.results.length} brutos)
                      </span>
                    )}
                  </>
                )}{' '}para <span className="text-text-primary font-medium">"{activeTab.query}"</span>
                <span className="text-text-muted">
                  ({activeTab.summary.live} ao vivo, {activeTab.summary.cached} cache)
                </span>
              </span>
            )}
            {activeTab.phase === 'error' && (
              <span className="flex items-center gap-2 text-red-400">
                <WifiOff className="w-3.5 h-3.5" />{activeTab.error || 'Erro na busca'}
              </span>
            )}
            {hasResults && (
              <div className="w-full sm:w-auto sm:ml-auto flex items-center gap-3 min-w-0">
                {isSearching && (
                  <span className="text-text-muted flex-shrink-0">{activeTab.results.length} até agora</span>
                )}
                {sortControls()}
              </div>
            )}
          </div>
        )}

        {/* Filter toolbar — shown once results start arriving. A ordenação NÃO
            vive mais aqui: foi pro cabeçalho dos resultados (junto da contagem),
            então a barra é só filtros e não tem mais o grupo de sort com
            `ml-auto` quebrando pra uma 2ª linha colada à direita. Inline no XL+;
            abaixo disso (telefone E TABLET) usa o botão que abre o Sheet. */}
        {hasResults && (
          <>
            <div className="hidden xl:flex flex-wrap items-center gap-2 p-3 bg-surface-secondary/60 rounded-xl border border-default">
              <Filter className="w-3.5 h-3.5 text-text-muted flex-shrink-0" />
              {filterFields(false)}
              <MusicSearchFilterToggle active={mediaMode === 'audio'} stacked={false} showAll={showAll} onToggle={() => setShowAll(v => !v)} />
            </div>
            <button
              onClick={() => setFilterSheetOpen(true)}
              className="xl:hidden flex items-center justify-center gap-2 min-h-[44px] px-3 rounded-xl border border-default bg-surface-secondary/60 text-sm text-text-primary"
            >
              <Filter className="w-4 h-4" />
              Filtros
              {activeFilterCount > 0 && (
                <span className="ml-1 inline-flex items-center justify-center min-w-[20px] h-5 px-1.5 rounded-full bg-green-500/20 text-green-400 text-xs font-medium">
                  {activeFilterCount}
                </span>
              )}
            </button>
          </>
        )}

        {/* Aviso proeminente quando os filtros estão ESCONDENDO resultados — o caso
            do Windows/ISO (sem resolução) sumindo por um filtro de qualidade
            persistido. Sem isso o usuário não percebe e acha que "não retornou". */}
        {hasResults && isFiltered && groupedCount - filteredResults.length > 0 && (
          <div className="flex items-center gap-2 bg-amber-500/10 border border-amber-500/30 text-amber-700 dark:text-amber-300 rounded-xl px-4 py-2.5 text-sm">
            <Filter className="w-4 h-4 flex-shrink-0" />
            <span className="flex-1">
              <span className="font-semibold">{groupedCount - filteredResults.length}</span>{' '}
              {groupedCount - filteredResults.length === 1 ? 'resultado oculto' : 'resultados ocultos'} pelos filtros ativos
            </span>
            <button
              onClick={clearFilters}
              className="flex-shrink-0 inline-flex items-center gap-1 font-medium underline underline-offset-2 hover:text-amber-900 dark:hover:text-amber-100"
            >
              <X className="w-3.5 h-3.5" />Limpar filtros
            </button>
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
            {Array.from({ length: 9 }, () => uid()).map(key => <SkeletonCard key={key} />)}
          </div>
        )}

        {/* Results grid (paginated via infinite scroll) */}
        {hasResults && filteredResults.length > 0 && groupSeries && (
          <>
            {/* Paginate by RESULTS first (same `visible` window + sentinel as the
                flat view), then group what's visible — so a huge result set
                doesn't mount every card at once. */}
            <div className="grid grid-cols-1 md:grid-cols-2 lg:grid-cols-3 gap-4">
              {buildSeriesLayout(filteredResults.slice(0, visible)).map((item, i) => (
                item.kind === 'header' ? (
                  <div key={item.id} className="col-span-full flex items-center gap-2 mt-2 first:mt-0">
                    <Layers className="w-4 h-4 text-green-400 flex-shrink-0" />
                    <h3 className="text-sm font-semibold text-text-primary truncate">{item.series}</h3>
                    <span className="text-xs text-text-muted whitespace-nowrap">Temporada {item.season} • {item.count} ep.</span>
                    <div className="flex-1 h-px bg-strong/40" />
                  </div>
                ) : renderResultCard(item.result, `${item.result.infoHash || item.result.link}-${i}`)
              ))}
            </div>
            {visible < filteredResults.length && (
              <div ref={sentinelRef} className="text-center py-6 text-xs text-text-muted">
                Mostrando {visible} de {filteredResults.length} • role pra ver mais
              </div>
            )}
          </>
        )}
        {hasResults && filteredResults.length > 0 && !groupSeries && (
          <>
            <div className="grid grid-cols-1 md:grid-cols-2 lg:grid-cols-3 gap-4">
              {filteredResults.slice(0, visible).map((result, i) => (
                renderResultCard(result, `${result.infoHash || result.link}-${i}`)
              ))}
            </div>
            {visible < filteredResults.length && (
              <div ref={sentinelRef} className="text-center py-6 text-xs text-text-muted">
                Mostrando {visible} de {filteredResults.length} • role pra ver mais
              </div>
            )}
          </>
        )}

        {/* Empty after filter */}
        {hasResults && filteredResults.length === 0 && (
          <div className="flex flex-col items-center justify-center py-16 text-text-muted">
            <SearchX className="w-12 h-12 mb-3 opacity-30" />
            <p className="font-medium">Nenhum resultado com os filtros aplicados</p>
            <p className="text-sm mt-1">{activeTab.results.length} resultado{(activeTab.results.length === 1 ? '' : 's')} disponíve{(activeTab.results.length === 1 ? 'l' : 'is')} antes dos filtros</p>
          </div>
        )}

        {/* Empty after search */}
        {activeTab.phase === 'done' && !hasResults && !activeTab.error && (
          <div className="flex flex-col items-center justify-center py-20 text-text-muted">
            <SearchX className="w-16 h-16 mb-4 opacity-30" />
            <p className="text-xl font-medium">Nenhum resultado encontrado</p>
            <p className="text-sm mt-2">Tente termos diferentes ou outros indexers</p>
          </div>
        )}

        {/* Initial state */}
        {activeTab.phase === 'idle' && (
          <div className="flex flex-col items-center justify-center py-20 text-text-muted">
            <p className="text-lg">Digite algo para buscar torrents</p>
            <SavedSearches
              recent={historyQueries}
              onPick={q => { updateTab(activeTab.id, { query: q }); handleSearch(activeTab.id, q) }}
            />
          </div>
        )}
      </main>

      {/* Filtros (mobile) — campos empilhados num Sheet */}
      <Sheet
        open={filterSheetOpen}
        onClose={() => setFilterSheetOpen(false)}
        title="Filtros"
        icon={<Filter className="w-4 h-4 text-text-secondary flex-shrink-0" />}
        size="md"
      >
        <div className="flex flex-col gap-3">{filterFields(true)}<MusicSearchFilterToggle active={mediaMode === 'audio'} stacked showAll={showAll} onToggle={() => setShowAll(v => !v)} /></div>
      </Sheet>

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
