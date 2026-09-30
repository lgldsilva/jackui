import { useEffect, useRef, useState, type RefObject } from 'react'

// nextReveal: next count of visible lines when revealing one more batch, without going
// past the total. Pure/testable; the DOM part (IntersectionObserver) stays in the hook.
export function nextReveal(visible: number, step: number, total: number): number {
  return Math.min(visible + step, total)
}

export type IncrementalReveal = {
  visible: number      // how many lines to render now (≤ total)
  hasMore: boolean     // are there still hidden lines?
  remaining: number    // how many are left to reveal
  sentinelRef: RefObject<HTMLDivElement> // marker at the end of the list
  showMore: () => void // reveals one more batch (fallback button)
}

// useIncrementalReveal: renders a long list in BATCHES (default 100), revealing
// more as the user SCROLLS to the end (sentinel + IntersectionObserver) and also
// via showMore() (button). Keeps the performance protection — never mounts thousands of
// lines at once — WITHOUT hiding the rest behind a filter the user would have to
// guess. `resetKey` changes (new torrent / filter / sort) → starts over from the 1st batch.
export function useIncrementalReveal(total: number, resetKey: unknown, step = 100): IncrementalReveal {
  const [visible, setVisible] = useState(step)
  useEffect(() => { setVisible(step) }, [resetKey, step])

  const shown = Math.min(visible, total)
  const hasMore = shown < total
  const sentinelRef = useRef<HTMLDivElement>(null)

  useEffect(() => {
    const el = sentinelRef.current
    if (!el || !hasMore || typeof IntersectionObserver === 'undefined') return
    // Generous rootMargin so it starts loading a bit before hitting the end.
    // Since a batch (100 lines) is much taller than the sidebar viewport, on
    // revealing more the sentinel leaves the screen → it doesn't cascade everything at once.
    const io = new IntersectionObserver(
      (entries) => { if (entries.some((e) => e.isIntersecting)) setVisible((v) => nextReveal(v, step, total)) },
      { rootMargin: '240px' },
    )
    io.observe(el)
    return () => io.disconnect()
  }, [hasMore, total, step])

  return { visible: shown, hasMore, remaining: Math.max(0, total - shown), sentinelRef, showMore: () => setVisible((v) => nextReveal(v, step, total)) }
}
