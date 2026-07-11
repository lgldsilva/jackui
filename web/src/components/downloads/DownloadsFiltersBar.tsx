import { useTranslation } from 'react-i18next'
import { Search, X, SlidersHorizontal, ArrowUp, ArrowDown, ArrowDownWideNarrow } from 'lucide-react'
import type { QuerySetter } from '../../lib/useQueryState'
import type { DownloadUserEntry } from '../../api/client'

// DownloadsFiltersBar — search input + collapsible filter/sort panel. Every field
// is URL-backed in the page; this component only renders and delegates through the
// setters passed in (setQuery is the atomic multi-key setter used by "Limpar").
export function DownloadsFiltersBar(props: {
  readonly filterSearch: string
  readonly setFilterSearch: (v: string) => void
  readonly showFilters: boolean
  readonly setFiltersParam: (v: string) => void
  readonly filterStatus: string
  readonly setFilterStatus: (v: string) => void
  readonly filterTracker: string
  readonly setFilterTracker: (v: string) => void
  readonly availableTrackers: string[]
  readonly filterCategory: string
  readonly setFilterCategory: (v: string) => void
  readonly availableCategories: string[]
  readonly showAllUsers: boolean
  readonly availableUsers: DownloadUserEntry[]
  readonly filterUserId: string
  readonly setFilterUserId: (v: string) => void
  readonly sortCol: string
  readonly setSortCol: (v: string) => void
  readonly sortDir: string
  readonly setSortDir: (v: string) => void
  readonly setQuery: QuerySetter
}) {
  const {
    filterSearch, setFilterSearch, showFilters, setFiltersParam,
    filterStatus, setFilterStatus, filterTracker, setFilterTracker, availableTrackers,
    filterCategory, setFilterCategory, availableCategories,
    showAllUsers, availableUsers, filterUserId, setFilterUserId,
    sortCol, setSortCol, sortDir, setSortDir, setQuery,
  } = props
  const { t } = useTranslation()
  return (
    <div className="flex flex-col gap-2">
      <div className="flex items-center gap-2 flex-wrap">
        <div className="relative flex-1 min-w-[200px]">
          <Search className="absolute left-3 top-1/2 -translate-y-1/2 w-4 h-4 text-text-muted" />
          <input
            type="text"
            value={filterSearch}
            onChange={e => setFilterSearch(e.target.value)}
            placeholder={t('downloads.page.searchPlaceholder')}
            className="w-full bg-surface-secondary/80 border border-default rounded-lg pl-9 pr-3 py-2 text-sm text-text-primary placeholder-gray-500 focus:outline-none focus:border-cyan-500/50 transition-colors"
          />
          {filterSearch && (
            <button onClick={() => setFilterSearch('')} className="absolute right-3 top-1/2 -translate-y-1/2 text-text-muted hover:text-text-primary">
              <X className="w-3.5 h-3.5" />
            </button>
          )}
        </div>
        <button
          onClick={() => setFiltersParam(showFilters ? '' : '1')}
          className={`flex items-center gap-1.5 text-xs px-3 py-2 rounded-lg border transition-colors ${
            showFilters || filterStatus || filterTracker || filterCategory
              ? 'bg-cyan-500/10 border-cyan-500/30 text-cyan-700 dark:text-cyan-300'
              : 'bg-surface-secondary border-default text-text-secondary hover:text-text-primary'
          }`}
        >
          <SlidersHorizontal className="w-3.5 h-3.5" />
          {t('downloads.page.filters')}
          {(filterStatus || filterTracker || filterCategory) && (
            <span className="w-1.5 h-1.5 rounded-full bg-cyan-400" />
          )}
        </button>
      </div>

      {showFilters && (
        <div className="bg-surface-secondary/40 border border-default/50 rounded-xl p-3 flex flex-col gap-3">
          {/* Filtros — selects preenchem a largura no mobile (flex-1) e ficam
              no tamanho natural no desktop. */}
          <div className="flex flex-wrap items-center gap-2">
            <select
              value={filterStatus}
              onChange={e => setFilterStatus(e.target.value)}
              className="flex-1 min-w-[140px] sm:flex-none bg-surface border border-default rounded-lg px-3 py-1.5 text-xs text-text-primary focus:outline-none focus:border-cyan-500/50"
            >
              <option value="">{t('downloads.page.filterAllStatus')}</option>
              <option value="downloading">{t('downloads.page.statusDownloading')}</option>
              <option value="paused">{t('downloads.page.statusPaused')}</option>
              <option value="queued">{t('downloads.page.statusQueued')}</option>
              <option value="completed">{t('downloads.page.statusCompleted')}</option>
              <option value="failed">{t('downloads.page.statusFailed')}</option>
            </select>
            {availableTrackers.length > 0 && (
              <select
                value={filterTracker}
                onChange={e => setFilterTracker(e.target.value)}
                className="flex-1 min-w-[140px] sm:flex-none bg-surface border border-default rounded-lg px-3 py-1.5 text-xs text-text-primary focus:outline-none focus:border-cyan-500/50"
              >
                <option value="">{t('downloads.page.filterAllTrackers')}</option>
                {availableTrackers.map(tr => (
                  <option key={tr} value={tr}>{tr}</option>
                ))}
              </select>
            )}
            {availableCategories.length > 0 && (
              <select
                value={filterCategory}
                onChange={e => setFilterCategory(e.target.value)}
                className="flex-1 min-w-[140px] sm:flex-none bg-surface border border-default rounded-lg px-3 py-1.5 text-xs text-text-primary focus:outline-none focus:border-cyan-500/50"
              >
                <option value="">{t('downloads.page.filterAllCategories')}</option>
                {availableCategories.map(c => (
                  <option key={c} value={c}>{c}</option>
                ))}
              </select>
            )}
            {showAllUsers && availableUsers.length > 0 && (
              <select
                value={filterUserId}
                onChange={e => setFilterUserId(e.target.value)}
                className="flex-1 min-w-[140px] sm:flex-none bg-surface border border-default rounded-lg px-3 py-1.5 text-xs text-text-primary focus:outline-none focus:border-cyan-500/50"
              >
                <option value="">{t('downloads.page.filterAllUsers')}</option>
                {availableUsers.map(u => (
                  <option key={u.id} value={String(u.id)}>{u.username}</option>
                ))}
              </select>
            )}
          </div>

          {/* Ordenar — grupo próprio, separado por divisória; Limpar à direita. */}
          <div className="flex items-center gap-2 flex-wrap border-t border-default/50 pt-3">
            <span className="text-xs text-text-muted flex items-center gap-1.5 flex-shrink-0">
              <ArrowDownWideNarrow className="w-3.5 h-3.5" /> {t('downloads.page.sort')}
            </span>
            <select
              value={sortCol}
              onChange={e => setSortCol(e.target.value)}
              className="flex-1 min-w-[140px] sm:flex-none bg-surface border border-default rounded-lg px-3 py-1.5 text-xs text-text-primary focus:outline-none focus:border-cyan-500/50"
            >
              <option value="created_at">{t('downloads.page.sortDate')}</option>
              <option value="name">{t('downloads.page.sortName')}</option>
              <option value="size">{t('downloads.page.sortSize')}</option>
              <option value="progress">{t('downloads.page.sortProgress')}</option>
              <option value="downRate">{t('downloads.page.sortDownSpeed')}</option>
              <option value="upRate">{t('downloads.page.sortUpSpeed')}</option>
              <option value="seeders">{t('downloads.page.sortSeeds')}</option>
              <option value="status">{t('downloads.page.sortStatus')}</option>
              <option value="tracker">{t('downloads.page.sortTracker')}</option>
              <option value="category">{t('downloads.page.sortCategory')}</option>
            </select>
            <button
              onClick={() => setSortDir(sortDir === 'asc' ? 'desc' : 'asc')}
              title={sortDir === 'asc' ? t('downloads.page.ascending') : t('downloads.page.descending')}
              aria-label={t('downloads.page.invertOrder')}
              className="flex-shrink-0 bg-surface border border-default rounded-lg px-2 py-1.5 text-text-primary hover:text-cyan-600 dark:hover:text-cyan-300 hover:border-cyan-500/40 transition-colors"
            >
              {sortDir === 'asc' ? <ArrowUp className="w-3.5 h-3.5" /> : <ArrowDown className="w-3.5 h-3.5" />}
            </button>
            <button
              onClick={() => setQuery({ status: null, tracker: null, cat: null, q: null, uid: null, sort: null, dir: null })}
              className="ml-auto text-xs text-text-muted hover:text-text-primary px-2 py-1 flex-shrink-0"
            >
              {t('downloads.page.clear')}
            </button>
          </div>
        </div>
      )}
    </div>
  )
}
