import { useState } from 'react'
import { useTranslation } from 'react-i18next'
import { Loader2, Trash2, LogOut, Monitor } from 'lucide-react'
import { listSessions, revokeSession, revokeOtherSessions, SessionInfo } from '../../api/client'
import AccountCard from '../../components/AccountCard'
import UsersAdminCard from '../../components/UsersAdminCard'
import ErrorBoundary from '../../components/ErrorBoundary'
import { Sheet } from '../../components/Sheet'
import { getRefreshToken } from '../../auth/AuthContext'

// SessionsList — body of the sessions modal. Early-returns instead of nested
// ternaries (S3358), outside the parent component (S6478).
function SessionsList({ loading, sessions, onRevoke, onRevokeOthers }: {
  readonly loading: boolean
  readonly sessions: SessionInfo[]
  readonly onRevoke: (id: SessionInfo['id']) => void
  readonly onRevokeOthers: () => void
}) {
  const { t } = useTranslation()
  if (loading) {
    return <div className="flex justify-center py-8"><Loader2 className="w-6 h-6 animate-spin text-text-muted" /></div>
  }
  if (sessions.length === 0) {
    return <p className="text-sm text-text-muted text-center py-4">{t('account.sessions_none')}</p>
  }
  return (
    <div className="flex flex-col gap-1.5">
      {sessions.map(sess => (
        <div key={sess.id} className="flex items-center gap-2 bg-surface border border-default rounded-lg px-3 py-2 text-sm">
          <div className="flex-1 min-w-0">
            <div className="text-text-primary flex items-center gap-1.5 flex-wrap">
              {sess.current && <span className="text-green-400">● {t('account.session_current')}</span>}
              {!sess.current && <span className="text-text-muted">○</span>}
              {sess.remember && <span className="text-text-muted text-xs">· {t('account.session_remember')}</span>}
              {(sess.userAgent || sess.ip) && (
                <span className="text-text-muted text-xs truncate" title={sess.userAgent}>
                  · {[sess.userAgent, sess.ip].filter(Boolean).join(' · ')}
                </span>
              )}
            </div>
            <div className="text-text-muted text-xs mt-0.5">
              {t('account.session_created')} {new Date(sess.createdAt).toLocaleString()} · {t('account.session_expires')} {new Date(sess.expiresAt).toLocaleDateString()}
            </div>
          </div>
          {!sess.current && (
            <button onClick={() => onRevoke(sess.id)}
              title={t('account.session_revoke')} className="text-text-muted hover:text-red-400 flex-shrink-0">
              <Trash2 className="w-3.5 h-3.5" />
            </button>
          )}
        </div>
      ))}
      {sessions.some(s => !s.current) && (
        <button onClick={onRevokeOthers}
          className="flex items-center gap-1.5 text-sm bg-surface-tertiary hover:bg-surface-tertiary text-text-primary rounded-lg px-3 py-2 self-start mt-2">
          <LogOut className="w-4 h-4" /> {t('account.sessions_revoke_others')}
        </button>
      )}
    </div>
  )
}

// AccountTab — the whole "Conta" tab of Settings, extracted from SettingsPage:
// self-service AccountCard (every user), admin user management, and the
// active-sessions modal. Needs no /api/config access, so it renders fine for
// non-admins (getConfig 403s for them).
export default function AccountTab() {
  const { t } = useTranslation()
  const [sessionModalOpen, setSessionModalOpen] = useState(false)
  const [sessions, setSessions] = useState<SessionInfo[]>([])
  const [sessionsLoading, setSessionsLoading] = useState(false)

  const loadSessions = async () => {
    setSessionsLoading(true)
    try { setSessions(await listSessions(getRefreshToken())) } catch { /* ignore */ }
    setSessionsLoading(false)
  }

  return (
    <>
      <ErrorBoundary title="Erro no card Conta"><AccountCard /></ErrorBoundary>
      <ErrorBoundary title="Erro no card Usuários"><UsersAdminCard /></ErrorBoundary>

      {/* Sessions modal trigger */}
      <section className="card">
        <button
          onClick={() => { loadSessions(); setSessionModalOpen(true) }}
          className="flex items-center gap-2 text-sm text-text-primary hover:text-text-primary w-full"
        >
          <Monitor className="w-4 h-4 text-text-muted" />
          {t('account.sessions_view')}
          <span className="text-xs text-text-muted">→</span>
        </button>
      </section>

      <Sheet open={sessionModalOpen} onClose={() => setSessionModalOpen(false)} title={t('account.sessions_title')} size="sm">
        <SessionsList
          loading={sessionsLoading}
          sessions={sessions}
          onRevoke={async (id) => { await revokeSession(id); loadSessions() }}
          onRevokeOthers={async () => { await revokeOtherSessions(getRefreshToken()); loadSessions() }}
        />
      </Sheet>
    </>
  )
}
