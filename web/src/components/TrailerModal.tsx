import { useEffect } from 'react'
import { useTranslation } from 'react-i18next'
import { X } from 'lucide-react'

type Props = {
  readonly videoKey: string
  readonly title: string
  readonly onClose: () => void
}

// YouTube video keys are exactly 11 chars of [A-Za-z0-9_-]. The key is API
// data interpolated into the embed URL — validating the shape keeps anything
// that is not a plain video id (path traversal, query injection, javascript:)
// out of the iframe src.
export function isValidYouTubeKey(videoKey: string): boolean {
  return /^[A-Za-z0-9_-]{11}$/.test(videoKey)
}

// TrailerModal embeds a YouTube trailer via the privacy-enhanced
// youtube-nocookie host. Unmounting the iframe stops playback, so closing
// needs no player API.
export default function TrailerModal({ videoKey, title, onClose }: Readonly<Props>) {
  const { t } = useTranslation()
  useEffect(() => {
    const onKey = (e: KeyboardEvent) => { if (e.key === 'Escape') onClose() }
    globalThis.addEventListener('keydown', onKey)
    return () => globalThis.removeEventListener('keydown', onKey)
  }, [onClose])

  return (
    <div
      className="fixed inset-0 z-[60] bg-black/80 flex items-center justify-center p-4"
      onClick={onClose}
      onKeyDown={e => { if (e.key === 'Escape') onClose() }}
      role="dialog"
      aria-label={title}
    >
      <div
        className="w-full max-w-3xl"
        role="presentation"
        onClick={e => e.stopPropagation()}
        onKeyDown={e => { if (e.key === 'Escape') onClose() }}
      >
        <div className="flex items-center justify-between mb-2">
          <p className="text-sm text-white/90 truncate pr-2" title={title}>{title}</p>
          <button
            onClick={onClose}
            className="flex items-center justify-center w-9 h-9 rounded-lg text-white/80 hover:text-white hover:bg-white/10"
            aria-label={t('misc.close')}
          >
            <X className="w-5 h-5" />
          </button>
        </div>
        <div className="aspect-video bg-black rounded-lg overflow-hidden">
          {/* Unvalidated key → no iframe at all (empty shell stays closable). */}
          {isValidYouTubeKey(videoKey) && (
            <iframe
              src={`https://www.youtube-nocookie.com/embed/${videoKey}?autoplay=1&rel=0`}
              title={title}
              className="w-full h-full"
              allow="autoplay; encrypted-media; picture-in-picture"
              allowFullScreen
            />
          )}
        </div>
      </div>
    </div>
  )
}
