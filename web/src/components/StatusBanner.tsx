import type { ReactNode } from 'react'

export type StatusBannerVariant = 'error' | 'warning' | 'info' | 'success'

const VARIANT_CLASS: Record<StatusBannerVariant, string> = {
  error: 'bg-red-500/10 border-red-500/30 text-red-400',
  warning: 'bg-yellow-500/10 border-yellow-500/30 text-yellow-400',
  info: 'bg-blue-500/10 border-blue-500/30 text-blue-300',
  success: 'bg-emerald-500/10 border-emerald-500/30 text-emerald-700 dark:text-emerald-300',
}

type StatusBannerProps = {
  readonly variant?: StatusBannerVariant
  readonly title?: string
  readonly children: ReactNode
  readonly onDismiss?: () => void
  readonly dismissLabel?: string
  readonly className?: string
}

/** Banner inline para erros/avisos persistentes (UX-2.3). */
export function StatusBanner({
  variant = 'error',
  title,
  children,
  onDismiss,
  dismissLabel,
  className = '',
}: StatusBannerProps) {
  const live = variant === 'error' || variant === 'warning' ? 'assertive' : 'polite'
  return (
    <div
      role={variant === 'error' ? 'alert' : 'status'}
      aria-live={live}
      className={`rounded-xl border px-4 py-3 text-sm ${VARIANT_CLASS[variant]} ${className}`.trim()}
    >
      <div className="flex items-start justify-between gap-3">
        <div className="min-w-0 flex-1">
          {title && <p className="font-medium">{title}</p>}
          <div className={title ? 'text-sm mt-1 opacity-90' : ''}>{children}</div>
        </div>
        {onDismiss && (
          <button
            type="button"
            onClick={onDismiss}
            className="shrink-0 text-xs opacity-70 hover:opacity-100"
            aria-label={dismissLabel}
          >
            ×
          </button>
        )}
      </div>
    </div>
  )
}
