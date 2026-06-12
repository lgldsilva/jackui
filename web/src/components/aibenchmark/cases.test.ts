import { describe, it, expect } from 'vitest'
import { casesToText, textToCases } from './cases'

describe('benchmark cases textarea round-trip', () => {
  it('parses a plain rename line (no task prefix → no task field)', () => {
    const cases = textToCases('Inception.2010.1080p => Inception - 2010')
    expect(cases).toEqual([{ raw: 'Inception.2010.1080p', expect: 'Inception - 2010' }])
  })

  it('parses a [schedule] / [identify] task prefix', () => {
    const cases = textToCases([
      '[schedule] toda segunda-feira às 07h00 => weekly:1:7:0',
      '[identify] Sicario.2015 => Sicario',
      'The.Matrix.1999 => The Matrix - 1999',
    ].join('\n'))
    expect(cases).toEqual([
      { raw: 'toda segunda-feira às 07h00', expect: 'weekly:1:7:0', task: 'schedule' },
      { raw: 'Sicario.2015', expect: 'Sicario', task: 'identify' },
      { raw: 'The.Matrix.1999', expect: 'The Matrix - 1999' },
    ])
  })

  it('serializes the task prefix only for non-rename tasks (retrocompat)', () => {
    const text = casesToText([
      { raw: 'Inception.2010', expect: 'Inception - 2010' },
      { raw: 'Inception.2010', expect: 'Inception - 2010', task: 'rename' }, // rename → no prefix
      { raw: 'toda quinta às 20h', expect: 'weekly:4:20:0', task: 'schedule' },
    ])
    expect(text).toBe([
      'Inception.2010 => Inception - 2010',
      'Inception.2010 => Inception - 2010',
      '[schedule] toda quinta às 20h => weekly:4:20:0',
    ].join('\n'))
  })

  it('round-trips a mixed multi-task set unchanged', () => {
    const original = [
      'The.Bear.S03E01 => The Bear - S03E01',
      '[schedule] a cada 2 horas => interval:120',
      '[identify] Parasite.2019 => Parasite',
    ].join('\n')
    expect(casesToText(textToCases(original))).toBe(original)
  })

  it('drops malformed lines (no =>)', () => {
    expect(textToCases('just some noise\nfoo => bar')).toEqual([{ raw: 'foo', expect: 'bar' }])
  })
})
