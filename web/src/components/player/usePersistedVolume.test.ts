import { describe, it, expect, beforeEach } from 'vitest'
import { renderHook } from '@testing-library/react'
import { createRef } from 'react'
import { usePersistedVolume, clampVolume, readPersistedAudio, MUTED_KEY, VOLUME_KEY } from './usePersistedVolume'
import { save, remove } from '../../lib/storage'

// jsdom implementa volume/muted como propriedades comuns, mas não emite
// `volumechange` sozinho — o browser emite. Disparamos manualmente, que é
// exatamente o que os controles nativos e o atalho M provocam.
function mediaEl(): HTMLMediaElement {
  const el = document.createElement('video')
  return el
}
function changeVolume(el: HTMLMediaElement, patch: { muted?: boolean; volume?: number }) {
  if (patch.muted !== undefined) el.muted = patch.muted
  if (patch.volume !== undefined) el.volume = patch.volume
  el.dispatchEvent(new Event('volumechange'))
}

beforeEach(() => {
  remove(MUTED_KEY)
  remove(VOLUME_KEY)
})

describe('clampVolume', () => {
  it('mantém valores válidos e corta fora da faixa', () => {
    expect(clampVolume(0.4)).toBe(0.4)
    expect(clampVolume(1.7)).toBe(1)
    expect(clampVolume(-2)).toBe(0)
  })
  it('cai no default com valor corrompido', () => {
    expect(clampVolume('abc')).toBe(1)
    expect(clampVolume(null)).toBe(1)
    expect(clampVolume(undefined)).toBe(1)
  })
})

describe('usePersistedVolume', () => {
  it('grava o mudo quando o usuário muta', () => {
    const el = mediaEl()
    const ref = createRef<HTMLMediaElement>() as { current: HTMLMediaElement | null }
    ref.current = el
    renderHook(() => usePersistedVolume({ mediaRef: ref }))

    changeVolume(el, { muted: true })

    expect(readPersistedAudio().muted).toBe(true)
  })

  // O sintoma relatado: deixei mudo, o próximo play voltou com som. Cada play
  // monta um <video> NOVO (key = audioElementKey), então o estado tem que ser
  // restaurado no elemento novo.
  it('restaura o mudo num elemento recém-montado', () => {
    save(MUTED_KEY, true)
    save(VOLUME_KEY, 0.3)
    const fresh = mediaEl()
    const ref = { current: fresh as HTMLMediaElement | null }

    renderHook(() => usePersistedVolume({ mediaRef: ref }))

    expect(fresh.muted).toBe(true)
    expect(fresh.volume).toBe(0.3)
  })

  it('restaura o volume mesmo sem mudo', () => {
    save(VOLUME_KEY, 0.55)
    const el = mediaEl()
    const ref = { current: el as HTMLMediaElement | null }

    renderHook(() => usePersistedVolume({ mediaRef: ref }))

    expect(el.volume).toBe(0.55)
    expect(el.muted).toBe(false)
  })

  // Sem preferência salva o player continua como sempre foi: com som, volume máximo.
  it('usa o default quando não há nada salvo', () => {
    const el = mediaEl()
    const ref = { current: el as HTMLMediaElement | null }

    renderHook(() => usePersistedVolume({ mediaRef: ref }))

    expect(el.muted).toBe(false)
    expect(el.volume).toBe(1)
  })

  // O motor gapless toca o áudio pelo próprio <audio>; o <video> fica mudo por
  // imposição do motor. Isso não pode ser confundido com "o usuário mutou",
  // senão o silêncio vaza para os próximos plays sem gapless.
  it('não persiste o mudo imposto pelo motor gapless', () => {
    const el = mediaEl()
    const ref = { current: el as HTMLMediaElement | null }
    renderHook(() => usePersistedVolume({ mediaRef: ref, forceMuted: true }))

    expect(el.muted).toBe(true)
    changeVolume(el, { muted: true })

    expect(readPersistedAudio().muted).toBe(false)
  })

  it('mantém o <video> mudo com o motor ativo mesmo sem preferência de mudo', () => {
    save(MUTED_KEY, false)
    const el = mediaEl()
    const ref = { current: el as HTMLMediaElement | null }

    renderHook(() => usePersistedVolume({ mediaRef: ref, forceMuted: true }))

    expect(el.muted).toBe(true)
  })

  // Quando o motor gapless desliga (forceMuted vira false), o elemento volta a
  // respeitar a preferência do usuário em vez de ficar mudo para sempre.
  it('reaplica a preferência quando o motor gapless desliga', () => {
    const el = mediaEl()
    const ref = { current: el as HTMLMediaElement | null }
    const { rerender } = renderHook(
      ({ forceMuted }) => usePersistedVolume({ mediaRef: ref, forceMuted }),
      { initialProps: { forceMuted: true } },
    )
    expect(el.muted).toBe(true)

    rerender({ forceMuted: false })

    expect(el.muted).toBe(false)
  })

  it('reaplica no loadstart (troca de src sem remontar)', () => {
    const el = mediaEl()
    const ref = { current: el as HTMLMediaElement | null }
    renderHook(() => usePersistedVolume({ mediaRef: ref }))

    save(MUTED_KEY, true)
    el.dispatchEvent(new Event('loadstart'))

    expect(el.muted).toBe(true)
  })
})
