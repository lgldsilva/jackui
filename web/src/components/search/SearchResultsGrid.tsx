import type { ReactNode, RefObject } from 'react'
import { Layers } from 'lucide-react'
import { useTranslation } from 'react-i18next'
import type { SearchResult } from '../../api/client'
import { buildSeriesLayout } from '../../lib/seriesGroup'

type Props = {
  readonly filteredResults: SearchResult[]
  readonly visible: number
  readonly groupSeries: boolean
  readonly renderCard: (result: SearchResult, key: string) => ReactNode
  readonly sentinelRef: RefObject<HTMLDivElement>
}

// Grade de resultados paginada por infinite-scroll. Extraído do SearchPage
// (god-file): ramifica entre a visão agrupada por série e a lista plana, ambas
// paginando por RESULTADOS (mesma janela `visible` + sentinela).
export function SearchResultsGrid({ filteredResults, visible, groupSeries, renderCard, sentinelRef }: Props) {
  const { t } = useTranslation()
  if (groupSeries) {
    return (
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
                <span className="text-xs text-text-muted whitespace-nowrap">{t('search.season_eps', { season: item.season, count: item.count })}</span>
                <div className="flex-1 h-px bg-strong/40" />
              </div>
            ) : renderCard(item.result, `${item.result.infoHash || item.result.link}-${i}`)
          ))}
        </div>
        {visible < filteredResults.length && (
          <div ref={sentinelRef} className="text-center py-6 text-xs text-text-muted">
            {t('search.showing_more', { visible, total: filteredResults.length })}
          </div>
        )}
      </>
    )
  }
  return (
    <>
      <div className="grid grid-cols-1 md:grid-cols-2 lg:grid-cols-3 gap-4">
        {filteredResults.slice(0, visible).map((result, i) => (
          renderCard(result, `${result.infoHash || result.link}-${i}`)
        ))}
      </div>
      {visible < filteredResults.length && (
        <div ref={sentinelRef} className="text-center py-6 text-xs text-text-muted">
          {t('search.showing_more', { visible, total: filteredResults.length })}
        </div>
      )}
    </>
  )
}
