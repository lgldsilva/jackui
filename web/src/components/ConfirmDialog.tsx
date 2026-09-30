import { createContext, ReactNode, useCallback, useContext, useMemo, useRef, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { AlertTriangle } from 'lucide-react'
import { Sheet } from './Sheet'

export type ConfirmOptions = {
  readonly title?: string
  readonly message?: ReactNode
  readonly confirmLabel?: string
  readonly cancelLabel?: string
  /** Red confirm button + alert icon. Default true (typical use is delete). */
  readonly destructive?: boolean
}

type Pending = ConfirmOptions & { readonly resolve: (ok: boolean) => void }

const ConfirmContext = createContext<((opts: ConfirmOptions) => Promise<boolean>) | null>(null)

/**
 * Replaces the native `confirm()` (inaccessible/ugly on mobile) with a dialog in the
 * dark theme, mounted on top of the Sheet (bottom-sheet on mobile, card on desktop).
 * Wraps the app once; the `useConfirm()` hook returns an async function.
 */
export function ConfirmProvider({ children }: { readonly children: ReactNode }) {
  const { t } = useTranslation()
  const [pending, setPending] = useState<Pending | null>(null)
  const pendingRef = useRef<Pending | null>(null)
  pendingRef.current = pending

  const confirm = useCallback((opts: ConfirmOptions) => {
    // If a dialog is already open, resolve it as cancelled before opening the new one.
    pendingRef.current?.resolve(false)
    return new Promise<boolean>(resolve => setPending({ ...opts, resolve }))
  }, [])

  const settle = useCallback((ok: boolean) => {
    setPending(prev => { prev?.resolve(ok); return null })
  }, [])

  const destructive = pending?.destructive ?? true

  const footer = (
    <div className="flex items-center justify-end gap-2">
      <button
        onClick={() => settle(false)}
        className="px-4 py-2 rounded-lg text-sm text-text-primary hover:bg-surface-tertiary transition-colors min-h-[44px]"
      >
        {pending?.cancelLabel ?? t('misc.cancel')}
      </button>
      <button
        onClick={() => settle(true)}
        className={`px-4 py-2 rounded-lg text-sm font-medium transition-colors min-h-[44px] ${
          destructive
            ? 'bg-red-500/90 hover:bg-red-500 text-white'
            : 'bg-green-500 hover:bg-green-600 text-white'
        }`}
      >
        {pending?.confirmLabel ?? t('misc.confirm')}
      </button>
    </div>
  )

  return (
    <ConfirmContext.Provider value={confirm}>
      {children}
      <Sheet
        open={pending !== null}
        onClose={() => settle(false)}
        size="sm"
        title={pending?.title ?? t('misc.confirm')}
        icon={destructive ? <AlertTriangle className="w-4 h-4 text-red-400 flex-shrink-0" /> : undefined}
        footer={footer}
      >
        <div className="text-sm text-text-primary leading-relaxed">{pending?.message}</div>
      </Sheet>
    </ConfirmContext.Provider>
  )
}

export function useConfirm(): (opts: ConfirmOptions) => Promise<boolean> {
  const ctx = useContext(ConfirmContext)
  if (!ctx) throw new Error('useConfirm must be used within a <ConfirmProvider>')
  // useMemo only to stabilize the reference (the ctx is already stable via useCallback).
  return useMemo(() => ctx, [ctx])
}
