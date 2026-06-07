import { useEffect, useState } from 'react'
import { Loader2, Play, Save, Cpu, RefreshCw } from 'lucide-react'
import {
  aiBenchmarkStatus, runAIBenchmark, runAIBenchmarkIncomplete, saveAICases, saveAICostConfig,
  AIStatus, AISlotScore, AIBenchmarkCase, AICostConfig,
} from '../api/client'
import { useConfirm } from './ConfirmDialog'

// AIBenchmarkCard lets an admin measure each model in the rename/identification
// chain (accuracy + latency → composite score), re-rank the chain by that
// score, and edit the test set the score is computed from. Editing the cases is
// the "modifiable benchmark" the product needs: tune it to the releases you
// actually grab and the chain re-orders for them.
//
// The benchmark measures the FULL rename extraction (título + ano for movies,
// série + temporada/episódio for TV), so the expected label carries that
// structure inline — coherent with what "Renomear e Organizar via IA" produces.

// The cases editor uses a plain textarea (one "raw => expected" per line) — far
// less fiddly on mobile than a grid of paired inputs, and trivially round-trips.
// The expected label encodes the structure: "Filme - Ano", "Série - S03E07",
// "Série - E01", or just a bare title (then only the title is scored).
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

// formatCost renders $/1M; small local-energy costs (cents) keep 3 decimals so
// they don't round to "$0.00".
function formatCost(c?: number): string {
  if (!c || c <= 0) return 'grátis'
  const decimals = c < 0.1 ? 3 : 2
  return `$${c.toFixed(decimals)}/1M`
}

function scoreCells(s: AISlotScore) {
  return {
    acc: `${Math.round(s.accuracy * 100)}%`,
    lat: s.avgLatencyMs > 0 ? `${s.avgLatencyMs} ms` : '—',
    comp: s.composite > 0 ? s.composite.toFixed(3) : '—',
    cost: formatCost(s.costPer1M),
  }
}

// A result is worth re-running ("Rodar faltantes") when it was left incomplete OR
// failed with a rate limit — the latter also covers results saved before the
// incomplete flag existed, so the button shows for pre-existing rate-limited rows.
function needsRerun(s: AISlotScore): boolean {
  return !!s.incomplete || /rate limit/i.test(s.failureReason || '')
}

function scoreRow(s: AISlotScore) {
  const { acc, lat, comp, cost } = scoreCells(s)
  return (
    <tr key={s.slotId} className="border-t border-default/60">
      <td className="py-1.5 pr-3 text-text-primary">{s.model}<span className="text-text-muted text-xs block">{s.provider}</span></td>
      <td className="py-1.5 pr-3 text-right tabular-nums">{acc}</td>
      <td className="py-1.5 pr-3 text-right tabular-nums">{lat}</td>
      <td className="py-1.5 pr-3 text-right tabular-nums text-text-secondary">{cost}</td>
      <td className="py-1.5 pr-3 text-right tabular-nums font-medium text-green-400">{comp}</td>
      <td className="py-1.5 text-text-muted text-xs truncate max-w-[10rem]" title={s.failureReason}>
        {needsRerun(s) && <span className="text-amber-400">faltante</span>}
        {needsRerun(s) && s.failureReason ? ' · ' : ''}
        {s.failureReason || ''}
      </td>
    </tr>
  )
}

function scoreCard(s: AISlotScore) {
  const { acc, lat, comp, cost } = scoreCells(s)
  return (
    <div key={s.slotId} className="rounded-lg border border-default/60 bg-surface/40 p-3 flex flex-col gap-2">
      <div className="min-w-0">
        <div className="text-text-primary text-sm truncate">{s.model}</div>
        <div className="text-text-muted text-xs">{s.provider}</div>
      </div>
      <div className="grid grid-cols-4 gap-2 text-xs">
        <div>
          <div className="text-text-muted">Acurácia</div>
          <div className="tabular-nums text-text-primary">{acc}</div>
        </div>
        <div>
          <div className="text-text-muted">Latência</div>
          <div className="tabular-nums text-text-primary">{lat}</div>
        </div>
        <div>
          <div className="text-text-muted">Custo</div>
          <div className="tabular-nums text-text-secondary">{cost}</div>
        </div>
        <div>
          <div className="text-text-muted">Score</div>
          <div className="tabular-nums font-medium text-green-400">{comp}</div>
        </div>
      </div>
      {needsRerun(s) && (
        <div className="text-amber-400 text-xs">faltante — rode os faltantes</div>
      )}
      {s.failureReason && (
        <div className="text-text-muted text-xs break-words">Falha: {s.failureReason}</div>
      )}
    </div>
  )
}

