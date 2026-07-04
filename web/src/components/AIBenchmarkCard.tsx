import { useEffect, useRef, useState } from 'react'
import { useTranslation, Trans } from 'react-i18next'
import { Loader2, Play, Save, Cpu, RefreshCw, Square } from 'lucide-react'
import {
  aiBenchmarkStatus, runAIBenchmark, runAIBenchmarkIncomplete, cancelAIBenchmark, saveAICases, saveAICostConfig,
  AIStatus, AICostConfig,
} from '../api/client'
import { useConfirm } from './ConfirmDialog'
import BenchResultsTable from './aibenchmark/BenchResultsTable'
import { needsRerun } from './aibenchmark/benchSort'
import { casesToText, textToCases } from './aibenchmark/cases'

// AIBenchmarkCard lets an admin measure each model in the rename/identification
// chain (accuracy + latency → composite score), re-rank the chain by that
// score, and edit the test set the score is computed from. Editing the cases is
// the "modifiable benchmark" the product needs: tune it to the releases you
// actually grab and the chain re-orders for them.
//
// The benchmark measures the FULL rename extraction (título + ano for movies,
// série + temporada/episódio for TV), so the expected label carries that
// structure inline — coherent with what "Renomear e Organizar via IA" produces.

// The cases editor (textarea round-trip) lives in ./aibenchmark/cases for
// unit-testing the multi-task "[task]" prefix parsing without rendering the card.

