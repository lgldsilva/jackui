import { useEffect } from 'react'

// Fullscreen affordances for the <video>: an explicit request (button) and an
// iPhone-landscape auto-fullscreen (the custom modal layout doesn't reflow for a
// short, wide phone viewport, so we hand off to the native player there).
export function useFullscreen(videoRef: React.RefObject<HTMLVideoElement | null>) {
  const handleRequestFullscreen = () => {
    const v = videoRef.current as any
    if (!v) return
    // iOS Safari uses webkitEnterFullscreen on the <video> element
    if (typeof v.webkitEnterFullscreen === 'function') {
      v.webkitEnterFullscreen()
    } else if (v.requestFullscreen) {
      v.requestFullscreen()
    } else if (v.webkitRequestFullscreen) {
      v.webkitRequestFullscreen()
    }
  }

  // iPhone landscape → native iOS fullscreen. The custom modal layout isn't
  // built to reflow for a short, wide phone viewport (it got cramped/garbled),
  // and the native player is the chosen behaviour there. We trigger on
  // orientation change for phone-sized viewports only — `max-height: 600px` in
  // landscape rules out tablets (iPad landscape is ~768px+ tall). iOS only
  // honours fullscreen on the <video> via webkitEnterFullscreen, and may refuse
  // it outside a user gesture, so this is best-effort (rotate again if it
  // didn't catch, or tap the fullscreen button).
  useEffect(() => {
    const mq = globalThis.matchMedia('(orientation: landscape) and (max-height: 600px)')
    const handleOrient = () => {
      const v = videoRef.current as any
      if (!v || !mq.matches || v.readyState < 1) return
      try {
        if (typeof v.webkitEnterFullscreen === 'function') v.webkitEnterFullscreen()
        else if (v.requestFullscreen) v.requestFullscreen()
        else if (v.webkitRequestFullscreen) v.webkitRequestFullscreen()
      } catch {
        /* iOS may block fullscreen outside a user gesture — ignore */
      }
    }
    mq.addEventListener?.('change', handleOrient)
    globalThis.addEventListener('orientationchange', handleOrient)
    return () => {
      mq.removeEventListener?.('change', handleOrient)
      globalThis.removeEventListener('orientationchange', handleOrient)
    }
  }, [])

  return handleRequestFullscreen
}
