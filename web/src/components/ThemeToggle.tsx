// Theme toggle — cycles through Light / System / Dark on each click. Mirrors
// the incognito toggle pattern in NavHeader: a 9x9 button that fits both the
// desktop sidebar footer and the mobile top bar.
//
// The `variant` prop matches the incognito toggle's: 'sidebar' on the desktop
// rail (smaller), 'mobile' on the top bar.

import { Sun, Moon } from 'lucide-react'
import { useTheme, type ThemeChoice } from '../lib/theme'

type Variant = 'sidebar' | 'mobile'

const ORDER: ThemeChoice[] = ['light', 'system', 'dark']

const LABELS: Record<ThemeChoice, { title: string; active: string; inactive: string; aria: string }> = {
  light:  {
    title: 'Tema: claro (fixo)',
    active: 'Tema claro ATIVO — clique para seguir o sistema',
    inactive: 'Mudar para tema claro',
    aria: 'Tema claro',
  },
  system: {
    title: 'Tema: segue o sistema operacional',
    active: 'Seguindo o sistema operacional — clique para escuro',
    inactive: 'Seguir o sistema operacional',
    aria: 'Tema do sistema',
  },
  dark:   {
    title: 'Tema: escuro (fixo)',
    active: 'Tema escuro ATIVO — clique para claro',
    inactive: 'Mudar para tema escuro',
    aria: 'Tema escuro',
  },
}

function iconFor(choice: ThemeChoice, resolved: 'light' | 'dark') {
  if (choice === 'light')  return <Sun className="w-5 h-5" />
  if (choice === 'dark')   return <Moon className="w-5 h-5" />
  // 'system' — show what the OS would pick, so the icon previews the resolved theme.
  return resolved === 'dark' ? <Moon className="w-5 h-5" /> : <Sun className="w-5 h-5" />
}

export default function ThemeToggle({ variant = 'sidebar' }: { readonly variant?: Variant }) {
  const { choice, resolved, setChoice, systemPrefersDark } = useTheme()
  const size = variant === 'mobile' ? 'w-10 h-10' : 'w-9 h-9'
  const labels = LABELS[choice]
  const title = choice === 'system'
    ? `${labels.title} (SO prefere ${systemPrefersDark ? 'escuro' : 'claro'})`
    : labels.title

  const onClick = () => {
    const idx = ORDER.indexOf(choice)
    setChoice(ORDER[(idx + 1) % ORDER.length])
  }

  // Highlighted state when the user's choice overrides the OS preference.
  // 'system' mode is "neutral" — the icon shows the resolved theme but the button
  // doesn't claim a fixed state.
  const isFixed = choice !== 'system'
  const activeCls = isFixed
    ? 'text-green-700 dark:text-green-300 bg-green-500/10 ring-1 ring-green-400/40 hover:bg-green-500/20'
    : 'text-text-secondary hover:text-text-primary hover:bg-surface-tertiary/40'

  return (
    <button
      type="button"
      onClick={onClick}
      className={`flex items-center justify-center rounded-lg transition-colors ${size} ${activeCls}`}
      title={title}
      aria-label={labels.aria}
      aria-pressed={isFixed}
    >
      {iconFor(choice, resolved)}
    </button>
  )
}
