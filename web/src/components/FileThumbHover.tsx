import { useRef, useState, useCallback } from 'react'
import { createPortal } from 'react-dom'

type ThumbState = { url: string | null; label?: string; x: number; y: number }
type MouseLike = { clientX: number; clientY: number }

// The floating preview only makes sense with a real pointer. On touch devices it
// has no hover trigger and the fixed/z-[70] portal ends up pinned over other UI
// (e.g. the video player controls on mobile). Gate it on `(hover: hover)`.
function canHoverPreview(): boolean {
  if (typeof globalThis === 'undefined' || !globalThis.matchMedia) return true
  return globalThis.matchMedia('(hover: hover)').matches
}

/**
 * useHoverThumb — floating frame-preview ("zoom") shown while the pointer rests
 * over a file row. The thumbnail endpoints (`streamThumbnailURL`/`localThumbURL`)
 * extract a JPEG frame on demand and cache it on disk, so we only fetch after a
 * short hover delay to avoid hammering ffmpeg while the user scans the list.
 *
 * Usage:
 *   const { show, move, hide, popover } = useHoverThumb()
 *   <div onMouseEnter={e => show(thumbUrl, e, fullName)} onMouseMove={move} onMouseLeave={hide}>…</div>
 *   {popover}
 *
 * `url` may be null for non-previewable rows; `label` is the full (untruncated)
 * text to caption the preview with. show() no-ops only when BOTH are empty, so a
 * long filename with no thumbnail still surfaces its full name on hover.
 */
export function useHoverThumb(delayMs = 320) {
  const [state, setState] = useState<ThumbState | null>(null)
  const timerRef = useRef<ReturnType<typeof setTimeout> | undefined>(undefined)

  const show = useCallback((url: string | null, e: MouseLike, label?: string) => {
    if (!canHoverPreview()) return // touch device — no hover preview
    if (!url && !label) return
    const { clientX: x, clientY: y } = e
    clearTimeout(timerRef.current)
    timerRef.current = setTimeout(() => setState({ url, label, x, y }), delayMs)
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

function FloatingThumb({ url, label, x, y }: ThumbState) {
  const [broken, setBroken] = useState(false)
  const imgUrl = !broken && url ? url : null
  // Nothing to show (image failed AND no caption) → render nothing.
  if (!imgUrl && !label) return null

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
      {imgUrl && (
        <img
          src={imgUrl}
          alt=""
          className="block w-full h-auto"
          onError={() => setBroken(true)}
        />
      )}
      {label && (
        <p className="px-2.5 py-2 text-xs text-gray-200 break-words leading-snug max-h-40 overflow-hidden">
          {label}
        </p>
      )}
    </div>,
    document.body,
  )
}
