import { useEffect, useRef, lazy, Suspense } from 'react'
import { Routes, Route, Navigate, useLocation, useNavigate } from 'react-router-dom'
import { Loader2 } from 'lucide-react'
import { load, save } from './lib/storage'
import { isIncognito } from './lib/incognito'
import { isStandalonePWA, shouldRestoreRoute } from './lib/routeRestore'
// Route-level code-splitting: each page loads on navigation, not up front.
const SearchPage = lazy(() => import('./pages/SearchPage'))
const SettingsPage = lazy(() => import('./pages/SettingsPage'))
const HistoryPage = lazy(() => import('./pages/HistoryPage'))
const FavoritesPage = lazy(() => import('./pages/FavoritesPage'))
const PlaylistsPage = lazy(() => import('./pages/PlaylistsPage'))
const PlaylistDetailPage = lazy(() => import('./pages/PlaylistDetailPage'))
const LibraryPage = lazy(() => import('./pages/LibraryPage'))
const DiscoverPage = lazy(() => import('./pages/DiscoverPage'))
const WatchlistPage = lazy(() => import('./pages/WatchlistPage'))
const LocalPage = lazy(() => import('./pages/LocalPage'))
const DownloadsPage = lazy(() => import('./pages/DownloadsPage'))
const StatsPage = lazy(() => import('./pages/StatsPage'))
const LoginPage = lazy(() => import('./pages/LoginPage'))
const RegisterPage = lazy(() => import('./pages/AuthFlows').then(m => ({ default: m.RegisterPage })))
const VerifyEmailPage = lazy(() => import('./pages/AuthFlows').then(m => ({ default: m.VerifyEmailPage })))
const ForgotPasswordPage = lazy(() => import('./pages/AuthFlows').then(m => ({ default: m.ForgotPasswordPage })))
const ResetPasswordPage = lazy(() => import('./pages/AuthFlows').then(m => ({ default: m.ResetPasswordPage })))
import { AuthProvider, useAuth } from './auth/AuthContext'
import PlayerProvider from './components/PlayerProvider'
import { ConfirmProvider } from './components/ConfirmDialog'
import { ToastProvider } from './components/Toast'
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
    // Restore ONLY in an installed PWA (OS relaunches at start_url "/"). On a
    // normal browser REFRESH the current URL is authoritative — redirecting to a
    // previously-opened page was the bug. (The deep-link ?play= guard is folded
    // into shouldRestoreRoute, keeping the comment's invariant: never hijack a
    // player deep-link, since that would streamDrop the torrent mid-transcode.)
    const last = load<string>('lastRoute', '')
    if (shouldRestoreRoute({
      standalone: isStandalonePWA(),
      authenticated: isAuthenticated,
      pathname: location.pathname,
      search: location.search,
      lastRoute: last,
    })) {
      navigate(last, { replace: true })
    }
  }, [isAuthenticated, location.pathname, location.search, navigate])

  useEffect(() => {
    // Don't persist /login, nor a player deep-link (?play=) — restoring straight
    // into a stale ?play= on next launch would auto-reopen an old video. Incognito
    // leaves no trace, so it doesn't record the last route either.
    if (location.pathname === '/login' || isIncognito()) return
    // Remove SÓ os params do deep-link do player (play/f/t) pra não reabrir um vídeo
    // velho no próximo launch — mas PRESERVA o resto (ex.: ?mount=&path= da aba Local),
    // pra o usuário voltar exatamente pra pasta onde estava. (Antes zerava a search
    // inteira quando havia play=, perdendo mount/path se uma música estava tocando.)
    const params = new URLSearchParams(location.search)
    params.delete('play'); params.delete('f'); params.delete('t')
    const qs = params.toString()
    save('lastRoute', location.pathname + (qs ? `?${qs}` : ''))
  }, [location.pathname, location.search])

  return null
}

function RouteFallback() {
  return (
    <div className="flex items-center justify-center min-h-screen">
      <Loader2 className="w-8 h-8 animate-spin text-[var(--muted)]" />
    </div>
  )
}

function AppRoutes() {
  return (
    <Suspense fallback={<RouteFallback />}>
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
    </Suspense>
  )
}

function App() {
  return (
    <AuthProvider>
      <ConfirmProvider>
        <ToastProvider>
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
        </ToastProvider>
      </ConfirmProvider>
    </AuthProvider>
  )
}

export default App
