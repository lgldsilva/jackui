import type { ReactNode } from 'react'

interface EmptyStateProps {
  readonly icon: ReactNode
  readonly title: string
  readonly description: ReactNode
  readonly action?: ReactNode
}

export function EmptyState({ icon, title, description, action }: EmptyStateProps) {
  return (
    <div
      role="status"
      aria-live="polite"
      className="flex flex-col items-center justify-center py-16 text-center"
    >
      <div className="text-text-muted mb-3" aria-hidden>{icon}</div>
      <h3 className="text-lg font-semibold text-text-secondary mb-1">{title}</h3>
      <p className="text-sm text-text-muted max-w-md">{description}</p>
      {action && <div className="mt-4">{action}</div>}
    </div>
  )
}
