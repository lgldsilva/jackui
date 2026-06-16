// Tiny localStorage wrapper. All keys live under "jackui:" namespace.

import { useState, useEffect, type Dispatch, type SetStateAction } from 'react'

const PREFIX = 'jackui:'

export function load<T>(key: string, fallback: T): T {
  try {
    const raw = localStorage.getItem(PREFIX + key)
    if (raw === null) return fallback
    return JSON.parse(raw) as T
  } catch {
    return fallback
  }
}

export function save<T>(key: string, value: T): void {
  try {
    localStorage.setItem(PREFIX + key, JSON.stringify(value))
  } catch {
    // Quota exceeded or storage unavailable — silently ignore.
  }
}

export function remove(key: string): void {
  try {
    localStorage.removeItem(PREFIX + key)
  } catch { /* ignore */ }
}

// Remove every key under a sub-namespace (e.g. removeByPrefix('nav.') wipes all
// persisted navigation state). Used by the incognito/logout sweep and to
// invalidate a stale snapshot schema.
export function removeByPrefix(prefix: string): void {
  try {
    const full = PREFIX + prefix
    // Snapshot the keys first — removing while iterating localStorage mutates it.
    const keys: string[] = []
    for (let i = 0; i < localStorage.length; i++) {
      const k = localStorage.key(i)
      if (k?.startsWith(full)) keys.push(k)
    }
    for (const k of keys) localStorage.removeItem(k)
  } catch { /* ignore */ }
}

// useState that mirrors to localStorage — filters/sorts/view-modes survive
// reloads. Same signature as useState so it's a drop-in replacement. The
// initial value is read once from storage (lazy), then every change persists.
export function usePersistedState<T>(key: string, fallback: T): [T, Dispatch<SetStateAction<T>>] {
  const [value, setValue] = useState<T>(() => load(key, fallback))
  useEffect(() => {
    save(key, value)
  }, [key, value])
  return [value, setValue]
}

// Append to a bounded MRU list (most-recent-first, deduped, capped).
export function pushMRU(key: string, value: string, cap = 8): string[] {
  const v = value.trim()
  if (!v) return load<string[]>(key, [])
  const current = load<string[]>(key, [])
  const next = [v, ...current.filter(x => x !== v)].slice(0, cap)
  save(key, next)
  return next
}
