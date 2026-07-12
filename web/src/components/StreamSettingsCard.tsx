import { useState, useEffect } from 'react'
import { useTranslation } from 'react-i18next'
import { SlidersHorizontal, Loader2, Save, AlertTriangle, Zap } from 'lucide-react'
import {
  getStreamSettings,
  updateStreamSettings,
  StreamSettings,
  StreamSettingsDefaults,
  StorageBackend,
} from '../api/client'
import { errMessage } from '../lib/errMessage'

const MIB = 1024 * 1024

// UI-friendly form: rates em MB/s (a API usa bytes/seg). Conversão no load/save.
type Form = {
  downMbps: number
  upMbps: number
  readaheadMB: number
  storageBackend: StorageBackend
  maxConnsPerTorrent: number
  halfOpenConns: number
  peersHighWater: number
  pieceHashers: number
  maxCacheGB: number
  // Editado como texto (um tracker por linha); convertido pra []string no save.
  seedTrackersText: string
  hlsMediaRenditions: boolean
}

function toForm(s: StreamSettings): Form {
  return {
    downMbps: Math.round(s.maxDownloadRate / MIB),
    upMbps: Math.round(s.maxUploadRate / MIB),
    readaheadMB: s.readaheadMB,
    storageBackend: s.storageBackend,
    maxConnsPerTorrent: s.maxConnsPerTorrent,
    halfOpenConns: s.halfOpenConns,
    peersHighWater: s.peersHighWater,
    pieceHashers: s.pieceHashers,
    maxCacheGB: s.maxCacheGB,
    seedTrackersText: (s.seedTrackers ?? []).join('\n'),
    hlsMediaRenditions: s.hlsMediaRenditions ?? false,
  }
}

function toPayload(f: Form): StreamSettings {
  return {
    maxDownloadRate: f.downMbps * MIB,
    maxUploadRate: f.upMbps * MIB,
    readaheadMB: f.readaheadMB,
    storageBackend: f.storageBackend,
    maxConnsPerTorrent: f.maxConnsPerTorrent,
    halfOpenConns: f.halfOpenConns,
    peersHighWater: f.peersHighWater,
    pieceHashers: f.pieceHashers,
    maxCacheGB: f.maxCacheGB,
    seedTrackers: f.seedTrackersText
      .split('\n')
      .map((l) => l.trim())
      .filter(Boolean),
    hlsMediaRenditions: f.hlsMediaRenditions,
  }
}

// NumberField: input numérico tocável (>=44px, 16px anti-zoom iOS). placeholder
// mostra o default da lib quando o valor é 0 ("usar default").
function NumberField(props: Readonly<{
  label: string
  value: number
  onChange: (n: number) => void
  placeholder?: string | number
  suffix?: string
  hint?: string
}>) {
  const { label, value, onChange, placeholder, suffix, hint } = props
  return (
    <label className="flex flex-col gap-1">
      <span className="text-xs text-text-secondary">{label}</span>
      <div className="flex items-center gap-2">
        <input
          type="number"
          min={0}
          inputMode="numeric"
          value={value || ''}
          placeholder={placeholder == null ? undefined : String(placeholder)}
          onChange={(e) => onChange(Math.max(0, Number(e.target.value) || 0))}
          className="input-field min-h-[44px]"
        />
        {suffix && <span className="text-xs text-text-muted flex-shrink-0">{suffix}</span>}
      </div>
      {hint && <span className="text-[11px] text-text-muted">{hint}</span>}
    </label>
  )
}

function Badge({ kind }: Readonly<{ kind: 'live' | 'restart' }>) {
  const { t } = useTranslation()
  if (kind === 'live') {
    return (
      <span className="inline-flex items-center gap-1 text-[10px] uppercase tracking-wide text-green-400 bg-green-500/10 border border-green-500/20 px-1.5 py-0.5 rounded">
        <Zap className="w-2.5 h-2.5" />{t('stream.badge_live')}
      </span>
    )
  }
  return (
    <span className="inline-flex items-center gap-1 text-[10px] uppercase tracking-wide text-amber-400 bg-amber-500/10 border border-amber-500/20 px-1.5 py-0.5 rounded">
      <AlertTriangle className="w-2.5 h-2.5" />{t('stream.badge_restart')}
    </span>
  )
}

function SectionTitle({ title, badge }: Readonly<{ title: string; badge: 'live' | 'restart' }>) {
  return (
    <div className="flex items-center gap-2">
      <h3 className="text-sm font-medium text-text-primary">{title}</h3>
      <Badge kind={badge} />
    </div>
  )
}

