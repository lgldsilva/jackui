import { useTranslation, Trans } from 'react-i18next'
import { Link, useNavigate } from 'react-router-dom'
import { History, Trash2, Search, ArrowLeft, Calendar, Database, Filter, X, Loader2, RefreshCw } from 'lucide-react'
import { SearchResult, HistoryEntry } from '../../api/client'
import ResultCard from '../ResultCard'
import { SwipeRow } from '../SwipeRow'
import { formatDate } from '../../lib/format'
import { uid } from '../../lib/uid'
import { newTabProps, searchHref } from '../../lib/cardNav'
import { EntrySortKey, ResultSortKey } from './types'
import { ResultSortButtons } from './ResultSortButtons'

// Estado vazio do modo "browse" (nenhuma busca em cache). Extraído pra fora de
// renderBrowseContent pra manter a complexidade cognitiva daquele render baixa.
export function BrowseEmptyState() {
  const { t } = useTranslation()
  return (
    <div className="flex flex-col items-center justify-center py-20 text-text-muted">
      <History className="w-16 h-16 mb-4 opacity-30" />
      <p className="text-xl font-medium">{t('history.noSavedTitle')}</p>
      <p className="text-sm mt-2">{t('history.noSavedHint')}</p>
      <Link to="/" className="mt-4 text-green-500 hover:text-green-400 text-sm transition-colors">{t('history.goToSearch')}</Link>
    </div>
  )
}

type BrowseEntryListProps = {
  readonly selected: string | null
  readonly refreshingQueries: Set<string>
  readonly queryFilter: string
  readonly setQueryFilter: (v: string) => void
  readonly entrySort: EntrySortKey
  readonly setEntrySort: (v: EntrySortKey) => void
  readonly filteredEntries: HistoryEntry[]
  readonly onSelect: (q: string) => void
  readonly onDeleteEntry: (q: string, e: React.MouseEvent) => void
  readonly onDeleteEntryByQuery: (q: string) => void
  readonly navigate: ReturnType<typeof useNavigate>
}

// Left column of the browse view: the saved-query list with its filter + sort.
// Extracted from renderBrowseContent to cut its cognitive complexity (the
// entry-row `map` alone nested several `selected === entry.query` ternaries).
export function BrowseEntryList({
  selected, refreshingQueries, queryFilter, setQueryFilter, entrySort, setEntrySort,
  filteredEntries, onSelect, onDeleteEntry, onDeleteEntryByQuery, navigate,
}: BrowseEntryListProps) {
  const { t } = useTranslation()
  return (
    <div className={`flex-col gap-2 ${selected ? 'hidden lg:flex' : 'flex'}`}>
      <div className="relative">
        <Search className="absolute left-3 top-1/2 -translate-y-1/2 w-3.5 h-3.5 text-text-muted" />
        <input type="text" placeholder={t('history.filterSearches')} value={queryFilter} onChange={e => setQueryFilter(e.target.value)} className="w-full bg-surface-secondary border border-default rounded-lg pl-9 pr-8 py-2 text-base sm:text-sm text-text-primary placeholder-gray-500 focus:outline-none focus:border-green-500" />
        {queryFilter && (<button onClick={() => setQueryFilter('')} className="absolute right-3 top-1/2 -translate-y-1/2 text-text-muted hover:text-text-primary"><X className="w-3.5 h-3.5" /></button>)}
      </div>
      <div className="flex gap-1">
        {([['recent',t('history.entrySortRecent')],['oldest',t('history.entrySortOldest')],['most',t('history.entrySortMost')],['alpha',t('history.entrySortAlpha')]] as [EntrySortKey,string][]).map(([key, label]) => (
          <button key={key} onClick={() => setEntrySort(key)} className={`flex-1 text-xs px-2 py-1.5 rounded-lg transition-colors ${entrySort === key ? 'bg-green-500/20 text-green-400 border border-green-500/30' : 'bg-surface-secondary text-text-secondary border border-default hover:text-text-primary'}`}>{label}</button>
        ))}
      </div>
      <div className="bg-surface-secondary rounded-xl border border-default overflow-hidden flex-1 overflow-y-auto max-h-[calc(100vh-280px)]">
        {filteredEntries.length === 0 ? (
          <p className="text-text-muted text-sm text-center py-8">{t('history.noSearchesFound')}</p>
        ) : filteredEntries.map((entry) => (
          <SwipeRow key={entry.query} onDelete={() => onDeleteEntryByQuery(entry.query)} deleteLabel={t('history.delete')}>
          <button {...newTabProps(searchHref(entry.query), () => onSelect(entry.query))} className={`w-full flex items-start justify-between gap-2 px-4 py-3 min-h-[44px] text-sm transition-colors border-b border-default/50 last:border-b-0 text-left ${selected === entry.query ? 'bg-green-500/10 border-l-2 border-l-green-500' : 'hover:bg-surface-tertiary/50'}`}>
            <div className="flex-1 min-w-0">
              <p className={`truncate font-medium ${selected === entry.query ? 'text-green-400' : 'text-text-primary'}`} title={entry.query}>{entry.query}</p>
              <div className="flex items-center gap-2 mt-0.5 flex-wrap">
                <span className="flex items-center gap-1 text-xs text-text-muted"><Database className="w-2.5 h-2.5" />{entry.resultCount.toLocaleString()}</span>
                <span className="flex items-center gap-1 text-xs text-text-muted"><Calendar className="w-2.5 h-2.5" />{formatDate(entry.lastSaved)}</span>
              </div>
            </div>
            <div className="flex items-center gap-1.5 flex-shrink-0 mt-0.5">
              {refreshingQueries.has(entry.query) && (
                <Loader2 className="w-3.5 h-3.5 text-green-400 animate-spin" aria-label={t('history.refreshingSearch')} />
              )}
              <button onClick={e => { e.stopPropagation(); navigate(`/?q=${encodeURIComponent(entry.query)}`) }} title={t('history.newSearch')} aria-label={t('history.newSearch')} className="flex items-center justify-center min-w-[44px] min-h-[44px] sm:min-w-0 sm:min-h-0 text-text-muted hover:text-green-400 transition-colors"><Search className="w-3.5 h-3.5" /></button>
              {/* Delete por hover — desktop. No mobile usa o swipe-to-delete do SwipeRow. */}
              <button onClick={e => onDeleteEntry(entry.query, e)} title={t('history.removeFromCache')} aria-label={t('history.removeFromCache')} className="hidden sm:flex items-center justify-center text-text-muted hover:text-red-400 transition-colors"><Trash2 className="w-3.5 h-3.5" /></button>
            </div>
          </button>
          </SwipeRow>
        ))}
      </div>
    </div>
  )
}

