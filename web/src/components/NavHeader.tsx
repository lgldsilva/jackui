import { useState, useEffect, useRef } from 'react'
import { Link, useLocation } from 'react-router-dom'
import {
  Heart, History, Settings, ListMusic, Search, Library as LibraryIcon,
  Bell, HardDrive, Download, Menu, X, PanelLeftClose, PanelLeftOpen, Flame,
  Eye, EyeOff,
} from 'lucide-react'
import UserBadge from './UserBadge'
import RateWidget from './RateWidget'
import ThemeToggle from './ThemeToggle'
import { useIncognito } from '../lib/incognito'
import { useRevealHidden, isRevealHidden } from '../lib/reveal'
import { useSwipe } from '../lib/useSwipe'

type Props = {
  readonly rightExtra?: React.ReactNode
}

const STORAGE_KEY = 'jackui.sidebar.collapsed'

// Single source of truth for the nav routes. `hover` keeps the per-section
// accent colour the old header used.
const LINKS = [
  { to: '/', icon: Search, label: 'Buscar', hover: 'hover:!text-text-primary' },
  { to: '/discover', icon: Flame, label: 'Em alta', hover: 'hover:!text-orange-400' },
  { to: '/playlists', icon: ListMusic, label: 'Playlists', hover: 'hover:!text-blue-400' },
  { to: '/library', icon: LibraryIcon, label: 'Continuar', hover: 'hover:!text-purple-400' },
  { to: '/local', icon: HardDrive, label: 'Local', hover: 'hover:!text-cyan-400' },
  { to: '/downloads', icon: Download, label: 'Downloads', hover: 'hover:!text-green-400' },
  { to: '/watchlist', icon: Bell, label: 'Watch', hover: 'hover:!text-amber-400' },
  { to: '/favorites', icon: Heart, label: 'Favoritos', hover: 'hover:!text-pink-400' },
  { to: '/history', icon: History, label: 'Histórico', hover: 'hover:!text-text-primary' },
  { to: '/settings', icon: Settings, label: 'Settings', hover: 'hover:!text-text-primary' },
]

/**
 * Shared navigation. Renders as a collapsible LEFT sidebar on desktop and a
 * slide-in drawer (behind a hamburger) on mobile. Kept named NavHeader so all
 * 9 pages keep importing/rendering it unchanged — instead of refactoring every
 * page's layout, we push page content right via a body padding-left equal to
 * the sidebar width (desktop only). The collapsed state persists in localStorage.
 *
 * Why a sidebar: the old horizontal top bar ran out of room on narrow phones
 * and the per-section accents were cramped. A vertical rail scales to more
 * destinations and gives each page the full viewport height.
 */
