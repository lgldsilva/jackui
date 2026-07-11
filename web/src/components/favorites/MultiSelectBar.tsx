import { useTranslation } from 'react-i18next'
import { Trash2, X } from 'lucide-react'
import { FavoriteFolder } from '../../api/client'

type MultiSelectBarProps = {
  readonly count: number
  readonly folders: FavoriteFolder[]
  readonly onMoveToFolder: (folderId: number | null) => void
  readonly onDeleteSelected: () => void
  readonly onClear: () => void
}

export default function MultiSelectBar(p: MultiSelectBarProps) {
  const { t } = useTranslation()
  return (
    <div className="fixed bottom-4 left-1/2 -translate-x-1/2 z-40 flex items-center gap-3 bg-surface-secondary border border-default rounded-full shadow-2xl px-4 py-2 safe-bottom">
      <span className="text-sm text-text-primary whitespace-nowrap">{t('favorites.selectedCount', { count: p.count })}</span>
      <select
        defaultValue=""
        onChange={e => { p.onMoveToFolder(e.target.value === '' ? null : Number(e.target.value)); e.target.value = '' }}
        className="bg-surface border border-default rounded-lg text-sm text-text-primary px-2 py-1 focus:outline-none focus:border-green-500"
      >
        <option value="" disabled>{t('favorites.moveTo')}</option>
        <option value="">{t('favorites.rootNoFolder')}</option>
        {p.folders.map(f => (
          <option key={f.id} value={f.id}>{f.name}</option>
        ))}
      </select>
      <button
        onClick={p.onDeleteSelected}
        className="flex items-center gap-1 text-xs text-red-400 hover:text-red-500 dark:hover:text-red-300 px-2 py-1"
        title={t('favorites.deleteSelectedTooltip')}
      >
        <Trash2 className="w-3.5 h-3.5" />
        {t('favorites.delete')}
      </button>
      <button onClick={p.onClear} title={t('favorites.clearSelection')} className="text-text-secondary hover:text-text-primary">
        <X className="w-4 h-4" />
      </button>
    </div>
  )
}
