// The "hidden curtain" reveal flag — session-only and in-memory ON PURPOSE:
// it resets on reload, so the easter egg has to be re-triggered each visit
// (more "secret" than a persisted toggle). When ON, the axios interceptor tags
// every request with X-JackUI-Reveal-Hidden and the backend includes hidden
// favourites / Continue Watching / downloads / local entries. Consumers use
// useRevealHidden() to re-fetch their lists when the curtain flips.

import { useEffect, useState } from 'react'

const EVT = 'jackui:revealHidden'
let revealed = false

export function isRevealHidden(): boolean {
  return revealed
}

// setRevealHidden flips the curtain and notifies every consumer in the tab.
export function setRevealHidden(next: boolean): void {
  revealed = next
  globalThis.dispatchEvent(new CustomEvent<boolean>(EVT, { detail: next }))
}

// useRevealHidden returns [on, setter]; the component re-renders (and should
// re-fetch) whenever the curtain flips, from anywhere in the app.
export function useRevealHidden(): [boolean, (next: boolean) => void] {
  const [on, setOn] = useState<boolean>(revealed)
  useEffect(() => {
    const handler = (e: Event) => setOn(!!(e as CustomEvent<boolean>).detail)
    globalThis.addEventListener(EVT, handler)
    return () => globalThis.removeEventListener(EVT, handler)
  }, [])
  return [on, setRevealHidden]
}
