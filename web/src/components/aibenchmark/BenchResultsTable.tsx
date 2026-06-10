import { useMemo } from 'react'
import { Loader2, Play } from 'lucide-react'
import type { AISlotScore } from '../../api/client'
import SortableTh from '../SortableTh'
import { useTableSort } from '../../lib/useTableSort'
import { BENCH_DESC_FIRST, formatCost, needsRerun, sortScores, type BenchSortKey } from './benchSort'

function scoreCells(s: AISlotScore) {
  return {
    acc: `${Math.round(s.accuracy * 100)}%`,
    lat: s.avgLatencyMs > 0 ? `${s.avgLatencyMs} ms` : '—',
    comp: s.composite > 0 ? s.composite.toFixed(3) : '—',
    cost: formatCost(s.costPer1M),
  }
}

type RowProps = {
  onRunSingle: (provider: string, model: string) => void
  busy: boolean
  runningSlotId: string | null
}

function scoreRow(s: AISlotScore, { onRunSingle, busy, runningSlotId }: RowProps) {
  const { acc, lat, comp, cost } = scoreCells(s)
  const isThisRunning = runningSlotId === s.slotId
  return (
    <tr key={s.slotId} className="border-t border-default/60">
      <td className="py-1.5 pr-3 text-text-primary">
        <div className="flex items-center gap-2">
          <span>{s.model}</span>
          <button
            onClick={() => onRunSingle(s.provider, s.model)}
            disabled={busy}
            title={`Rodar benchmark para ${s.model}`}
            className="p-1 text-text-muted hover:text-green-500 hover:bg-surface disabled:opacity-30 rounded-md transition-colors"
          >
            {isThisRunning ? (
              <Loader2 className="w-3.5 h-3.5 animate-spin text-green-500" />
            ) : (
              <Play className="w-3.5 h-3.5" />
            )}
          </button>
          <span className="text-text-muted text-xs font-normal">({s.provider})</span>
        </div>
      </td>
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

function scoreCard(s: AISlotScore, { onRunSingle, busy, runningSlotId }: RowProps) {
  const { acc, lat, comp, cost } = scoreCells(s)
  const isThisRunning = runningSlotId === s.slotId
  return (
    <div key={s.slotId} className="rounded-lg border border-default/60 bg-surface/40 p-3 flex flex-col gap-2">
      <div className="flex items-center justify-between gap-2 min-w-0">
        <div>
          <div className="text-text-primary text-sm truncate">{s.model}</div>
          <div className="text-text-muted text-xs">{s.provider}</div>
        </div>
        <button
          onClick={() => onRunSingle(s.provider, s.model)}
          disabled={busy}
          title={`Rodar benchmark para ${s.model}`}
          className="p-1 text-text-muted hover:text-green-500 hover:bg-surface disabled:opacity-30 rounded-md transition-colors"
        >
          {isThisRunning ? (
            <Loader2 className="w-4 h-4 animate-spin text-green-500" />
          ) : (
            <Play className="w-4 h-4" />
          )}
        </button>
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

// Benchmark results: sortable desktop table + mobile stacked cards, both
// consuming the SAME sorted list (the chosen order persists across reloads).
export default function BenchResultsTable({ results, onRunSingle, busy, runningSlotId }: {
  results: AISlotScore[]
} & RowProps) {
  const sort = useTableSort<BenchSortKey>({
    defaultKey: 'score',
    defaultDir: 'desc',
    descFirst: BENCH_DESC_FIRST,
    persistKey: 'aibench.sort',
  })
  const sorted = useMemo(
    () => sortScores(results, sort.sortKey, sort.dir),
    [results, sort.sortKey, sort.dir]
  )
  const rowProps: RowProps = { onRunSingle, busy, runningSlotId }
  return (
    <>
      {/* Desktop: table */}
      <div className="hidden sm:block overflow-x-auto">
        <table className="w-full text-sm">
          <thead>
            <tr className="text-text-secondary text-xs text-left">
              <SortableTh label="Modelo" columnKey="model" sort={sort} />
              <SortableTh label="Acurácia" columnKey="accuracy" sort={sort} align="right" />
              <SortableTh label="Latência" columnKey="latency" sort={sort} align="right" />
              <SortableTh label="Custo" columnKey="cost" sort={sort} align="right" />
              <SortableTh label="Score" columnKey="score" sort={sort} align="right" />
              <SortableTh label="Falha" columnKey="failure" sort={sort} className="py-1" />
            </tr>
          </thead>
          <tbody>{sorted.map(s => scoreRow(s, rowProps))}</tbody>
        </table>
      </div>
      {/* Mobile: stacked cards */}
      <div className="flex flex-col gap-2 sm:hidden">
        {sorted.map(s => scoreCard(s, rowProps))}
      </div>
    </>
  )
}
