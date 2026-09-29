import { useEffect } from 'react'
import { useTranslation } from 'react-i18next'
import { Trash2, FolderInput, ArrowUpCircle, X, Loader2 } from 'lucide-react'
import { SelectAllButton } from './SelectAllButton'

export type BatchActionBarProps = {
  readonly count: number
  readonly onCancel: () => void
  readonly onSelectAll?: () => void
  readonly allSelected?: boolean
  readonly canMove: boolean
  readonly canPromote: boolean
  readonly onDelete: () => void
  readonly onMove: () => void
  readonly onPromote: () => void
  readonly running?: boolean
}

/**
 * Batch action bar fixed at the bottom (LocalPage selection mode). z-40 sits
 * below Sheets/modals (z-50) and above the list. `safe-bottom` respects the
 * iPhone home-indicator. The list gets `pb-20` while the bar is open.
 */
export function BatchActionBar({
  count, onCancel, onSelectAll, allSelected = false, canMove, canPromote, onDelete, onMove, onPromote, running = false,
}: BatchActionBarProps) {
  const { t } = useTranslation()
  // Reserves footer space while the bar is mounted (CSS var on :root) so
  // the player's floating dock (bottom-right, z-50) rises above it instead of
  // covering the right-hand buttons. Cleared on unmount (leaving selection mode).
  useEffect(() => {
    const root = document.documentElement
    root.style.setProperty('--bottom-bar-h', '4.5rem')
    return () => { root.style.setProperty('--bottom-bar-h', '0px') }
  }, [])
  const actionBtn = 'flex items-center justify-center gap-1.5 px-3 min-h-[44px] rounded-lg text-sm font-medium transition-colors disabled:opacity-40'
  return (
    <div className="fixed bottom-0 inset-x-0 z-40 bg-surface-secondary border-t border-default px-3 pt-2 safe-bottom shadow-2xl">
      <div className="max-w-7xl mx-auto flex items-center gap-2">
        <button onClick={onCancel} aria-label={t('downloads.batchBar.cancelSelection')} className={`${actionBtn} text-text-primary hover:bg-surface-tertiary`}>
          <X className="w-4 h-4" />
        </button>
        <span className="text-sm text-text-primary font-medium whitespace-nowrap">{t('downloads.batchBar.selectedShort', { count })}</span>
        {onSelectAll && (
          <SelectAllButton allSelected={allSelected} onToggle={onSelectAll}
            className={`${actionBtn} text-text-primary hover:bg-surface-tertiary`} />
        )}
        <div className="flex-1" />
        {running && <Loader2 className="w-4 h-4 animate-spin text-text-secondary" />}
        {canPromote && (
          <button onClick={onPromote} disabled={count === 0 || running} className={`${actionBtn} text-cyan-700 dark:text-cyan-300 hover:bg-cyan-500/15`}>
            <ArrowUpCircle className="w-4 h-4" /><span className="hidden min-[400px]:inline">{t('downloads.batchBar.promote')}</span>
          </button>
        )}
        {canMove && (
          <button onClick={onMove} disabled={count === 0 || running} className={`${actionBtn} text-amber-700 dark:text-amber-300 hover:bg-amber-500/15`}>
            <FolderInput className="w-4 h-4" /><span className="hidden min-[400px]:inline">{t('downloads.batchBar.move')}</span>
          </button>
        )}
        <button onClick={onDelete} disabled={count === 0 || running} className={`${actionBtn} text-red-700 dark:text-red-300 hover:bg-red-500/15`}>
          <Trash2 className="w-4 h-4" /><span className="hidden min-[400px]:inline">{t('downloads.batchBar.delete')}</span>
        </button>
      </div>
    </div>
  )
}