export default function NavHeader({ rightExtra }: Props) {
  const [collapsed, setCollapsed] = useState(() => localStorage.getItem(STORAGE_KEY) === '1')
  const [drawerOpen, setDrawerOpen] = useState(false)
  const [incognito, setIncognito] = useIncognito()
  const [revealed, setRevealed] = useRevealHidden()
  const location = useLocation()

  // Easter egg: 7 taps on the JackUI logo (within 1.5s) flip the global "hidden
  // curtain" for this session — revealing hidden favourites/Continue Watching/
  // downloads/local everywhere. Works on mobile and desktop (the logo shows on
  // both). Each tap still navigates home, which is harmless.
  const tapCount = useRef(0)
  const tapTimer = useRef<ReturnType<typeof setTimeout> | undefined>(undefined)
  const onLogoTap = () => {
    setDrawerOpen(false)
    tapCount.current += 1
    if (tapTimer.current) clearTimeout(tapTimer.current)
    tapTimer.current = setTimeout(() => { tapCount.current = 0 }, 1500)
    if (tapCount.current >= 7) {
      tapCount.current = 0
      setRevealed(!isRevealHidden())
    }
  }

  useEffect(() => {
    localStorage.setItem(STORAGE_KEY, collapsed ? '1' : '0')
  }, [collapsed])

  // Desktop only: reserve space for the fixed sidebar by padding the body, so
  // each page's existing layout shifts right without per-page edits. Mobile
  // uses the drawer (overlay), so no padding there.
  useEffect(() => {
    const mq = globalThis.matchMedia('(min-width: 768px)')
    const apply = () => {
      let padding = ''
      if (mq.matches) {
        padding = collapsed ? '4rem' : '15rem'
      }
      document.body.style.paddingLeft = padding
    }
    apply()
    mq.addEventListener?.('change', apply)
    return () => {
      mq.removeEventListener?.('change', apply)
      document.body.style.paddingLeft = ''
    }
  }, [collapsed])

  // Close the mobile drawer whenever the route changes (link tapped).
  useEffect(() => {
    setDrawerOpen(false)
  }, [location.pathname])

  // Edge-swipe from the left opens the mobile drawer (iOS/Android idiom). No-op
  // on desktop where the rail is always visible; disabled while already open so
  // it doesn't fight the backdrop tap-to-close.
  useSwipe('document', { onRight: () => setDrawerOpen(true) }, { edge: 'left', enabled: !drawerOpen })

  const isActive = (to: string) =>
    to === '/' ? location.pathname === '/' : location.pathname.startsWith(to)

  // The sidebar panel — shared by the desktop rail and the mobile drawer. On
  // mobile it's always expanded (labels visible); `collapsed` only applies to
  // the desktop rail.
  const railWidth = collapsed ? 'md:w-16' : 'md:w-60'
  const drawerTransform = drawerOpen ? 'translate-x-0' : '-translate-x-full md:translate-x-0'

  const incognitoToggle = (variant: 'sidebar' | 'mobile') => {
    const base = 'flex items-center justify-center rounded-lg transition-colors'
    const cls = incognito
      ? 'text-amber-300 bg-amber-500/10 ring-1 ring-amber-400/40 hover:bg-amber-500/20'
      : 'text-text-secondary hover:text-text-primary hover:bg-surface-tertiary/40'
    const size = variant === 'mobile' ? 'w-10 h-10' : 'w-9 h-9'
    return (
      <button
        type="button"
        onClick={() => setIncognito(!incognito)}
        className={`${base} ${size} ${cls}`}
        title={incognito ? 'Modo incógnito ATIVO — clique para sair' : 'Ativar modo incógnito (não grava histórico nem biblioteca)'}
        aria-pressed={incognito}
        aria-label="Modo incógnito"
      >
        {incognito ? <EyeOff className="w-5 h-5" /> : <Eye className="w-5 h-5" />}
      </button>
    )
  }

  const panel = (
    <aside
      className={`fixed top-0 left-0 z-40 h-full bg-surface-secondary border-r border-default
        flex flex-col safe-top safe-left transition-transform md:transition-[width]
        w-60 ${railWidth} ${drawerTransform}`}
    >
      {/* Header: logo + collapse toggle (desktop) / close (mobile drawer) */}
      <div className="flex items-center justify-between px-3 h-14 flex-shrink-0 border-b border-default/60">
        {/* Logo — hidden on the DESKTOP collapsed rail (no room beside the toggle);
            always shown on mobile (drawer) and on the expanded desktop rail. */}
        <Link to="/" onClick={onLogoTap} className={`flex items-center gap-1 min-w-0 ${collapsed ? 'md:hidden' : ''}`} title="Início">
          <span className="text-xl font-bold text-green-500">Jack</span>
          <span className="text-xl font-bold text-text-primary">UI</span>
          {revealed && <Eye className="w-3.5 h-3.5 text-amber-400 ml-1 flex-shrink-0" aria-label="ocultos visíveis" />}
        </Link>
        {/* Desktop: collapse/expand. Centered (mx-auto) when collapsed so it
            doesn't overlap the (hidden) logo. Hidden on mobile (drawer closes via X). */}
        <button
          onClick={() => setCollapsed((c) => !c)}
          className={`hidden md:flex items-center justify-center w-9 h-9 rounded-lg text-text-secondary hover:text-text-primary hover:bg-surface-tertiary/40 transition-colors ${collapsed ? 'md:mx-auto' : ''}`}
          title={collapsed ? 'Expandir menu' : 'Retrair menu'}
        >
          {collapsed ? <PanelLeftOpen className="w-5 h-5" /> : <PanelLeftClose className="w-5 h-5" />}
        </button>
        {/* Mobile: close drawer */}
        <button
          onClick={() => setDrawerOpen(false)}
          className="md:hidden flex items-center justify-center w-11 h-11 rounded-lg text-text-secondary hover:text-text-primary"
          title="Fechar menu"
        >
          <X className="w-5 h-5" />
        </button>
      </div>

      {/* Links */}
      <nav className="flex-1 overflow-y-auto py-2 px-2 flex flex-col gap-0.5">
        {LINKS.map(({ to, icon: Icon, label, hover }) => (
          <Link
            key={to}
            to={to}
            title={label}
            onClick={() => setDrawerOpen(false)}
            className={`flex items-center gap-3 rounded-lg px-3 py-2.5 min-h-[44px] text-sm transition-colors
              ${isActive(to)
                ? 'bg-surface-tertiary text-text-primary'
                : `text-text-secondary hover:bg-surface-tertiary/40 ${hover}`}
              ${collapsed ? 'md:justify-center md:px-2' : ''}`}
          >
            <Icon className="w-5 h-5 flex-shrink-0" />
            <span className={collapsed ? 'md:hidden' : ''}>{label}</span>
          </Link>
        ))}
      </nav>

      {/* Footer: rate widget, page extra, user. On the DESKTOP collapsed rail
          (~64px) there's no room for the rate widget / page extra — hide them
          there (they're back on expand); keep the user badge, which shrinks to
          its icon. md:hidden only affects desktop, so the mobile drawer (always
          expanded) still shows everything. */}
      {/* SEM overflow-hidden: o dropdown do UserBadge abre pra cima (bottom-full)
          e era clipado por aqui; os toggles, quando colapsados, empilham na
          vertical (md:flex-col abaixo) em vez de vazar a rail estreita. */}
      <div className={`flex-shrink-0 border-t border-default/60 p-2 flex flex-col gap-2 safe-bottom ${collapsed ? 'md:items-center' : ''}`}>
        {/* Tema + incógnito: só na sidebar do DESKTOP. No mobile a barra de topo
            já tem esses toggles, então escondemos a linha aqui (hidden) p/ não
            duplicar no drawer; md:flex traz de volta no desktop, que não tem topo.
            Colapsado: empilha vertical (md:flex-col) p/ caber na rail de 64px. */}
        <div className={`hidden md:flex items-center gap-2 ${collapsed ? 'md:flex-col md:justify-center' : ''}`}>
          <ThemeToggle variant="sidebar" />
          {incognitoToggle('sidebar')}
          {incognito && !collapsed && (
            <span className="text-[10px] font-semibold tracking-wider text-amber-300/90 uppercase">
              Incógnito
            </span>
          )}
        </div>
        <div className={collapsed ? 'md:hidden' : ''}><RateWidget /></div>
        {rightExtra && <div className={collapsed ? 'md:hidden' : ''}>{rightExtra}</div>}
        <UserBadge />
      </div>
    </aside>
  )

  return (
    <>
      {/* Mobile top bar — hamburger + logo. Hidden on desktop (sidebar is fixed).
          safe-top (status-bar inset) lives on the <header> as PADDING; the inner
          row owns the 48px content height. Putting both on one element made the
          inset eat into the fixed height (border-box) and squashed the row. */}
      <header className="md:hidden bg-surface-secondary border-b border-default sticky top-0 z-30 safe-top">
        <div className="flex items-center justify-between gap-2 px-3 h-12">
          <button
            onClick={() => setDrawerOpen(true)}
            className="flex items-center justify-center w-10 h-10 -ml-1 rounded-lg text-text-secondary hover:text-text-primary hover:bg-surface-tertiary/40"
            title="Abrir menu"
          >
            <Menu className="w-6 h-6" />
          </button>
          <Link to="/" onClick={onLogoTap} className="flex items-center gap-1 flex-1 min-w-0" title="Início">
            <span className="text-xl font-bold text-green-500">Jack</span>
            <span className="text-xl font-bold text-text-primary">UI</span>
            {revealed && <Eye className="w-3.5 h-3.5 text-amber-400 ml-1 flex-shrink-0" aria-label="ocultos visíveis" />}
            {incognito && (
              <span className="ml-2 text-[9px] font-semibold tracking-wider text-amber-300/90 uppercase">
                Incógnito
              </span>
            )}
          </Link>
          <div className="flex items-center gap-1 flex-shrink-0">
            <ThemeToggle variant="mobile" />
            {incognitoToggle('mobile')}
            {rightExtra}
          </div>
        </div>
      </header>

      {/* Mobile drawer backdrop */}
      {drawerOpen && (
        <div
          className="md:hidden fixed inset-0 z-30 bg-black/60"
          onClick={() => setDrawerOpen(false)}
          aria-hidden
        />
      )}

      {panel}
    </>
  )
}
