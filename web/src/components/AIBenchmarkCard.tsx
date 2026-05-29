import { useEffect, useState } from 'react'
import { Loader2, Play, Save, Cpu } from 'lucide-react'
import {
  aiBenchmarkStatus, runAIBenchmark, saveAICases,
  AIStatus, AISlotScore, AIBenchmarkCase,
} from '../api/client'

// AIBenchmarkCard lets an admin measure each model in the title-identification
// chain (accuracy + latency → composite score), re-rank the chain by that
// score, and edit the test set the score is computed from. Editing the cases is
// the "modifiable benchmark" the product needs: tune it to the releases you
// actually grab and the chain re-orders for them.

// The cases editor uses a plain textarea (one "raw => expected" per line) — far
// less fiddly on mobile than a grid of paired inputs, and trivially round-trips.
function casesToText(cases: AIBenchmarkCase[]): string {
  return cases.map(c => `${c.raw} => ${c.expect}`).join('\n')
}
function textToCases(text: string): AIBenchmarkCase[] {
  return text.split('\n')
    .map(line => {
      const i = line.indexOf('=>')
      if (i < 0) return null
      const raw = line.slice(0, i).trim()
      const expect = line.slice(i + 2).trim()
      return raw ? { raw, expect } : null
    })
    .filter((c): c is AIBenchmarkCase => c !== null)
}

function scoreRow(s: AISlotScore) {
  const acc = `${Math.round(s.accuracy * 100)}%`
  const lat = s.avgLatencyMs > 0 ? `${s.avgLatencyMs} ms` : '—'
  const comp = s.composite > 0 ? s.composite.toFixed(3) : '—'
  return (
    <tr key={s.slotId} className="border-t border-gray-700/60">
      <td className="py-1.5 pr-3 text-gray-200">{s.model}<span className="text-gray-500 text-xs block">{s.provider}</span></td>
      <td className="py-1.5 pr-3 text-right tabular-nums">{acc}</td>
      <td className="py-1.5 pr-3 text-right tabular-nums">{lat}</td>
      <td className="py-1.5 pr-3 text-right tabular-nums font-medium text-green-400">{comp}</td>
      <td className="py-1.5 text-gray-500 text-xs truncate max-w-[10rem]" title={s.failureReason}>{s.failureReason || ''}</td>
    </tr>
  )
}

export default function AIBenchmarkCard() {
  const [status, setStatus] = useState<AIStatus | null>(null)
  const [running, setRunning] = useState(false)
  const [casesText, setCasesText] = useState('')
  const [saving, setSaving] = useState(false)
  const [msg, setMsg] = useState('')

  useEffect(() => {
    aiBenchmarkStatus()
      // Normalize: the Go backend marshals empty slices as null, which would
      // crash status.chain.map / status.results.length downstream.
      .then(s => { s = { ...s, chain: s.chain || [], results: s.results || [], cases: s.cases || [] }; setStatus(s); setCasesText(casesToText(s.cases)) })
      .catch(() => setStatus({ enabled: false, chain: [], results: [], cases: [] }))
  }, [])

  if (!status) {
    return (
      <section className="card flex items-center gap-2 text-gray-400">
        <Loader2 className="w-4 h-4 animate-spin" /> Carregando IA…
      </section>
    )
  }

  if (!status.enabled) {
    return (
      <section className="card flex flex-col gap-2">
        <h2 className="text-lg font-semibold text-gray-100 flex items-center gap-2"><Cpu className="w-5 h-5" /> Identificação por IA</h2>
        <p className="text-sm text-gray-400">
          Desabilitada — defina <code className="text-gray-300">GROQ_API_KEY</code>,{' '}
          <code className="text-gray-300">OPENROUTER_API_KEY</code> ou{' '}
          <code className="text-gray-300">OLLAMA_BASE_URL</code> para ativar a limpeza de títulos por IA.
        </p>
      </section>
    )
  }

  const run = async () => {
    // Intentional: each run spends free-tier quota (remote models are rate-limited).
    if (!confirm('Rodar o benchmark consome cota dos modelos free (testa cada modelo várias vezes). Continuar?')) return
    setRunning(true); setMsg('')
    try {
      const results = await runAIBenchmark()
      setStatus(s => s ? { ...s, results } : s)
      setMsg('Benchmark concluído — chain adotada pelo melhor score.')
    } catch (e: any) {
      setMsg(e?.response?.data?.error || 'Falha (pode ter excedido o tempo; recarregue p/ ver o resultado salvo).')
    } finally { setRunning(false) }
  }

  const save = async () => {
    setSaving(true); setMsg('')
    try {
      const cases = await saveAICases(textToCases(casesText))
      setCasesText(casesToText(cases))
      setMsg(`Casos salvos (${cases.length}).`)
    } catch (e: any) {
      setMsg(e?.response?.data?.error || 'Falha ao salvar os casos.')
    } finally { setSaving(false) }
  }

  return (
    <section className="card flex flex-col gap-4">
      <div className="flex items-center justify-between gap-2">
        <h2 className="text-lg font-semibold text-gray-100 flex items-center gap-2"><Cpu className="w-5 h-5" /> Identificação por IA</h2>
        <button
          onClick={run}
          disabled={running}
          className="flex items-center gap-1.5 text-sm bg-green-600 hover:bg-green-500 disabled:opacity-50 text-white rounded-lg px-3 py-1.5"
        >
          {running ? <Loader2 className="w-4 h-4 animate-spin" /> : <Play className="w-4 h-4" />}
          {running ? 'Rodando…' : 'Rodar benchmark'}
        </button>
      </div>

      <p className="text-xs text-gray-500">
        Chain atual: {status.chain.map(s => s.id).join(' → ') || '—'}. O benchmark mede acurácia e
        latência por modelo, calcula o score composto (acurácia ÷ √latência) e reordena a chain.
      </p>

      {status.results.length > 0 && (
        <div className="overflow-x-auto">
          <table className="w-full text-sm">
            <thead>
              <tr className="text-gray-400 text-xs text-left">
                <th className="py-1 pr-3 font-medium">Modelo</th>
                <th className="py-1 pr-3 font-medium text-right">Acurácia</th>
                <th className="py-1 pr-3 font-medium text-right">Latência</th>
                <th className="py-1 pr-3 font-medium text-right">Score</th>
                <th className="py-1 font-medium">Falha</th>
              </tr>
            </thead>
            <tbody>{status.results.map(scoreRow)}</tbody>
          </table>
        </div>
      )}

      <div className="flex flex-col gap-1.5">
        <label htmlFor="ai-testcases" className="text-sm text-gray-300">Casos de teste (um por linha: <code className="text-gray-400">nome.do.torrent =&gt; Título Esperado</code>)</label>
        <textarea
          id="ai-testcases"
          value={casesText}
          onChange={e => setCasesText(e.target.value)}
          rows={6}
          spellCheck={false}
          className="w-full bg-gray-900 border border-gray-700 rounded-lg p-2 text-sm font-mono text-gray-200 resize-y"
        />
        <div className="flex items-center gap-3">
          <button
            onClick={save}
            disabled={saving}
            className="flex items-center gap-1.5 text-sm bg-gray-700 hover:bg-gray-600 disabled:opacity-50 text-gray-100 rounded-lg px-3 py-1.5"
          >
            {saving ? <Loader2 className="w-4 h-4 animate-spin" /> : <Save className="w-4 h-4" />}
            Salvar casos
          </button>
          {msg && <span className="text-xs text-gray-400">{msg}</span>}
        </div>
      </div>
    </section>
  )
}
