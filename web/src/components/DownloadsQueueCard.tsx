import { useState, useEffect } from 'react'
import { useTranslation, Trans } from 'react-i18next'
import { ListOrdered, Loader2, Save, Zap } from 'lucide-react'
import {
  getDownloadsQueueSettings,
  updateDownloadsQueueSettings,
  DownloadsQueueSettings,
} from '../api/client'
import { errMessage } from '../lib/errMessage'

// NumberField: touch-friendly numeric input (>=44px, 16px to avoid iOS zoom).
function NumberField(props: Readonly<{
  label: string
  value: number
  onChange: (n: number) => void
  min?: number
  suffix?: string
  hint?: string
}>) {
  const { label, value, onChange, min = 1, suffix, hint } = props
  return (
    <label className="flex flex-col gap-1">
      <span className="text-xs text-text-secondary">{label}</span>
      <div className="flex items-center gap-2">
        <input
          type="number"
          min={min}
          inputMode="numeric"
          value={value || ''}
          onChange={(e) => onChange(Math.max(min, Number(e.target.value) || min))}
          className="input-field min-h-[44px]"
        />
        {suffix && <span className="text-xs text-text-muted flex-shrink-0">{suffix}</span>}
      </div>
      {hint && <span className="text-[11px] text-text-muted">{hint}</span>}
    </label>
  )
}

function LiveBadge() {
  const { t } = useTranslation()
  return (
    <span className="inline-flex items-center gap-1 text-[10px] uppercase tracking-wide text-green-400 bg-green-500/10 border border-green-500/20 px-1.5 py-0.5 rounded">
      <Zap className="w-2.5 h-2.5" />{t('downloads.queueCard.live')}
    </span>
  )
}

