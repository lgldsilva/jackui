import { useTranslation } from 'react-i18next'
import { Pause, Play, Trash2 } from 'lucide-react'
import { Sheet } from '../Sheet'

// BulkActionsSheet — mobile grouping of the global bulk actions (the desktop
// inline toolbar equivalent). Each action closes the sheet then runs.
export function BulkActionsSheet(props: {
  readonly open: boolean
  readonly onClose: () => void
  readonly bulkBusy: boolean
  readonly completedCount: number
  readonly failedCount: number
  readonly queuedCount: number
  readonly onResumeAll: () => void
  readonly onPauseAll: () => void
  readonly onRemoveCompleted: () => void
  readonly onClearFailed: () => void
  readonly onClearQueued: () => void
}) {
  const {
    open, onClose, bulkBusy, completedCount, failedCount, queuedCount,
    onResumeAll, onPauseAll, onRemoveCompleted, onClearFailed, onClearQueued,
  } = props
  const { t } = useTranslation()
  return (
    <Sheet
      open={open}
      onClose={onClose}
      title={t('downloads.page.actions')}
      size="sm"
    >
      <div className="flex flex-col gap-2">
        <button
          onClick={() => { onClose(); onResumeAll() }}
          disabled={bulkBusy}
          className="flex items-center gap-2 min-h-[48px] px-4 rounded-lg bg-emerald-500/10 text-emerald-700 dark:text-emerald-300 border border-emerald-500/30 disabled:opacity-50"
        >
          <Play className="w-4 h-4" /> {t('downloads.page.startAll')}
        </button>
        <button
          onClick={() => { onClose(); onPauseAll() }}
          disabled={bulkBusy}
          className="flex items-center gap-2 min-h-[48px] px-4 rounded-lg bg-surface-tertiary/60 text-text-primary border border-default disabled:opacity-50"
        >
          <Pause className="w-4 h-4" /> {t('downloads.page.pauseAll')}
        </button>
        {completedCount > 0 && (
          <button
            onClick={() => { onClose(); onRemoveCompleted() }}
            disabled={bulkBusy}
            className="flex items-center gap-2 min-h-[48px] px-4 rounded-lg bg-red-500/10 text-red-700 dark:text-red-300 border border-red-500/30 disabled:opacity-50"
          >
            <Trash2 className="w-4 h-4" /> {t('downloads.page.removeCompletedCount', { count: completedCount })}
          </button>
        )}
        {failedCount > 0 && (
          <button
            onClick={() => { onClose(); onClearFailed() }}
            disabled={bulkBusy}
            className="flex items-center gap-2 min-h-[48px] px-4 rounded-lg bg-red-500/10 text-red-700 dark:text-red-300 border border-red-500/30 disabled:opacity-50"
          >
            <Trash2 className="w-4 h-4" /> {t('downloads.clear_failed')} ({failedCount})
          </button>
        )}
        {queuedCount > 0 && (
          <button
            onClick={() => { onClose(); onClearQueued() }}
            disabled={bulkBusy}
            className="flex items-center gap-2 min-h-[48px] px-4 rounded-lg bg-red-500/10 text-red-700 dark:text-red-300 border border-red-500/30 disabled:opacity-50"
          >
            <Trash2 className="w-4 h-4" /> {t('downloads.clear_queued')} ({queuedCount})
          </button>
        )}
      </div>
    </Sheet>
  )
}
