import { describe, it, expect } from 'vitest'
import type { AISlotScore } from '../../api/client'
import { nextSortState } from '../../lib/useTableSort'
import { BENCH_DESC_FIRST, sortScores } from './benchSort'

// Linhas cobrindo os casos reais da tabela: modelo grátis, modelo pago, modelo
// local que falhou (latência 0 / composite 0) e modelo rate-limited (failure).
const mk = (over: Partial<AISlotScore>): AISlotScore => ({
  slotId: `${over.provider ?? 'groq'}:${over.model ?? 'm'}`,
  provider: 'groq',
  model: 'm',
  accuracy: 0,
  avgLatencyMs: 0,
  composite: 0,
  samples: 10,
  ...over,
})

const free = mk({ model: 'llama-free', accuracy: 0.9, avgLatencyMs: 800, composite: 0.5, costPer1M: 0 })
const paid = mk({ model: 'gpt-paid', provider: 'openrouter', accuracy: 0.95, avgLatencyMs: 400, composite: 0.7, costPer1M: 2.5 })
const cheap = mk({ model: 'cheap', provider: 'openrouter', accuracy: 0.6, avgLatencyMs: 1200, composite: 0.2, costPer1M: 0.05 })
const failed = mk({ model: 'broken-local', provider: 'ollama', accuracy: 0, avgLatencyMs: 0, composite: 0, failureReason: 'connection refused' })
const limited = mk({ model: 'limited', accuracy: 0.3, avgLatencyMs: 600, composite: 0.1, failureReason: 'rate limit exceeded', incomplete: true })

const all = [failed, cheap, paid, limited, free]
const models = (rows: AISlotScore[]) => rows.map(r => r.model)

describe('sortScores por coluna', () => {
  it('model asc/desc: localeCompare, desempate por provider', () => {
    expect(models(sortScores(all, 'model', 'asc')))
      .toEqual(['broken-local', 'cheap', 'gpt-paid', 'limited', 'llama-free'])
    expect(models(sortScores(all, 'model', 'desc')))
      .toEqual(['llama-free', 'limited', 'gpt-paid', 'cheap', 'broken-local'])
    const dupModel = [mk({ model: 'x', provider: 'b' }), mk({ model: 'x', provider: 'a' })]
    expect(sortScores(dupModel, 'model', 'asc').map(r => r.provider)).toEqual(['a', 'b'])
  })

  it('accuracy asc/desc', () => {
    expect(models(sortScores(all, 'accuracy', 'asc')))
      .toEqual(['broken-local', 'limited', 'cheap', 'llama-free', 'gpt-paid'])
    expect(models(sortScores(all, 'accuracy', 'desc')))
      .toEqual(['gpt-paid', 'llama-free', 'cheap', 'limited', 'broken-local'])
  })

  it('latency: 0 (falha) afunda nas DUAS direções', () => {
    expect(models(sortScores(all, 'latency', 'asc')))
      .toEqual(['gpt-paid', 'limited', 'llama-free', 'cheap', 'broken-local'])
    expect(models(sortScores(all, 'latency', 'desc')))
      .toEqual(['cheap', 'llama-free', 'limited', 'gpt-paid', 'broken-local'])
  })

  it('cost: 0 é grátis legítimo — NÃO afunda (vem primeiro no asc)', () => {
    const asc = models(sortScores(all, 'cost', 'asc'))
    // grátis (custo 0) na frente; o desempate composite desc ordena entre eles
    expect(asc).toEqual(['llama-free', 'limited', 'broken-local', 'cheap', 'gpt-paid'])
    expect(models(sortScores(all, 'cost', 'desc')))
      .toEqual(['gpt-paid', 'cheap', 'llama-free', 'limited', 'broken-local'])
  })

  it('score: composite 0 afunda nas DUAS direções', () => {
    expect(models(sortScores(all, 'score', 'asc')))
      .toEqual(['limited', 'cheap', 'llama-free', 'gpt-paid', 'broken-local'])
    expect(models(sortScores(all, 'score', 'desc')))
      .toEqual(['gpt-paid', 'llama-free', 'cheap', 'limited', 'broken-local'])
  })

  it('failure (coluna "Status"): ordena por severidade error→incomplete→ok', () => {
    const ok = mk({ model: 'ok-model', lastOutcome: 'ok', composite: 0.9 })
    const inc = mk({ model: 'inc-model', lastOutcome: 'incomplete', composite: 0.5 })
    const err = mk({ model: 'err-model', lastOutcome: 'error', failureReason: 'boom', samples: 0, composite: 0 })
    const rows = [ok, inc, err]
    expect(models(sortScores(rows, 'failure', 'asc'))).toEqual(['err-model', 'inc-model', 'ok-model'])
    expect(models(sortScores(rows, 'failure', 'desc'))).toEqual(['ok-model', 'inc-model', 'err-model'])
  })
})

describe('sortScores determinismo', () => {
  it('empate no primário → composite desc, depois model asc', () => {
    const a = mk({ model: 'bbb', accuracy: 0.5, composite: 0.9 })
    const b = mk({ model: 'aaa', accuracy: 0.5, composite: 0.3 })
    const c = mk({ model: 'ccc', accuracy: 0.5, composite: 0.3 })
    expect(models(sortScores([b, c, a], 'accuracy', 'asc'))).toEqual(['bbb', 'aaa', 'ccc'])
  })

  it('estável/determinístico: mesma entrada em qualquer ordem → mesma saída, sem mutar a original', () => {
    const input = [...all]
    const out1 = sortScores(input, 'score', 'desc')
    const out2 = sortScores([...all].reverse(), 'score', 'desc')
    expect(models(out1)).toEqual(models(out2))
    expect(input).toEqual(all) // não mutou
  })
})

describe('nextSortState (toggle do useTableSort)', () => {
  it('clicar na coluna ativa inverte a direção', () => {
    expect(nextSortState({ key: 'score', dir: 'desc' }, 'score', BENCH_DESC_FIRST))
      .toEqual({ key: 'score', dir: 'asc' })
    expect(nextSortState({ key: 'score', dir: 'asc' }, 'score', BENCH_DESC_FIRST))
      .toEqual({ key: 'score', dir: 'desc' })
  })

  it('coluna nova entra com a direção default dela (descFirst → desc)', () => {
    expect(nextSortState({ key: 'score', dir: 'desc' }, 'accuracy', BENCH_DESC_FIRST))
      .toEqual({ key: 'accuracy', dir: 'desc' })
    expect(nextSortState({ key: 'score', dir: 'desc' }, 'latency', BENCH_DESC_FIRST))
      .toEqual({ key: 'latency', dir: 'asc' })
    expect(nextSortState({ key: 'accuracy', dir: 'asc' }, 'model', BENCH_DESC_FIRST))
      .toEqual({ key: 'model', dir: 'asc' })
  })
})
