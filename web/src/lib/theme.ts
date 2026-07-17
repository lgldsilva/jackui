// Theme preference — Light / Dark / System. When the user picks 'system'
// (the default), we honour the OS preference via `prefers-color-scheme`.
//
// Mirrors the incognito.ts pattern: a custom in-tab event lets every consumer
// see the change without waiting for the storage event (which only fires
// across tabs).
//
// On first load (no stored choice) we set the dark class BEFORE React renders
// (applyTheme in main.tsx) so the user never sees a flash of the wrong theme.

import { useEffect, useState, useCallback } from 'react'
import { useMediaQuery } from './useMediaQuery'

export type ThemeChoice = 'light' | 'dark' | 'system'
export type ResolvedTheme = 'light' | 'dark'

const KEY = 'theme'
const EVT = 'jackui:theme'
const DARK_CLASS = 'dark'

function resolveTheme(choice: ThemeChoice, systemPrefersDark: boolean): ResolvedTheme {
  if (choice === 'system') return systemPrefersDark ? 'dark' : 'light'
  return choice
}

function readChoice(): ThemeChoice {
  if (typeof localStorage === 'undefined') return 'system'
  try {
    const raw = localStorage.getItem('jackui:' + KEY)
    if (raw === 'light' || raw === 'dark' || raw === 'system') return raw
  } catch { /* ignore */ }
  return 'system'
}

function applyClass(resolved: ResolvedTheme): void {
  if (typeof document === 'undefined') return
  const root = document.documentElement
  root.classList.toggle(DARK_CLASS, resolved === 'dark')
  // Native form elements / scrollbar follow the theme.
  root.style.colorScheme = resolved
}

// Synchronous bootstrap — called from main.tsx BEFORE React renders to avoid
// a flash of the wrong theme on first paint.
export function bootstrapTheme(): void {
  applyClass(resolveTheme(readChoice(), getSystemPrefersDark()))
}

function getSystemPrefersDark(): boolean {
  if (typeof globalThis.matchMedia !== 'function') return false
  return globalThis.matchMedia('(prefers-color-scheme: dark)').matches
}

export function useTheme(): {
  choice: ThemeChoice
  resolved: ResolvedTheme
  setChoice: (next: ThemeChoice) => void
  systemPrefersDark: boolean
} {
  const [choice, setChoice] = useState<ThemeChoice>(() => readChoice())
  const systemPrefersDark = useMediaQuery('(prefers-color-scheme: dark)')
  const resolved = resolveTheme(choice, systemPrefersDark)

  useEffect(() => {
    applyClass(resolved)
  }, [resolved])

  useEffect(() => {
    const handler = (e: Event) => {
      const detail = (e as CustomEvent<ThemeChoice>).detail
      if (detail === 'light' || detail === 'dark' || detail === 'system') {
        setChoice(detail)
      }
    }
    globalThis.addEventListener(EVT, handler as EventListener)
    return () => globalThis.removeEventListener(EVT, handler as EventListener)
  }, [])

  // Persist + broadcast; wraps the useState setter (same name would shadow).
  const commitChoice = useCallback((next: ThemeChoice) => {
    try { localStorage.setItem('jackui:' + KEY, next) } catch { /* ignore */ }
    setChoice(next)
    globalThis.dispatchEvent(new CustomEvent<ThemeChoice>(EVT, { detail: next }))
  }, [])

  return { choice, resolved, setChoice: commitChoice, systemPrefersDark }
}