export default function DownloadsQueueCard() {
  const { t } = useTranslation()
  const [form, setForm] = useState<DownloadsQueueSettings | null>(null)
  const [loading, setLoading] = useState(true)
  const [saving, setSaving] = useState(false)
  const [error, setError] = useState('')
  const [notice, setNotice] = useState('')

  useEffect(() => {
    getDownloadsQueueSettings()
      .then(setForm)
      .catch((e: unknown) => setError(errMessage(e)))
      .finally(() => setLoading(false))
  }, [])

  const set = <K extends keyof DownloadsQueueSettings>(key: K, val: DownloadsQueueSettings[K]) =>
    setForm((f) => (f ? { ...f, [key]: val } : f))

  const handleSave = async () => {
    if (!form) return
    setSaving(true)
    setError('')
    setNotice('')
    try {
      await updateDownloadsQueueSettings(form)
      setNotice(t('downloads.queueCard.savedLive'))
    } catch (e: unknown) {
      setError(errMessage(e))
    } finally {
      setSaving(false)
    }
  }

  if (loading) {
    return (
      <div className="card flex items-center gap-3 text-text-secondary">
        <Loader2 className="w-4 h-4 animate-spin" />
        {t('downloads.queueCard.loading')}
      </div>
    )
  }
  if (error && !form) {
    return <div className="card text-red-400 text-sm">{t('downloads.queueCard.unavailable', { error })}</div>
  }
  if (!form) return null

  return (
    <div className="card flex flex-col gap-5">
      <div className="flex items-center gap-2">
        <ListOrdered className="w-5 h-5 text-cyan-400" />
        <h2 className="text-lg font-semibold text-text-primary">{t('downloads.queueCard.title')}</h2>
        <LiveBadge />
      </div>

      <p className="text-xs text-text-muted -mt-2">
        {t('downloads.queueCard.intro')}
      </p>

      <div className="grid grid-cols-1 sm:grid-cols-2 gap-3">
        <NumberField label={t('downloads.queueCard.maxActiveLabel')} value={form.maxActive} onChange={(n) => set('maxActive', n)}
          suffix={t('downloads.queueCard.suffixTotal')} hint={t('downloads.queueCard.maxActiveHint')} />
        <NumberField label={t('downloads.queueCard.perUserLabel')} value={form.perUserMaxActive} onChange={(n) => set('perUserMaxActive', n)}
          min={0} suffix={t('downloads.queueCard.suffixPerUser')} hint={t('downloads.queueCard.perUserHint')} />
        <NumberField label={t('downloads.queueCard.verifyLabel')} value={form.maxConcurrentVerify ?? 1} onChange={(n) => set('maxConcurrentVerify', n)}
          suffix={t('downloads.queueCard.suffixVerify')} hint={t('downloads.queueCard.verifyHint')} />
        <NumberField label={t('downloads.queueCard.stallLabel')} value={form.stallThresholdMin} onChange={(n) => set('stallThresholdMin', n)}
          suffix={t('downloads.queueCard.suffixMin')} hint={t('downloads.queueCard.stallHint')} />
        <NumberField label={t('downloads.queueCard.maxStallsLabel')} value={form.maxStalls} onChange={(n) => set('maxStalls', n)}
          suffix={t('downloads.queueCard.suffixRounds')} hint={t('downloads.queueCard.maxStallsHint')} />
        <NumberField label={t('downloads.queueCard.agingLabel')} value={form.agingStepMin} onChange={(n) => set('agingStepMin', n)}
          min={0} suffix={t('downloads.queueCard.suffixMinStep')} hint={t('downloads.queueCard.agingHint')} />
      </div>

      <div className="flex items-center justify-between gap-3 flex-wrap">
        <label className="flex items-center gap-2 text-sm text-text-primary">
          <input
            type="checkbox"
            checked={form.rotationEnabled}
            onChange={(e) => set('rotationEnabled', e.target.checked)}
            className="accent-cyan-500 w-4 h-4"
          />
          <span>{t('downloads.queueCard.rotationLabel')}</span>
          <span className="text-[10px] uppercase tracking-wide text-amber-400 bg-amber-500/10 border border-amber-500/20 px-1.5 py-0.5 rounded">{t('downloads.queueCard.experimental')}</span>
        </label>
      </div>
      <p className="text-[11px] text-text-muted -mt-3">
        {t('downloads.queueCard.rotationHint')}
      </p>

      <div className="flex items-center justify-between gap-3 flex-wrap">
        <label className="flex items-center gap-2 text-sm text-text-primary">
          <input
            type="checkbox"
            checked={form.autoPromoteArr}
            onChange={(e) => set('autoPromoteArr', e.target.checked)}
            className="accent-cyan-500 w-4 h-4"
          />
          <span>{t('downloads.queueCard.arrPromoteLabel')}</span>
          <LiveBadge />
        </label>
      </div>
      <p className="text-[11px] text-text-muted -mt-3">
        <Trans i18nKey="downloads.queueCard.arrHint" values={{ path: 'Downloads/<categoria>/' }} components={{ code: <code /> }} />
      </p>

      <div className="flex flex-col gap-1.5 border-t border-default pt-4">
        <label className="text-sm text-text-primary font-medium flex items-center gap-2">
          {t('downloads.queueCard.copyModeLabel')}
          <LiveBadge />
        </label>
        <select
          value={form.transferConcurrencyMode || 'auto'}
          onChange={(e) => set('transferConcurrencyMode', e.target.value as DownloadsQueueSettings['transferConcurrencyMode'])}
          className="input-field min-h-[44px]"
        >
          <option value="auto">{t('downloads.queueCard.copyModeAuto')}</option>
          <option value="serial">{t('downloads.queueCard.copyModeSerial')}</option>
          <option value="parallel">{t('downloads.queueCard.copyModeParallel')}</option>
        </select>
        <span className="text-[11px] text-text-muted">
          {t('downloads.queueCard.copyModeHint')}
        </span>
      </div>

      <div className="flex items-center justify-between gap-3 border-t border-default pt-4">
        <div className="text-xs">
          {error && <span className="text-red-400">{error}</span>}
          {notice && <span className="text-green-400">{notice}</span>}
        </div>
        <button
          onClick={handleSave}
          disabled={saving}
          className="flex items-center gap-2 bg-cyan-500 hover:bg-cyan-600 disabled:opacity-50 text-white font-semibold px-4 py-2 rounded-lg text-sm transition-colors min-h-[44px]"
        >
          {saving ? <Loader2 className="w-4 h-4 animate-spin" /> : <Save className="w-4 h-4" />}
          {t('downloads.queueCard.save')}
        </button>
      </div>
    </div>
  )
}
