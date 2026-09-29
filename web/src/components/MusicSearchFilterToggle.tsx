import { Music2 } from 'lucide-react'
import { useTranslation } from 'react-i18next'

type Props = {
  readonly active: boolean    // Music mode on (otherwise renders nothing)
  readonly stacked: boolean   // full-width layout (mobile filter sheet)
  readonly showAll: boolean   // escape: showing ALL results
  readonly onToggle: () => void
}

/**
 * "Music only" toggle on the search filter bar. Shows only when Music
 * mode is active; pressed (purple) = filtering audio only, clicking shows ALL
 * results without leaving music mode. Lives in its own file to avoid bloating
 * the SearchPage (god-file) or the filterFields function (which renders this
 * button as a sibling of the other filters, via the call-sites).
 */
export function MusicSearchFilterToggle({ active, stacked, showAll, onToggle }: Props) {
  const { t } = useTranslation()
  if (!active) return null
  return (
    <button
      onClick={onToggle}
      title={t('search.music_only_hint')}
      aria-pressed={!showAll}
      className={`flex items-center gap-1.5 text-sm px-3 py-1.5 rounded-lg transition-colors border ${stacked ? 'w-full justify-center' : ''} ${
        showAll
          ? 'bg-surface-tertiary hover:bg-surface-tertiary text-text-primary border-strong'
          : 'bg-purple-500/20 text-purple-700 dark:text-purple-300 border-purple-500/30'
      }`}
    >
      <Music2 className={`w-3.5 h-3.5 ${showAll ? '' : 'fill-current'}`} />
      {showAll ? t('search.music_show_all') : t('search.music_only')}
    </button>
  )
}
