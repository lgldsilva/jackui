import { useEffect, useRef, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { FolderX, ChevronDown, Home, MapPin } from 'lucide-react'

type Props = {
  /** true quando o usuário já está na raiz do mount (as duas opções coincidem). */
  readonly atRoot: boolean
  readonly onClean: (scope: 'here' | 'root') => void
}

// Botão "Limpar vazias" com dois alcances: a pasta atual (recursivo a partir
// dela) ou o mount inteiro (desde a raiz). Na raiz vira um botão simples — as
// duas opções dariam no mesmo. O menu fecha ao clicar fora / Esc.
export default function CleanEmptyButton({ atRoot, onClean }: Props) {
  const { t } = useTranslation()
  const [open, setOpen] = useState(false)
  const ref = useRef<HTMLDivElement>(null)

  useEffect(() => {
    if (!open) return
    const onDocClick = (e: MouseEvent) => {
      if (ref.current && !ref.current.contains(e.target as Node)) setOpen(false)
    }
    const onKey = (e: KeyboardEvent) => { if (e.key === 'Escape') setOpen(false) }
    document.addEventListener('mousedown', onDocClick)
    document.addEventListener('keydown', onKey)
    return () => {
      document.removeEventListener('mousedown', onDocClick)
      document.removeEventListener('keydown', onKey)
    }
  }, [open])

  const btnClass = 'flex-shrink-0 inline-flex items-center gap-1.5 text-sm bg-surface-tertiary/60 hover:bg-surface-tertiary text-text-primary border border-strong px-3 py-1.5 rounded-lg transition-colors font-medium'

  if (atRoot) {
    return (
      <button onClick={() => onClean('root')} title={t('local.clean.rootTitle')} className={btnClass}>
        <FolderX className="w-4 h-4" />
        <span className="hidden sm:inline">{t('local.clean.button')}</span>
      </button>
    )
  }

  return (
    <div ref={ref} className="relative flex-shrink-0">
      <button onClick={() => setOpen(o => !o)} title={t('local.clean.menuTitle')} className={btnClass}>
        <FolderX className="w-4 h-4" />
        <span className="hidden sm:inline">{t('local.clean.button')}</span>
        <ChevronDown className="w-3.5 h-3.5 text-text-muted" />
      </button>
      {open && (
        <div className="absolute right-0 z-20 mt-1 w-56 rounded-lg border border-default bg-surface-secondary shadow-lg py-1">
          <button
            onClick={() => { setOpen(false); onClean('here') }}
            className="w-full flex items-center gap-2 px-3 py-2 text-sm text-text-primary hover:bg-surface-tertiary/60 text-left"
          >
            <MapPin className="w-4 h-4 text-text-muted flex-shrink-0" />
            {t('local.clean.here')}
          </button>
          <button
            onClick={() => { setOpen(false); onClean('root') }}
            className="w-full flex items-center gap-2 px-3 py-2 text-sm text-text-primary hover:bg-surface-tertiary/60 text-left"
          >
            <Home className="w-4 h-4 text-text-muted flex-shrink-0" />
            {t('local.clean.wholeMount')}
          </button>
        </div>
      )}
    </div>
  )
}
