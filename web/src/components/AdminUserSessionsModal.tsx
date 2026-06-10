import { useEffect, useState, useCallback } from 'react'
import { useTranslation } from 'react-i18next'
import { Loader2, Trash2, LogOut } from 'lucide-react'
import { Sheet } from './Sheet'
import { adminListUserSessions, adminRevokeUserSession, adminRevokeUserSessions, AdminUser, SessionInfo } from '../api/client'

// AdminUserSessionsModal — an admin inspects the target user's active sessions
// (device + IP + lifetime) and revokes one or all of them.
export default function AdminUserSessionsModal({ user, onClose }: {
  readonly user: AdminUser | null
  readonly onClose: () => void
}) {
  const { t } = useTranslation()
  const [sessions, setSessions] = useState<SessionInfo[] | null>(null)
  const [err, setErr] = useState('')

  const load = useCallback(async () => {
    if (!user) return
    setErr('')
    try { setSessions(await adminListUserSessions(user.id)) }
    catch (e: any) { setErr(e?.response?.data?.error || t('admin.sessions_failed')) }
  }, [user, t])

  useEffect(() => { setSessions(null); load() }, [load])

  const revoke = async (sid: string) => {
    if (!user) return
    try { await adminRevokeUserSession(user.id, sid); await load() }
    catch (e: any) { setErr(e?.response?.data?.error || t('admin.sessions_failed')) }
  }
  const revokeAll = async () => {
    if (!user) return
    try { await adminRevokeUserSessions(user.id); await load() }
    catch (e: any) { setErr(e?.response?.data?.error || t('admin.sessions_failed')) }
  }

  if (!user) return null
  return (
    <Sheet open onClose={onClose} size="md" title={t('admin.sessions_title', { name: user.username })}>
      <div className="flex flex-col gap-2">
        {err && <p className="text-xs text-red-400">{err}</p>}
        {sessions === null && (
          <div className="flex justify-center py-8"><Loader2 className="w-6 h-6 animate-spin text-text-muted" /></div>
        )}
        {sessions !== null && sessions.length === 0 && (
          <p className="text-sm text-text-muted text-center py-4">{t('admin.sessions_none')}</p>
        )}
        {sessions?.map(sess => (
          <div key={sess.id} className="flex items-center gap-2 bg-surface border border-default rounded-lg px-3 py-2 text-sm">
            <div className="flex-1 min-w-0">
              <div className="text-text-primary text-xs truncate" title={sess.userAgent}>
                {sess.userAgent || '—'}
                {sess.ip && <span className="text-text-muted"> · {sess.ip}</span>}
                {sess.remember && <span className="text-text-muted"> · {t('account.session_remember')}</span>}
              </div>
              <div className="text-text-muted text-xs mt-0.5">
                {t('account.session_created')} {new Date(sess.createdAt).toLocaleString()} · {t('account.session_expires')} {new Date(sess.expiresAt).toLocaleDateString()}
              </div>
            </div>
            <button onClick={() => revoke(sess.id)} title={t('admin.sessions_revoke')}
              className="text-text-muted hover:text-red-400 flex-shrink-0">
              <Trash2 className="w-3.5 h-3.5" />
            </button>
          </div>
        ))}
        {sessions !== null && sessions.length > 0 && (
          <button onClick={revokeAll}
            className="flex items-center gap-1.5 text-sm bg-surface-tertiary hover:bg-surface-tertiary text-text-primary rounded-lg px-3 py-2 self-start mt-1">
            <LogOut className="w-4 h-4" /> {t('admin.sessions_revoke_all')}
          </button>
        )}
      </div>
    </Sheet>
  )
}
