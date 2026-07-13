import { RefreshCw } from 'lucide-react'
import { useTranslation } from 'react-i18next'

type RetryPanelProps = {
  readonly onRetry: () => void
  readonly label?: string
  readonly className?: string
}

/** Botão de retry padronizado (UX-2). */
export function RetryPanel({ onRetry, label, className = '' }: RetryPanelProps) {
  const { t } = useTranslation()
  return (
    <button
      type="button"
      onClick={onRetry}
      className={`btn-primary flex items-center gap-2 ${className}`.trim()}
    >
      <RefreshCw className="w-4 h-4" aria-hidden />
      <span>{label ?? t('common.retry')}</span>
    </button>
  )
}