type BrowseResultsPanelProps = {
  readonly selected: string
  readonly resultFilter: string
  readonly setResultFilter: (v: string) => void
  readonly trackers: string[]
  readonly trackerFilter: string
  readonly setTrackerFilter: (v: string) => void
  readonly minSeeders: number
  readonly setMinSeeders: (v: number) => void
  readonly resultSort: ResultSortKey
  readonly resultSortAsc: boolean
  readonly setResultSort: (v: ResultSortKey) => void
  readonly setResultSortAsc: (v: boolean) => void
  readonly loadingResults: boolean
  readonly results: SearchResult[]
  readonly filteredResults: SearchResult[]
  readonly browseVisible: number
  readonly browseSentinelRef: React.RefObject<HTMLDivElement>
  readonly refreshingSearch: boolean
  readonly onRefreshSearch: () => void
  readonly onBack: () => void
  readonly onDownload: (r: SearchResult) => void
  readonly onPlay: (r: SearchResult) => void
  readonly onAddToPlaylist: (r: SearchResult) => void
  readonly onExploreContents: (r: SearchResult) => void
  readonly onRefreshResult: (r: SearchResult) => void
  readonly refreshingIDs: Set<number>
  readonly refreshedLabels: Map<number, string>
}

// Skeleton placeholders while the cached results load. Tiny helper kept out of
// the panel to avoid one more nested map+ternary there.
function BrowseResultsSkeleton() {
  return (
    <div className="grid grid-cols-1 md:grid-cols-2 xl:grid-cols-3 gap-4">
      {Array.from({ length: 6 }, () => uid()).map(key => (
        <div key={key} className="card animate-pulse flex flex-col gap-3">
          <div className="h-4 bg-surface-tertiary rounded w-3/4" /><div className="h-3 bg-surface-tertiary rounded w-1/4" />
          <div className="grid grid-cols-2 gap-2"><div className="h-3 bg-surface-tertiary rounded" /><div className="h-3 bg-surface-tertiary rounded" /></div>
        </div>
      ))}
    </div>
  )
}

