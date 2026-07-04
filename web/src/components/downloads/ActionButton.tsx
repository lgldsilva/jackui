// ActionButton — botão de ação primitivo (tocar/pausar/remover/…) usado nos
// cards de download e torrent. Variantes de cor via `variant`.
export function ActionButton({ onClick, disabled, variant, icon, label, className = '', title }: {
  readonly onClick: () => void
  readonly disabled: boolean
  readonly variant: 'success' | 'danger' | 'neutral' | 'info'
  readonly icon: React.ReactNode
  readonly label: string
  readonly className?: string
  readonly title?: string
}) {
  const styles: Record<typeof variant, string> = {
    success: 'bg-emerald-500/10 hover:bg-emerald-500/20 text-emerald-700 dark:text-emerald-300 border-emerald-500/30',
    danger:  'bg-red-500/10 hover:bg-red-500/20 text-red-700 dark:text-red-300 border-red-500/30',
    neutral: 'bg-surface-tertiary/60 hover:bg-surface-tertiary text-text-primary border-strong/60',
    info:    'bg-blue-500/10 hover:bg-blue-500/20 text-blue-700 dark:text-blue-300 border-blue-500/30',
  }
  return (
    <button
      onClick={onClick}
      disabled={disabled}
      title={title}
      className={`
        flex items-center gap-1.5 text-xs border px-3 py-1.5 rounded-lg
        disabled:opacity-50 transition-all duration-200 font-medium
        ${styles[variant]} ${className}
      `}
    >
      {icon} {label}
    </button>
  )
}
