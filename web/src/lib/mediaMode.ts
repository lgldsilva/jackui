// Cinema/Música preference. Two jobs:
//   1) Tie-breaker for ambiguous titles (no clear audio/video signal) on the
//      NEXT playback — see playable.detectKind / PlayerProvider.currentKind.
//   2) When toggled WHILE something is playing, it switches the active player
//      to that mode immediately (PlayerProvider re-keys the modal, preserving
//      the playhead).
//
// Shared across the tab via a CustomEvent so the NavHeader toggle and the
// PlayerProvider stay in sync (plain usePersistedState does NOT — each hook
// keeps its own useState, so a change in one component never reaches another).
// Persisted in localStorage so the choice sticks across reloads.

import { useEffect, useState } from 'react'
import { load, save } from './storage'

export type MediaMode = 'audio' | 'video'

const KEY = 'mediaModePref'
const EVT = 'jackui:mediaMode'
let mode: MediaMode = load<MediaMode>(KEY, 'video')

export function getMediaMode(): MediaMode {
  return mode
}

// setMediaMode flips the preference and notifies every consumer in the tab.
export function setMediaMode(next: MediaMode): void {
  mode = next
  save(KEY, next)
  // Guard for non-DOM environments (vitest 'node', SSR) — in the browser this
  // always runs; elsewhere the in-memory value still updates.
  if (typeof globalThis.dispatchEvent === 'function') {
    globalThis.dispatchEvent(new CustomEvent<MediaMode>(EVT, { detail: next }))
  }
}

// useMediaMode returns [mode, setter]; the component re-renders whenever the
// preference flips, from anywhere in the app.
export function useMediaMode(): [MediaMode, (next: MediaMode) => void] {
  const [m, setM] = useState<MediaMode>(mode)
  useEffect(() => {
    const handler: EventListener = (e) => setM((e as CustomEvent<MediaMode>).detail)
    globalThis.addEventListener(EVT, handler)
    return () => globalThis.removeEventListener(EVT, handler)
  }, [])
  return [m, setMediaMode]
}
