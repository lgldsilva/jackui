import { ReactNode, useRef } from 'react'
import { X } from 'lucide-react'
import { useScrollLock } from '../lib/useScrollLock'
import { useSwipe } from '../lib/useSwipe'
import { useIsMobile } from '../lib/useMediaQuery'

export type SheetSize = 'sm' | 'md' | 'lg' | 'xl' | '2xl' | '3xl' | '4xl'

const SIZE_MAXW: Record<SheetSize, string> = {
  sm: 'sm:max-w-sm',
  md: 'sm:max-w-md',
  lg: 'sm:max-w-lg',
  xl: 'sm:max-w-xl',
  '2xl': 'sm:max-w-2xl',
  '3xl': 'sm:max-w-3xl',
  '4xl': 'sm:max-w-4xl',
}

export type SheetProps = {
  readonly open: boolean
  readonly onClose: () => void
  readonly title?: ReactNode
  readonly icon?: ReactNode
  readonly children: ReactNode
  readonly footer?: ReactNode
  readonly size?: SheetSize
  /** z-index do backdrop. Default 'z-50'. O info-modal do player passa 'z-[60]'. */
  readonly zClass?: string
  readonly hideHeader?: boolean
  /** Quando false, NÃO bloqueia o scroll do body (ex: já bloqueado pelo player). Default true. */
  readonly lockScroll?: boolean
}

/**
 * Modal responsivo: no mobile (<sm) vira um bottom-sheet full-height que sobe de
 * baixo (com safe-area e drag-to-close pelo cabeçalho); no desktop (sm+) é o card
 * centralizado de sempre. O layout é 100% CSS por breakpoint (`items-end
 * sm:items-center`, `rounded-t-2xl sm:rounded-2xl`) — o único JS é armar o
 * swipe-to-close só no mobile. Usa <div role="dialog"> em vez de <dialog> pra
 * evitar a quirk de `width: fit-content` do user-agent.
 */
export function Sheet({
  open,
  onClose,
  title,
  icon,
  children,
  footer,
  size = 'md',
  zClass = 'z-50',
  hideHeader = false,
  lockScroll = true,
}: SheetProps) {
  const headerRef = useRef<HTMLDivElement>(null)
  const isMobile = useIsMobile()

  useScrollLock(open && lockScroll)
  // Swipe-to-close: só pelo header/grabber (não pelo corpo, pra não brigar com
  // o scroll interno). Restraint alto = aceita um arraste vertical "puro".
  useSwipe(headerRef, { onDown: onClose }, { enabled: open && isMobile, threshold: 70, restraint: 9999 })

  if (!open) return null

  return (
    <div
      className={`fixed inset-0 bg-black/60 backdrop-blur-sm flex items-end sm:items-center justify-center ${zClass} sm:p-4`}
      onClick={e => e.target === e.currentTarget && onClose()}
      onKeyDown={e => e.key === 'Escape' && onClose()}
      tabIndex={-1}
    >
      <div
        role="dialog"
        aria-modal="true"
        className={`bg-gray-800 w-full ${SIZE_MAXW[size]} rounded-t-2xl sm:rounded-2xl border-0 sm:border border-gray-700 shadow-2xl flex flex-col max-h-[92dvh] sm:max-h-[90vh] p-0 m-0 text-inherit`}
      >
        <div ref={headerRef}>
          {/* Grabber — só no mobile, sinaliza o drag-to-close */}
          <div className="sm:hidden mx-auto mt-2 mb-1 h-1.5 w-10 rounded-full bg-gray-600" aria-hidden />
          {!hideHeader && (
            <div className="flex items-center justify-between p-4 border-b border-gray-700 flex-shrink-0">
              {title != null && (
                <h2 className="text-base font-semibold text-gray-100 flex items-center gap-2 min-w-0">
                  {icon}
                  <span className="truncate">{title}</span>
                </h2>
              )}
              <button
                onClick={onClose}
                aria-label="Fechar"
                className="ml-auto text-gray-400 hover:text-gray-200 p-1.5 -m-1.5 rounded-lg hover:bg-gray-700/50"
              >
                <X className="w-5 h-5" />
              </button>
            </div>
          )}
        </div>

        <div className="flex-1 overflow-y-auto p-4 overscroll-contain">{children}</div>

        {footer != null && (
          <div className="flex-shrink-0 border-t border-gray-700 p-4 safe-bottom sm:pb-4">{footer}</div>
        )}
      </div>
    </div>
  )
}
