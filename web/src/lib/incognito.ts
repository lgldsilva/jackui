// Incognito-mode flag — when ON, the backend records history and library entries
// but marks them with incognito=1. They are visible during this session but
// deleted when the user disables incognito mode or logs out.
//
// Heartbeat: while incognito is active, the frontend sends POST /api/user/incognito/heartbeat
// every HEARTBEAT_INTERVAL ms. The backend resets a per-user TTL; if the TTL
// expires (tab closed / crash), the backend auto-cleans after 1 hour. On a
// server restart the in-memory TTL map is gone, so any leftover incognito rows
// are purged at startup (see StartIncognitoReaper boot sweep).

import { useEffect, useState } from 'react'
import { load, save } from './storage'
import api from '../api/client'

const KEY = 'incognito'
const EVT = 'jackui:incognito'
const HEARTBEAT_INTERVAL = 5 * 60 * 1000 // 5 min — well within the 1h server TTL

export function isIncognito(): boolean {
  return load<boolean>(KEY, false)
}

/** Clears the client-side incognito flag (does not call the backend). Used on
 *  logout so the next user/session does not inherit "incognito still on". */
export function resetIncognitoFlag(): void {
  save(KEY, false)
  globalThis.dispatchEvent(new CustomEvent<boolean>(EVT, { detail: false }))
}

// clearIncognitoData calls the backend to delete all incognito entries for the user.
// Fire-and-forget — UI should not block on this.
export async function clearIncognitoData(): Promise<void> {
  await api.delete('/user/incognito')
}

// useIncognito mirrors the flag to localStorage + notifies same-tab listeners
// so every consumer (axios interceptor, header indicator) sees the change
// without waiting for the storage event (which fires across tabs only).

export function useIncognito(): [boolean, (next: boolean) => void] {
  const [on, setOn] = useState<boolean>(() => load<boolean>(KEY, false))

  useEffect(() => {
    const handler = (e: Event) => {
      const detail = (e as CustomEvent<boolean>).detail
      setOn(!!detail)
    }
    globalThis.addEventListener(EVT, handler as EventListener)
    return () => globalThis.removeEventListener(EVT, handler as EventListener)
  }, [])

  // Heartbeat while incognito is active
  useEffect(() => {
    if (!on) return
    // Immediate ping on activation
    api.post('/user/incognito/heartbeat').catch(() => {})
    const id = globalThis.setInterval(() => {
      api.post('/user/incognito/heartbeat').catch(() => {})
    }, HEARTBEAT_INTERVAL)
    return () => globalThis.clearInterval(id)
  }, [on])

  const set = (next: boolean) => {
    save(KEY, next)
    setOn(next)
    globalThis.dispatchEvent(new CustomEvent<boolean>(EVT, { detail: next }))
    // When turning OFF: trigger cleanup of incognito data
    if (!next) {
      clearIncognitoData().catch(() => {})
    }
  }
  return [on, set]
}
