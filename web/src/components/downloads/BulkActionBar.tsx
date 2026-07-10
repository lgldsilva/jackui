import { useTranslation } from 'react-i18next'
import { Pause, Play, Trash2, ArrowUpCircle } from 'lucide-react'
import { SelectAllButton } from '../SelectAllButton'

// BulkActionBar — floating bar shown while completed rows are multi-selected
// (pause / resume / promote / remove + select-all toggle).
export function BulkActionBar(props: {
  readonly selectedCount: number
  readonly allSelected: boolean
  readonly bulkBusy: boolean
  readonly onBatchPause: () => void
  readonly onBatchResume: () => void
  readonly onPromoteSelected: () => void
  readonly onBatchDelete: () => void
  readonly onToggleSelectAll: () => void
}) {
  const {
    selectedCount, allSelected, bulkBusy,
    onBatchPause, onBatchResume, onPromoteSelected, onBatchDelete, onToggleSelectAll,
  } = props
  const { t } = useTranslation()
  return (
    <div
      style={{ bottom: 'calc(1rem + env(safe-area-inset-bottom, 0px))' }}
      className="fixed left-1/2 -translate-x-1/2 z-40 flex items-center gap-2 bg-surface-secondary border border-cyan-500/40 shadow-2xl rounded-full px-4 py-2 backdrop-blur"
    >
      <span className="text-sm text-text-primary font-medium whitespace-nowrap">{t('downloads.page.selectedCount', { count: selectedCount })}</span>
      <div className="w-px h-5 bg-surface-tertiary" />
      <button
        onClick={onBatchPause}
        disabled={bulkBusy}
        className="flex items-center gap-1 text-xs bg-surface-tertiary/60 hover:bg-surface-tertiary disabled:opacity-50 text-text-primary px-3 py-1 rounded-full transition-colors"
      >
        <Pause className="w-3 h-3" /> {t('downloads.page.pause')}
      </button>
      <button
        onClick={onBatchResume}
        disabled={bulkBusy}
        className="flex items-center gap-1 text-xs bg-emerald-500/10 hover:bg-emerald-500/20 disabled:opacity-50 text-emerald-700 dark:text-emerald-300 px-3 py-1 rounded-full transition-colors"
      >
        <Play className="w-3 h-3" /> {t('downloads.page.resume')}
      </button>
      <button
        onClick={onPromoteSelected}
        className="flex items-center gap-1 text-xs bg-cyan-500/20 hover:bg-cyan-500/30 text-cyan-700 dark:text-cyan-300 px-3 py-1 rounded-full transition-colors"
      >
        <ArrowUpCircle className="w-3 h-3" />
        {t('downloads.page.promote')}
      </button>
      <button
        onClick={onBatchDelete}
        disabled={bulkBusy}
        className="flex items-center gap-1 text-xs bg-red-500/10 hover:bg-red-500/20 disabled:opacity-50 text-red-700 dark:text-red-300 px-3 py-1 rounded-full transition-colors"
      >
        <Trash2 className="w-3 h-3" /> {t('downloads.page.remove')}
      </button>
      <div className="w-px h-5 bg-surface-tertiary" />
      <SelectAllButton
        allSelected={allSelected}
        onToggle={onToggleSelectAll}
      />
    </div>
  )
}
