import { useEffect, useRef } from 'react'

const FOCUSABLE_SELECTOR = [
  'a[href]',
  'button:not([disabled])',
  'textarea:not([disabled])',
  'input:not([disabled]):not([type="hidden"])',
  'select:not([disabled])',
  'summary',
  '[contenteditable="true"]',
  'audio[controls]',
  'video[controls]',
  '[tabindex]',
].join(', ')

export type DialogFocusOptions = {
  /**
   * Where focus lands on open. 'first' (default) = the first tabbable control;
   * 'container' = the dialog element itself (needs tabIndex={-1}), for shells
   * whose first control is not a sensible target, e.g. the media player.
   */
  readonly initialFocus?: 'first' | 'container'
}

function isHidden(el: HTMLElement, container: HTMLElement): boolean {
  if (el.closest('[hidden], [inert]') != null) return true
  // checkVisibility covers display:none on any ancestor; jsdom and older
  // Safari lack it, hence the manual walk below.
  if (typeof el.checkVisibility === 'function') return !el.checkVisibility({ visibilityProperty: true })
  if (getComputedStyle(el).visibility === 'hidden') return true
  for (let node: HTMLElement | null = el; node != null; node = node.parentElement) {
    if (getComputedStyle(node).display === 'none') return true
    if (node === container) break
  }
  return false
}

function isTabbable(el: HTMLElement, container: HTMLElement): boolean {
  return el.getAttribute('tabindex') !== '-1' && !isHidden(el, container)
}

// Only the two ends of the tab order matter to the trap, so the visibility
// checks stop at the first hit from each side instead of walking every control
// (the player's file list can hold thousands).
function getTabbableEdges(container: HTMLElement): { first: HTMLElement; last: HTMLElement } | null {
  const candidates = Array.from(container.querySelectorAll<HTMLElement>(FOCUSABLE_SELECTOR))
  const first = candidates.find(el => isTabbable(el, container))
  if (first == null) return null
  for (let i = candidates.length - 1; i >= 0; i--) {
    if (isTabbable(candidates[i], container)) return { first, last: candidates[i] }
  }
  return { first, last: first }
}

function focusLost(): boolean {
  const active = document.activeElement
  return active == null || active === document.body || !active.isConnected
}

export function useDialogFocus(open: boolean, { initialFocus = 'first' }: DialogFocusOptions = {}) {
  const containerRef = useRef<HTMLDivElement>(null)
  const previousActiveElementRef = useRef<HTMLElement | null>(null)
  const wasOpenRef = useRef(false)

  useEffect(() => {
    const container = containerRef.current

    if (open && !wasOpenRef.current) {
      previousActiveElementRef.current = document.activeElement instanceof HTMLElement ? document.activeElement : null

      const first = initialFocus === 'first' && container != null ? getTabbableEdges(container)?.first : undefined
      if (first == null) container?.focus()
      else first.focus()
    }

    if (!open && wasOpenRef.current && previousActiveElementRef.current?.isConnected) {
      previousActiveElementRef.current.focus()
    }

    wasOpenRef.current = open
    // initialFocus only matters at the open transition.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [open])

  // A dialog that is unmounted while still open (conditionally rendered) never
  // sees open=false. Restore only if focus died with it — if the user already
  // moved elsewhere, leave them there.
  useEffect(() => () => {
    const previous = previousActiveElementRef.current
    if (wasOpenRef.current && previous?.isConnected && focusLost()) previous.focus()
  }, [])

  useEffect(() => {
    if (!open) return

    const container = containerRef.current
    if (container == null) return

    const handleKeyDown = (event: KeyboardEvent) => {
      // defaultPrevented = a nested dialog's trap already moved focus.
      if (event.key !== 'Tab' || event.defaultPrevented) return

      const edges = getTabbableEdges(container)
      if (edges == null) {
        event.preventDefault()
        container.focus()
        return
      }

      const activeElement = document.activeElement

      if (event.shiftKey) {
        if (activeElement === edges.first || activeElement === container || !container.contains(activeElement)) {
          event.preventDefault()
          edges.last.focus()
        }
        return
      }

      if (activeElement === edges.last) {
        event.preventDefault()
        edges.first.focus()
      }
    }

    container.addEventListener('keydown', handleKeyDown)

    return () => {
      container.removeEventListener('keydown', handleKeyDown)
    }
  }, [open])

  return containerRef
}
