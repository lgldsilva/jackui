import { useEffect, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { useNavigate, useSearchParams } from 'react-router-dom'
import { Loader2, Tv } from 'lucide-react'
import { decideDevice, getPendingDevice, type PendingDevice } from '../api/devices'
import { errMessage } from '../lib/errMessage'
import { Err, Ok, Shell } from './AuthFlows'

// LinkPage — /link?code=ABCD-EFGH: the phone side of TV pairing (#201). The
// TV shows a QR of this URL; the user (already logged in, any method) sees
// WHICH device asks (name/model/IP — anti-phishing) and must explicitly
// authorize. Opening the link alone never pairs anything.

type Phase = 'enter' | 'loading' | 'confirm' | 'approved' | 'denied'

export default function LinkPage() {
  const { t } = useTranslation()
  const nav = useNavigate()
  const [params] = useSearchParams()
  const initial = (params.get('code') || '').toUpperCase()
  const [code, setCode] = useState(initial)
  const [phase, setPhase] = useState<Phase>(initial ? 'loading' : 'enter')
  const [device, setDevice] = useState<PendingDevice | null>(null)
  const [error, setError] = useState('')
  const [busy, setBusy] = useState(false)

  const lookup = async (c: string) => {
    setPhase('loading'); setError('')
    try {
      setDevice(await getPendingDevice(c))
      setPhase('confirm')
    } catch (err) {
      const status = (err as { response?: { status?: number } })?.response?.status
      setError(status === 404 ? t('link.not_found') : errMessage(err))
      setPhase('enter')
    }
  }

  useEffect(() => {
    if (initial) void lookup(initial)
    // Look the code up once per landing URL.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [initial])

  const decide = async (approve: boolean) => {
    if (!device) return
    setBusy(true); setError('')
    try {
      await decideDevice(device.userCode, approve)
      setPhase(approve ? 'approved' : 'denied')
    } catch (err) {
      setError(errMessage(err))
    } finally { setBusy(false) }
  }

  if (phase === 'approved' || phase === 'denied') {
    return (
      <Shell title={t('link.title')}>
        <Ok text={phase === 'approved' ? t('link.approved', { name: device?.name }) : t('link.denied')} />
        <button className="btn-primary" onClick={() => nav('/')}>{t('link.go_home')}</button>
      </Shell>
    )
  }

  if (phase === 'loading') {
    return <Shell title={t('link.title')}><div className="flex justify-center py-6"><Loader2 className="w-6 h-6 animate-spin" aria-label={t('link.loading')} /></div></Shell>
  }

  if (phase === 'confirm' && device) {
    return (
      <Shell title={t('link.title')}>
        <div className="flex items-start gap-3">
          <Tv className="w-8 h-8 text-green-500 flex-shrink-0" />
          <div className="text-sm">
            <p className="text-text-primary font-medium">{t('link.asks', { name: device.name })}</p>
            {device.model && <p className="text-text-secondary">{device.model}</p>}
            <p className="text-text-muted">{t('link.from_ip', { ip: device.ip })} · {device.userCode}</p>
          </div>
        </div>
        <p className="text-xs text-text-muted">{t('link.warning')}</p>
        {error && <Err text={error} />}
        <div className="flex gap-2">
          <button className="btn-primary flex-1" disabled={busy} onClick={() => decide(true)}>{t('link.authorize')}</button>
          <button className="btn-secondary flex-1" disabled={busy} onClick={() => decide(false)}>{t('link.deny')}</button>
        </div>
      </Shell>
    )
  }

  return (
    <Shell title={t('link.title')}>
      <form className="flex flex-col gap-3" onSubmit={e => { e.preventDefault(); if (code.trim()) void lookup(code.trim()) }}>
        <label className="text-sm text-text-secondary" htmlFor="link-code">{t('link.enter_code')}</label>
        <input
          id="link-code"
          className="input-field font-mono tracking-widest uppercase text-center"
          placeholder="ABCD-EFGH"
          autoComplete="one-time-code"
          maxLength={9}
          value={code}
          onChange={e => setCode(e.target.value.toUpperCase())}

        />
        {error && <Err text={error} />}
        <button type="submit" className="btn-primary" disabled={!code.trim()}>{t('link.continue')}</button>
      </form>
    </Shell>
  )
}
