import { useState, useEffect } from 'react'
import { Link, useLocation } from 'react-router-dom'
import {
  Heart, History, Settings, ListMusic, Search, Library as LibraryIcon,
  Bell, HardDrive, Download, Menu, X, PanelLeftClose, PanelLeftOpen, Flame,
  Eye, EyeOff,
} from 'lucide-react'
import UserBadge from './UserBadge'
import RateWidget from './RateWidget'
import { useIncognito } from '../lib/incognito'

interface Props {
  /** Optional custom element (e.g., a page-specific "back" button). Rendered in
   *  the sidebar footer (desktop) and the mobile top bar. */
  rightExtra?: React.ReactNode
}

const STORAGE_KEY = 'jackui.sidebar.collapsed'

// Single source of truth for the nav routes. `hover` keeps the per-section
// accent colour the old header used.
const LINKS = [
  { to: '/', icon: Search, label: 'Buscar', hover: 'hover:!text-gray-100' },
  { to: '/discover', icon: Flame, label: 'Em alta', hover: 'hover:!text-orange-400' },
  { to: '/playlists', icon: ListMusic, label: 'Playlists', hover: 'hover:!text-blue-400' },
  { to: '/library', icon: LibraryIcon, label: 'Continuar', hover: 'hover:!text-purple-400' },
  { to: '/local', icon: HardDrive, label: 'Local', hover: 'hover:!text-cyan-400' },
  { to: '/downloads', icon: Download, label: 'Downloads', hover: 'hover:!text-green-400' },
  { to: '/watchlist', icon: Bell, label: 'Watch', hover: 'hover:!text-amber-400' },
  { to: '/favorites', icon: Heart, label: 'Favoritos', hover: 'hover:!text-pink-400' },
  { to: '/history', icon: History, label: 'Histórico', hover: 'hover:!text-gray-100' },
  { to: '/settings', icon: Settings, label: 'Settings', hover: 'hover:!text-gray-100' },
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
  const location = useLocation()

  useEffect(() => {
    localStorage.setItem(STORAGE_KEY, collapsed ? '1' : '0')
  }, [collapsed])

  // Desktop only: reserve space for the fixed sidebar by padding the body, so
  // each page's existing layout shifts right without per-page edits. Mobile
  // uses the drawer (overlay), so no padding there.
  useEffect(() => {
    const mq = window.matchMedia('(min-width: 768px)')
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
      : 'text-gray-400 hover:text-gray-100 hover:bg-gray-700/40'
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
      className={`fixed top-0 left-0 z-40 h-full bg-gray-800 border-r border-gray-700
        flex flex-col safe-top safe-left transition-transform md:transition-[width]
        w-60 ${railWidth} ${drawerTransform}`}
    >
      {/* Header: logo + collapse toggle (desktop) / close (mobile drawer) */}
      <div className="flex items-center justify-between px-3 h-14 flex-shrink-0 border-b border-gray-700/60">
        {/* Logo — hidden on the DESKTOP collapsed rail (no room beside the toggle);
            always shown on mobile (drawer) and on the expanded desktop rail. */}
        <Link to="/" className={`flex items-center gap-1 min-w-0 ${collapsed ? 'md:hidden' : ''}`} title="Início">
          <span className="text-xl font-bold text-green-500">Jack</span>
          <span className="text-xl font-bold text-gray-100">UI</span>
        </Link>
        {/* Desktop: collapse/expand. Centered (mx-auto) when collapsed so it
            doesn't overlap the (hidden) logo. Hidden on mobile (drawer closes via X). */}
        <button
          onClick={() => setCollapsed((c) => !c)}
          className={`hidden md:flex items-center justify-center w-9 h-9 rounded-lg text-gray-400 hover:text-gray-100 hover:bg-gray-700/40 transition-colors ${collapsed ? 'md:mx-auto' : ''}`}
          title={collapsed ? 'Expandir menu' : 'Retrair menu'}
        >
          {collapsed ? <PanelLeftOpen className="w-5 h-5" /> : <PanelLeftClose className="w-5 h-5" />}
        </button>
        {/* Mobile: close drawer */}
        <button
          onClick={() => setDrawerOpen(false)}
          className="md:hidden flex items-center justify-center w-11 h-11 rounded-lg text-gray-400 hover:text-gray-100"
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
            className={`flex items-center gap-3 rounded-lg px-3 py-2.5 min-h-[44px] text-sm transition-colors
              ${isActive(to)
                ? 'bg-gray-700 text-gray-100'
                : `text-gray-400 hover:bg-gray-700/40 ${hover}`}
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
      <div className={`flex-shrink-0 border-t border-gray-700/60 p-2 flex flex-col gap-2 safe-bottom overflow-hidden ${collapsed ? 'md:items-center' : ''}`}>
        <div className={`flex items-center gap-2 ${collapsed ? 'md:justify-center' : ''}`}>
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
      <header className="md:hidden bg-gray-800 border-b border-gray-700 sticky top-0 z-30 safe-top">
        <div className="flex items-center justify-between gap-2 px-3 h-12">
          <button
            onClick={() => setDrawerOpen(true)}
            className="flex items-center justify-center w-10 h-10 -ml-1 rounded-lg text-gray-300 hover:text-gray-100 hover:bg-gray-700/40"
            title="Abrir menu"
          >
            <Menu className="w-6 h-6" />
          </button>
          <Link to="/" className="flex items-center gap-1 flex-1 min-w-0" title="Início">
            <span className="text-xl font-bold text-green-500">Jack</span>
            <span className="text-xl font-bold text-gray-100">UI</span>
            {incognito && (
              <span className="ml-2 text-[9px] font-semibold tracking-wider text-amber-300/90 uppercase">
                Incógnito
              </span>
            )}
          </Link>
          <div className="flex items-center gap-1 flex-shrink-0">
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
