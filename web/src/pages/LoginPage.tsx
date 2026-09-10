import { useEffect, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { useNavigate, useLocation, useSearchParams } from 'react-router-dom'
import { LogIn, Loader2, AlertCircle, KeyRound } from 'lucide-react'
import { useAuth } from '../auth/AuthContext'
import { isPasskeySupported, oauthProviders } from '../api/client'

// Google "G" mark (single-color variant inherits the current text color).
function GoogleMark() {
  return (
    <svg viewBox="0 0 24 24" className="w-4 h-4" fill="currentColor" aria-hidden="true">
      <path d="M21.35 11.1h-9.17v2.73h6.51c-.33 3.81-3.5 5.44-6.5 5.44C8.36 19.27 5 16.25 5 12c0-4.1 3.2-7.27 7.2-7.27 3.09 0 4.9 1.97 4.9 1.97L19 4.72S16.56 2 12.1 2C6.42 2 2.03 6.8 2.03 12c0 5.05 4.13 10 10.22 10 5.35 0 9.25-3.67 9.25-9.09 0-1.15-.15-1.81-.15-1.81Z" />
    </svg>
  )
}

export default function LoginPage() {
  const { t } = useTranslation()
  const { login, loginWithPasskey } = useAuth()
  const nav = useNavigate()
  const location = useLocation()
  const [params] = useSearchParams()
  const from = (location.state as { from?: { pathname?: string } })?.from?.pathname || '/'

  const [username, setUsername] = useState('')
  const [password, setPassword] = useState('')
  const [remember, setRemember] = useState(true)
  const [totp, setTotp] = useState('')
  const [mfaStep, setMfaStep] = useState(false)
  const [loading, setLoading] = useState(false)
  const [error, setError] = useState('')
  const [googleEnabled, setGoogleEnabled] = useState(false)

  // Feature-detect the Google button (backend answers false unless the
  // JACKUI_OAUTH_* settings are complete) and surface redirect errors.
  useEffect(() => {
    oauthProviders().then(r => setGoogleEnabled(!!r.google)).catch(() => setGoogleEnabled(false))
    const oauthError = params.get('oauthError')
    if (oauthError) {
      const key = `login.oauth_error_${oauthError}`
      setError(t(key) === key ? t('login.oauth_error_generic') : t(key))
    }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [])

  const googleStart = () => {
    window.location.href = `/api/auth/oauth/google/start?remember=${remember ? '1' : '0'}`
  }

  const submit = async (e: React.FormEvent) => {
    e.preventDefault()
    setLoading(true)
    setError('')
    try {
      await login(username, password, remember, totp)
      nav(from, { replace: true })
    } catch (err: any) {
      // Account has MFA → ask for the 6-digit code and resubmit.
      if (err?.response?.data?.mfaRequired) {
        setMfaStep(true)
        setError(totp ? t('login.invalid_code') : '')
      } else {
        setError(err?.response?.data?.error || err.message || t('login.login_failed'))
      }
    } finally {
      setLoading(false)
    }
  }

  const passkeyLogin = async () => {
    if (!username) {
      setError(t('login.passkey_need_user'))
      return
    }
    setLoading(true)
    setError('')
    try {
      await loginWithPasskey(username, remember)
      nav(from, { replace: true })
    } catch (err: any) {
      // A user cancelling the browser prompt throws NotAllowedError — treat as silent.
      if (err?.name === 'NotAllowedError' || err?.name === 'AbortError') {
        setError(t('login.passkey_cancelled'))
      } else {
        setError(err?.response?.data?.error || err.message || t('login.passkey_failed'))
      }
    } finally {
      setLoading(false)
    }
  }

  return (
    <main id="main-content" tabIndex={-1} className="min-h-screen bg-surface flex items-center justify-center px-4 safe-top safe-bottom">
      <div className="w-full max-w-sm">
        <div className="flex justify-center mb-6">
          <div className="flex items-center gap-2">
            <span className="text-3xl font-bold text-green-500">Jack</span>
            <span className="text-3xl font-bold text-text-primary">UI</span>
          </div>
        </div>

        <form
          onSubmit={submit}
          className="bg-surface-secondary border border-default rounded-2xl p-6 flex flex-col gap-4 shadow-elevated"
        >
          <div>
            <label htmlFor="login-username" className="block text-sm text-text-secondary mb-1.5">{t('login.username')}</label>
            <input
              id="login-username"
              type="text"
              autoFocus
              autoComplete="username"
              value={username}
              onChange={e => setUsername(e.target.value)}
              required
              className="input-field"
            />
          </div>

          <div>
            <label htmlFor="login-password" className="block text-sm text-text-secondary mb-1.5">{t('login.password')}</label>
            <input
              id="login-password"
              type="password"
              autoComplete="current-password"
              value={password}
              onChange={e => setPassword(e.target.value)}
              required
              className="input-field"
            />
          </div>

          {mfaStep && (
            <div>
              <label htmlFor="login-totp" className="block text-sm text-text-secondary mb-1.5">{t('login.mfa_label')}</label>
              <input
                id="login-totp"
                type="text"
                autoFocus
                autoComplete="one-time-code"
                value={totp}
                onChange={e => setTotp(e.target.value.slice(0, 14))}
                placeholder={t('login.mfa_placeholder')}
                className="input-field tracking-widest text-center font-mono"
              />
              <p className="text-[11px] text-text-muted mt-1">{t('login.mfa_hint')}</p>
            </div>
          )}

          <label className="flex items-center gap-2 cursor-pointer text-sm text-text-primary">
            <input
              type="checkbox"
              checked={remember}
              onChange={e => setRemember(e.target.checked)}
              className="w-4 h-4 accent-green-500"
            />
            {' '}{t('login.remember_me')}
          </label>

          {error && (
            <div className="bg-red-500/10 border border-red-500/30 text-red-400 text-sm rounded-lg p-3 flex items-center gap-2">
              <AlertCircle className="w-4 h-4 flex-shrink-0" />
              {error}
            </div>
          )}

          <button
            type="submit"
            disabled={loading || !username || !password}
            className="btn-primary flex items-center justify-center gap-2 disabled:opacity-50"
          >
            {loading ? <Loader2 className="w-4 h-4 animate-spin" /> : <LogIn className="w-4 h-4" />}
            {t('login.sign_in')}
          </button>

          {isPasskeySupported() && (
            <button
              type="button"
              onClick={passkeyLogin}
              disabled={loading || !username}
              className="btn-secondary flex items-center justify-center gap-2 disabled:opacity-50"
            >
              <KeyRound className="w-4 h-4" />
              {t('login.sign_in_passkey')}
            </button>
          )}

          {googleEnabled && (
            <>
              <div className="flex items-center gap-3 text-[11px] text-text-muted" role="separator">
                <span className="h-px flex-1 bg-default" />
                {t('login.sso_or')}
                <span className="h-px flex-1 bg-default" />
              </div>
              <button
                type="button"
                onClick={googleStart}
                className="btn-secondary flex items-center justify-center gap-2"
              >
                <GoogleMark />
                {t('login.sso_google')}
              </button>
            </>
          )}

          <div className="flex items-center justify-between text-xs">
            <button type="button" onClick={() => nav('/register')} className="text-text-secondary hover:text-green-400">{t('login.create_account')}</button>
            <button type="button" onClick={() => nav('/forgot-password')} className="text-text-secondary hover:text-green-400">{t('login.forgot_password')}</button>
          </div>
        </form>

        <p className="text-center text-xs text-text-muted mt-4">
          {t('login.tagline')}
        </p>
      </div>
    </main>
  )
}
