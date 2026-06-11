import { useState } from 'react'
import { useTranslation } from 'react-i18next'
import { KeyRound, Link2, Loader2, Check, Copy, AlertTriangle } from 'lucide-react'
import { Sheet } from './Sheet'
import { adminResetPassword, AdminUser } from '../api/client'

// AdminResetPasswordModal — an admin either sets a new password directly or
// generates a single-use reset link (1h) to hand to the user. Both paths
// revoke every session of the target account on the backend.
export default function AdminResetPasswordModal({ user, onClose }: {
  readonly user: AdminUser | null
  readonly onClose: () => void
}) {
  const { t } = useTranslation()
  const [password, setPassword] = useState('')
  const [busy, setBusy] = useState(false)
  const [link, setLink] = useState('')
  const [copied, setCopied] = useState(false)
  const [msg, setMsg] = useState<{ ok: boolean; text: string } | null>(null)

  const reset = () => { setPassword(''); setBusy(false); setLink(''); setCopied(false); setMsg(null) }
  const close = () => { reset(); onClose() }

  const run = async (pw?: string) => {
    if (!user) return
    setBusy(true); setMsg(null); setLink('')
    try {
      const r = await adminResetPassword(user.id, pw)
      if (r.link) setLink(r.link)
      else { setMsg({ ok: true, text: t('admin.reset_done') }); setPassword('') }
    } catch (e: any) {
      setMsg({ ok: false, text: e?.response?.data?.error || t('admin.reset_failed') })
    } finally { setBusy(false) }
  }

  if (!user) return null
  return (
    <Sheet open onClose={close} size="sm" title={t('admin.reset_title', { name: user.username })}>
      <div className="flex flex-col gap-3">
        <p className="text-xs text-amber-700 dark:text-amber-300 flex items-start gap-1.5">
          <AlertTriangle className="w-3.5 h-3.5 mt-0.5 flex-shrink-0" />
          {t('admin.reset_warning', { name: user.username })}
        </p>

        <div className="flex items-center gap-2">
          <input type="password" value={password} onChange={e => setPassword(e.target.value)}
            placeholder={t('admin.reset_new_password')} autoComplete="new-password"
            className="bg-surface border border-default rounded-lg px-3 py-2 text-sm text-text-primary flex-1 focus:outline-none focus:border-green-500" />
          <button onClick={() => run(password)} disabled={busy || password.length < 6}
            className="flex items-center gap-1.5 text-sm bg-green-600 hover:bg-green-500 disabled:opacity-50 text-white rounded-lg px-3 py-2">
            {busy ? <Loader2 className="w-4 h-4 animate-spin" /> : <KeyRound className="w-4 h-4" />} {t('admin.reset_set')}
          </button>
        </div>

        <div className="flex items-center gap-2 text-xs text-text-muted">
          <span className="flex-1 border-t border-default/60" />
          {t('admin.reset_or')}
          <span className="flex-1 border-t border-default/60" />
        </div>

        <button onClick={() => run()} disabled={busy}
          className="flex items-center gap-1.5 text-sm bg-surface-tertiary hover:bg-surface-tertiary disabled:opacity-50 text-text-primary rounded-lg px-3 py-2 self-start">
          {busy ? <Loader2 className="w-4 h-4 animate-spin" /> : <Link2 className="w-4 h-4" />} {t('admin.reset_generate_link')}
        </button>

        {link && (
          <div className="flex flex-col gap-1.5">
            <p className="text-xs text-text-secondary">{t('admin.reset_link_hint')}</p>
            <div className="flex items-center gap-2 bg-surface border border-default rounded-lg px-2 py-1.5">
              <span className="text-xs text-text-primary font-mono truncate flex-1 min-w-0" title={link}>{link}</span>
              <button onClick={() => { navigator.clipboard?.writeText(link); setCopied(true) }}
                title={copied ? t('admin.copied') : t('admin.copy')}
                className="text-text-secondary hover:text-text-primary flex-shrink-0">
                {copied ? <Check className="w-4 h-4 text-green-400" /> : <Copy className="w-4 h-4" />}
              </button>
            </div>
          </div>
        )}
        {msg && <span className={`text-xs ${msg.ok ? 'text-green-400' : 'text-red-400'}`}>{msg.text}</span>}
      </div>
    </Sheet>
  )
}