export default function StreamSettingsCard() {
  const { t } = useTranslation()
  const [form, setForm] = useState<Form | null>(null)
  const [defaults, setDefaults] = useState<StreamSettingsDefaults | null>(null)
  const [loading, setLoading] = useState(true)
  const [saving, setSaving] = useState(false)
  const [error, setError] = useState('')
  const [notice, setNotice] = useState('')

  useEffect(() => {
    getStreamSettings()
      .then((s) => {
        setForm(toForm(s))
        setDefaults(s.defaults)
      })
      .catch((e: unknown) => setError(errMessage(e)))
      .finally(() => setLoading(false))
  }, [])

  const set = <K extends keyof Form>(key: K, val: Form[K]) =>
    setForm((f) => (f ? { ...f, [key]: val } : f))

  const handleSave = async () => {
    if (!form) return
    setSaving(true)
    setError('')
    setNotice('')
    try {
      const { restartRequired } = await updateStreamSettings(toPayload(form))
      setNotice(
        restartRequired
          ? t('stream.saved_restart')
          : t('stream.saved_live'),
      )
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
        {t('stream.loading')}
      </div>
    )
  }
  if (error && !form) {
    return <div className="card text-red-400 text-sm">{t('stream.unavailable', { error })}</div>
  }
  if (!form || !defaults) return null

  return (
    <div className="card flex flex-col gap-5">
      <div className="flex items-center gap-2">
        <SlidersHorizontal className="w-5 h-5 text-green-500" />
        <h2 className="text-lg font-semibold text-text-primary">{t('stream.title')}</h2>
      </div>

      {/* Banda — ao vivo */}
      <div className="flex flex-col gap-3">
        <SectionTitle title={t('stream.band')} badge="live" />
        <div className="grid grid-cols-1 sm:grid-cols-2 gap-3">
          <NumberField label={t('stream.download')} value={form.downMbps} onChange={(n) => set('downMbps', n)}
            placeholder={t('stream.unlimited_placeholder')} suffix="MB/s" />
          <NumberField label={t('stream.upload')} value={form.upMbps} onChange={(n) => set('upMbps', n)}
            placeholder={t('stream.unlimited_placeholder')} suffix="MB/s" />
        </div>
      </div>

      {/* Seed contínuo por tracker — ao vivo */}
      <div className="flex flex-col gap-3">
        <SectionTitle title={t('stream.seed_tracker_title')} badge="live" />
        <label className="flex flex-col gap-1">
          <span className="text-xs text-text-secondary">{t('stream.seed_trackers_label')}</span>
          <textarea
            value={form.seedTrackersText}
            onChange={(e) => set('seedTrackersText', e.target.value)}
            rows={3}
            placeholder={'jackui\noutro-tracker-privado'}
            spellCheck={false}
            className="input-field font-mono text-sm min-h-[80px]"
          />
          <span className="text-[11px] text-text-muted">
            {t('stream.seed_trackers_help')}
          </span>
        </label>
      </div>

      {/* HLS renditions de áudio/legenda (Phase 2 M2b) — ao vivo */}
      <div className="flex flex-col gap-3">
        <SectionTitle title={t('stream.hls_renditions_title')} badge="live" />
        <label className="flex items-start gap-3 cursor-pointer">
          <input
            type="checkbox"
            checked={form.hlsMediaRenditions}
            onChange={(e) => set('hlsMediaRenditions', e.target.checked)}
            className="mt-1 h-5 w-5 shrink-0 accent-accent"
          />
          <span className="flex flex-col gap-0.5">
            <span className="text-sm text-text-primary">{t('stream.hls_renditions_label')}</span>
            <span className="text-[11px] text-text-muted">{t('stream.hls_renditions_help')}</span>
          </span>
        </label>
      </div>

      {/* Memória / leitura — ao vivo */}
      <div className="flex flex-col gap-3">
        <SectionTitle title={t('stream.readahead_title')} badge="live" />
        <NumberField label={t('stream.readahead_label')} value={form.readaheadMB} onChange={(n) => set('readaheadMB', n)}
          placeholder={defaults.readaheadMB} suffix="MB"
          hint={t('stream.readahead_hint')} />
      </div>

      {/* Cache — requer reinício */}
      <div className="flex flex-col gap-3">
        <SectionTitle title={t('stream.cache_disk_title')} badge="restart" />
        <NumberField label={t('stream.cache_limit_label')} value={form.maxCacheGB} onChange={(n) => set('maxCacheGB', n)}
          placeholder={t('stream.unlimited_placeholder')} suffix="GB"
          hint={t('stream.cache_limit_hint')} />
      </div>

      {/* Avançado / hardware — requer reinício */}
      <div className="flex flex-col gap-3">
        <SectionTitle title={t('stream.advanced_title')} badge="restart" />
        <label className="flex flex-col gap-1">
          <span className="text-xs text-text-secondary">{t('stream.storage_backend')}</span>
          <select
            value={form.storageBackend}
            onChange={(e) => set('storageBackend', e.target.value as StorageBackend)}
            className="input-field min-h-[44px]"
          >
            <option value="file">{t('stream.storage_file')}</option>
            <option value="mmap">{t('stream.storage_mmap')}</option>
          </select>
        </label>
        <div className="grid grid-cols-1 sm:grid-cols-2 gap-3">
          <NumberField label={t('stream.conns_per_torrent')} value={form.maxConnsPerTorrent} onChange={(n) => set('maxConnsPerTorrent', n)}
            placeholder={defaults.maxConnsPerTorrent} />
          <NumberField label="Half-open" value={form.halfOpenConns} onChange={(n) => set('halfOpenConns', n)}
            placeholder={defaults.halfOpenConns} />
          <NumberField label="Peers (high water)" value={form.peersHighWater} onChange={(n) => set('peersHighWater', n)}
            placeholder={defaults.peersHighWater} />
          <NumberField label="Piece hashers (CPU)" value={form.pieceHashers} onChange={(n) => set('pieceHashers', n)}
            placeholder={defaults.pieceHashers} />
        </div>
        <p className="text-[11px] text-text-muted">{t('stream.field_zero_hint')}</p>
      </div>

      {/* Footer: save + feedback */}
      <div className="flex flex-col gap-2 sm:flex-row sm:items-center sm:justify-between border-t border-default pt-4">
        {notice && <span className="text-xs text-green-400">{notice}</span>}
        {error && <span className="text-xs text-red-400">{error}</span>}
        {!notice && !error && <span />}
        <button
          onClick={handleSave}
          disabled={saving}
          className="btn-primary flex items-center justify-center gap-2 min-h-[44px] disabled:opacity-50"
        >
          {saving ? <Loader2 className="w-4 h-4 animate-spin" /> : <Save className="w-4 h-4" />}
          {t('stream.save')}
        </button>
      </div>
    </div>
  )
}
