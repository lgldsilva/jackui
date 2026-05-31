import { RefObject, useEffect, useRef } from 'react'

export type SwipeHandlers = {
  readonly onLeft?: () => void
  readonly onRight?: () => void
  readonly onUp?: () => void
  readonly onDown?: () => void
}

export type SwipeOptions = {
  /** Min primary-axis travel (px) to count as a swipe. Default 60. */
  readonly threshold?: number
  /** Max off-axis travel (px) still allowed — keeps diagonal drags from firing. Default 80. */
  readonly restraint?: number
  /** Require the gesture to START within `edgeSize` px of this screen edge. */
  readonly edge?: 'left' | 'right'
  /** Edge band width in px (used with `edge`). Default 28. */
  readonly edgeSize?: number
  /**
   * Ignore gestures that START within this many px of either horizontal screen
   * edge. Use on a content swipe to leave the screen edges reserved for an
   * edge-`edge` gesture elsewhere (e.g. swipe-to-open-drawer). Ignored when
   * `edge` is set.
   */
  readonly ignoreEdgePx?: number
  readonly enabled?: boolean
}

/**
 * Lightweight touch-swipe detector. Attaches to a DOM element (via ref) or the
 * whole document ('document', for edge gestures like opening a drawer). Fires a
 * single directional callback on touchend, deciding the dominant axis then —
 * so it never blocks native scroll (all listeners are passive) and a vertical
 * scroll won't be misread as a horizontal swipe.
 *
 * Single-finger only: a second touch (pinch-zoom) cancels the gesture so we
 * don't hijack the user's zoom.
 */
export function useSwipe(
  target: RefObject<HTMLElement> | 'document',
  handlers: SwipeHandlers,
  opts: SwipeOptions = {},
): void {
  const { threshold = 60, restraint = 80, edge, edgeSize = 28, ignoreEdgePx = 0, enabled = true } = opts
  // Handlers num ref (atualizado a cada render) pra o efeito NÃO re-anexar os
  // listeners quando os callbacks são arrow inline (nova referência a cada render).
  // Também evita abortar um gesto em curso quando o handler muda (ex: troca de aba).
  const handlersRef = useRef(handlers)
  handlersRef.current = handlers

  useEffect(() => {
    if (!enabled) return
    const node: HTMLElement | Document | null =
      target === 'document' ? document : target.current
    if (!node) return

    let startX = 0
    let startY = 0
    let armed = false

    const onStart = (e: TouchEvent) => {
      if (e.touches.length !== 1) { armed = false; return }
      const t = e.touches[0]
      if (edge === 'left' && t.clientX > edgeSize) { armed = false; return }
      if (edge === 'right' && t.clientX < globalThis.innerWidth - edgeSize) { armed = false; return }
      if (!edge && ignoreEdgePx > 0 &&
          (t.clientX < ignoreEdgePx || t.clientX > globalThis.innerWidth - ignoreEdgePx)) {
        armed = false
        return
      }
      startX = t.clientX
      startY = t.clientY
      armed = true
    }

    const onMove = (e: TouchEvent) => {
      // Second finger mid-gesture → it's a pinch, not a swipe.
      if (e.touches.length > 1) armed = false
    }

    const onEnd = (e: TouchEvent) => {
      if (!armed) return
      armed = false
      const t = e.changedTouches[0]
      if (!t) return
      const dx = t.clientX - startX
      const dy = t.clientY - startY
      const absX = Math.abs(dx)
      const absY = Math.abs(dy)
      const h = handlersRef.current
      if (absX >= absY) {
        if (absX < threshold || absY > restraint) return
        if (dx < 0) h.onLeft?.()
        else h.onRight?.()
      } else {
        if (absY < threshold || absX > restraint) return
        if (dy < 0) h.onUp?.()
        else h.onDown?.()
      }
    }

    node.addEventListener('touchstart', onStart as EventListener, { passive: true })
    node.addEventListener('touchmove', onMove as EventListener, { passive: true })
    node.addEventListener('touchend', onEnd as EventListener, { passive: true })
    return () => {
      node.removeEventListener('touchstart', onStart as EventListener)
      node.removeEventListener('touchmove', onMove as EventListener)
      node.removeEventListener('touchend', onEnd as EventListener)
    }
  }, [target, enabled, threshold, restraint, edge, edgeSize, ignoreEdgePx])
}
