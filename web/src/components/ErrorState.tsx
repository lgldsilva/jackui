import { useTranslation } from 'react-i18next'
import { AlertCircle } from 'lucide-react'
import { RetryPanel } from './RetryPanel'

interface ErrorStateProps {
  readonly message: string
  readonly onRetry?: () => void
}

export function ErrorState({ message, onRetry }: ErrorStateProps) {
  const { t } = useTranslation()

  return (
    <div
      role="alert"
      aria-live="assertive"
      className="flex flex-col items-center justify-center py-16 text-center"
    >
      <div className="text-text-muted mb-3" aria-hidden>
        <AlertCircle className="w-16 h-16 opacity-30" />
      </div>
      <h3 className="text-lg font-semibold text-text-secondary mb-1">
        {t('common.errorTitle')}
      </h3>
      <p className="text-sm text-text-muted max-w-md mb-4">{message}</p>
      {onRetry && <RetryPanel onRetry={onRetry} />}
    </div>
  )
}