export default function AIBenchmarkCard() {
  const { t } = useTranslation()
  const confirm = useConfirm()
  const [status, setStatus] = useState<AIStatus | null>(null)
  const [running, setRunning] = useState(false)
  const [runningIncomplete, setRunningIncomplete] = useState(false)
  const [casesText, setCasesText] = useState('')
  const [saving, setSaving] = useState(false)
  const [savingCost, setSavingCost] = useState(false)
  const [cost, setCost] = useState<AICostConfig>({ maxCostPer1M: 0, kwhPrice: 0, localWatts: 250 })
  const [msg, setMsg] = useState('')
  const [selectedProvider, setSelectedProvider] = useState<string>('')
  const [runningSlotId, setRunningSlotId] = useState<string | null>(null)
  // serverRunning tracks the BACKEND's run tracker — distinct from `running`
  // (this tab's own in-flight POST). A run keeps going server-side even after
  // this tab's request errors out (proxy/browser timeout) or after a reload,
  // so this is what actually decides whether "Cancelar" should show.
  const [serverRunning, setServerRunning] = useState(false)
  const pollRef = useRef<ReturnType<typeof setInterval> | null>(null)

  const stopPolling = () => {
    if (pollRef.current) { clearInterval(pollRef.current); pollRef.current = null }
  }
  // pollStatus refreshes results/running every few seconds while a run is in
  // flight — the backend saves each slot's result AS IT FINISHES (not just at
  // the end), so this surfaces live progress instead of a blank table until
  // the whole batch completes.
  const pollStatus = () => {
    stopPolling()
    pollRef.current = setInterval(async () => {
      if (document.hidden) return // don't poll a backgrounded tab (resumes on return)
      try {
        const s = await aiBenchmarkStatus()
        setStatus(prev => prev ? { ...prev, results: s.results || prev.results } : prev)
        setServerRunning(!!s.running)
        if (!s.running) {
          stopPolling()
          setMsg(t('ai.run_done_background'))
        }
      } catch { /* transient poll error — try again next tick */ }
    }, 5000)
  }
  useEffect(() => stopPolling, [])

  const emptyCost: AICostConfig = { maxCostPer1M: 0, kwhPrice: 0, localWatts: 250 }
  useEffect(() => {
    aiBenchmarkStatus()
      // Normalize: the Go backend marshals empty slices as null, which would
      // crash status.chain.map / status.results.length downstream.
      .then(s => {
        s = { ...s, chain: s.chain || [], results: s.results || [], cases: s.cases || [], cost: s.cost || emptyCost, providers: s.providers || [] }
        setStatus(s); setCasesText(casesToText(s.cases)); setCost(s.cost)
        // A run may still be going from before this page load (another tab, or
        // a reload after this tab's own request timed out) — pick it back up.
        if (s.running) { setServerRunning(true); pollStatus() }
      })
      .catch(() => setStatus({ enabled: false, chain: [], results: [], cases: [], cost: emptyCost, providers: [] }))
  }, [])

  if (!status) {
    return (
      <section className="card flex items-center gap-2 text-text-secondary">
        <Loader2 className="w-4 h-4 animate-spin" /> {t('ai.loading')}
      </section>
    )
  }

  if (!status.enabled) {
    return (
      <section className="card flex flex-col gap-2">
        <h2 className="text-lg font-semibold text-text-primary flex items-center gap-2"><Cpu className="w-5 h-5" /> {t('ai.title')}</h2>
        <p className="text-sm text-text-secondary">
          <Trans i18nKey="ai.disabled" components={{ c: <code className="text-text-primary" /> }} />
        </p>
      </section>
    )
  }

  const run = async () => {
    // Intentional: each run spends free-tier quota (remote models are rate-limited).
    const providerLabel = selectedProvider ? t('ai.for_provider', { provider: selectedProvider }) : ''
    const ok = await confirm({
      title: t('ai.run'),
      message: t('ai.confirm_run_message', { provider: providerLabel }),
      confirmLabel: t('ai.run_short'),
      destructive: false,
    })
    if (!ok) return
    setRunning(true); setMsg('')
    setServerRunning(true); pollStatus()
    try {
      const results = await runAIBenchmark(selectedProvider || undefined)
      setStatus(s => s ? { ...s, results } : s)
      setMsg(t('ai.run_done', { provider: providerLabel }))
      setServerRunning(false); stopPolling()
    } catch (e: any) {
      const serverErr = e?.response?.data?.error
      if (serverErr) {
        setMsg(serverErr)
        setServerRunning(false); stopPolling()
      } else {
        // No response reached us (proxy/browser gave up) — the run itself keeps
        // going server-side by design. Check the tracker instead of assuming
        // it died, and start following it if it's still active.
        try {
          const s = await aiBenchmarkStatus()
          if (s.running) {
            setMsg(t('ai.run_timeout_still_running'))
            setServerRunning(true)
            pollStatus()
          } else {
            setMsg(t('ai.run_failed'))
          }
        } catch {
          setMsg(t('ai.run_failed_timeout'))
        }
      }
    } finally { setRunning(false) }
  }

  // handleCancel stops whatever benchmark run is currently tracked server-side
  // (started by this tab or another). Whatever was already measured stays
  // saved — the backend persists each slot's result as it finishes.
  const handleCancel = async () => {
    try {
      await cancelAIBenchmark()
      setMsg(t('ai.cancel_done'))
    } catch {
      setMsg(t('ai.cancel_nothing'))
    } finally {
      stopPolling()
      setServerRunning(false)
      try {
        const s = await aiBenchmarkStatus()
        setStatus(prev => prev ? { ...prev, results: s.results || prev.results } : prev)
      } catch { /* best-effort refresh */ }
    }
  }

  // Re-runs ONLY the models left incomplete (cases cut by a rate limit). Meant to
  // be clicked LATER (even a day after) so the retry lands outside the limit window.
  const runIncomplete = async () => {
    setRunningIncomplete(true); setMsg('')
    setServerRunning(true); pollStatus()
    try {
      const results = await runAIBenchmarkIncomplete()
      setStatus(s => s ? { ...s, results } : s)
      const left = results.filter(needsRerun).length
      setMsg(left > 0 ? t('ai.rerun_incomplete_left', { count: left }) : t('ai.rerun_incomplete_all'))
      setServerRunning(false); stopPolling()
    } catch (e: any) {
      const serverErr = e?.response?.data?.error
      if (serverErr) {
        setMsg(serverErr)
        setServerRunning(false); stopPolling()
      } else {
        // No response reached us (proxy/browser gave up) — the run itself keeps
        // going server-side by design. Check the tracker instead of assuming
        // it died, and start following it if it's still active.
        try {
          const s = await aiBenchmarkStatus()
          if (s.running) {
            setMsg(t('ai.run_timeout_still_running'))
            setServerRunning(true)
            pollStatus()
          } else {
            setMsg(t('ai.run_failed'))
            setServerRunning(false)
            stopPolling()
          }
        } catch {
          setMsg(t('ai.run_failed_timeout'))
          setServerRunning(false)
          stopPolling()
        }
      }
    } finally { setRunningIncomplete(false) }
  }

  const save = async () => {
    setSaving(true); setMsg('')
    try {
      const cases = await saveAICases(textToCases(casesText))
      setCasesText(casesToText(cases))
      setMsg(t('ai.cases_saved', { count: cases.length }))
    } catch (e: any) {
      setMsg(e?.response?.data?.error || t('ai.cases_save_failed'))
    } finally { setSaving(false) }
  }

  // NOSONAR: false positive by SonarQube hook order rule
  const saveCost = async () => {
    setSavingCost(true); setMsg('')
    try {
      const saved = await saveAICostConfig(cost)
      setCost(saved)
      setMsg(t('ai.cost_saved'))
    } catch (e: any) {
      setMsg(e?.response?.data?.error || t('ai.cost_save_failed'))
    } finally { setSavingCost(false) }
  }

  const runSingle = async (provider: string, model: string) => {
    const slotId = `${provider}:${model}`
    setRunningSlotId(slotId); setMsg('')
    try {
      const results = await runAIBenchmark(provider, model)
      setStatus(s => s ? { ...s, results } : s)
      setMsg(t('ai.run_single_done', { model }))
    } catch (e: any) {
      setMsg(e?.response?.data?.error || t('ai.run_single_failed', { model }))
    } finally { setRunningSlotId(null) }
  }

  const busy = running || runningIncomplete || !!runningSlotId || serverRunning
  const incompleteCount = status.results.filter(needsRerun).length
  const chainLabel = status.chain.map(s => s.id).join(' → ') || '—'

  return (
    <section className="card flex flex-col gap-4">
      <div className="flex items-center justify-between gap-2 flex-wrap">
        <h2 className="text-lg font-semibold text-text-primary flex items-center gap-2"><Cpu className="w-5 h-5" /> {t('ai.title')}</h2>
        {/* w-full + flex-wrap no mobile: o grupo de ações (select + botões)
            quebra dentro do card em vez de vazar pra fora da borda. */}
        <div className="flex items-center gap-2 flex-wrap w-full sm:w-auto justify-start sm:justify-end">
          {status.providers && status.providers.length > 0 && (
            <select
              value={selectedProvider}
              onChange={e => setSelectedProvider(e.target.value)}
              disabled={busy}
              className="bg-surface border border-default rounded-lg px-2.5 py-1.5 text-sm text-text-primary focus:outline-none focus:ring-1 focus:ring-green-600 min-w-0"
            >
              <option value="">{t('ai.all_providers')}</option>
              {status.providers.map(p => (
                <option key={p} value={p}>{p}</option>
              ))}
            </select>
          )}
          {incompleteCount > 0 && (
            <button
              onClick={runIncomplete}
              disabled={busy}
              title={t('ai.rerun_incomplete_title')}
              className="flex items-center gap-1.5 text-sm bg-surface hover:bg-surface-hover border border-default disabled:opacity-50 text-text-primary rounded-lg px-3 py-1.5"
            >
              {runningIncomplete ? <Loader2 className="w-4 h-4 animate-spin" /> : <RefreshCw className="w-4 h-4" />}
              {runningIncomplete ? t('ai.running') : t('ai.run_incomplete', { count: incompleteCount })}
            </button>
          )}
          <button
            onClick={run}
            disabled={busy}
            className="flex items-center gap-1.5 text-sm bg-green-600 hover:bg-green-500 disabled:opacity-50 text-white rounded-lg px-3 py-1.5"
          >
            {running ? <Loader2 className="w-4 h-4 animate-spin" /> : <Play className="w-4 h-4" />}
            {running ? t('ai.running') : t('ai.run')}
          </button>
          {serverRunning && (
            <button
              onClick={handleCancel}
              title={t('ai.cancel_title')}
              className="flex items-center gap-1.5 text-sm bg-red-900/40 hover:bg-red-900/60 border border-red-800 text-red-200 rounded-lg px-3 py-1.5"
            >
              <Square className="w-4 h-4" /> {t('ai.cancel')}
            </button>
          )}
        </div>
      </div>

      <p className="text-xs text-text-muted">
        <Trans i18nKey="ai.explain" values={{ chain: chainLabel }} components={{ s: <strong className="text-text-secondary" /> }} />
      </p>

      <div className="grid grid-cols-1 sm:grid-cols-3 gap-3">
        <label className="text-xs text-text-muted flex flex-col gap-1">
          <span>{t('ai.cost_cap')}</span>
          <input type="number" step="0.01" min="0" value={cost.maxCostPer1M}
            onChange={e => setCost({ ...cost, maxCostPer1M: Number.parseFloat(e.target.value) || 0 })}
            className="bg-surface border border-default rounded-lg px-2 py-1 text-sm text-text-primary tabular-nums" />
        </label>
        <label className="text-xs text-text-muted flex flex-col gap-1">
          <span>{t('ai.energy_price')}</span>
          <input type="number" step="0.01" min="0" value={cost.kwhPrice}
            onChange={e => setCost({ ...cost, kwhPrice: Number.parseFloat(e.target.value) || 0 })}
            className="bg-surface border border-default rounded-lg px-2 py-1 text-sm text-text-primary tabular-nums" />
        </label>
        <label className="text-xs text-text-muted flex flex-col gap-1">
          <span>{t('ai.gpu_power')}</span>
          <input type="number" step="10" min="0" value={cost.localWatts}
            onChange={e => setCost({ ...cost, localWatts: Number.parseFloat(e.target.value) || 0 })}
            className="bg-surface border border-default rounded-lg px-2 py-1 text-sm text-text-primary tabular-nums" />
        </label>
      </div>
      <div>
        <button onClick={saveCost} disabled={savingCost}
          className="flex items-center gap-1.5 text-sm bg-surface hover:bg-surface-hover border border-default disabled:opacity-50 text-text-primary rounded-lg px-3 py-1.5">
          {savingCost ? <Loader2 className="w-4 h-4 animate-spin" /> : <Save className="w-4 h-4" />}
          {t('ai.save_costs')}
        </button>
      </div>

      {status.results.length > 0 && (
        <BenchResultsTable
          results={status.results}
          onRunSingle={runSingle}
          busy={busy}
          runningSlotId={runningSlotId}
        />
      )}

      <div className="flex flex-col gap-1.5">
        <label htmlFor="ai-testcases" className="text-sm text-text-primary">
          <Trans i18nKey="ai.cases_help" components={{ c: <code className="text-text-secondary" /> }} />
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
            {t('ai.save_cases')}
          </button>
          {msg && <span className="text-xs text-text-secondary">{msg}</span>}
        </div>
      </div>
    </section>
  )
}
