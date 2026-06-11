import { useCallback, useEffect, useRef, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { ChevronLeft, ChevronRight, ZoomIn, ZoomOut, Maximize } from 'lucide-react'
import { ViewerError } from './common'

export type ImageItem = { label: string; url: string }

type ImageViewerProps = {
  readonly items: ReadonlyArray<ImageItem>
  readonly start: number
}

const MIN_ZOOM = 1
const MAX_ZOOM = 8

// isSvgURL — SVGs are fetched into an inert blob: the server intentionally
// serves .svg as download/sandboxed (stored-XSS guard), and a blob <img> keeps
// rendering working while never executing embedded scripts.
function isSvgLabel(label: string): boolean {
  return label.toLowerCase().endsWith('.svg')
}

// ImageViewer — zoom (wheel/buttons/double-click), pan (drag when zoomed) and
// prev/next navigation across the sibling images of the same torrent/folder.
export default function ImageViewer({ items, start }: ImageViewerProps) {
  const { t } = useTranslation()
  const [idx, setIdx] = useState(Math.min(Math.max(start, 0), Math.max(items.length - 1, 0)))
  const [zoom, setZoom] = useState(MIN_ZOOM)
  const [pan, setPan] = useState({ x: 0, y: 0 })
  const [blobURL, setBlobURL] = useState('')
  const [error, setError] = useState('')
  const dragRef = useRef<{ x: number; y: number; panX: number; panY: number } | null>(null)

  const item = items[idx]
  const navigate = useCallback((delta: number) => {
    setIdx(i => Math.min(Math.max(i + delta, 0), items.length - 1))
    setZoom(MIN_ZOOM)
    setPan({ x: 0, y: 0 })
    setError('')
  }, [items.length])

  // SVG → inert blob URL (no script execution via <img>, ever).
  useEffect(() => {
    if (!item || !isSvgLabel(item.label)) {
      setBlobURL('')
      return
    }
    let revoked = ''
    let cancelled = false
    fetch(item.url)
      .then(async r => {
        if (!r.ok) throw new Error(`HTTP ${r.status}`)
        const buf = await r.arrayBuffer()
        if (cancelled) return
        revoked = URL.createObjectURL(new Blob([buf], { type: 'image/svg+xml' }))
        setBlobURL(revoked)
      })
      .catch(e => { if (!cancelled) setError(e?.message || t('viewer.load_failed')) })
    return () => {
      cancelled = true
      if (revoked) URL.revokeObjectURL(revoked)
    }
  }, [item, t])

  useEffect(() => {
    const onKey = (e: KeyboardEvent) => {
      if (e.key === 'ArrowLeft') navigate(-1)
      if (e.key === 'ArrowRight') navigate(1)
    }
    globalThis.addEventListener('keydown', onKey)
    return () => globalThis.removeEventListener('keydown', onKey)
  }, [navigate])

  if (!item) return <ViewerError message={t('viewer.load_failed')} />

  const setZoomClamped = (z: number) => {
    const next = Math.min(Math.max(z, MIN_ZOOM), MAX_ZOOM)
    setZoom(next)
    if (next === MIN_ZOOM) setPan({ x: 0, y: 0 })
  }
  const onPointerDown = (e: React.PointerEvent) => {
    if (zoom === MIN_ZOOM) return
    dragRef.current = { x: e.clientX, y: e.clientY, panX: pan.x, panY: pan.y }
    e.currentTarget.setPointerCapture(e.pointerId)
  }
  const onPointerMove = (e: React.PointerEvent) => {
    const d = dragRef.current
    if (!d) return
    setPan({ x: d.panX + (e.clientX - d.x), y: d.panY + (e.clientY - d.y) })
  }
  const onPointerUp = () => { dragRef.current = null }
  const src = isSvgLabel(item.label) ? blobURL : item.url

  return (
    <div className="relative flex flex-col h-full min-h-[50vh]">
      {/* zoom controls */}
      <div className="absolute top-2 right-2 z-10 flex items-center gap-1 bg-surface-elevated/80 rounded-lg border border-default p-1">
        <button onClick={() => setZoomClamped(zoom / 1.5)} title={t('viewer.zoom_out')} className="p-1.5 rounded hover:bg-surface-tertiary text-text-secondary">
          <ZoomOut className="w-4 h-4" />
        </button>
        <span className="text-[11px] text-text-muted tabular-nums w-10 text-center">{(zoom * 100).toFixed(0)}%</span>
        <button onClick={() => setZoomClamped(zoom * 1.5)} title={t('viewer.zoom_in')} className="p-1.5 rounded hover:bg-surface-tertiary text-text-secondary">
          <ZoomIn className="w-4 h-4" />
        </button>
        <button onClick={() => setZoomClamped(MIN_ZOOM)} title={t('viewer.zoom_reset')} className="p-1.5 rounded hover:bg-surface-tertiary text-text-secondary">
          <Maximize className="w-4 h-4" />
        </button>
      </div>

      <div
        className={`flex-1 flex items-center justify-center overflow-hidden p-4 ${zoom > MIN_ZOOM ? 'cursor-grab active:cursor-grabbing' : ''}`}
        onWheel={e => setZoomClamped(zoom * (e.deltaY < 0 ? 1.15 : 1 / 1.15))}
        onPointerDown={onPointerDown}
        onPointerMove={onPointerMove}
        onPointerUp={onPointerUp}
        onDoubleClick={() => setZoomClamped(zoom > MIN_ZOOM ? MIN_ZOOM : 2)}
      >
        {error && <ViewerError message={error} />}
        {!error && src && (
          <img
            src={src}
            alt={item.label}
            draggable={false}
            onError={() => setError(t('viewer.load_failed'))}
            className="max-w-full max-h-[72vh] object-contain select-none transition-transform"
            style={{ transform: `translate(${pan.x}px, ${pan.y}px) scale(${zoom})` }}
          />
        )}
      </div>

      {items.length > 1 && (
        <div className="flex items-center justify-center gap-3 py-2 border-t border-default">
          <button onClick={() => navigate(-1)} disabled={idx === 0} title={t('viewer.prev')} className="p-1.5 rounded hover:bg-surface-tertiary text-text-secondary disabled:opacity-30">
            <ChevronLeft className="w-5 h-5" />
          </button>
          <span className="text-xs text-text-muted tabular-nums">{idx + 1} / {items.length}</span>
          <button onClick={() => navigate(1)} disabled={idx === items.length - 1} title={t('viewer.next')} className="p-1.5 rounded hover:bg-surface-tertiary text-text-secondary disabled:opacity-30">
            <ChevronRight className="w-5 h-5" />
          </button>
          <span className="text-[11px] text-text-muted truncate max-w-[40%]" title={item.label}>{item.label.split('/').at(-1)}</span>
        </div>
      )}
    </div>
  )
}
