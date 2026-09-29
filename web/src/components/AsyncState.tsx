import type { ReactNode } from 'react'
import { EmptyState } from './EmptyState'
import { ErrorState } from './ErrorState'
import { LoadingState } from './LoadingState'

export type AsyncEmptyConfig = {
  readonly icon: ReactNode
  readonly title: string
  readonly description: ReactNode
  readonly action?: ReactNode
}

type AsyncStateProps = {
  readonly loading?: boolean
  readonly error?: string | null
  readonly empty?: boolean
  readonly loadingLabel?: string
  readonly emptyConfig?: AsyncEmptyConfig
  readonly onRetry?: () => void
  readonly children: ReactNode
}

/**
 * Single async-state model (UX-2.1): loading → error → empty → content.
 * Recoverable errors show retry; empty distinguishes a successful list with no items.
 */
export function AsyncState({
  loading = false,
  error = null,
  empty = false,
  loadingLabel,
  emptyConfig,
  onRetry,
  children,
}: AsyncStateProps) {
  if (error) return <ErrorState message={error} onRetry={onRetry} />
  if (loading) return <LoadingState label={loadingLabel} />
  if (empty && emptyConfig) {
    return (
      <EmptyState
        icon={emptyConfig.icon}
        title={emptyConfig.title}
        description={emptyConfig.description}
        action={emptyConfig.action}
      />
    )
  }
  if (empty) {
    return null
  }
  return <>{children}</>
}
