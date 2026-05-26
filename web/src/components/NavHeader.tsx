import { Link } from 'react-router-dom'
import { Heart, History, Settings, ListMusic, Search, Library as LibraryIcon, Bell, HardDrive, Download } from 'lucide-react'
import UserBadge from './UserBadge'
import RateWidget from './RateWidget'

interface Props {
  /** Optional custom right-side element (e.g., admin toggle, page-specific buttons). */
  rightExtra?: React.ReactNode
}

/**
 * Shared top navigation. Single source of truth for routes shown in the header.
 * Every page should render this — guarantees consistent nav across the app,
 * Apple-HIG touch targets, and proper safe-area handling for iOS PWA.
 *
 * Why this exists: when each page had its own header, navigating between pages
 * progressively hid links (each page omitted its own link, leading to "menu shrinks"
 * UX as the user moved deeper).
 */
export default function NavHeader({ rightExtra }: Props) {
  return (
    <header className="bg-gray-800 border-b border-gray-700 px-4 py-2 safe-top sticky top-0 z-30">
      <div className="max-w-7xl 2xl:max-w-[min(95vw,1600px)] mx-auto flex items-center justify-between gap-2">
        <Link to="/" className="flex items-center gap-1 flex-shrink-0" title="Início">
          <span className="text-2xl font-bold text-green-500">Jack</span>
          <span className="text-2xl font-bold text-gray-100 hidden xs:inline">UI</span>
        </Link>

        {/*
         * The scroll-clipping nav holds only the route Links. Anything that opens
         * a floating dropdown (UserBadge, RateWidget, rightExtra) lives OUTSIDE
         * the overflow context — otherwise iOS Safari (and Chrome with
         * overflow-x: auto) clips the menu vertically, hiding the "Sair" option.
         */}
        <nav className="flex items-center gap-0.5 sm:gap-1 overflow-x-auto min-w-0 flex-1">
          <Link to="/" className="header-link" title="Busca">
            <Search className="w-4 h-4" />
            <span className="hidden md:inline">Buscar</span>
          </Link>
          <Link to="/playlists" className="header-link hover:!text-blue-400" title="Playlists">
            <ListMusic className="w-4 h-4" />
            <span className="hidden md:inline">Playlists</span>
          </Link>
          <Link to="/library" className="header-link hover:!text-purple-400" title="Continue assistindo">
            <LibraryIcon className="w-4 h-4" />
            <span className="hidden md:inline">Continuar</span>
          </Link>
          <Link to="/local" className="header-link hover:!text-cyan-400" title="Arquivos locais (mounts)">
            <HardDrive className="w-4 h-4" />
            <span className="hidden md:inline">Local</span>
          </Link>
          <Link to="/downloads" className="header-link hover:!text-green-400" title="Downloads em background">
            <Download className="w-4 h-4" />
            <span className="hidden md:inline">Downloads</span>
          </Link>
          <Link to="/watchlist" className="header-link hover:!text-amber-400" title="Watchlists (push)">
            <Bell className="w-4 h-4" />
            <span className="hidden md:inline">Watch</span>
          </Link>
          <Link to="/favorites" className="header-link hover:!text-pink-400" title="Favoritos">
            <Heart className="w-4 h-4" />
            <span className="hidden md:inline">Favoritos</span>
          </Link>
          <Link to="/history" className="header-link" title="Histórico">
            <History className="w-4 h-4" />
            <span className="hidden md:inline">Histórico</span>
          </Link>
          <Link to="/settings" className="header-link" title="Configurações">
            <Settings className="w-4 h-4" />
            <span className="hidden lg:inline">Settings</span>
          </Link>
        </nav>

        {/* Floating-dropdown zone — kept outside the scrolling nav so menus aren't clipped */}
        <div className="flex items-center gap-1 flex-shrink-0">
          <RateWidget />
          {rightExtra}
          <UserBadge />
        </div>
      </div>
    </header>
  )
}
