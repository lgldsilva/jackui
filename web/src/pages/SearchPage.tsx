import { useState, useEffect, useRef, useCallback, useMemo } from 'react'
import { useSearchParams } from 'react-router-dom'
import { SearchX, X, Filter } from 'lucide-react'
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
import { SearchResult, Indexer, getIndexers, getHistory, favoritesList } from '../api/client'
import { load, save } from '../lib/storage'
import { reorder } from '../lib/reorder'
import { syncTabsToCache } from '../lib/searchResultsCache'
import { useRehydratedResults, canApplyRehydrated } from '../lib/useRehydratedResults'
import { useFilteredResults } from '../lib/useFilteredResults'
import { useMediaMode } from '../lib/mediaMode'
import { MusicSearchFilterToggle } from '../components/MusicSearchFilterToggle'
import { useSwipe } from '../lib/useSwipe'
import { uid } from '../lib/uid'
import { useTranslation, Trans } from 'react-i18next'

import { hydrateTabs, persistTabs, newTab, nextTabId, FILTER_DEFAULTS_KEY, type FilterDefaults, type TabState } from '../lib/searchTabs'
import { SkeletonCard } from '../components/search/SkeletonCard'
import { SearchFilterFields } from '../components/search/SearchFilterFields'
import { SearchTabStrip } from '../components/search/SearchTabStrip'
import { StatusBanner } from '../components/StatusBanner'
import { SearchStatusBar } from '../components/search/SearchStatusBar'
import { SearchResultsGrid } from '../components/search/SearchResultsGrid'
import { JackettSetupPrompt } from '../components/search/JackettSetupPrompt'
import { useDiscoveredIndexers } from '../components/search/useDiscoveredIndexers'
import { useJackettSetup } from '../components/search/useJackettSetup'
import { useSearchStreams } from '../components/search/useSearchStreams'

