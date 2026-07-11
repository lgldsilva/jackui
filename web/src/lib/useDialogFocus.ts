import { useEffect, useRef } from 'react'

const FOCUSABLE_SELECTOR =
  'a[href], button:not([disabled]), textarea:not([disabled]), input:not([disabled]), [tabindex]:not([tabindex="-1"])'

function getFocusableElements(container: HTMLDivElement | null) {
  if (container == null) return []
  return Array.from(container.querySelectorAll<HTMLElement>(FOCUSABLE_SELECTOR))
}

export function useDialogFocus(open: boolean) {
  const containerRef = useRef<HTMLDivElement>(null)
  const previousActiveElementRef = useRef<HTMLElement | null>(null)
  const wasOpenRef = useRef(false)

  useEffect(() => {
    const container = containerRef.current

    if (open && !wasOpenRef.current) {
      previousActiveElementRef.current = document.activeElement instanceof HTMLElement ? document.activeElement : null

      const [firstFocusable] = getFocusableElements(container)
      if (firstFocusable != null) firstFocusable.focus()
      else container?.focus()
    }

    if (!open && wasOpenRef.current && previousActiveElementRef.current?.isConnected) {
      previousActiveElementRef.current.focus()
    }

    wasOpenRef.current = open
  }, [open])

  useEffect(() => {
    if (!open) return

    const container = containerRef.current
    if (container == null) return

    const handleKeyDown = (event: KeyboardEvent) => {
      if (event.key !== 'Tab') return

      const focusableElements = getFocusableElements(container)
      if (focusableElements.length === 0) {
        event.preventDefault()
        container.focus()
        return
      }

      const firstFocusable = focusableElements[0]
      const lastFocusable = focusableElements[focusableElements.length - 1]
      const activeElement = document.activeElement

      if (event.shiftKey) {
        if (activeElement === firstFocusable || !container.contains(activeElement)) {
          event.preventDefault()
          lastFocusable.focus()
        }
        return
      }

      if (activeElement === lastFocusable) {
        event.preventDefault()
        firstFocusable.focus()
      }
    }

    container.addEventListener('keydown', handleKeyDown)

    return () => {
      container.removeEventListener('keydown', handleKeyDown)
    }
  }, [open])

  return containerRef
}
