import { useEffect, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { useSearchParams } from 'react-router-dom'
import { oauthProviders } from '../api/client'
import { resolveOAuthError } from '../lib/oauthError'

// Google "G" mark (single-color variant inherits the current text color).
function GoogleMark() {
  return (
    <svg viewBox="0 0 24 24" className="w-4 h-4" fill="currentColor" aria-hidden="true">
      <path d="M21.35 11.1h-9.17v2.73h6.51c-.33 3.81-3.5 5.44-6.5 5.44C8.36 19.27 5 16.25 5 12c0-4.1 3.2-7.27 7.2-7.27 3.09 0 4.9 1.97 4.9 1.97L19 4.72S16.56 2 12.1 2C6.42 2 2.03 6.8 2.03 12c0 5.05 4.13 10 10.22 10 5.35 0 9.25-3.67 9.25-9.09 0-1.15-.15-1.81-.15-1.81Z" />
    </svg>
  )
}

type Props = {
  readonly remember: boolean
  readonly onError: (message: string) => void
}

// Feature-detects JACKUI_OAUTH_* via /oauth/providers and, when enabled,
// renders the "Sign in with Google" button. Redirect errors from the
// Google callback land on /login?oauthError= and are surfaced here.
export default function GoogleSignInSection({ remember, onError }: Props) {
  const { t } = useTranslation()
  const [params] = useSearchParams()
  const [enabled, setEnabled] = useState(false)

  useEffect(() => {
    oauthProviders().then(r => setEnabled(!!r.google)).catch(() => setEnabled(false))
    const oauthError = params.get('oauthError')
    if (oauthError) {
      onError(resolveOAuthError(oauthError, t))
    }
    // Mount-only feature-detect + one-shot redirect error, same as the
    // original login-page effect.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [])

  if (!enabled) return null

  const start = () => {
    window.location.href = `/api/auth/oauth/google/start?remember=${remember ? '1' : '0'}`
  }

  return (
    <>
      <div className="flex items-center gap-3 text-[11px] text-text-muted" role="separator">
        <span className="h-px flex-1 bg-default" />
        {t('login.sso_or')}
        <span className="h-px flex-1 bg-default" />
      </div>
      <button
        type="button"
        onClick={start}
        className="btn-secondary flex items-center justify-center gap-2"
      >
        <GoogleMark />
        {t('login.sso_google')}
      </button>
    </>
  )
}