export default function SearchPage() {
  const { t } = useTranslation()
  const [initial] = useState(() => hydrateTabs())
  const [tabs, setTabs] = useState<TabState[]>(initial.tabs)
  const [activeId, setActiveId] = useState<string>(initial.activeId)
  // Restores the active tab's scroll when it already has results (best-effort: on
  // re-search the results arrive via SSE, so it may restore partially).
  useScrollRestoration((tabs.find(t => t.id === activeId)?.results.length ?? 0) > 0)
  const [indexers, setIndexers] = useState<Indexer[]>([])

  // Configured + auto-discovered indexers (collection/persistence in the hook)
  const allIndexers = useDiscoveredIndexers(tabs, indexers)

  const [downloadTarget, setDownloadTarget] = useState<SearchResult | null>(null)
  const { playSingle } = usePlayer()
  const [playlistTarget, setPlaylistTarget] = useState<SearchResult | null>(null)
  const [playlistTargetFile, setPlaylistTargetFile] = useState<{ index: number; title: string } | null>(null)
  const [contentsTarget, setContentsTarget] = useState<SearchResult | null>(null)
  const [filterSheetOpen, setFilterSheetOpen] = useState(false)

  // Jackett connection status — for first-run / config prompt
  const {
    showJackettSetup, setShowJackettSetup,
    setupUrl, setSetupUrl, setupKey, setSetupKey,
    setupTesting, setupError, setupTestOk, setSetupTestOk,
    runJackettSetup,
  } = useJackettSetup()

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

  const updateTab = useCallback((id: string, patch: Partial<TabState>) => {
    setTabs(prev => prev.map(t => t.id === id ? { ...t, ...patch } : t))
  }, [])

  // SSE streams (uma por aba): start/stop/close vivem no hook.
  const { handleSearch, stopSearch, closeStream } = useSearchStreams(tabs, updateTab, setTabs)

  const closeTab = useCallback((id: string, e?: React.MouseEvent) => {
    e?.stopPropagation()
    closeStream(id)
    setTabs(prev => {
      if (prev.length === 1) return prev
      const next = prev.filter(t => t.id !== id)
      if (id === activeId) {
        const idx = prev.findIndex(t => t.id === id)
        setActiveId(next[Math.max(0, idx - 1)].id)
      }
      return next
    })
  }, [activeId, closeStream])

  const closeActiveTab = useCallback(() => {
    closeTab(activeId)
  }, [activeId, closeTab])

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
    const id = nextTabId()
    setTabs(prev => [...prev, newTab(id)])
    setActiveId(id)
  }

  const addTabAndFocus = () => {
    addTab()
    setTimeout(() => searchInputRef.current?.focus(), 50)
  }

  // Drag-to-reorder the tab strip. dragIndexRef holds the index of the tab being
  // dragged; dropping over another tab moves it there. Persisted by the tabs
  // effect like any other change.
  const dragIndexRef = useRef<number | null>(null)
  const [dragOverIndex, setDragOverIndex] = useState<number | null>(null)
  const moveTab = (from: number, to: number) => {
    if (from === to) return
    setTabs(prev => reorder(prev, from, to))
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

  // Music mode (NavHeader): the search filters to audio. `showAll` is an EPHEMERAL
  // escape (doesn't persist in TabState) — goes away when switching mode or tab.
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
  // `isFiltered` now reflects only the user's FILTER-caused reduction, NOT the
  // dedup one. groupedCount is the already-grouped universe (the same torrent on
  // multiple trackers becomes 1) — the count that makes sense to the user.
  const isFiltered = filteredResults.length !== groupedCount
  const hasDuplicates = groupedCount !== activeTab.results.length

  const activeFilterCount = [
    activeTab.titleFilter,
    activeTab.trackerFilter !== 'all',
    activeTab.minSeeders > 1, // 0 or 1 = permissive/default; only counts if the user raised it
    activeTab.minLeechers > 0,
    activeTab.maxSizeGb,
    activeTab.onlyPlayable,
    activeTab.resolution,
    activeTab.hdrOnly,
    activeTab.codecGroup,
  ].filter(Boolean).length

  // clearFilters zeroes EVERY filter that could hide results, in the MOST
  // permissive way possible — including minSeeders=0 (reveals torrents with 0 seeders, which
  // the default minSeeders=1 hid) and the quality ones (resolution/codec/HDR, which
  // silently discard what lacks that metadata, e.g. software/ISO without
  // resolution) — and lifts the audio/video mode restriction (showAll). It's the hidden
  // banner's "show everything": after it, filteredResults == groupedCount.
  const clearFilters = () => {
    setShowAll(true)
    // groupSeries is a filter-bar control the reset didn't touch — reset it too.
    if (groupSeries) { setGroupSeries(false); save('searchGroupSeries', false) }
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

  // Props compartilhadas pelos campos de filtro (barra inline e Sheet mobile).
  const onUpdateActive = (patch: Partial<TabState>) => updateTab(activeTab.id, patch)

  return (
    <div className="min-h-screen bg-surface flex flex-col">
      <NavHeader />

      {/* Tab strip */}
      <SearchTabStrip
        tabs={tabs}
        activeId={activeId}
        onSelect={setActiveId}
        stripRef={stripRef}
        activeTabRef={activeTabRef}
        dragIndexRef={dragIndexRef}
        dragOverIndex={dragOverIndex}
        setDragOverIndex={setDragOverIndex}
        onMoveTab={moveTab}
        onCloseTab={closeTab}
        onAddTab={addTab}
      />

      <main id="main-content" tabIndex={-1} ref={mainRef} className="flex-1 max-w-7xl 2xl:max-w-[min(95vw,1600px)] mx-auto w-full min-w-0 overflow-x-clip px-4 py-6 flex flex-col gap-4">
        {/* Jackett setup prompt */}
        {showJackettSetup && (
          <JackettSetupPrompt
            setupUrl={setupUrl}
            setSetupUrl={setSetupUrl}
            setupKey={setupKey}
            setSetupKey={setSetupKey}
            setupTesting={setupTesting}
            setupError={setupError}
            setupTestOk={setupTestOk}
            setShowJackettSetup={setShowJackettSetup}
            setSetupTestOk={setSetupTestOk}
            runJackettSetup={runJackettSetup}
            onConfigured={() => getIndexers().then(setIndexers).catch(() => {})}
          />
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

        {/* Status bar — on mobile the count and the sort control do NOT
            share the same line (they piled up over the `ml-auto`): they stack, with
            sorting on its own full-width line (horizontal scroll). On
            sm:+ they return to the same line, with the sort pushed right. */}
        {activeTab.phase !== 'idle' && (
          <SearchStatusBar
            tab={activeTab}
            onUpdate={onUpdateActive}
            isFiltered={isFiltered}
            filteredCount={filteredResults.length}
            groupedCount={groupedCount}
            hasDuplicates={hasDuplicates}
            isSearching={isSearching}
            hasResults={hasResults}
          />
        )}

        {/* Filter toolbar — shown once results start arriving. Sorting does NOT
            live here anymore: it moved to the results header (with the count),
            so the bar is filters only and no longer has the sort group with
            `ml-auto` wrapping onto a 2nd line glued to the right. Inline on XL+;
            below that (phone AND TABLET) it uses the button that opens the Sheet. */}
        {hasResults && (
          <>
            <div className="hidden xl:flex flex-wrap items-center gap-2 p-3 bg-surface-secondary/60 rounded-xl border border-default">
              <Filter className="w-3.5 h-3.5 text-text-muted flex-shrink-0" />
              <SearchFilterFields
                stacked={false}
                tab={activeTab}
                onUpdate={onUpdateActive}
                trackers={trackers}
                groupSeries={groupSeries}
                onToggleGroupSeries={toggleGroupSeries}
                activeFilterCount={activeFilterCount}
                onClearFilters={clearFilters}
              />
              <MusicSearchFilterToggle active={mediaMode === 'audio'} stacked={false} showAll={showAll} onToggle={() => setShowAll(v => !v)} />
            </div>
            <button
              onClick={() => setFilterSheetOpen(true)}
              className="xl:hidden flex items-center justify-center gap-2 min-h-[44px] px-3 rounded-xl border border-default bg-surface-secondary/60 text-sm text-text-primary"
            >
              <Filter className="w-4 h-4" />
              {t('search.filters')}
              {activeFilterCount > 0 && (
                <span className="ml-1 inline-flex items-center justify-center min-w-[20px] h-5 px-1.5 rounded-full bg-green-500/20 text-green-400 text-xs font-medium">
                  {activeFilterCount}
                </span>
              )}
            </button>
          </>
        )}

        {/* Prominent notice when the filters are HIDING results — the case of
            Windows/ISO (no resolution) vanishing due to a persisted quality
            filter. Without this the user doesn't notice and thinks "nothing returned". */}
        {hasResults && isFiltered && groupedCount - filteredResults.length > 0 && (
          <div className="flex items-center gap-2 bg-amber-500/10 border border-amber-500/30 text-amber-700 dark:text-amber-300 rounded-xl px-4 py-2.5 text-sm">
            <Filter className="w-4 h-4 flex-shrink-0" />
            <span className="flex-1">
              <Trans i18nKey="search.hidden_by_filters" values={{ count: groupedCount - filteredResults.length }} components={{ b: <span className="font-semibold" /> }} />
            </span>
            <button
              onClick={clearFilters}
              className="flex-shrink-0 inline-flex items-center gap-1 font-medium underline underline-offset-2 hover:text-amber-900 dark:hover:text-amber-100"
            >
              <X className="w-3.5 h-3.5" />{t('search.clean_filters')}
            </button>
          </div>
        )}

        {activeTab.error && hasResults && (
          <StatusBanner variant="warning">{activeTab.error}</StatusBanner>
        )}

        {activeTab.error && !hasResults && (
          <StatusBanner variant="error" title={t('search.search_error')}>
            {activeTab.error}
          </StatusBanner>
        )}

        {/* Loading skeletons */}
        {isSearching && !hasResults && (
          <div className="grid grid-cols-1 md:grid-cols-2 lg:grid-cols-3 gap-4">
            {Array.from({ length: 9 }, () => uid()).map(key => <SkeletonCard key={key} />)}
          </div>
        )}

        {/* Results grid (paginated via infinite scroll) */}
        {hasResults && filteredResults.length > 0 && (
          <SearchResultsGrid
            filteredResults={filteredResults}
            visible={visible}
            groupSeries={groupSeries}
            renderCard={renderResultCard}
            sentinelRef={sentinelRef}
          />
        )}

        {/* Empty after filter */}
        {hasResults && filteredResults.length === 0 && (
          <div className="flex flex-col items-center justify-center py-16 text-text-muted">
            <SearchX className="w-12 h-12 mb-3 opacity-30" />
            <p className="font-medium">{t('search.no_results_filtered')}</p>
            <p className="text-sm mt-1">{t('search.available_before_filters', { count: activeTab.results.length })}</p>
          </div>
        )}

        {/* Empty after search */}
        {activeTab.phase === 'done' && !hasResults && !activeTab.error && (
          <div className="flex flex-col items-center justify-center py-20 text-text-muted">
            <SearchX className="w-16 h-16 mb-4 opacity-30" />
            <p className="text-xl font-medium">{t('search.no_results')}</p>
            <p className="text-sm mt-2">{t('search.try_different')}</p>
          </div>
        )}

        {/* Initial state */}
        {activeTab.phase === 'idle' && (
          <div className="flex flex-col items-center justify-center py-20 text-text-muted">
            <p className="text-lg">{t('search.type_to_search')}</p>
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
        title={t('search.filters')}
        icon={<Filter className="w-4 h-4 text-text-secondary flex-shrink-0" />}
        size="md"
      >
        <div className="flex flex-col gap-3">
          <SearchFilterFields
            stacked
            tab={activeTab}
            onUpdate={onUpdateActive}
            trackers={trackers}
            groupSeries={groupSeries}
            onToggleGroupSeries={toggleGroupSeries}
            activeFilterCount={activeFilterCount}
            onClearFilters={clearFilters}
          />
          <MusicSearchFilterToggle active={mediaMode === 'audio'} stacked showAll={showAll} onToggle={() => setShowAll(v => !v)} />
        </div>
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
        onDownload={(r) => { setContentsTarget(null); setDownloadTarget(r) }}
        onAddFileToPlaylist={(r, fileIdx, title) => {
          setContentsTarget(null)
          setPlaylistTargetFile({ index: fileIdx, title })
          setPlaylistTarget(r)
        }}
      />
    </div>
  )
}
