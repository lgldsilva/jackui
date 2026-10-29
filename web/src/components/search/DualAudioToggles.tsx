import { AudioLines } from 'lucide-react'
import { useTranslation } from 'react-i18next'

// Which dual-audio preference the user toggled. Keys double as the localStorage
// keys (`search.preferDual` / `search.dualOnly`) — keep them in sync.
export type DualAudioPrefKey = 'preferDual' | 'dualOnly'

type Props = {
  readonly preferDual: boolean
  readonly dualOnly: boolean
  readonly onToggle: (which: DualAudioPrefKey) => void
}

// Toggle button styled like the other search filter toggles (playable/HDR):
// inactive = neutral surface, pressed = tinted. `activeClass` differentiates the
// soft preference (green, brand accent) from the hard filter (amber, "results
// will be cut" — same tone as the hidden-by-filters banner).
function DualToggle({ active, activeClass, title, label, onClick }: {
  readonly active: boolean
  readonly activeClass: string
  readonly title: string
  readonly label: string
  readonly onClick: () => void
}) {
  return (
    <button
      onClick={onClick}
      title={title}
      aria-pressed={active}
      className={`flex items-center gap-1.5 text-sm px-3 py-1.5 rounded-lg transition-colors border ${
        active ? activeClass : 'bg-surface-tertiary hover:bg-surface-tertiary text-text-primary border-strong'
      }`}
    >
      <AudioLines className={`w-3.5 h-3.5 ${active ? 'fill-current' : ''}`} />
      {label}
    </button>
  )
}

/**
 * Dual-audio (PT-BR) preference toggles for the search page: "prefer dual"
 * re-ranks results within the same quality tier, "dual only" is a hard filter.
 * Both are global request prefs (not per-tab) — the page persists them and
 * re-triggers the current search when they change. Lives in its own file to
 * avoid bloating the SearchPage (god-file), like MusicSearchFilterToggle.
 */
export function DualAudioToggles({ preferDual, dualOnly, onToggle }: Props) {
  const { t } = useTranslation()
  return (
    <div className="flex flex-wrap items-center gap-2">
      <DualToggle
        active={preferDual}
        activeClass="bg-green-500/20 text-green-700 dark:text-green-300 border-green-500/30"
        title={t('search.preferDual_hint')}
        label={t('search.preferDual')}
        onClick={() => onToggle('preferDual')}
      />
      <DualToggle
        active={dualOnly}
        activeClass="bg-amber-500/20 text-amber-700 dark:text-amber-300 border-amber-500/30"
        title={t('search.dualOnly_hint')}
        label={t('search.dualOnly')}
        onClick={() => onToggle('dualOnly')}
      />
    </div>
  )
}

export default DualAudioToggles
