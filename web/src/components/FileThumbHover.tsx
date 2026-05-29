import { useRef, useState, useCallback } from 'react'
import { createPortal } from 'react-dom'

type ThumbState = { url: string; x: number; y: number }
type MouseLike = { clientX: number; clientY: number }

/**
 * useHoverThumb — floating frame-preview ("zoom") shown while the pointer rests
 * over a file row. The thumbnail endpoints (`streamThumbnailURL`/`localThumbURL`)
 * extract a JPEG frame on demand and cache it on disk, so we only fetch after a
 * short hover delay to avoid hammering ffmpeg while the user scans the list.
 *
 * Usage:
 *   const { show, move, hide, popover } = useHoverThumb()
 *   <div onMouseEnter={e => show(thumbUrl, e)} onMouseMove={move} onMouseLeave={hide}>…</div>
 *   {popover}
 *
 * Pass `null` as the url for non-previewable rows — show() simply no-ops.
 */
export function useHoverThumb(delayMs = 320) {
  const [state, setState] = useState<ThumbState | null>(null)
  const timerRef = useRef<ReturnType<typeof setTimeout> | undefined>(undefined)

  const show = useCallback((url: string | null, e: MouseLike) => {
    if (!url) return
    const { clientX: x, clientY: y } = e
    clearTimeout(timerRef.current)
    timerRef.current = setTimeout(() => setState({ url, x, y }), delayMs)
  }, [delayMs])

  const move = useCallback((e: MouseLike) => {
    setState(s => (s ? { ...s, x: e.clientX, y: e.clientY } : s))
  }, [])

  const hide = useCallback(() => {
    clearTimeout(timerRef.current)
    setState(null)
  }, [])

  const popover = state ? <FloatingThumb url={state.url} x={state.x} y={state.y} /> : null
  return { show, move, hide, popover }
}

function FloatingThumb({ url, x, y }: ThumbState) {
  const [broken, setBroken] = useState(false)
  if (broken) return null

  // Follow the cursor, flipping to the left and clamping vertically so the
  // preview never spills off-screen on long lists near the viewport edges.
  const W = 320, PAD = 16, ESTIMATED_H = 200
  const vw = globalThis.innerWidth || 1024
  const vh = globalThis.innerHeight || 768
  let left = x + PAD
  if (left + W > vw - PAD) left = x - W - PAD
  if (left < PAD) left = PAD
  let top = y - ESTIMATED_H / 2
  if (top < PAD) top = PAD
  if (top + ESTIMATED_H > vh - PAD) top = vh - ESTIMATED_H - PAD

  return createPortal(
    <div
      className="fixed z-[70] pointer-events-none rounded-lg overflow-hidden border border-gray-600 shadow-2xl bg-gray-900"
      style={{ left, top, width: W }}
    >
      <img
        src={url}
        alt=""
        className="block w-full h-auto"
        onError={() => setBroken(true)}
      />
    </div>,
    document.body,
  )
}