export default function AIBenchmarkCard() {
  const confirm = useConfirm()
  const [status, setStatus] = useState<AIStatus | null>(null)
  const [running, setRunning] = useState(false)
  const [runningIncomplete, setRunningIncomplete] = useState(false)
  const [casesText, setCasesText] = useState('')
  const [saving, setSaving] = useState(false)
  const [savingCost, setSavingCost] = useState(false)
  const [cost, setCost] = useState<AICostConfig>({ maxCostPer1M: 0, kwhPrice: 0, localWatts: 250 })
  const [msg, setMsg] = useState('')

  const emptyCost: AICostConfig = { maxCostPer1M: 0, kwhPrice: 0, localWatts: 250 }
  useEffect(() => {
    aiBenchmarkStatus()
      // Normalize: the Go backend marshals empty slices as null, which would
      // crash status.chain.map / status.results.length downstream.
      .then(s => { s = { ...s, chain: s.chain || [], results: s.results || [], cases: s.cases || [], cost: s.cost || emptyCost }; setStatus(s); setCasesText(casesToText(s.cases)); setCost(s.cost) })
      .catch(() => setStatus({ enabled: false, chain: [], results: [], cases: [], cost: emptyCost }))
  }, [])

  if (!status) {
    return (
      <section className="card flex items-center gap-2 text-text-secondary">
        <Loader2 className="w-4 h-4 animate-spin" /> Carregando IA…
      </section>
    )
  }

  if (!status.enabled) {
    return (
      <section className="card flex flex-col gap-2">
        <h2 className="text-lg font-semibold text-text-primary flex items-center gap-2"><Cpu className="w-5 h-5" /> Identificação por IA</h2>
        <p className="text-sm text-text-secondary">
          Desabilitada — defina <code className="text-text-primary">GROQ_API_KEY</code>,{' '}
          <code className="text-text-primary">OPENROUTER_API_KEY</code> ou{' '}
          <code className="text-text-primary">OLLAMA_BASE_URL</code> para ativar a limpeza de títulos por IA.
        </p>
      </section>
    )
  }

  const run = async () => {
    // Intentional: each run spends free-tier quota (remote models are rate-limited).
    const ok = await confirm({
      title: 'Rodar benchmark',
      message: 'Rodar o benchmark consome cota dos modelos free (testa cada modelo várias vezes). Continuar?',
      confirmLabel: 'Rodar',
      destructive: false,
    })
    if (!ok) return
    setRunning(true); setMsg('')
    try {
      const results = await runAIBenchmark()
      setStatus(s => s ? { ...s, results } : s)
      setMsg('Benchmark concluído — chain adotada pelo melhor score.')
    } catch (e: any) {
      setMsg(e?.response?.data?.error || 'Falha (pode ter excedido o tempo; recarregue p/ ver o resultado salvo).')
    } finally { setRunning(false) }
  }

  // Re-runs ONLY the models left incomplete (cases cut by a rate limit). Meant to
  // be clicked LATER (even a day after) so the retry lands outside the limit window.
  const runIncomplete = async () => {
    setRunningIncomplete(true); setMsg('')
    try {
      const results = await runAIBenchmarkIncomplete()
      setStatus(s => s ? { ...s, results } : s)
      const left = results.filter(needsRerun).length
      setMsg(left > 0 ? `Faltantes re-rodados — ${left} ainda incompleto(s) (tente fora da janela do rate limit).` : 'Faltantes re-rodados — todos completos agora.')
    } catch (e: any) {
      setMsg(e?.response?.data?.error || 'Falha (pode ter excedido o tempo; recarregue p/ ver o resultado salvo).')
    } finally { setRunningIncomplete(false) }
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

  const saveCost = async () => {
    setSavingCost(true); setMsg('')
    try {
      const saved = await saveAICostConfig(cost)
      setCost(saved)
      setMsg('Custos salvos — rode o benchmark p/ aplicar no ranking.')
    } catch (e: any) {
      setMsg(e?.response?.data?.error || 'Falha ao salvar os custos.')
    } finally { setSavingCost(false) }
  }

  const busy = running || runningIncomplete
  const incompleteCount = status.results.filter(needsRerun).length

  return (
    <section className="card flex flex-col gap-4">
      <div className="flex items-center justify-between gap-2 flex-wrap">
        <h2 className="text-lg font-semibold text-text-primary flex items-center gap-2"><Cpu className="w-5 h-5" /> Identificação por IA</h2>
        <div className="flex items-center gap-2">
          {incompleteCount > 0 && (
            <button
              onClick={runIncomplete}
              disabled={busy}
              title="Re-roda só os modelos cortados por rate limit. Rode mais tarde (até no dia seguinte) p/ sair da janela do limite."
              className="flex items-center gap-1.5 text-sm bg-surface hover:bg-surface-hover border border-default disabled:opacity-50 text-text-primary rounded-lg px-3 py-1.5"
            >
              {runningIncomplete ? <Loader2 className="w-4 h-4 animate-spin" /> : <RefreshCw className="w-4 h-4" />}
              {runningIncomplete ? 'Rodando…' : `Rodar faltantes (${incompleteCount})`}
            </button>
          )}
          <button
            onClick={run}
            disabled={busy}
            className="flex items-center gap-1.5 text-sm bg-green-600 hover:bg-green-500 disabled:opacity-50 text-white rounded-lg px-3 py-1.5"
          >
            {running ? <Loader2 className="w-4 h-4 animate-spin" /> : <Play className="w-4 h-4" />}
            {running ? 'Rodando…' : 'Rodar benchmark'}
          </button>
        </div>
      </div>

      <p className="text-xs text-text-muted">
        Chain atual: {status.chain.map(s => s.id).join(' → ') || '—'}. O benchmark mede a extração
        completa (título + ano para filmes, série + temporada/episódio para TV) e a latência por
        modelo, calcula o score composto (acurácia ÷ √latência ÷ (1 + custo/1M)) e reordena a chain.
        A latência é a <strong className="text-text-secondary">mediana</strong> das chamadas (desconta
        o tempo de carga do modelo); o <strong className="text-text-secondary">custo</strong> ($/1M
        tokens, em USD) penaliza modelos caros, então grátis/barato sobe. Ajuste abaixo o teto de
        custo, a tarifa de energia e a potência da GPU — modelos locais não são grátis (gastam
        energia: latência × tokens × potência × tarifa).
      </p>

      <div className="grid grid-cols-1 sm:grid-cols-3 gap-3">
        <label className="text-xs text-text-muted flex flex-col gap-1">
          <span>Teto p/ pagos ($/1M)</span>
          <input type="number" step="0.01" min="0" value={cost.maxCostPer1M}
            onChange={e => setCost({ ...cost, maxCostPer1M: Number.parseFloat(e.target.value) || 0 })}
            className="bg-surface border border-default rounded-lg px-2 py-1 text-sm text-text-primary tabular-nums" />
        </label>
        <label className="text-xs text-text-muted flex flex-col gap-1">
          <span>Tarifa energia ($/kWh)</span>
          <input type="number" step="0.01" min="0" value={cost.kwhPrice}
            onChange={e => setCost({ ...cost, kwhPrice: Number.parseFloat(e.target.value) || 0 })}
            className="bg-surface border border-default rounded-lg px-2 py-1 text-sm text-text-primary tabular-nums" />
        </label>
        <label className="text-xs text-text-muted flex flex-col gap-1">
          <span>Potência GPU (W)</span>
          <input type="number" step="10" min="0" value={cost.localWatts}
            onChange={e => setCost({ ...cost, localWatts: Number.parseFloat(e.target.value) || 0 })}
            className="bg-surface border border-default rounded-lg px-2 py-1 text-sm text-text-primary tabular-nums" />
        </label>
      </div>
      <div>
        <button onClick={saveCost} disabled={savingCost}
          className="flex items-center gap-1.5 text-sm bg-surface hover:bg-surface-hover border border-default disabled:opacity-50 text-text-primary rounded-lg px-3 py-1.5">
          {savingCost ? <Loader2 className="w-4 h-4 animate-spin" /> : <Save className="w-4 h-4" />}
          Salvar custos
        </button>
      </div>

      {status.results.length > 0 && (
        <>
          {/* Desktop: table */}
          <div className="hidden sm:block overflow-x-auto">
            <table className="w-full text-sm">
              <thead>
                <tr className="text-text-secondary text-xs text-left">
                  <th className="py-1 pr-3 font-medium">Modelo</th>
                  <th className="py-1 pr-3 font-medium text-right">Acurácia</th>
                  <th className="py-1 pr-3 font-medium text-right">Latência</th>
                  <th className="py-1 pr-3 font-medium text-right">Custo</th>
                  <th className="py-1 pr-3 font-medium text-right">Score</th>
                  <th className="py-1 font-medium">Falha</th>
                </tr>
              </thead>
              <tbody>{status.results.map(scoreRow)}</tbody>
            </table>
          </div>
          {/* Mobile: stacked cards */}
          <div className="flex flex-col gap-2 sm:hidden">
            {status.results.map(scoreCard)}
          </div>
        </>
      )}

      <div className="flex flex-col gap-1.5">
        <label htmlFor="ai-testcases" className="text-sm text-text-primary">
          Casos de teste (um por linha: <code className="text-text-secondary">nome.do.torrent =&gt; Rótulo Esperado</code>).
          O rótulo separa a estrutura: <code className="text-text-secondary">Filme - Ano</code>,{' '}
          <code className="text-text-secondary">Série - S03E07</code>,{' '}
          <code className="text-text-secondary">Série - E01</code>, ou só o título.
        </label>
        <textarea
          id="ai-testcases"
          value={casesText}
          onChange={e => setCasesText(e.target.value)}
          rows={6}
          spellCheck={false}
          className="w-full bg-surface border border-default rounded-lg p-2 text-base sm:text-sm font-mono text-text-primary resize-y"
        />
        <div className="flex items-center gap-3">
          <button
            onClick={save}
            disabled={saving}
            className="flex items-center gap-1.5 text-sm bg-surface-tertiary hover:bg-surface-tertiary disabled:opacity-50 text-text-primary rounded-lg px-3 py-1.5"
          >
            {saving ? <Loader2 className="w-4 h-4 animate-spin" /> : <Save className="w-4 h-4" />}
            Salvar casos
          </button>
          {msg && <span className="text-xs text-text-secondary">{msg}</span>}
        </div>
      </div>
    </section>
  )
}
