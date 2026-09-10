import { useEffect, useRef, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { useNavigate, useSearchParams } from 'react-router-dom'
import { Loader2, AlertCircle } from 'lucide-react'
import { useAuth } from '../auth/AuthContext'
import { oauthExchange } from '../api/client'

function AuthErrorBanner({ message }: { readonly message: string }) {
  return (
    <div className="bg-red-500/10 border border-red-500/30 text-red-400 text-sm rounded-lg p-3 flex items-center gap-2">
      <AlertCircle className="w-4 h-4 flex-shrink-0" />
      {message}
    </div>
  )
}

type MfaFormProps = {
  readonly totp: string
  readonly busy: boolean
  readonly error: string
  readonly onTotp: (value: string) => void
  readonly onSubmit: (e: React.FormEvent) => void
}

function MfaForm({ totp, busy, error, onTotp, onSubmit }: MfaFormProps) {
  const { t } = useTranslation()
  const totpRef = useRef<HTMLInputElement>(null)
  useEffect(() => {
    totpRef.current?.focus()
  }, [])
  return (
    <form onSubmit={onSubmit} className="flex flex-col gap-3">
      <h1 className="text-lg font-semibold text-text-primary">{t('login.mfa_label')}</h1>
      <input
        ref={totpRef}
        type="text"
        autoComplete="one-time-code"
        value={totp}
        onChange={e => onTotp(e.target.value.slice(0, 14))}
        placeholder={t('login.mfa_placeholder')}
        className="input-field tracking-widest text-center font-mono"
      />
      {error && <AuthErrorBanner message={error} />}
      <button type="submit" disabled={busy || !totp} className="btn-primary flex items-center justify-center gap-2 disabled:opacity-50">
        {busy ? <Loader2 className="w-4 h-4 animate-spin" /> : null}
        {t('login.sign_in')}
      </button>
    </form>
  )
}

function ExchangeError({ message, onBack }: { readonly message: string; readonly onBack: () => void }) {
  const { t } = useTranslation()
  return (
    <div className="flex flex-col gap-3">
      <AuthErrorBanner message={message} />
      <button type="button" onClick={onBack} className="btn-secondary">
        {t('auth.go_to_login')}
      </button>
    </div>
  )
}

function Finishing() {
  const { t } = useTranslation()
  return (
    <div className="flex items-center justify-center gap-3 text-text-secondary">
      <Loader2 className="w-5 h-5 animate-spin" />
      <span>{t('login.sso_finishing')}</span>
    </div>
  )
}

// OAuthCallbackPage is the browser landing after Google bounces back to
// /auth/google/callback?code=... — the code here is a single-use, 5-minute
// exchange code (not a token). Tokens come from POST /auth/oauth/exchange, so
// they never touch browser history. MFA accounts get the same TOTP prompt as
// the password login.
export default function OAuthCallbackPage() {
  const { t } = useTranslation()
  const nav = useNavigate()
  const { completeOAuthLogin } = useAuth()
  const [params] = useSearchParams()
  const [busy, setBusy] = useState(true)
  const [error, setError] = useState('')
  const [mfaStep, setMfaStep] = useState(false)
  const [totp, setTotp] = useState('')
  const code = params.get('code') || ''

  useEffect(() => {
    if (!code) {
      nav('/login?oauthError=' + (params.get('oauthError') || 'state'), { replace: true })
      return
    }
    oauthExchange(code)
      .then(bundle => {
        completeOAuthLogin(bundle)
        nav('/', { replace: true })
      })
      .catch((err: any) => {
        if (err?.response?.data?.mfaRequired) {
          setMfaStep(true)
          setBusy(false)
        } else {
          setError(err?.response?.data?.error || t('login.login_failed'))
          setBusy(false)
        }
      })
    // Exchange once per landing code; MFA is a separate submit path.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [code])

  const submitMfa = async (e: React.FormEvent) => {
    e.preventDefault()
    setBusy(true)
    setError('')
    try {
      const bundle = await oauthExchange(code, totp)
      completeOAuthLogin(bundle)
      nav('/', { replace: true })
    } catch (err: any) {
      setError(err?.response?.data?.error || t('login.invalid_code'))
    } finally {
      setBusy(false)
    }
  }

  let body = <Finishing />
  if (mfaStep) {
    body = <MfaForm totp={totp} busy={busy} error={error} onTotp={setTotp} onSubmit={submitMfa} />
  } else if (error) {
    body = <ExchangeError message={error} onBack={() => nav('/login', { replace: true })} />
  }

  return (
    <main id="main-content" tabIndex={-1} className="min-h-screen bg-surface flex items-center justify-center px-4 safe-top safe-bottom">
      <div className="w-full max-w-sm">
        <div className="flex justify-center mb-6">
          <span className="text-3xl font-bold text-green-500">Jack</span>
          <span className="text-3xl font-bold text-text-primary">UI</span>
        </div>
        <div className="bg-surface-secondary border border-default rounded-2xl p-6 flex flex-col gap-4 shadow-2xl">
          {body}
        </div>
      </div>
    </main>
  )
}
