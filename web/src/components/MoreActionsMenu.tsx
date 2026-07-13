import { useEffect, useRef, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { MoreHorizontal, Loader2 } from 'lucide-react'

export type MoreActionItem = {
  readonly id: string
  readonly label: string
  readonly onClick: () => void
  readonly disabled?: boolean
  readonly icon?: React.ReactNode
}

type MoreActionsMenuProps = {
  readonly items: MoreActionItem[]
  readonly className?: string
}

/** Agrupa ações secundárias do card (UX-3.1) num menu ⋯ compacto. */
export default function MoreActionsMenu({ items, className = '' }: MoreActionsMenuProps) {
  const { t } = useTranslation()
  const [open, setOpen] = useState(false)
  const rootRef = useRef<HTMLDivElement>(null)

  useEffect(() => {
    if (!open) return
    const onDoc = (e: MouseEvent) => {
      if (!rootRef.current?.contains(e.target as Node)) setOpen(false)
    }
    document.addEventListener('mousedown', onDoc)
    return () => document.removeEventListener('mousedown', onDoc)
  }, [open])

  if (items.length === 0) return null

  return (
    <div ref={rootRef} className={`relative ${className}`}>
      <button
        type="button"
        onClick={(e) => { e.stopPropagation(); setOpen(v => !v) }}
        aria-expanded={open}
        aria-haspopup="menu"
        aria-label={t('search.moreActions')}
        title={t('search.moreActions')}
        className="flex items-center gap-1 text-xs bg-surface-tertiary hover:bg-surface-secondary text-text-primary px-2.5 py-1.5 rounded-lg transition-colors"
      >
        <MoreHorizontal className="w-3.5 h-3.5" />
        {t('search.moreActions')}
      </button>
      {open && (
        <div
          role="menu"
          className="absolute right-0 bottom-full mb-1 z-30 min-w-[10rem] rounded-lg border border-strong bg-surface shadow-lg py-1"
          onClick={(e) => e.stopPropagation()}
        >
          {items.map(item => (
            <button
              key={item.id}
              type="button"
              role="menuitem"
              disabled={item.disabled}
              onClick={(e) => {
                e.stopPropagation()
                setOpen(false)
                item.onClick()
              }}
              className="w-full flex items-center gap-2 px-3 py-2 text-xs text-text-primary hover:bg-surface-tertiary disabled:opacity-50 text-left transition-colors"
            >
              {item.disabled ? <Loader2 className="w-3.5 h-3.5 animate-spin flex-shrink-0" /> : item.icon}
              <span>{item.label}</span>
            </button>
          ))}
        </div>
      )}
    </div>
  )
}
