import { useState, useEffect } from 'react'
import { ListOrdered, Loader2, Save, Zap } from 'lucide-react'
import {
  getDownloadsQueueSettings,
  updateDownloadsQueueSettings,
  DownloadsQueueSettings,
} from '../api/client'

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
  return (
    <span className="inline-flex items-center gap-1 text-[10px] uppercase tracking-wide text-green-400 bg-green-500/10 border border-green-500/20 px-1.5 py-0.5 rounded">
      <Zap className="w-2.5 h-2.5" />ao vivo
    </span>
  )
}

export default function DownloadsQueueCard() {
  const [form, setForm] = useState<DownloadsQueueSettings | null>(null)
  const [loading, setLoading] = useState(true)
  const [saving, setSaving] = useState(false)
  const [error, setError] = useState('')
  const [notice, setNotice] = useState('')

  useEffect(() => {
    getDownloadsQueueSettings()
      .then(setForm)
      .catch((e: unknown) => setError(e instanceof Error ? e.message : String(e)))
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
      setNotice('Salvo e aplicado ao vivo.')
    } catch (e: unknown) {
      setError(e instanceof Error ? e.message : String(e))
    } finally {
      setSaving(false)
    }
  }

  if (loading) {
    return (
      <div className="card flex items-center gap-3 text-text-secondary">
        <Loader2 className="w-4 h-4 animate-spin" />
        Carregando configurações da fila...
      </div>
    )
  }
  if (error && !form) {
    return <div className="card text-red-400 text-sm">Fila indisponível: {error}</div>
  }
  if (!form) return null

  return (
    <div className="card flex flex-col gap-5">
      <div className="flex items-center gap-2">
        <ListOrdered className="w-5 h-5 text-cyan-400" />
        <h2 className="text-lg font-semibold text-text-primary">Fila de Downloads</h2>
        <LiveBadge />
      </div>

      <p className="text-xs text-text-muted -mt-2">
        Quantos downloads rodam ao mesmo tempo e como a fila se comporta. Streaming (tocar agora) não conta no limite.
      </p>

      <div className="grid grid-cols-1 sm:grid-cols-2 gap-3">
        <NumberField label="Ativos (global)" value={form.maxActive} onChange={(n) => set('maxActive', n)}
          suffix="total" hint="Teto do servidor: máximo baixando ao mesmo tempo entre TODOS os usuários." />
        <NumberField label="Ativos por usuário" value={form.perUserMaxActive} onChange={(n) => set('perUserMaxActive', n)}
          min={0} suffix="por user" hint="Máximo que CADA usuário baixa ao mesmo tempo. 0 = sem limite (só o teto global vale)." />
        <NumberField label="Sem seed por" value={form.stallThresholdMin} onChange={(n) => set('stallThresholdMin', n)}
          suffix="min" hint="Tempo sem progresso E sem seeds antes de ir pro fim da fila." />
        <NumberField label="Pausar após" value={form.maxStalls} onChange={(n) => set('maxStalls', n)}
          suffix="voltas" hint="Depois de N voltas sem baixar, pausa o download." />
        <NumberField label="Envelhecimento" value={form.agingStepMin} onChange={(n) => set('agingStepMin', n)}
          min={0} suffix="min/degrau" hint="A cada X min na fila um item de prioridade baixa sobe (anti-fome). 0 desliga." />
      </div>

      <div className="flex items-center justify-between gap-3 flex-wrap">
        <label className="flex items-center gap-2 text-sm text-text-primary">
          <input
            type="checkbox"
            checked={form.rotationEnabled}
            onChange={(e) => set('rotationEnabled', e.target.checked)}
            className="accent-cyan-500 w-4 h-4"
          />
          <span>Rotação automática de fontes</span>
          <span className="text-[10px] uppercase tracking-wide text-amber-400 bg-amber-500/10 border border-amber-500/20 px-1.5 py-0.5 rounded">experimental</span>
        </label>
      </div>
      <p className="text-[11px] text-text-muted -mt-3">
        Quando um download fica sem seed, busca outras fontes do mesmo conteúdo no Jackett e alterna entre elas (round-robin).
      </p>

      <div className="flex items-center justify-between gap-3 flex-wrap">
        <label className="flex items-center gap-2 text-sm text-text-primary">
          <input
            type="checkbox"
            checked={form.autoPromoteArr}
            onChange={(e) => set('autoPromoteArr', e.target.checked)}
            className="accent-cyan-500 w-4 h-4"
          />
          <span>Promover downloads dos *arr</span>
          <LiveBadge />
        </label>
      </div>
      <p className="text-[11px] text-text-muted -mt-3">
        Downloads vindos do Sonarr/Radarr (via Transmission RPC) são gravados direto em <code>Downloads/&lt;categoria&gt;/</code> — a mesma árvore que o Transmission usa — para os *arr importarem como esperado. Requer o diretório compartilhado (JACKUI_SHARED_DIR) configurado.
      </p>

      <div className="flex flex-col gap-1.5 border-t border-default pt-4">
        <label className="text-sm text-text-primary font-medium flex items-center gap-2">
          Cópia ao promover/mover
          <LiveBadge />
        </label>
        <select
          value={form.transferConcurrencyMode || 'auto'}
          onChange={(e) => set('transferConcurrencyMode', e.target.value as DownloadsQueueSettings['transferConcurrencyMode'])}
          className="input-field min-h-[44px]"
        >
          <option value="auto">Automático (recomendado) — serializa em HD, paraleliza em SSD</option>
          <option value="serial">Sempre uma de cada vez</option>
          <option value="parallel">Sempre em paralelo</option>
        </select>
        <span className="text-[11px] text-text-muted">
          Em HD mecânico, cópias paralelas competem pela cabeça do disco e ficam lentas (seek thrashing).
          O automático detecta o disco de destino e escolhe sozinho.
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
          Salvar
        </button>
      </div>
    </div>
  )
}
