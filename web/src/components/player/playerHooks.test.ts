import { describe, it, expect } from 'vitest'
import { backstopStuck, backstopShouldFire, hlsFatalAction, startGapNudgeTarget } from './playerHooks'

// Regressão do bug do Star Wars (a376440b): um H264/AAC/MP4 (browser-safe) que
// trava por falta de dados (moov do MP4 ainda não baixou → readyState 0,
// buffered 0) NÃO pode disparar o backstop e forçar transcode. Transcodar
// H264→H264 da mesma fonte fria não acelera nada. O backstop só existe pra a
// falha SILENCIOSA de HEVC do Safari (codec que de fato precisa de transcode).

describe('backstopStuck', () => {
  it('detecta stall: readyState<2 + currentTime<0.1 + buffered<0.5', () => {
    expect(backstopStuck(0, 0, 0)).toBe(true)
    expect(backstopStuck(1, 0.05, 0.2)).toBe(true)
  })
  it('não é stall quando já há frame tocável (readyState>=2)', () => {
    expect(backstopStuck(2, 0, 0)).toBe(false)
    expect(backstopStuck(4, 0, 0)).toBe(false)
  })
  it('não é stall quando o tempo já andou', () => {
    expect(backstopStuck(0, 0.5, 0)).toBe(false)
  })
  it('não é stall quando já há buffer suficiente', () => {
    expect(backstopStuck(1, 0, 1)).toBe(false)
  })
})

describe('backstopShouldFire', () => {
  const stuck = true

  it('NÃO dispara para codec browser-safe (needsTranscode=false) — o FIX', () => {
    // Caso Star Wars: H264/AAC/MP4, travado por moov/rede, com GPU disponível.
    expect(backstopShouldFire(stuck, false, true)).toBe(false)
  })

  it('dispara para codec que precisa transcode (HEVC) com encoder', () => {
    expect(backstopShouldFire(stuck, true, true)).toBe(true)
  })

  it('dispara quando o codec é desconhecido (probe não chegou) com encoder', () => {
    // Preserva o comportamento histórico: na dúvida, tenta o fallback.
    expect(backstopShouldFire(stuck, undefined, true)).toBe(true)
  })

  it('NÃO dispara sem encoder de GPU, mesmo precisando transcode', () => {
    expect(backstopShouldFire(stuck, true, false)).toBe(false)
    expect(backstopShouldFire(stuck, undefined, false)).toBe(false)
  })

  it('NÃO dispara quando não está travado, qualquer codec', () => {
    expect(backstopShouldFire(false, true, true)).toBe(false)
    expect(backstopShouldFire(false, undefined, true)).toBe(false)
    expect(backstopShouldFire(false, false, true)).toBe(false)
  })
})

// Regressão do "28 Years Later" (arquivo local MKV/H264 no Safari): o transcode
// EVENT/live bufferiza 12s começando em buffered.start=0.000002, mas o
// currentTime fica em 0 (logo ANTES do buffer) → Safari nunca chega a canplay e
// trava. O nudge avança o currentTime pra dentro do buffer.
describe('startGapNudgeTarget', () => {
  it('nudga quando travado em 0 com buraco sub-tick (0.000002)', () => {
    expect(startGapNudgeTarget(0, 0.000002)).toBeCloseTo(0.050002, 5)
  })
  it('nudga no histórico initial_offset de 1,4s', () => {
    expect(startGapNudgeTarget(0, 1.4)).toBeCloseTo(1.45, 5)
  })
  it('NÃO nudga sem buffer ainda', () => {
    expect(startGapNudgeTarget(0, null)).toBeNull()
  })
  it('NÃO nudga quando o buffer já cobre o t=0 (gap<=0)', () => {
    expect(startGapNudgeTarget(0, 0)).toBeNull()
    expect(startGapNudgeTarget(0.1, 0.05)).toBeNull()
  })
  it('NÃO nudga quando o tempo já andou (>0.25) — playback normal', () => {
    expect(startGapNudgeTarget(5, 5.2)).toBeNull()
    expect(startGapNudgeTarget(0.3, 0.5)).toBeNull()
  })
  it('NÃO nudga com gap grande demais (>1.5s) — seria pular conteúdo real', () => {
    expect(startGapNudgeTarget(0, 3)).toBeNull()
  })
})

describe('hlsFatalAction', () => {
  // Espelha o enum Hls.ErrorTypes do hls.js (string literals).
  const TYPES = { NETWORK_ERROR: 'networkError', MEDIA_ERROR: 'mediaError' }

  it('NETWORK_ERROR → startLoad (recarrega o stream)', () => {
    expect(hlsFatalAction(TYPES.NETWORK_ERROR, TYPES)).toBe('startLoad')
  })
  it('MEDIA_ERROR → recoverMedia', () => {
    expect(hlsFatalAction(TYPES.MEDIA_ERROR, TYPES)).toBe('recoverMedia')
  })
  it('qualquer outro tipo → destroy', () => {
    expect(hlsFatalAction('muxError', TYPES)).toBe('destroy')
    expect(hlsFatalAction('otherError', TYPES)).toBe('destroy')
    expect(hlsFatalAction('', TYPES)).toBe('destroy')
  })
})
