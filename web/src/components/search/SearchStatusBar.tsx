import { Loader2, Wifi, WifiOff } from 'lucide-react'
import { useTranslation, Trans } from 'react-i18next'
import type { TabState } from '../../lib/searchTabs'
import { SearchSortControls } from './SearchSortControls'

type Props = {
  readonly tab: TabState
  readonly onUpdate: (patch: Partial<TabState>) => void
  readonly isFiltered: boolean
  readonly filteredCount: number
  readonly groupedCount: number
  readonly hasDuplicates: boolean
  readonly isSearching: boolean
  readonly hasResults: boolean
}

// Barra de status da busca (fase/contagem + controle de ordenação). No mobile a
// contagem e o sort NÃO dividem a mesma linha (encavalavam com o `ml-auto`):
// empilham, com a ordenação numa linha própria full-width; no sm:+ voltam pra
// mesma linha, com o sort empurrado pra direita. Extraído do SearchPage (god-file).
export function SearchStatusBar({
  tab, onUpdate, isFiltered, filteredCount, groupedCount, hasDuplicates, isSearching, hasResults,
}: Props) {
  const { t } = useTranslation()
  return (
    <div className="flex flex-col items-start gap-2 text-sm sm:flex-row sm:flex-wrap sm:items-center sm:gap-3">
      {tab.phase === 'cache' && (
        <span className="flex items-center gap-2 text-blue-400">
          <Loader2 className="w-3.5 h-3.5 animate-spin" />{t('search.loading_cache')}
        </span>
      )}
      {tab.phase === 'live' && (
        <span className="flex items-center gap-2 text-yellow-400">
          <Wifi className="w-3.5 h-3.5 animate-pulse" />{t('search.searching_live')}
        </span>
      )}
      {tab.phase === 'done' && tab.summary && (
        <span className="flex items-center gap-2 text-green-400">
          <Wifi className="w-3.5 h-3.5" />
          {isFiltered ? (
            <Trans i18nKey="search.filtered_of_unique" values={{ shown: filteredCount, total: groupedCount }} components={{ b: <span className="text-text-primary font-medium" /> }} />
          ) : (
            <>
              <span className="text-text-primary font-medium">{groupedCount}</span>
              {' '}{t('search.unique')}
              {hasDuplicates && (
                <span
                  className="text-text-muted"
                  title={t('search.raw_tooltip', { count: tab.results.length })}
                >
                  {' '}{t('search.raw_count', { count: tab.results.length })}
                </span>
              )}
            </>
          )}{' '}{t('search.for')} <span className="text-text-primary font-medium">"{tab.query}"</span>
          <span className="text-text-muted">
            {t('search.live_cache_summary', { live: tab.summary.live, cached: tab.summary.cached })}
          </span>
        </span>
      )}
      {tab.phase === 'error' && (
        <span className="flex items-center gap-2 text-red-400">
          <WifiOff className="w-3.5 h-3.5" />{tab.error || t('search.search_error')}
        </span>
      )}
      {hasResults && (
        <div className="w-full sm:w-auto sm:ml-auto flex items-center gap-3 min-w-0">
          {isSearching && (
            <span className="text-text-muted flex-shrink-0">{t('search.so_far', { count: tab.results.length })}</span>
          )}
          <SearchSortControls tab={tab} onUpdate={onUpdate} />
        </div>
      )}
    </div>
  )
}
