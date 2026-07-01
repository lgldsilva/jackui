import { describe, it, expect } from 'vitest'
import type { AISlotScore } from '../../api/client'
import { runStatus, lastSuccessLabel, persistenceLabel, absoluteDateTime } from './benchHistory'

const mk = (over: Partial<AISlotScore>): AISlotScore => ({
  slotId: 'groq:m',
  provider: 'groq',
  model: 'm',
  accuracy: 0,
  avgLatencyMs: 0,
  composite: 0,
  samples: 0,
  ...over,
})

describe('runStatus', () => {
  it('prefere o outcome registrado pelo backend', () => {
    expect(runStatus(mk({ lastOutcome: 'ok' }))).toBe('ok')
    expect(runStatus(mk({ lastOutcome: 'error' }))).toBe('error')
    expect(runStatus(mk({ lastOutcome: 'incomplete' }))).toBe('incomplete')
  })
  it('outcome inválido cai no fallback da medição ao vivo', () => {
    expect(runStatus(mk({ lastOutcome: 'garbage' }))).not.toBe('garbage')
  })
  it('fallback legado (sem histórico): incomplete > samples > failure > unknown', () => {
    expect(runStatus(mk({ incomplete: true }))).toBe('incomplete')
    expect(runStatus(mk({ samples: 5 }))).toBe('ok')
    expect(runStatus(mk({ failureReason: 'boom' }))).toBe('error')
    expect(runStatus(mk({}))).toBe('unknown')
  })
})

describe('lastSuccessLabel', () => {
  it('mostra a data relativa quando houve sucesso', () => {
    const iso = new Date(Date.now() - 3 * 3_600_000).toISOString()
    expect(lastSuccessLabel(mk({ lastSuccessAt: iso, lastOutcome: 'error' }))).toMatch(/^último OK: /)
  })
  it('"nunca deu certo" quando rodou mas nunca teve sucesso', () => {
    expect(lastSuccessLabel(mk({ lastOutcome: 'error' }))).toBe('nunca deu certo')
  })
  it('vazio para linha legada sem histórico algum', () => {
    expect(lastSuccessLabel(mk({ samples: 3 }))).toBe('')
  })
  it('vazio para "incomplete" com score real — não é "nunca deu certo"', () => {
    expect(lastSuccessLabel(mk({ lastOutcome: 'incomplete', samples: 4, composite: 0.7 }))).toBe('')
  })
})

describe('persistenceLabel', () => {
  it('só aparece com erro atual e streak >= 2', () => {
    expect(persistenceLabel(mk({ lastOutcome: 'error', consecutiveFailures: 1 }))).toBe('')
    expect(persistenceLabel(mk({ lastOutcome: 'ok', consecutiveFailures: 5 }))).toBe('')
    expect(persistenceLabel(mk({ lastOutcome: 'error', consecutiveFailures: 3 }))).toMatch(/3 falhas seguidas/)
  })
  it('inclui "desde <data>" quando firstFailureAt está presente', () => {
    const iso = new Date(Date.now() - 5 * 86_400_000).toISOString()
    expect(persistenceLabel(mk({ lastOutcome: 'error', consecutiveFailures: 4, firstFailureAt: iso }))).toMatch(/desde /)
  })
  it('sem firstFailureAt não acrescenta "desde"', () => {
    expect(persistenceLabel(mk({ lastOutcome: 'error', consecutiveFailures: 2 }))).not.toMatch(/desde/)
  })
})

describe('absoluteDateTime', () => {
  it('vazio para entrada ausente ou inválida', () => {
    expect(absoluteDateTime(undefined)).toBe('')
    expect(absoluteDateTime('not-a-date')).toBe('')
  })
  it('formata uma data válida', () => {
    expect(absoluteDateTime('2020-01-01T00:00:00Z')).not.toBe('')
  })
})
