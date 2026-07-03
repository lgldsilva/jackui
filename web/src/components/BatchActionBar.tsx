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
 * Barra de ações em lote fixa no rodapé (modo de seleção do LocalPage). z-40 fica
 * abaixo dos Sheets/modais (z-50) e acima da lista. `safe-bottom` respeita a
 * home-indicator do iPhone. A lista recebe `pb-20` enquanto a barra está aberta.
 */
export function BatchActionBar({
  count, onCancel, onSelectAll, allSelected = false, canMove, canPromote, onDelete, onMove, onPromote, running = false,
}: BatchActionBarProps) {
  const { t } = useTranslation()
  // Reserva espaço no rodapé enquanto a barra está montada (CSS var no :root), pra
  // o dock flutuante do player (bottom-right, z-50) subir acima dela em vez de
  // cobrir os botões da direita. Limpa ao desmontar (sair do modo de seleção).
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
