import { useState, useEffect } from 'react'
import { SlidersHorizontal, Loader2, Save, AlertTriangle, Zap } from 'lucide-react'
import {
  getStreamSettings,
  updateStreamSettings,
  StreamSettings,
  StreamSettingsDefaults,
  StorageBackend,
} from '../api/client'

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
  }
}

// NumberField: input numérico tocável (>=44px, 16px anti-zoom iOS). placeholder
// mostra o default da lib quando o valor é 0 ("usar default").
function NumberField(props: {
  label: string
  value: number
  onChange: (n: number) => void
  placeholder?: string | number
  suffix?: string
  hint?: string
}) {
  const { label, value, onChange, placeholder, suffix, hint } = props
  return (
    <label className="flex flex-col gap-1">
      <span className="text-xs text-gray-400">{label}</span>
      <div className="flex items-center gap-2">
        <input
          type="number"
          min={0}
          inputMode="numeric"
          value={value || ''}
          placeholder={placeholder != null ? String(placeholder) : undefined}
          onChange={(e) => onChange(Math.max(0, Number(e.target.value) || 0))}
          className="input-field min-h-[44px]"
        />
        {suffix && <span className="text-xs text-gray-500 flex-shrink-0">{suffix}</span>}
      </div>
      {hint && <span className="text-[11px] text-gray-600">{hint}</span>}
    </label>
  )
}

function Badge({ kind }: { kind: 'live' | 'restart' }) {
  if (kind === 'live') {
    return (
      <span className="inline-flex items-center gap-1 text-[10px] uppercase tracking-wide text-green-400 bg-green-500/10 border border-green-500/20 px-1.5 py-0.5 rounded">
        <Zap className="w-2.5 h-2.5" />ao vivo
      </span>
    )
  }
  return (
    <span className="inline-flex items-center gap-1 text-[10px] uppercase tracking-wide text-amber-400 bg-amber-500/10 border border-amber-500/20 px-1.5 py-0.5 rounded">
      <AlertTriangle className="w-2.5 h-2.5" />requer reinício
    </span>
  )
}

function SectionTitle({ title, badge }: { title: string; badge: 'live' | 'restart' }) {
  return (
    <div className="flex items-center gap-2">
      <h3 className="text-sm font-medium text-gray-200">{title}</h3>
      <Badge kind={badge} />
    </div>
  )
}

export default function StreamSettingsCard() {
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
      .catch((e: unknown) => setError(e instanceof Error ? e.message : String(e)))
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
          ? 'Salvo. Algumas mudanças (storage/conexões/cache) só valem após reiniciar o container.'
          : 'Salvo e aplicado ao vivo.',
      )
    } catch (e: unknown) {
      setError(e instanceof Error ? e.message : String(e))
    } finally {
      setSaving(false)
    }
  }

  if (loading) {
    return (
      <div className="card flex items-center gap-3 text-gray-400">
        <Loader2 className="w-4 h-4 animate-spin" />
        Carregando configurações do streamer...
      </div>
    )
  }
  if (error && !form) {
    return <div className="card text-red-400 text-sm">Streaming indisponível: {error}</div>
  }
  if (!form || !defaults) return null

  return (
    <div className="card flex flex-col gap-5">
      <div className="flex items-center gap-2">
        <SlidersHorizontal className="w-5 h-5 text-green-500" />
        <h2 className="text-lg font-semibold text-gray-100">Streamer &amp; Performance</h2>
      </div>

      {/* Banda — ao vivo */}
      <div className="flex flex-col gap-3">
        <SectionTitle title="Banda" badge="live" />
        <div className="grid grid-cols-1 sm:grid-cols-2 gap-3">
          <NumberField label="Download" value={form.downMbps} onChange={(n) => set('downMbps', n)}
            placeholder="0 = ilimitado" suffix="MB/s" />
          <NumberField label="Upload" value={form.upMbps} onChange={(n) => set('upMbps', n)}
            placeholder="0 = ilimitado" suffix="MB/s" />
        </div>
      </div>

      {/* Memória / leitura — ao vivo */}
      <div className="flex flex-col gap-3">
        <SectionTitle title="Memória de leitura (readahead)" badge="live" />
        <NumberField label="Readahead por stream" value={form.readaheadMB} onChange={(n) => set('readaheadMB', n)}
          placeholder={defaults.readaheadMB} suffix="MB"
          hint="Buffer lido à frente por sessão. Maior = playback mais suave em rede/disco lento, porém mais RAM por stream. Vale no próximo play." />
      </div>

      {/* Cache — requer reinício */}
      <div className="flex flex-col gap-3">
        <SectionTitle title="Cache em disco" badge="restart" />
        <NumberField label="Limite do cache" value={form.maxCacheGB} onChange={(n) => set('maxCacheGB', n)}
          placeholder="0 = ilimitado" suffix="GB"
          hint="Ao ultrapassar, entradas inativas mais antigas são removidas (favoritos protegidos)." />
      </div>

      {/* Avançado / hardware — requer reinício */}
      <div className="flex flex-col gap-3">
        <SectionTitle title="Avançado (hardware/peers)" badge="restart" />
        <label className="flex flex-col gap-1">
          <span className="text-xs text-gray-400">Backend de storage</span>
          <select
            value={form.storageBackend}
            onChange={(e) => set('storageBackend', e.target.value as StorageBackend)}
            className="input-field min-h-[44px]"
          >
            <option value="file">file — grava direto no disco (padrão)</option>
            <option value="mmap">mmap — mapeia em memória (page cache; seek mais rápido)</option>
          </select>
        </label>
        <div className="grid grid-cols-1 sm:grid-cols-2 gap-3">
          <NumberField label="Conexões por torrent" value={form.maxConnsPerTorrent} onChange={(n) => set('maxConnsPerTorrent', n)}
            placeholder={defaults.maxConnsPerTorrent} />
          <NumberField label="Half-open" value={form.halfOpenConns} onChange={(n) => set('halfOpenConns', n)}
            placeholder={defaults.halfOpenConns} />
          <NumberField label="Peers (high water)" value={form.peersHighWater} onChange={(n) => set('peersHighWater', n)}
            placeholder={defaults.peersHighWater} />
          <NumberField label="Piece hashers (CPU)" value={form.pieceHashers} onChange={(n) => set('pieceHashers', n)}
            placeholder={defaults.pieceHashers} />
        </div>
        <p className="text-[11px] text-gray-600">Campo em 0 = usar o default da biblioteca (mostrado no placeholder).</p>
      </div>

      {/* Footer: save + feedback */}
      <div className="flex flex-col gap-2 sm:flex-row sm:items-center sm:justify-between border-t border-gray-700 pt-4">
        {notice && <span className="text-xs text-green-400">{notice}</span>}
        {error && <span className="text-xs text-red-400">{error}</span>}
        {!notice && !error && <span />}
        <button
          onClick={handleSave}
          disabled={saving}
          className="btn-primary flex items-center justify-center gap-2 min-h-[44px] disabled:opacity-50"
        >
          {saving ? <Loader2 className="w-4 h-4 animate-spin" /> : <Save className="w-4 h-4" />}
          Salvar
        </button>
      </div>
    </div>
  )
}
