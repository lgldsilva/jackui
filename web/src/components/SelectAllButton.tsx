import { CheckCheck, Square } from 'lucide-react'
import { useTranslation } from 'react-i18next'

// SelectAllButton — standardized control for lists with multi-selection.
// Toggle: when not everything is checked → "Select all"; when everything is
// checked → "Clear". Used in Downloads and BatchActionBar (Local) for a
// consistent affordance (icon + label, not just an icon).
export function SelectAllButton({
  allSelected, onToggle, className,
}: {
  readonly allSelected: boolean
  readonly onToggle: () => void
  readonly className?: string
}) {
  const { t } = useTranslation()
  return (
    <button
      onClick={onToggle}
      title={allSelected ? t('downloads.selectAll.clearSelection') : t('downloads.selectAll.selectAll')}
      className={className ?? 'flex items-center gap-1.5 text-xs text-text-primary hover:text-text-primary px-2.5 py-1 rounded-full hover:bg-surface-tertiary transition-colors whitespace-nowrap'}
    >
      {allSelected ? <Square className="w-3.5 h-3.5" /> : <CheckCheck className="w-3.5 h-3.5" />}
      {allSelected ? t('downloads.selectAll.clear') : t('downloads.selectAll.selectAll')}
    </button>
  )
}
