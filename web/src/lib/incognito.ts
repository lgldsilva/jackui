// Incognito-mode flag — when ON, the backend skips persisting search history
// and library entries (so Continuar Assistindo doesn't pick up this session).
// Favorites/watchlists/playlists/TMDB cache are unaffected.

import { useEffect, useState } from 'react'
import { load, save } from './storage'

const KEY = 'incognito'

export function isIncognito(): boolean {
  return load<boolean>(KEY, false)
}

// useIncognito mirrors the flag to localStorage + notifies same-tab listeners
// so every consumer (axios interceptor, header indicator) sees the change
// without waiting for the storage event (which fires across tabs only).
const EVT = 'jackui:incognito'

export function useIncognito(): [boolean, (next: boolean) => void] {
  const [on, setOn] = useState<boolean>(() => load<boolean>(KEY, false))
  useEffect(() => {
    const handler = (e: Event) => {
      const detail = (e as CustomEvent<boolean>).detail
      setOn(!!detail)
    }
    window.addEventListener(EVT, handler as EventListener)
    return () => window.removeEventListener(EVT, handler as EventListener)
  }, [])
  const set = (next: boolean) => {
    save(KEY, next)
    setOn(next)
    window.dispatchEvent(new CustomEvent<boolean>(EVT, { detail: next }))
  }
  return [on, set]
}
