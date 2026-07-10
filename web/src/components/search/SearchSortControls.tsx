import { SortAsc, SortDesc } from 'lucide-react'
import { useTranslation } from 'react-i18next'
import type { TabState, ResultSortKey } from '../../lib/searchTabs'

const SORT_OPTIONS: { key: ResultSortKey; labelKey: string }[] = [
  { key: 'seeders',  labelKey: 'search.seeds'    },
  { key: 'leechers', labelKey: 'search.leechers' },
  { key: 'size',     labelKey: 'search.size'     },
  { key: 'title',    labelKey: 'search.name'     },
  { key: 'age',      labelKey: 'search.date'     },
]

type Props = {
  readonly tab: TabState
  readonly onUpdate: (patch: Partial<TabState>) => void
}

// Controle de ordenação dos resultados. No celular (sem espaço pro segmented
// control de 5 opções, que gerava scroll horizontal no header) vira um dropdown
// compacto + botão asc/desc; no desktop fica o segmented control. Extraído do
// SearchPage (god-file), antes era a função sortControls.
export function SearchSortControls({ tab, onUpdate }: Props) {
  const { t } = useTranslation()
  const toggleSort = (key: ResultSortKey) => {
    if (tab.resultSort === key) {
      onUpdate({ resultSortAsc: !tab.resultSortAsc })
    } else {
      onUpdate({ resultSort: key, resultSortAsc: false })
    }
  }
  return (
    <>
      {/* Mobile: dropdown compacto — nunca estoura a largura da tela. */}
      <div className="flex sm:hidden items-center gap-1.5 min-w-0 w-full">
        <span className="text-xs text-text-muted flex-shrink-0">{t('search.sort_label')}</span>
        <select
          value={tab.resultSort}
          onChange={e => onUpdate({ resultSort: e.target.value as ResultSortKey, resultSortAsc: false })}
          className="flex-1 min-w-0 text-xs bg-surface-tertiary border border-strong rounded-lg px-2 py-1.5 text-text-primary"
        >
          {SORT_OPTIONS.map(({ key, labelKey }) => <option key={key} value={key}>{t(labelKey)}</option>)}
        </select>
        <button
          onClick={() => onUpdate({ resultSortAsc: !tab.resultSortAsc })}
          title={tab.resultSortAsc ? t('search.asc') : t('search.desc')}
          className="flex-shrink-0 p-1.5 rounded-lg bg-surface-tertiary border border-strong text-text-secondary"
        >
          {tab.resultSortAsc ? <SortAsc className="w-3.5 h-3.5" /> : <SortDesc className="w-3.5 h-3.5" />}
        </button>
      </div>
      {/* Desktop: segmented control (wrap em vez de scroll horizontal). */}
      <div className="hidden sm:flex items-center gap-1.5 max-w-full">
        <span className="text-xs text-text-muted flex-shrink-0">{t('search.sort_label')}</span>
        <div className="flex items-center gap-1 bg-surface-tertiary border border-strong rounded-lg p-1 flex-wrap">
          {SORT_OPTIONS.map(({ key, labelKey }) => (
            <button
              key={key}
              onClick={() => toggleSort(key)}
              className={`flex items-center gap-1 text-xs px-2.5 py-1 rounded-md transition-colors whitespace-nowrap ${
                tab.resultSort === key
                  ? 'bg-green-500/20 text-green-400'
                  : 'text-text-secondary hover:text-text-primary'
              }`}
            >
              {t(labelKey)}
              {tab.resultSort === key && (
                tab.resultSortAsc
                  ? <SortAsc className="w-3 h-3" />
                  : <SortDesc className="w-3 h-3" />
              )}
            </button>
          ))}
        </div>
      </div>
    </>
  )
}
