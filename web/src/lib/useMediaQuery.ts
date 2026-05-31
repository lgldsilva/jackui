import { useEffect, useState } from 'react'

/**
 * Reactive matchMedia hook. Centraliza o matchMedia ad-hoc espalhado pela app
 * (ex: PlayerModal landscape, NavHeader md). SSR-safe: assume `false` antes do
 * primeiro efeito (sem `window` durante render no servidor/embed).
 */
export function useMediaQuery(query: string): boolean {
  const [matches, setMatches] = useState(() => {
    if (typeof globalThis.matchMedia !== 'function') return false
    return globalThis.matchMedia(query).matches
  })

  useEffect(() => {
    if (typeof globalThis.matchMedia !== 'function') return
    const mq = globalThis.matchMedia(query)
    const onChange = () => setMatches(mq.matches)
    onChange()
    mq.addEventListener?.('change', onChange)
    return () => mq.removeEventListener?.('change', onChange)
  }, [query])

  return matches
}

/** < 640px = abaixo do breakpoint `sm` do Tailwind (a fronteira mobile da app). */
export function useIsMobile(): boolean {
  return useMediaQuery('(max-width: 639px)')
}
