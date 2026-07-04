import { useEffect, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { useNavigate, useSearchParams } from 'react-router-dom'
import { Loader2, AlertCircle, CheckCircle2, UserPlus, KeyRound, Mail } from 'lucide-react'
import { registerAccount, verifyEmail, forgotPassword, resetPassword } from '../api/client'

// Shared shell for the unauthenticated auth pages (register / verify / forgot /
// reset) — same centered card as the login screen.
function Shell({ title, children }: { readonly title: string; readonly children: React.ReactNode }) {
  return (
    <div className="min-h-screen bg-surface flex items-center justify-center px-4 safe-top safe-bottom">
      <div className="w-full max-w-sm">
        <div className="flex justify-center mb-6">
          <span className="text-3xl font-bold text-green-500">Jack</span>
          <span className="text-3xl font-bold text-text-primary">UI</span>
        </div>
        <div className="bg-surface-secondary border border-default rounded-2xl p-6 flex flex-col gap-4 shadow-2xl">
          <h1 className="text-lg font-semibold text-text-primary">{title}</h1>
          {children}
        </div>
      </div>
    </div>
  )
}

function Err({ text }: { readonly text: string }) {
  return <div className="bg-red-500/10 border border-red-500/30 text-red-400 text-sm rounded-lg p-3 flex items-center gap-2"><AlertCircle className="w-4 h-4 flex-shrink-0" />{text}</div>
}
function Ok({ text }: { readonly text: string }) {
  return <div className="bg-green-500/10 border border-green-500/30 text-green-700 dark:text-green-300 text-sm rounded-lg p-3 flex items-center gap-2"><CheckCircle2 className="w-4 h-4 flex-shrink-0" />{text}</div>
}

export function RegisterPage() {
  const { t } = useTranslation()
  const nav = useNavigate()
  const [params] = useSearchParams()
  const invite = params.get('invite') || ''
  const [username, setUsername] = useState('')
  const [email, setEmail] = useState('')
  const [password, setPassword] = useState('')
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState('')
  const [done, setDone] = useState('')

  const handleSubmit = async (e: React.FormEvent) => {
    e.preventDefault(); setBusy(true); setError('')
    try {
      const r = await registerAccount(username, email, password, invite)
      setDone(r.message)
    } catch (err: any) {
      setError(err?.response?.data?.error || t('auth.register_failed'))
    } finally { setBusy(false) }
  }

  if (done) return <Shell title={t('auth.register_created_title')}><Ok text={done} /><button onClick={() => nav('/login')} className="btn-primary">{t('auth.go_to_login')}</button></Shell>

  return (
    <Shell title={invite ? t('auth.register_invite_title') : t('auth.register_title')}>
      <form onSubmit={handleSubmit} className="flex flex-col gap-3">
        <input className="input-field" placeholder={t('auth.username')} autoComplete="username" value={username} onChange={e => setUsername(e.target.value)} required />
        <input className="input-field" placeholder={t('auth.email')} type="email" autoComplete="email" value={email} onChange={e => setEmail(e.target.value)} required />
        <input className="input-field" placeholder={t('auth.password_min')} type="password" autoComplete="new-password" value={password} onChange={e => setPassword(e.target.value)} required />
        {!invite && <p className="text-xs text-text-muted">{t('auth.pending_note')}</p>}
        {error && <Err text={error} />}
        <button type="submit" disabled={busy || !username || !email || password.length < 6} className="btn-primary flex items-center justify-center gap-2 disabled:opacity-50">
          {busy ? <Loader2 className="w-4 h-4 animate-spin" /> : <UserPlus className="w-4 h-4" />} {t('auth.register_button')}
        </button>
        <button type="button" onClick={() => nav('/login')} className="text-xs text-text-secondary hover:text-green-400">{t('auth.have_account')}</button>
      </form>
    </Shell>
  )
}

export function VerifyEmailPage() {
  const { t } = useTranslation()
  const nav = useNavigate()
  const [params] = useSearchParams()
  const [state, setState] = useState<'busy' | 'ok' | 'err'>('busy')
  const [msg, setMsg] = useState('')
  useEffect(() => {
    const token = params.get('token') || ''
    if (token) {
      verifyEmail(token).then(() => { setState('ok'); setMsg(t('auth.email_confirmed')) })
        .catch((e: any) => { setState('err'); setMsg(e?.response?.data?.error || t('auth.confirm_failed')) })
    } else {
      setState('err'); setMsg(t('auth.token_missing'))
    }
  }, [])
  return (
    <Shell title={t('auth.verify_title')}>
      {state === 'busy' && <div className="flex items-center gap-2 text-text-secondary"><Loader2 className="w-4 h-4 animate-spin" /> {t('auth.confirming')}</div>}
      {state === 'ok' && <Ok text={msg} />}
      {state === 'err' && <Err text={msg} />}
      {state !== 'busy' && <button onClick={() => nav('/login')} className="btn-primary">{t('auth.go_to_login')}</button>}
    </Shell>
  )
}

export function ForgotPasswordPage() {
  const { t } = useTranslation()
  const nav = useNavigate()
  const [email, setEmail] = useState('')
  const [busy, setBusy] = useState(false)
  const [msg, setMsg] = useState('')
  const handleSubmit = async (e: React.FormEvent) => {
    e.preventDefault(); setBusy(true)
    try { setMsg(await forgotPassword(email)) } catch { setMsg(t('auth.forgot_sent')) } finally { setBusy(false) }
  }
  if (msg) return <Shell title={t('auth.forgot_title')}><Ok text={msg} /><button onClick={() => nav('/login')} className="btn-primary">{t('auth.back_to_login')}</button></Shell>
  return (
    <Shell title={t('auth.forgot_title')}>
      <form onSubmit={handleSubmit} className="flex flex-col gap-3">
        <input className="input-field" placeholder={t('auth.your_email')} type="email" autoComplete="email" value={email} onChange={e => setEmail(e.target.value)} required />
        <button type="submit" disabled={busy || !email} className="btn-primary flex items-center justify-center gap-2 disabled:opacity-50">
          {busy ? <Loader2 className="w-4 h-4 animate-spin" /> : <Mail className="w-4 h-4" />} {t('auth.send_link')}
        </button>
        <button type="button" onClick={() => nav('/login')} className="text-xs text-text-secondary hover:text-green-400">{t('auth.back')}</button>
      </form>
    </Shell>
  )
}

export function ResetPasswordPage() {
  const { t } = useTranslation()
  const nav = useNavigate()
  const [params] = useSearchParams()
  const token = params.get('token') || ''
  const [password, setPassword] = useState('')
  const [confirm, setConfirm] = useState('')
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState('')
  const [done, setDone] = useState(false)
  const handleSubmit = async (e: React.FormEvent) => {
    e.preventDefault(); setError('')
    if (password.length < 6) { setError(t('auth.min_6_chars')); return }
    if (password !== confirm) { setError(t('auth.passwords_no_match')); return }
    setBusy(true)
    try { await resetPassword(token, password); setDone(true) }
    catch (err: any) { setError(err?.response?.data?.error || t('auth.reset_failed')) }
    finally { setBusy(false) }
  }
  if (token) {
    if (done) return <Shell title={t('auth.reset_done_title')}><Ok text={t('auth.reset_done_msg')} /><button onClick={() => nav('/login')} className="btn-primary">{t('auth.go_to_login')}</button></Shell>
  } else {
    return <Shell title={t('auth.reset_title')}><Err text={t('auth.token_invalid')} /></Shell>
  }
  return (
    <Shell title={t('auth.reset_title')}>
      <form onSubmit={handleSubmit} className="flex flex-col gap-3">
        <input className="input-field" placeholder={t('auth.new_password')} type="password" autoComplete="new-password" value={password} onChange={e => setPassword(e.target.value)} required />
        <input className="input-field" placeholder={t('auth.confirm_password')} type="password" autoComplete="new-password" value={confirm} onChange={e => setConfirm(e.target.value)} required />
        {error && <Err text={error} />}
        <button type="submit" disabled={busy} className="btn-primary flex items-center justify-center gap-2 disabled:opacity-50">
          {busy ? <Loader2 className="w-4 h-4 animate-spin" /> : <KeyRound className="w-4 h-4" />} {t('auth.reset_button')}
        </button>
      </form>
    </Shell>
  )
}
