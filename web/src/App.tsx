import { useEffect, useRef } from 'react'
import { Routes, Route, Navigate, useLocation, useNavigate } from 'react-router-dom'
import { Loader2 } from 'lucide-react'
import { load, save } from './lib/storage'
import { isIncognito } from './lib/incognito'
import SearchPage from './pages/SearchPage'
import SettingsPage from './pages/SettingsPage'
import HistoryPage from './pages/HistoryPage'
import FavoritesPage from './pages/FavoritesPage'
import PlaylistsPage from './pages/PlaylistsPage'
import PlaylistDetailPage from './pages/PlaylistDetailPage'
import LibraryPage from './pages/LibraryPage'
import DiscoverPage from './pages/DiscoverPage'
import WatchlistPage from './pages/WatchlistPage'
import LocalPage from './pages/LocalPage'
import DownloadsPage from './pages/DownloadsPage'
import StatsPage from './pages/StatsPage'
import LoginPage from './pages/LoginPage'
import { RegisterPage, VerifyEmailPage, ForgotPasswordPage, ResetPasswordPage } from './pages/AuthFlows'
import { AuthProvider, useAuth } from './auth/AuthContext'
import PlayerProvider from './components/PlayerProvider'
import { ConfirmProvider } from './components/ConfirmDialog'
import ErrorBoundary from './components/ErrorBoundary'
import { TransfersProvider } from './lib/transfers'
import TransfersDock from './components/TransfersDock'

function ProtectedRoute({ children }: { readonly children: React.ReactNode }) {
  const { isAuthenticated, loading } = useAuth()
  const location = useLocation()

  if (loading) {
    return (
      <div className="min-h-screen bg-surface flex items-center justify-center text-text-secondary">
        <Loader2 className="w-6 h-6 animate-spin" />
      </div>
    )
  }
  if (!isAuthenticated) {
    return <Navigate to="/login" state={{ from: location }} replace />
  }
  return <>{children}</>
}

// RouteRestorer remembers the last screen the user was on and returns there
// when the app is reopened — useful in the installed PWA, where iOS kills the
// webview and relaunches at the start_url ("/"). It persists the current route
// on every navigation and, once per session, redirects from the default "/"
// landing to the saved route. Skips /login (never restore into it) and only
// restores when authenticated and actually sitting on "/" (so a deep link or an
// intentional click on the logo isn't hijacked).
function RouteRestorer() {
  const location = useLocation()
  const navigate = useNavigate()
  const { isAuthenticated } = useAuth()
  const restoredRef = useRef(false)

  useEffect(() => {
    if (restoredRef.current || !isAuthenticated) return
    restoredRef.current = true
    // CRITICAL: never hijack navigation when a player deep-link is active
    // (?play=). Auth restore finishes ~1-2s AFTER the PlayerProvider opened the
    // player from the URL; navigating to lastRoute here would strip ?play=, the
    // provider would close() the player, and its unmount cleanup would
    // streamDrop the torrent mid-transcode (ffmpeg then reads a truncated source
    // → corrupt packets → SRC_NOT_SUPPORTED). Leave the deep-link alone.
    if (location.search.includes('play=')) return
    const last = load<string>('lastRoute', '')
    if (last && last !== '/' && location.pathname === '/') {
      navigate(last, { replace: true })
    }
  }, [isAuthenticated, location.pathname, location.search, navigate])

  useEffect(() => {
    // Don't persist /login, nor a player deep-link (?play=) — restoring straight
    // into a stale ?play= on next launch would auto-reopen an old video. Incognito
    // leaves no trace, so it doesn't record the last route either.
    if (location.pathname === '/login' || isIncognito()) return
    const search = location.search.includes('play=') ? '' : location.search
    save('lastRoute', location.pathname + search)
  }, [location.pathname, location.search])

  return null
}

function AppRoutes() {
  return (
    <Routes>
      <Route path="/login" element={<LoginPage />} />
      <Route path="/register" element={<RegisterPage />} />
      <Route path="/verify-email" element={<VerifyEmailPage />} />
      <Route path="/forgot-password" element={<ForgotPasswordPage />} />
      <Route path="/reset-password" element={<ResetPasswordPage />} />
      <Route path="/" element={<ProtectedRoute><SearchPage /></ProtectedRoute>} />
      <Route path="/settings" element={<ProtectedRoute><SettingsPage /></ProtectedRoute>} />
      <Route path="/history" element={<ProtectedRoute><HistoryPage /></ProtectedRoute>} />
      <Route path="/favorites" element={<ProtectedRoute><FavoritesPage /></ProtectedRoute>} />
      <Route path="/playlists" element={<ProtectedRoute><PlaylistsPage /></ProtectedRoute>} />
      <Route path="/playlists/:id" element={<ProtectedRoute><PlaylistDetailPage /></ProtectedRoute>} />
      <Route path="/library" element={<ProtectedRoute><LibraryPage /></ProtectedRoute>} />
      <Route path="/discover" element={<ProtectedRoute><DiscoverPage /></ProtectedRoute>} />
      <Route path="/watchlist" element={<ProtectedRoute><WatchlistPage /></ProtectedRoute>} />
      <Route path="/stats" element={<ProtectedRoute><StatsPage /></ProtectedRoute>} />
      <Route path="/local" element={<ProtectedRoute><LocalPage /></ProtectedRoute>} />
      <Route path="/downloads" element={<ProtectedRoute><DownloadsPage /></ProtectedRoute>} />
    </Routes>
  )
}

function App() {
  return (
    <AuthProvider>
      <ConfirmProvider>
        <TransfersProvider>
          <PlayerProvider>
            <div className="min-h-screen bg-surface">
              <RouteRestorer />
              {/* Global crash net: a render error in any page would otherwise blank
                  the whole app (white screen). Show a recoverable message; reset
                  does a hard reload to home so a wedged route always recovers. */}
              <ErrorBoundary title="Algo deu errado" onReset={() => { globalThis.location.href = '/' }}>
                <AppRoutes />
              </ErrorBoundary>
              {/* Global file-movement progress dock (bottom-left). */}
              <TransfersDock />
            </div>
          </PlayerProvider>
        </TransfersProvider>
      </ConfirmProvider>
    </AuthProvider>
  )
}

export default App