// Right column when a query is selected: filters bar, count/refresh row, and the
// cached result cards (loading / empty / grid). Extracted from renderBrowseContent
// — this branch held the bulk of the nested conditionals (loading, empty, paging,
// refresh button) that drove the cognitive complexity over the gate.
export function BrowseResultsDetail({
  selected, resultFilter, setResultFilter, trackers, trackerFilter, setTrackerFilter,
  minSeeders, setMinSeeders, resultSort, resultSortAsc, setResultSort, setResultSortAsc,
  loadingResults, results, filteredResults, browseVisible, browseSentinelRef,
  refreshingSearch, onRefreshSearch, onBack, onDownload, onPlay, onAddToPlaylist,
  onExploreContents, onRefreshResult, refreshingIDs, refreshedLabels,
}: BrowseResultsPanelProps) {
  const { t } = useTranslation()
  return (
    <>
      <button
        onClick={onBack}
        className="lg:hidden flex items-center gap-1.5 text-sm text-text-secondary hover:text-text-primary self-start"
      >
        <ArrowLeft className="w-4 h-4" /> {t('history.backToSearches')}
      </button>
      <div className="flex flex-wrap items-center gap-2">
        <div className="relative flex-1 min-w-[180px]">
          <Filter className="absolute left-3 top-1/2 -translate-y-1/2 w-3.5 h-3.5 text-text-muted" />
          <input type="text" placeholder={t('history.filterTitles')} value={resultFilter} onChange={e => setResultFilter(e.target.value)} className="w-full bg-surface-secondary border border-default rounded-lg pl-9 pr-8 py-2 text-base sm:text-sm text-text-primary placeholder-gray-500 focus:outline-none focus:border-green-500" />
          {resultFilter && (<button onClick={() => setResultFilter('')} className="absolute right-3 top-1/2 -translate-y-1/2 text-text-muted hover:text-text-primary"><X className="w-3.5 h-3.5" /></button>)}
        </div>
        <select value={trackerFilter} onChange={e => setTrackerFilter(e.target.value)} className="bg-surface-secondary border border-default rounded-lg px-3 py-2 text-sm text-text-primary focus:outline-none focus:border-green-500">
          {trackers.map(tr => (<option key={tr} value={tr}>{tr === 'all' ? t('history.allTrackers') : tr}</option>))}
        </select>
        <div className="flex items-center gap-2 bg-surface-secondary border border-default rounded-lg px-3 py-2">
          <span className="text-xs text-text-muted">{t('history.minSeeds')}</span>
          <input type="number" min={0} value={minSeeders} onChange={e => setMinSeeders(Math.max(0, Number.parseInt(e.target.value) || 0))} className="w-14 bg-transparent text-sm text-text-primary focus:outline-none" />
        </div>
        <ResultSortButtons
          sort={resultSort}
          sortAsc={resultSortAsc}
          onChange={(k, a) => { setResultSort(k); setResultSortAsc(a) }}
          defs={[['seeders',t('history.sortSeeds')],['size',t('history.sortSize')],['date',t('history.sortDate')],['title',t('history.sortTitle')]].map(([key, label]) => ({ key: key as ResultSortKey, label }))}
          className="flex items-center gap-1 bg-surface-secondary border border-default rounded-lg p-1"
        />
      </div>
      <div className="flex items-center justify-between gap-2 flex-wrap">
        <p className="text-xs text-text-muted">{loadingResults ? t('history.loading') : (<><span className="text-text-primary font-medium">{filteredResults.length}</span>{filteredResults.length !== results.length && <span> {t('history.ofTotal', { total: results.length })}</span>} {' '}<Trans i18nKey="history.resultsCachedFor" values={{ query: selected }} components={{ q: <span className="text-green-400 font-medium" /> }} /></>)}</p>
        {!loadingResults && (
          <button onClick={onRefreshSearch} disabled={refreshingSearch} title={t('history.refreshSearchTooltip')} className="flex items-center gap-1.5 text-xs bg-green-500/15 hover:bg-green-500/25 text-green-700 dark:text-green-300 border border-green-500/30 px-3 py-1.5 rounded-lg transition-colors disabled:opacity-50">
            {refreshingSearch ? <Loader2 className="w-3.5 h-3.5 animate-spin" /> : <RefreshCw className="w-3.5 h-3.5" />}
            {refreshingSearch ? t('history.refreshing') : t('history.refreshSearch')}
          </button>
        )}
      </div>
      {loadingResults && <BrowseResultsSkeleton />}
      {!loadingResults && filteredResults.length === 0 && (
        <div className="text-text-muted text-sm py-10 text-center">{results.length === 0 ? t('history.noResultsCached', { query: selected }) : t('history.noResultsFiltered')}</div>
      )}
      {!loadingResults && filteredResults.length > 0 && (
        <>
          <div className="grid grid-cols-1 md:grid-cols-2 xl:grid-cols-3 gap-4">
            {filteredResults.slice(0, browseVisible).map((result, i) => (
              <ResultCard key={`${result.infoHash || result.link}-${i}`} result={{ ...result, cached: true }} onDownload={onDownload} onPlay={onPlay} onAddToPlaylist={onAddToPlaylist} onExploreContents={onExploreContents} onRefresh={onRefreshResult} refreshing={result.id !== undefined && refreshingIDs.has(result.id)} refreshedAt={result.id === undefined ? null : refreshedLabels.get(result.id) ?? null} />
            ))}
          </div>
          {browseVisible < filteredResults.length && (
            <div ref={browseSentinelRef} className="text-center py-6 text-xs text-text-muted">{t('history.showingMore', { shown: browseVisible, total: filteredResults.length })}</div>
          )}
        </>
      )}
    </>
  )
}
