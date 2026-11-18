import { useEffect, useRef, type RefObject } from 'react'

// How the focused element got its focus: by a pointer (click/tap) or by keyboard
// navigation. Tracked by hand because `:focus-visible` is not a reliable signal
// (buttons match it after a click in some browsers; jsdom does not model it).

export type FocusModality = 'pointer' | 'keyboard'

export type FocusModalityTracker = {
  /** Modality of the focus currently on `el`; 'pointer' when it was not observed. */
  readonly focusedBy: (el: Element) => FocusModality
  readonly dispose: () => void
}

const POINTER_EVENTS = ['pointerdown', 'mousedown', 'touchstart'] as const
const NAVIGATION_KEYS = new Set(['Tab', 'ArrowUp', 'ArrowDown', 'ArrowLeft', 'ArrowRight', 'Home', 'End', 'PageUp', 'PageDown'])

export function trackFocusModality(target: Window = globalThis.window): FocusModalityTracker {
  let lastInput: FocusModality = 'pointer'
  // Bound at focus time, not read at key time: a shortcut arrow key pressed
  // after a click must not turn that click-focus into a keyboard one.
  let focused: { el: EventTarget | null; modality: FocusModality } | null = null

  const onPointer = () => {
    lastInput = 'pointer'
    // Clicking the element that already has focus fires no focusin.
    focused = null
  }
  const onKeyDown = (e: KeyboardEvent) => {
    if (NAVIGATION_KEYS.has(e.key)) lastInput = 'keyboard'
  }
  const onFocusIn = (e: FocusEvent) => {
    focused = { el: e.target, modality: lastInput }
  }

  // Capture phase: must see the input before a handler moves focus (roving
  // tabindex) or stops propagation.
  for (const type of POINTER_EVENTS) target.addEventListener(type, onPointer, true)
  target.addEventListener('keydown', onKeyDown, true)
  target.addEventListener('focusin', onFocusIn, true)

  return {
    focusedBy: el => (focused?.el === el ? focused.modality : 'pointer'),
    dispose: () => {
      for (const type of POINTER_EVENTS) target.removeEventListener(type, onPointer, true)
      target.removeEventListener('keydown', onKeyDown, true)
      target.removeEventListener('focusin', onFocusIn, true)
    },
  }
}

/** Tracker bound to a component's lifecycle; `current` is null while inactive. */
export function useFocusModality(active: boolean): RefObject<FocusModalityTracker | null> {
  const ref = useRef<FocusModalityTracker | null>(null)
  useEffect(() => {
    if (!active) return
    const tracker = trackFocusModality()
    ref.current = tracker
    return () => {
      tracker.dispose()
      ref.current = null
    }
  }, [active])
  return ref
}
