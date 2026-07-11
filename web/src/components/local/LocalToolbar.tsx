import { useTranslation } from 'react-i18next'
import { Search, X, ArrowUp, ArrowDown } from 'lucide-react'
import { type LocalStatusFilter } from '../../lib/localFilter'
import { type KindFilter, type SortKey } from './localViewTypes'

type Props = {
  readonly search: string
  readonly onSearchChange: (value: string) => void
  readonly canManipulate: boolean
  readonly isAdmin: boolean
  readonly selectMode: boolean
  readonly onEnterSelectMode: () => void
  readonly kind: KindFilter
  readonly onKindChange: (k: KindFilter) => void
  readonly statusFilter: LocalStatusFilter
  readonly onStatusChange: (s: LocalStatusFilter) => void
  readonly sortKey: SortKey
  readonly sortDir: 'asc' | 'desc'
  readonly onToggleSort: (key: SortKey) => void
}

// Toolbar: busca + selecionar; chips de tipo + status + ordenação (flex-shrink-0
// pra ficar fixa enquanto a lista abaixo rola).
export function LocalToolbar({
  search, onSearchChange, canManipulate, isAdmin, selectMode, onEnterSelectMode,
  kind, onKindChange, statusFilter, onStatusChange, sortKey, sortDir, onToggleSort,
}: Props) {
  const { t } = useTranslation()
  return (
    <div className="flex-shrink-0 flex flex-col gap-2">
      <div className="flex items-center gap-2">
        <div className="relative flex-1 min-w-0">
          <Search className="absolute left-3 top-1/2 -translate-y-1/2 w-4 h-4 text-text-muted pointer-events-none" />
          <input
            type="text"
            value={search}
            onChange={(e) => onSearchChange(e.target.value)}
            placeholder={t('local.searchPlaceholder')}
            className="w-full bg-surface-secondary border border-default rounded-lg pl-9 pr-8 py-2 text-base sm:text-sm text-text-primary placeholder-gray-500 focus:outline-none focus:border-green-500/50"
          />
          {search && (
            <button
              onClick={() => onSearchChange('')}
              aria-label={t('local.clearSearch')}
              className="absolute right-2 top-1/2 -translate-y-1/2 text-text-muted hover:text-text-primary p-1"
            >
              <X className="w-3.5 h-3.5" />
            </button>
          )}
        </div>
        {(canManipulate || isAdmin) && !selectMode && (
          <button
            onClick={onEnterSelectMode}
            className="flex-shrink-0 text-sm px-3 min-h-[44px] sm:min-h-0 sm:py-1.5 rounded-lg border border-default text-text-primary hover:bg-surface-tertiary transition-colors"
          >
            {t('local.select')}
          </button>
        )}
      </div>
      {/* Dois grupos rotulados (Tipo / Ordenar). No mobile empilham
          (flex-col) com rótulo visível em cada um — antes os chips dos
          dois grupos se misturavam numa mesma linha-que-quebra, sem
          rótulo, e ficava confuso. No desktop voltam pra uma linha. */}
      <div className="flex flex-col sm:flex-row sm:flex-wrap sm:items-center gap-2 text-xs">
        <div className="flex flex-wrap items-center gap-2">
          <span className="text-text-muted sm:hidden mr-0.5">{t('local.typeLabel')}</span>
          {(['all', 'video', 'audio', 'other'] as KindFilter[]).map((k) => (
            <button
              key={k}
              onClick={() => onKindChange(k)}
              className={`px-2.5 py-1 rounded-full border transition-colors ${
                kind === k
                  ? 'bg-green-500/15 text-green-400 border-green-500/40'
                  : 'text-text-secondary border-default hover:border-strong'
              }`}
            >
              {t(`local.kind.${k}`)}
            </button>
          ))}
        </div>
        <span className="mx-1 h-4 w-px bg-surface-tertiary hidden sm:block" />
        <div className="flex flex-wrap items-center gap-2">
          <span className="text-text-muted sm:hidden mr-0.5">{t('local.statusLabel')}</span>
          {(['all', 'downloading', 'done'] as LocalStatusFilter[]).map((s) => (
            <button
              key={s}
              onClick={() => onStatusChange(s)}
              className={`px-2.5 py-1 rounded-full border transition-colors ${
                statusFilter === s
                  ? 'bg-green-500/15 text-green-400 border-green-500/40'
                  : 'text-text-secondary border-default hover:border-strong'
              }`}
            >
              {t(`local.status.${s}`)}
            </button>
          ))}
        </div>
        <span className="mx-1 h-4 w-px bg-surface-tertiary hidden sm:block" />
        <div className="flex flex-wrap items-center gap-2">
          <span className="text-text-muted mr-0.5">{t('local.sortLabel')}</span>
          {(['name', 'size', 'date'] as SortKey[]).map((k) => (
            <button
              key={k}
              onClick={() => onToggleSort(k)}
              className={`flex items-center gap-1 px-2.5 py-1 rounded-full border transition-colors ${
                sortKey === k
                  ? 'bg-surface-tertiary text-text-primary border-strong'
                  : 'text-text-secondary border-default hover:border-strong'
              }`}
            >
              {t(`local.sort.${k}`)}
              {sortKey === k && (sortDir === 'asc' ? <ArrowUp className="w-3 h-3" /> : <ArrowDown className="w-3 h-3" />)}
            </button>
          ))}
        </div>
      </div>
    </div>
  )
}
