import { useCallback, useRef } from 'react'

export type LongPressHandlers = {
  readonly onTouchStart: (e: React.TouchEvent) => void
  readonly onTouchMove: (e: React.TouchEvent) => void
  readonly onTouchEnd: () => void
  readonly onContextMenu?: (e: React.MouseEvent) => void
}

type LongPressOptions = {
  /** Hold time (ms) before firing. Default 500. */
  readonly delay?: number
  /** Cancel if the finger travels more than this many px (it's a scroll). Default 10. */
  readonly moveTolerance?: number
  readonly enabled?: boolean
  /** Map desktop right-click (contextmenu) to the same callback. Default true.
      A page that owns the right-click for something else (e.g. LocalPage opens
      a new tab on right-click) passes false so this hook doesn't shadow it. */
  readonly contextMenu?: boolean
}

/**
 * Long-press (~500ms) that fires `onLongPress`. Cancels if the finger moves beyond
 * `moveTolerance` (= it's a scroll, not a hold). Also maps `onContextMenu` on
 * desktop (right-click) to the same callback. The listeners are `passive` because they are
 * React synthetic handlers; we don't call preventDefault so as not to disturb native
 * scrolling — the movement-based cancellation already avoids accidental triggers.
 */
export function useLongPress(onLongPress: () => void, opts: LongPressOptions = {}): LongPressHandlers {
  const { delay = 500, moveTolerance = 10, enabled = true, contextMenu = true } = opts
  const timer = useRef<ReturnType<typeof setTimeout> | null>(null)
  const start = useRef<{ x: number; y: number } | null>(null)

  const clear = useCallback(() => {
    if (timer.current) { clearTimeout(timer.current); timer.current = null }
    start.current = null
  }, [])

  const onTouchStart = useCallback((e: React.TouchEvent) => {
    if (!enabled || e.touches.length !== 1) return
    const t = e.touches[0]
    start.current = { x: t.clientX, y: t.clientY }
    timer.current = setTimeout(() => { onLongPress(); clear() }, delay)
  }, [enabled, delay, onLongPress, clear])

  const onTouchMove = useCallback((e: React.TouchEvent) => {
    if (!start.current || !timer.current) return
    const t = e.touches[0]
    if (Math.abs(t.clientX - start.current.x) > moveTolerance ||
        Math.abs(t.clientY - start.current.y) > moveTolerance) {
      clear()
    }
  }, [moveTolerance, clear])

  const onContextMenu = useCallback((e: React.MouseEvent) => {
    if (!enabled) return
    e.preventDefault()
    onLongPress()
  }, [enabled, onLongPress])

  return { onTouchStart, onTouchMove, onTouchEnd: clear, ...(contextMenu ? { onContextMenu } : {}) }
}
