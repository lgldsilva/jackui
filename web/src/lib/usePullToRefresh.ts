import { useState, useEffect, useRef } from 'react'

type Options = {
  /** Called when user releases past the threshold. Should be async. */
  readonly onRefresh: () => Promise<void> | void
  /** Distance (px) user must pull to trigger refresh. Default 80. */
  readonly threshold?: number
  /** Maximum visual displacement before clamping. Default 120. */
  readonly maxPull?: number
  /** If true, the hook is inactive (e.g., loading already in progress). */
  readonly disabled?: boolean
}

type Result = {
  /** Current pull displacement in px (0 if not pulling). */
  pull: number
  /** True while we're calling onRefresh after release. */
  refreshing: boolean
  /** Fraction 0..1 indicating progress towards threshold. */
  progress: number
}

/**
 * Pull-to-refresh hook for the document scroll. Touch-only — no-op on desktop.
 *
 * Attaches passive touch listeners to `document`. Tracks the gesture only when
 * the page is already scrolled to the top (`scrollY === 0`) — otherwise lets
 * the browser's native scroll handle it.
 *
 * Returns visual state for the caller to render a refresh indicator.
 */
export function usePullToRefresh({ onRefresh, threshold = 80, maxPull = 120, disabled = false }: Options): Result {
  const [pull, setPull] = useState(0)
  const [refreshing, setRefreshing] = useState(false)
  const startYRef = useRef<number | null>(null)
  const trackingRef = useRef(false)

  useEffect(() => {
    if (disabled) return

    const onTouchStart = (e: TouchEvent) => {
      if (globalThis.scrollY > 0) return        // only at the top
      if (e.touches.length !== 1) return
      startYRef.current = e.touches[0].clientY
      trackingRef.current = true
    }

    const onTouchMove = (e: TouchEvent) => {
      if (!trackingRef.current || startYRef.current === null) return
      const dy = e.touches[0].clientY - startYRef.current
      if (dy <= 0) {
        // Pulling up = let normal scroll happen
        setPull(0)
        return
      }
      // Apply a rubber-band feel — pull slows as it approaches maxPull
      const damped = Math.min(maxPull, dy * 0.6)
      setPull(damped)
    }

    const onTouchEnd = async () => {
      if (!trackingRef.current) return
      const dy = pull
      trackingRef.current = false
      startYRef.current = null
      if (dy >= threshold) {
        setRefreshing(true)
        try { await onRefresh() }
        finally {
          setRefreshing(false)
          setPull(0)
        }
      } else {
        setPull(0)
      }
    }

    document.addEventListener('touchstart', onTouchStart, { passive: true })
    document.addEventListener('touchmove', onTouchMove, { passive: true })
    document.addEventListener('touchend', onTouchEnd)
    document.addEventListener('touchcancel', onTouchEnd)
    return () => {
      document.removeEventListener('touchstart', onTouchStart)
      document.removeEventListener('touchmove', onTouchMove)
      document.removeEventListener('touchend', onTouchEnd)
      document.removeEventListener('touchcancel', onTouchEnd)
    }
  // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [onRefresh, threshold, maxPull, disabled, pull])

  return { pull, refreshing, progress: Math.min(1, pull / threshold) }
}
