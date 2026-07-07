// Theme toggle — cycles through Light / System / Dark on each click.

import { Sun, Moon } from 'lucide-react'
import { useTranslation } from 'react-i18next'
import { useTheme, type ThemeChoice } from '../lib/theme'

type Variant = 'sidebar' | 'mobile'

const ORDER: ThemeChoice[] = ['light', 'system', 'dark']

function iconFor(choice: ThemeChoice, resolved: 'light' | 'dark') {
  if (choice === 'light')  return <Sun className="w-5 h-5" />
  if (choice === 'dark')   return <Moon className="w-5 h-5" />
  return resolved === 'dark' ? <Moon className="w-5 h-5" /> : <Sun className="w-5 h-5" />
}

export default function ThemeToggle({ variant = 'sidebar' }: { readonly variant?: Variant }) {
  const { t } = useTranslation()
  const { choice, resolved, setChoice, systemPrefersDark } = useTheme()
  const size = variant === 'mobile' ? 'w-10 h-10' : 'w-9 h-9'

  const cycle = () => {
    const idx = ORDER.indexOf(choice)
    setChoice(ORDER[(idx + 1) % ORDER.length])
  }

  const titleKey = `theme.${choice}.title` as const
  const activeKey = `theme.${choice}.active` as const
  const inactiveKey = `theme.${choice}.inactive` as const

  return (
    <button
      type="button"
      onClick={cycle}
      title={t(titleKey)}
      aria-label={t(`theme.${choice}.aria`)}
      className={`${size} rounded-lg flex items-center justify-center transition-colors text-text-secondary hover:text-text-primary hover:bg-surface-secondary`}
    >
      <span title={systemPrefersDark ? t(activeKey) : t(inactiveKey)}>
        {iconFor(choice, resolved)}
      </span>
    </button>
  )
}
