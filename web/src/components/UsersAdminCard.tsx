import { useEffect, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { Loader2, Users, Check, Ban, Trash2, UserPlus, RotateCcw, Link2, Copy, KeyRound, MonitorSmartphone } from 'lucide-react'
import { adminListUsers, adminCreateUser, adminDeleteUser, adminSetUserStatus, adminInvite, AdminUser } from '../api/client'
import { useAuth } from '../auth/AuthContext'
import AdminResetPasswordModal from './AdminResetPasswordModal'
import AdminUserSessionsModal from './AdminUserSessionsModal'
import { useConfirm } from './ConfirmDialog'

// UsersAdminCard — admin-only user management: see everyone (with status/email),
// approve pending accounts, disable/re-enable, delete, and create users.
export default function UsersAdminCard() {
  const { t } = useTranslation()
  const confirm = useConfirm()
  const { isAdmin, user } = useAuth()
  const [users, setUsers] = useState<AdminUser[] | null>(null)
  const [err, setErr] = useState('')
  const [creating, setCreating] = useState({ username: '', password: '', role: 'user' as 'user' | 'admin' })
  const [inviteEmail, setInviteEmail] = useState('')
  const [inviteLink, setInviteLink] = useState('')
  const [copied, setCopied] = useState(false)
  const [resetUser, setResetUser] = useState<AdminUser | null>(null)
  const [sessionsUser, setSessionsUser] = useState<AdminUser | null>(null)

  const load = () => {
    adminListUsers().then(setUsers).catch(e => setErr(e?.response?.data?.error || t('admin.list_failed')))
  }
  useEffect(() => { if (isAdmin) load() }, [isAdmin])

  if (!isAdmin) return null

  const act = async (fn: () => Promise<void>) => {
    setErr('')
    try { await fn(); load() } catch (e: any) { setErr(e?.response?.data?.error || t('admin.op_failed')) }
  }

  const deleteUser = async (u: AdminUser) => {
    const ok = await confirm({ title: t('admin.delete_title'), message: t('admin.delete_message', { name: u.username }), confirmLabel: t('admin.delete'), destructive: true })
    if (!ok) return
    act(() => adminDeleteUser(u.id))
  }

  const statusChip = (s: AdminUser['status']) => {
    const map = { active: 'bg-green-500/15 text-green-400', pending: 'bg-amber-500/15 text-amber-400', disabled: 'bg-surface-tertiary text-text-secondary' }
    const label = { active: t('admin.status_active'), pending: t('admin.status_pending'), disabled: t('admin.status_disabled') }
    return <span className={`text-[10px] px-1.5 py-0.5 rounded ${map[s]}`}>{label[s]}</span>
  }

  return (
    <section className="card flex flex-col gap-3">
      <h2 className="text-lg font-semibold text-text-primary flex items-center gap-2"><Users className="w-5 h-5" /> {t('admin.title')}</h2>
      {err && <p className="text-xs text-red-400">{err}</p>}

      {users === null ? (
        <div className="flex items-center gap-2 text-text-secondary text-sm"><Loader2 className="w-4 h-4 animate-spin" /> {t('admin.loading')}</div>
      ) : (
        <div className="flex flex-col divide-y divide-default">
          {users.map(u => (
            <div key={u.id} className="flex items-center gap-2 py-2 text-sm flex-wrap">
              <div className="flex-1 min-w-0">
                <p className="text-text-primary truncate">
                  {u.username}
                  <span className="text-text-muted"> · {u.role === 'admin' ? 'admin' : 'user'}</span>
                  {u.email && <span className="text-text-muted"> · {u.email}{u.emailVerified ? '' : t('admin.email_unverified')}</span>}
                </p>
              </div>
              {statusChip(u.status)}
              <div className="flex items-center gap-1 flex-shrink-0">
                <button onClick={() => setResetUser(u)} title={t('admin.reset_password')}
                  className="p-1.5 rounded text-text-muted hover:text-amber-400 hover:bg-amber-500/10"><KeyRound className="w-4 h-4" /></button>
                <button onClick={() => setSessionsUser(u)} title={t('admin.user_sessions')}
                  className="p-1.5 rounded text-text-muted hover:text-blue-400 hover:bg-blue-500/10"><MonitorSmartphone className="w-4 h-4" /></button>
                {u.status === 'pending' && (
                  <button onClick={() => act(() => adminSetUserStatus(u.id, 'active'))} title={t('admin.approve')}
                    className="p-1.5 rounded text-green-400 hover:bg-green-500/15"><Check className="w-4 h-4" /></button>
                )}
                {u.status === 'active' && u.id !== user?.id && (
                  <button onClick={() => act(() => adminSetUserStatus(u.id, 'disabled'))} title={t('admin.disable')}
                    className="p-1.5 rounded text-amber-400 hover:bg-amber-500/15"><Ban className="w-4 h-4" /></button>
                )}
                {u.status === 'disabled' && (
                  <button onClick={() => act(() => adminSetUserStatus(u.id, 'active'))} title={t('admin.reactivate')}
                    className="p-1.5 rounded text-green-400 hover:bg-green-500/15"><RotateCcw className="w-4 h-4" /></button>
                )}
                {u.id !== user?.id && (
                  <button onClick={() => deleteUser(u)} title={t('admin.delete')}
                    className="p-1.5 rounded text-text-muted hover:text-red-400 hover:bg-red-500/10"><Trash2 className="w-4 h-4" /></button>
                )}
              </div>
            </div>
          ))}
        </div>
      )}

      {/* Invite link generator */}
      <div className="flex flex-col gap-2 pt-2 border-t border-default/60">
        <div className="flex flex-wrap items-center gap-2">
          <input value={inviteEmail} onChange={e => setInviteEmail(e.target.value)} placeholder={t('admin.invite_email_placeholder')}
            className="bg-surface border border-default rounded-lg px-2 py-1 text-sm text-text-primary flex-1 min-w-[12rem]" />
          <button
            onClick={() => act(async () => { const l = await adminInvite(inviteEmail); setInviteLink(l); setCopied(false) })}
            className="flex items-center gap-1.5 text-sm bg-surface-tertiary hover:bg-surface-tertiary text-text-primary rounded-lg px-3 py-1">
            <Link2 className="w-4 h-4" /> {t('admin.generate_invite')}
          </button>
        </div>
        {inviteLink && (
          <div className="flex items-center gap-2 bg-surface border border-default rounded-lg px-2 py-1">
            <span className="text-xs text-text-primary font-mono truncate flex-1 min-w-0" title={inviteLink}>{inviteLink}</span>
            <button onClick={() => { navigator.clipboard?.writeText(inviteLink); setCopied(true) }} title={t('admin.copy')}
              className="text-text-secondary hover:text-text-primary flex-shrink-0">{copied ? <Check className="w-4 h-4 text-green-400" /> : <Copy className="w-4 h-4" />}</button>
          </div>
        )}
      </div>

      {/* Create user inline */}
      <div className="flex flex-wrap items-center gap-2 pt-2 border-t border-default/60">
        <input value={creating.username} onChange={e => setCreating(c => ({ ...c, username: e.target.value }))} placeholder={t('admin.username_placeholder')}
          className="bg-surface border border-default rounded-lg px-2 py-1 text-sm text-text-primary w-32" />
        <input type="password" value={creating.password} onChange={e => setCreating(c => ({ ...c, password: e.target.value }))} placeholder={t('admin.password_placeholder')}
          className="bg-surface border border-default rounded-lg px-2 py-1 text-sm text-text-primary w-32" />
        <select value={creating.role} onChange={e => setCreating(c => ({ ...c, role: e.target.value as 'user' | 'admin' }))}
          className="bg-surface border border-default rounded-lg px-2 py-1 text-sm text-text-primary">
          <option value="user">user</option>
          <option value="admin">admin</option>
        </select>
        <button
          onClick={() => act(async () => { await adminCreateUser(creating.username, creating.password, creating.role); setCreating({ username: '', password: '', role: 'user' }) })}
          disabled={!creating.username || !creating.password}
          className="flex items-center gap-1.5 text-sm bg-surface-tertiary hover:bg-surface-tertiary disabled:opacity-50 text-text-primary rounded-lg px-3 py-1">
          <UserPlus className="w-4 h-4" /> {t('admin.create')}
        </button>
      </div>

      <AdminResetPasswordModal user={resetUser} onClose={() => setResetUser(null)} />
      <AdminUserSessionsModal user={sessionsUser} onClose={() => setSessionsUser(null)} />
    </section>
  )
}
