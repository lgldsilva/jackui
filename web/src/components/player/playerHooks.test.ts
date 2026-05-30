import { describe, it, expect } from 'vitest'
import { backstopStuck, backstopShouldFire } from './playerHooks'

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
    expect(backstopStuck(1, 0, 1.0)).toBe(false)
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
