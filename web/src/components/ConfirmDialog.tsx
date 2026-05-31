import { createContext, ReactNode, useCallback, useContext, useMemo, useRef, useState } from 'react'
import { AlertTriangle } from 'lucide-react'
import { Sheet } from './Sheet'

export type ConfirmOptions = {
  readonly title?: string
  readonly message?: ReactNode
  readonly confirmLabel?: string
  readonly cancelLabel?: string
  /** Botão de confirmar em vermelho + ícone de alerta. Default true (uso típico é apagar). */
  readonly destructive?: boolean
}

type Pending = ConfirmOptions & { readonly resolve: (ok: boolean) => void }

const ConfirmContext = createContext<((opts: ConfirmOptions) => Promise<boolean>) | null>(null)

/**
 * Substitui o `confirm()` nativo (inacessível/feio no mobile) por um diálogo no
 * tema dark, montado sobre o Sheet (bottom-sheet no mobile, card no desktop).
 * Envolve a app uma vez; o hook `useConfirm()` retorna uma função async.
 */
export function ConfirmProvider({ children }: { readonly children: ReactNode }) {
  const [pending, setPending] = useState<Pending | null>(null)
  const pendingRef = useRef<Pending | null>(null)
  pendingRef.current = pending

  const confirm = useCallback((opts: ConfirmOptions) => {
    // Se já há um diálogo aberto, resolve-o como cancelado antes de abrir o novo.
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
        className="px-4 py-2 rounded-lg text-sm text-gray-300 hover:bg-gray-700 transition-colors min-h-[44px]"
      >
        {pending?.cancelLabel ?? 'Cancelar'}
      </button>
      <button
        onClick={() => settle(true)}
        className={`px-4 py-2 rounded-lg text-sm font-medium transition-colors min-h-[44px] ${
          destructive
            ? 'bg-red-500/90 hover:bg-red-500 text-white'
            : 'bg-green-500 hover:bg-green-600 text-white'
        }`}
      >
        {pending?.confirmLabel ?? 'Confirmar'}
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
        title={pending?.title ?? 'Confirmar'}
        icon={destructive ? <AlertTriangle className="w-4 h-4 text-red-400 flex-shrink-0" /> : undefined}
        footer={footer}
      >
        <div className="text-sm text-gray-300 leading-relaxed">{pending?.message}</div>
      </Sheet>
    </ConfirmContext.Provider>
  )
}

export function useConfirm(): (opts: ConfirmOptions) => Promise<boolean> {
  const ctx = useContext(ConfirmContext)
  if (!ctx) throw new Error('useConfirm precisa de <ConfirmProvider> acima na árvore')
  // useMemo só pra estabilizar a referência (o ctx já é estável via useCallback).
  return useMemo(() => ctx, [ctx])
}
