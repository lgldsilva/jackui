import { useCallback, useEffect, useRef, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { ChevronLeft, ChevronRight } from 'lucide-react'
import { previewComicManifest, previewComicPageURL } from '../../api/preview'
import { ViewerLoading, ViewerError } from './common'

type ComicViewerProps = {
  readonly infoHash: string
  readonly fileIdx: number
}

// ComicViewer — full-page CBZ/CBR reader: arrows + keyboard + swipe, page
// counter, next-page prefetch. Pages come one at a time from
// /api/preview/comic/page, so a 500 MB comic never lands in the browser whole
// — and inside a torrent only the needed pieces get pulled.
export default function ComicViewer({ infoHash, fileIdx }: ComicViewerProps) {
  const { t } = useTranslation()
  const [pages, setPages] = useState<string[] | null>(null)
  const [page, setPage] = useState(0)
  const [error, setError] = useState('')
  const [pageLoading, setPageLoading] = useState(true)
  const touchRef = useRef<number | null>(null)

  useEffect(() => {
    let cancelled = false
    setPages(null)
    setPage(0)
    setError('')
    previewComicManifest(infoHash, fileIdx)
      .then(p => {
        if (cancelled) return
        if (p.length === 0) setError(t('viewer.comic_empty'))
        setPages(p)
      })
      .catch(e => { if (!cancelled) setError(e?.response?.data?.error || e?.message || t('viewer.load_failed')) })
    return () => { cancelled = true }
  }, [infoHash, fileIdx, t])

  const total = pages?.length ?? 0
  const navigate = useCallback((delta: number) => {
    setPage(p => {
      const next = Math.min(Math.max(p + delta, 0), Math.max(total - 1, 0))
      if (next !== p) setPageLoading(true)
      return next
    })
  }, [total])

  useEffect(() => {
    const onKey = (e: KeyboardEvent) => {
      if (e.key === 'ArrowLeft') navigate(-1)
      if (e.key === 'ArrowRight' || e.key === ' ') navigate(1)
    }
    globalThis.addEventListener('keydown', onKey)
    return () => globalThis.removeEventListener('keydown', onKey)
  }, [navigate])

  // Prefetch the next page so forward reading never waits.
  useEffect(() => {
    if (!pages || page + 1 >= pages.length) return
    const img = new Image()
    img.src = previewComicPageURL(infoHash, fileIdx, pages[page + 1])
  }, [pages, page, infoHash, fileIdx])

  if (error) return <ViewerError message={error} />
  if (!pages) return <ViewerLoading hint={t('viewer.comic_loading')} />

  const current = pages[page]
  return (
    <div
      className="flex flex-col h-full min-h-[60vh]"
      onTouchStart={e => { touchRef.current = e.touches[0]?.clientX ?? null }}
      onTouchEnd={e => {
        const startX = touchRef.current
        touchRef.current = null
        const endX = e.changedTouches[0]?.clientX
        if (startX === null || endX === undefined) return
        const dx = endX - startX
        if (Math.abs(dx) > 48) navigate(dx < 0 ? 1 : -1)
      }}
    >
      <div className="relative flex-1 flex items-center justify-center overflow-auto bg-black/40">
        {pageLoading && (
          <div className="absolute inset-0 flex items-center justify-center">
            <ViewerLoading hint={t('viewer.comic_page_loading')} />
          </div>
        )}
        {current && (
          <img
            key={current}
            src={previewComicPageURL(infoHash, fileIdx, current)}
            alt={t('viewer.comic_page_alt', { page: page + 1 })}
            onLoad={() => setPageLoading(false)}
            onError={() => { setPageLoading(false); setError(t('viewer.load_failed')) }}
            className="max-w-full max-h-[72vh] object-contain mx-auto"
          />
        )}
        {/* invisible tap zones: left third = back, right two-thirds = forward */}
        <button aria-label={t('viewer.prev')} onClick={() => navigate(-1)} className="absolute inset-y-0 left-0 w-1/3 cursor-w-resize" />
        <button aria-label={t('viewer.next')} onClick={() => navigate(1)} className="absolute inset-y-0 right-0 w-1/3 cursor-e-resize" />
      </div>
      <div className="flex items-center justify-center gap-3 py-2 border-t border-default">
        <button onClick={() => navigate(-1)} disabled={page === 0} title={t('viewer.prev')} className="p-1.5 rounded hover:bg-surface-tertiary text-text-secondary disabled:opacity-30">
          <ChevronLeft className="w-5 h-5" />
        </button>
        <span className="text-xs text-text-muted tabular-nums">{page + 1} / {total}</span>
        <button onClick={() => navigate(1)} disabled={page >= total - 1} title={t('viewer.next')} className="p-1.5 rounded hover:bg-surface-tertiary text-text-secondary disabled:opacity-30">
          <ChevronRight className="w-5 h-5" />
        </button>
      </div>
    </div>
  )
}
