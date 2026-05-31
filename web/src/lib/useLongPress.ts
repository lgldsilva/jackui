import { useCallback, useRef } from 'react'

export type LongPressHandlers = {
  readonly onTouchStart: (e: React.TouchEvent) => void
  readonly onTouchMove: (e: React.TouchEvent) => void
  readonly onTouchEnd: () => void
  readonly onContextMenu: (e: React.MouseEvent) => void
}

type LongPressOptions = {
  /** Hold time (ms) before firing. Default 500. */
  readonly delay?: number
  /** Cancel if the finger travels more than this many px (it's a scroll). Default 10. */
  readonly moveTolerance?: number
  readonly enabled?: boolean
}

/**
 * Toque-longo (~500ms) que dispara `onLongPress`. Cancela se o dedo se move além
 * de `moveTolerance` (= é scroll, não hold). Também mapeia `onContextMenu` no
 * desktop (right-click) pro mesmo callback. Os listeners são `passive` por serem
 * React synthetic handlers; não chamamos preventDefault pra não atrapalhar o
 * scroll nativo — o cancelamento por movimento já evita disparos acidentais.
 */
export function useLongPress(onLongPress: () => void, opts: LongPressOptions = {}): LongPressHandlers {
  const { delay = 500, moveTolerance = 10, enabled = true } = opts
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

  return { onTouchStart, onTouchMove, onTouchEnd: clear, onContextMenu }
}
