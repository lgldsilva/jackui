import { useTranslation } from 'react-i18next'
import { AlertCircle } from 'lucide-react'

/**
 * Persistent inline notice for a failing downloads refresh. The page keeps
 * retrying on its own, so this replaces a toast per poll cycle and disappears
 * on the first successful load.
 */
export function DownloadsLoadErrorBanner({ message }: { readonly message: string | null }) {
  const { t } = useTranslation()
  if (!message) return null
  return (
    <div
      role="alert"
      className="flex items-start gap-2 rounded-lg border border-red-500/40 bg-red-500/10 px-3 py-2.5 text-sm text-text-primary"
    >
      <AlertCircle className="w-4 h-4 mt-0.5 flex-shrink-0 text-red-600 dark:text-red-400" aria-hidden />
      <div className="min-w-0">
        <p className="font-medium">{t('downloads.page.loadErrorTitle')}</p>
        <p className="text-text-muted break-words">{message}</p>
      </div>
    </div>
  )
}
