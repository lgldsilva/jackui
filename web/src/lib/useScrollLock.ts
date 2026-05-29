import { useEffect } from 'react'

// Locks background scroll while a modal is open. `overflow: hidden` alone does
// NOT stop touch scrolling on iOS Safari (the page still rubber-bands under the
// modal when you drag), so we pin the body with position:fixed and restore the
// scroll position on release. Tailwind's preflight makes the body border-box,
// so width:100% absorbs the sidebar's padding-left instead of overflowing.
export function useScrollLock(active: boolean) {
  useEffect(() => {
    if (!active) return
    const scrollY = globalThis.scrollY
    const body = document.body
    const saved = {
      position: body.style.position,
      top: body.style.top,
      width: body.style.width,
      overflow: body.style.overflow,
    }
    body.style.position = 'fixed'
    body.style.top = `-${scrollY}px`
    body.style.width = '100%'
    body.style.overflow = 'hidden'
    return () => {
      body.style.position = saved.position
      body.style.top = saved.top
      body.style.width = saved.width
      body.style.overflow = saved.overflow
      globalThis.scrollTo(0, scrollY)
    }
  }, [active])
}
