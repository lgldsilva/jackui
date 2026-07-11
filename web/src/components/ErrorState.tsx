import { useTranslation } from 'react-i18next'
import { AlertCircle, RefreshCw } from 'lucide-react'

interface ErrorStateProps {
  readonly message: string
  readonly onRetry?: () => void
}

export function ErrorState({ message, onRetry }: ErrorStateProps) {
  const { t } = useTranslation()

  return (
    <div className="flex flex-col items-center justify-center py-16 text-center">
      <div className="text-text-muted mb-3">
        <AlertCircle className="w-16 h-16 opacity-30" />
      </div>
      <h3 className="text-lg font-semibold text-text-secondary mb-1">
        {t('common.errorTitle')}
      </h3>
      <p className="text-sm text-text-muted max-w-md mb-4">{message}</p>
      {onRetry && (
        <button
          onClick={onRetry}
          className="btn-primary flex items-center gap-2"
        >
          <RefreshCw className="w-4 h-4" />
          <span>{t('common.retry')}</span>
        </button>
      )}
    </div>
  )
}
