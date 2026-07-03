import { createContext, ReactNode, useContext, useEffect, useMemo, useState } from 'react'
import { CheckCircle2, AlertCircle, Info, X } from 'lucide-react'
import { errMessage } from '../lib/errMessage'

export type ToastKind = 'info' | 'success' | 'error'

type Toast = {
  readonly id: number
  readonly message: string
  readonly kind: ToastKind
}

// Store module-level (pub/sub) para que notify()/notifyError() também funcionem
// FORA de componentes React — vários alert() de erro moram em helpers puros
// (ResultCard etc.) que não podem usar hooks. O ToastProvider apenas se inscreve
// nesse store e renderiza a pilha; useToast() devolve as mesmas funções.
type Listener = (toasts: readonly Toast[]) => void

const AUTO_DISMISS_MS = 4000

let toasts: Toast[] = []
let nextId = 1
const listeners = new Set<Listener>()

function emit() {
  for (const l of listeners) l(toasts)
}

export function dismissToast(id: number): void {
  toasts = toasts.filter(t => t.id !== id)
  emit()
}

export function notify(message: string, kind: ToastKind = 'info'): number {
  const id = nextId++
  toasts = [...toasts, { id, message, kind }]
  emit()
  return id
}

export function notifyError(err: unknown): number {
  return notify(errMessage(err), 'error')
}

type ToastApi = { readonly notify: typeof notify; readonly notifyError: typeof notifyError }

const ToastContext = createContext<ToastApi>({ notify, notifyError })

const KIND_META: Record<ToastKind, { readonly icon: ReactNode; readonly border: string; readonly iconCls: string }> = {
  success: {
    icon: <CheckCircle2 className="w-4 h-4 flex-shrink-0" />,
    border: 'border-green-500/40',
    iconCls: 'text-green-600 dark:text-green-400',
  },
  error: {
    icon: <AlertCircle className="w-4 h-4 flex-shrink-0" />,
    border: 'border-red-500/40',
    iconCls: 'text-red-600 dark:text-red-400',
  },
  info: {
    icon: <Info className="w-4 h-4 flex-shrink-0" />,
    border: 'border-blue-500/40',
    iconCls: 'text-blue-600 dark:text-blue-400',
  },
}

function ToastItem({ toast }: { readonly toast: Toast }) {
  useEffect(() => {
    const h = setTimeout(() => dismissToast(toast.id), AUTO_DISMISS_MS)
    return () => clearTimeout(h)
  }, [toast.id])

  const meta = KIND_META[toast.kind]
  return (
    <div
      className={`pointer-events-auto flex items-start gap-2 rounded-lg border px-3 py-2.5 text-sm shadow-lg bg-surface-secondary text-text-primary ${meta.border}`}
    >
      <span className={meta.iconCls}>{meta.icon}</span>
      <span className="flex-1 min-w-0 break-words leading-snug">{toast.message}</span>
      <button
        onClick={() => dismissToast(toast.id)}
        aria-label="Fechar"
        className="flex-shrink-0 text-text-muted hover:text-text-primary transition-colors"
      >
        <X className="w-3.5 h-3.5" />
      </button>
    </div>
  )
}

function ToastViewport({ items }: { readonly items: readonly Toast[] }) {
  return (
    <div
      role="status"
      aria-live="polite"
      aria-atomic="false"
      className="fixed bottom-4 right-4 z-[100] flex flex-col gap-2 w-80 max-w-[calc(100vw-2rem)] pointer-events-none"
    >
      {items.map(t => <ToastItem key={t.id} toast={t} />)}
    </div>
  )
}

/**
 * Substitui o `alert()` nativo por toasts empilhados no canto, acessíveis
 * (role="status" + aria-live) e com auto-dismiss (~4s). Envolve a app uma vez;
 * notify()/notifyError() podem ser chamados via useToast() ou importados direto.
 */
export function ToastProvider({ children }: { readonly children: ReactNode }) {
  const [items, setItems] = useState<readonly Toast[]>(toasts)

  useEffect(() => {
    listeners.add(setItems)
    setItems(toasts)
    return () => { listeners.delete(setItems) }
  }, [])

  const api = useMemo<ToastApi>(() => ({ notify, notifyError }), [])

  return (
    <ToastContext.Provider value={api}>
      {children}
      <ToastViewport items={items} />
    </ToastContext.Provider>
  )
}

export function useToast(): ToastApi {
  return useContext(ToastContext)
}
