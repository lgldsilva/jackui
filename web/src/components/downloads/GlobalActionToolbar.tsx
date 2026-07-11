import { useTranslation, Trans } from 'react-i18next'
import {
  Pause, Play, Trash2, CheckCircle2, AlertCircle,
  Download, MoreHorizontal, AlertTriangle,
} from 'lucide-react'

// GlobalActionToolbar — the quick-stats strip + global bulk controls (start all /
// pause all / clear completed / clear failed / clear queued). Desktop shows the
// actions inline; mobile collapses them into a Sheet toggled by onOpenSheet.
export function GlobalActionToolbar(props: {
  readonly downloadingCount: number
  readonly pausedCount: number
  readonly completedCount: number
  readonly failedCount: number
  readonly queuedCount: number
  readonly stalledCount: number
  readonly isGuest: boolean
  readonly bulkBusy: boolean
  readonly onResumeAll: () => void
  readonly onPauseAll: () => void
  readonly onRemoveCompleted: () => void
  readonly onClearFailed: () => void
  readonly onClearQueued: () => void
  readonly onOpenSheet: () => void
}) {
  const {
    downloadingCount, pausedCount, completedCount, failedCount, queuedCount, stalledCount,
    isGuest, bulkBusy, onResumeAll, onPauseAll, onRemoveCompleted, onClearFailed, onClearQueued, onOpenSheet,
  } = props
  const { t } = useTranslation()
  return (
    <div className="flex items-center justify-between gap-3 flex-wrap">
      {/* Quick stats strip */}
      <div className="flex items-center gap-3 text-xs text-text-secondary flex-wrap">
        {downloadingCount > 0 && (
          <span className="flex items-center gap-1">
            <Download className="w-3.5 h-3.5 text-cyan-400" />
            <Trans i18nKey="downloads.page.quickDownloading" values={{ count: downloadingCount }} components={{ b: <span className="text-text-primary font-medium" /> }} />
          </span>
        )}
        {pausedCount > 0 && (
          <span className="flex items-center gap-1">
            <Pause className="w-3.5 h-3.5 text-text-secondary" />
            <Trans i18nKey="downloads.page.quickPaused" values={{ count: pausedCount }} components={{ b: <span className="text-text-primary font-medium" /> }} />
          </span>
        )}
        {completedCount > 0 && (
          <span className="flex items-center gap-1">
            <CheckCircle2 className="w-3.5 h-3.5 text-green-400" />
            <Trans i18nKey="downloads.page.quickCompleted" values={{ count: completedCount }} components={{ b: <span className="text-text-primary font-medium" /> }} />
          </span>
        )}
        {failedCount > 0 && (
          <span className="flex items-center gap-1">
            <AlertCircle className="w-3.5 h-3.5 text-red-400" />
            <Trans i18nKey="downloads.page.quickFailed" values={{ count: failedCount }} components={{ b: <span className="text-text-primary font-medium" /> }} />
          </span>
        )}
        {stalledCount > 0 && (
          <span className="flex items-center gap-1 text-amber-400">
            <AlertTriangle className="w-3.5 h-3.5" />
            <Trans i18nKey="downloads.page.quickStalled" values={{ count: stalledCount }} components={{ b: <span className="font-medium" /> }} />
          </span>
        )}
      </div>

      {/* Global controls */}
      {!isGuest && (
        <>
          {/* Desktop: ações inline */}
          <div className="hidden sm:flex items-center gap-2">
            <button
              onClick={onResumeAll}
              disabled={bulkBusy}
              title={t('downloads.page.startAllTitle')}
              className="flex items-center gap-1.5 text-xs bg-emerald-500/10 hover:bg-emerald-500/20 disabled:opacity-50 text-emerald-700 dark:text-emerald-300 border border-emerald-500/30 px-3 py-1.5 rounded-lg transition-colors"
            >
              <Play className="w-3 h-3" /> {t('downloads.page.startAll')}
            </button>
            <button
              onClick={onPauseAll}
              disabled={bulkBusy}
              title={t('downloads.page.pauseAllTitle')}
              className="flex items-center gap-1.5 text-xs bg-surface-secondary hover:bg-surface-tertiary disabled:opacity-50 text-text-primary border border-default px-3 py-1.5 rounded-lg transition-colors"
            >
              <Pause className="w-3 h-3" /> {t('downloads.page.pauseAll')}
            </button>
            {completedCount > 0 && (
              <button
                onClick={onRemoveCompleted}
                disabled={bulkBusy}
                title={t('downloads.page.removeCompletedBtnTitle')}
                className="flex items-center gap-1.5 text-xs bg-red-500/10 hover:bg-red-500/20 disabled:opacity-50 text-red-700 dark:text-red-300 border border-red-500/30 px-3 py-1.5 rounded-lg transition-colors"
              >
                <Trash2 className="w-3 h-3" /> {t('downloads.page.removeCompleted')}
              </button>
            )}
            {failedCount > 0 && (
              <button
                onClick={onClearFailed}
                disabled={bulkBusy}
                title={t('downloads.clear_failed_title')}
                className="flex items-center gap-1.5 text-xs bg-red-500/10 hover:bg-red-500/20 disabled:opacity-50 text-red-700 dark:text-red-300 border border-red-500/30 px-3 py-1.5 rounded-lg transition-colors"
              >
                <Trash2 className="w-3 h-3" /> {t('downloads.clear_failed')} ({failedCount})
              </button>
            )}
            {queuedCount > 0 && (
              <button
                onClick={onClearQueued}
                disabled={bulkBusy}
                title={t('downloads.clear_queued_title')}
                className="flex items-center gap-1.5 text-xs bg-red-500/10 hover:bg-red-500/20 disabled:opacity-50 text-red-700 dark:text-red-300 border border-red-500/30 px-3 py-1.5 rounded-lg transition-colors"
              >
                <Trash2 className="w-3 h-3" /> {t('downloads.clear_queued')} ({queuedCount})
              </button>
            )}
          </div>
          {/* Mobile: agrupadas num Sheet de "Ações" */}
          <button
            onClick={onOpenSheet}
            disabled={bulkBusy}
            className="sm:hidden flex items-center gap-1.5 text-xs px-3 min-h-[44px] rounded-lg border border-default bg-surface-secondary text-text-primary disabled:opacity-50"
          >
            <MoreHorizontal className="w-4 h-4" /> {t('downloads.page.actions')}
          </button>
        </>
      )}
    </div>
  )
}
