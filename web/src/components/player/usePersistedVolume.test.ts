import { describe, it, expect, beforeEach } from 'vitest'
import { renderHook } from '@testing-library/react'
import { createRef } from 'react'
import { usePersistedVolume, clampVolume, readPersistedAudio, MUTED_KEY, VOLUME_KEY } from './usePersistedVolume'
import { save, remove } from '../../lib/storage'

// jsdom implements volume/muted as plain properties, but doesn't emit
// `volumechange` by itself — the browser does. We fire it manually, which is
// exactly what the native controls and the M shortcut trigger.
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
  it('keeps valid values and clamps out-of-range ones', () => {
    expect(clampVolume(0.4)).toBe(0.4)
    expect(clampVolume(1.7)).toBe(1)
    expect(clampVolume(-2)).toBe(0)
  })
  it('falls back to the default on a corrupted value', () => {
    expect(clampVolume('abc')).toBe(1)
    expect(clampVolume(null)).toBe(1)
    expect(clampVolume(undefined)).toBe(1)
  })
})

describe('usePersistedVolume', () => {
  it('persists muted when the user mutes', () => {
    const el = mediaEl()
    const ref = createRef<HTMLMediaElement>() as { current: HTMLMediaElement | null }
    ref.current = el
    renderHook(() => usePersistedVolume({ mediaRef: ref }))

    changeVolume(el, { muted: true })

    expect(readPersistedAudio().muted).toBe(true)
  })

  // The reported symptom: muted it, the next play came back with sound. Each play
  // mounts a NEW <video> (key = audioElementKey), so the state has to be
  // restored on the new element.
  it('restores muted on a freshly mounted element', () => {
    save(MUTED_KEY, true)
    save(VOLUME_KEY, 0.3)
    const fresh = mediaEl()
    const ref = { current: fresh as HTMLMediaElement | null }

    renderHook(() => usePersistedVolume({ mediaRef: ref }))

    expect(fresh.muted).toBe(true)
    expect(fresh.volume).toBe(0.3)
  })

  it('restores volume even without mute', () => {
    save(VOLUME_KEY, 0.55)
    const el = mediaEl()
    const ref = { current: el as HTMLMediaElement | null }

    renderHook(() => usePersistedVolume({ mediaRef: ref }))

    expect(el.volume).toBe(0.55)
    expect(el.muted).toBe(false)
  })

  // Without a saved preference the player stays as it always was: with sound, max volume.
  it('uses the default when nothing is saved', () => {
    const el = mediaEl()
    const ref = { current: el as HTMLMediaElement | null }

    renderHook(() => usePersistedVolume({ mediaRef: ref }))

    expect(el.muted).toBe(false)
    expect(el.volume).toBe(1)
  })

  // The gapless engine plays the audio through its own <audio>; the <video> is muted by
  // engine imposition. That must not be confused with "the user muted",
  // otherwise the silence leaks into the next plays without gapless.
  it('does not persist the mute imposed by the gapless engine', () => {
    const el = mediaEl()
    const ref = { current: el as HTMLMediaElement | null }
    renderHook(() => usePersistedVolume({ mediaRef: ref, forceMuted: true }))

    expect(el.muted).toBe(true)
    changeVolume(el, { muted: true })

    expect(readPersistedAudio().muted).toBe(false)
  })

  it('keeps the <video> muted with the engine active even without a mute preference', () => {
    save(MUTED_KEY, false)
    const el = mediaEl()
    const ref = { current: el as HTMLMediaElement | null }

    renderHook(() => usePersistedVolume({ mediaRef: ref, forceMuted: true }))

    expect(el.muted).toBe(true)
  })

  // When the gapless engine turns off (forceMuted becomes false), the element goes back to
  // respecting the user's preference instead of staying muted forever.
  it('reapplies the preference when the gapless engine turns off', () => {
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

  it('reapplies on loadstart (src swap without remount)', () => {
    const el = mediaEl()
    const ref = { current: el as HTMLMediaElement | null }
    renderHook(() => usePersistedVolume({ mediaRef: ref }))

    save(MUTED_KEY, true)
    el.dispatchEvent(new Event('loadstart'))

    expect(el.muted).toBe(true)
  })
})
