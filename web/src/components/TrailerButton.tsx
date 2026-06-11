import { useState } from 'react'
import { Clapperboard, Loader2 } from 'lucide-react'
import { useTranslation } from 'react-i18next'
import { tmdbMatch, tmdbVideos, TmdbVideo } from '../api/client'
import TrailerModal from './TrailerModal'

type Props = {
  // Either the TMDB identity directly (Discover cards)...
  kind?: 'movie' | 'tv'
  tmdbId?: number
  // ...or a raw release title to resolve first (search/contents modal).
  title?: string
  className?: string
}

// TrailerButton lazily resolves a title's trailer on click (TMDB match by
// title when needed → /videos) and plays it in TrailerModal. Best-effort: when
// nothing is found the label flips to "no trailer" instead of erroring.
export default function TrailerButton({ kind, tmdbId, title, className }: Readonly<Props>) {
  const { t } = useTranslation()
  const [loading, setLoading] = useState(false)
  const [notFound, setNotFound] = useState(false)
  const [video, setVideo] = useState<TmdbVideo | null>(null)

  const resolve = async (): Promise<TmdbVideo | null> => {
    let k = kind
    let id = tmdbId
    if ((!k || !id) && title) {
      const m = await tmdbMatch(title)
      if (!m) return null
      k = m.kind
      id = m.tmdbId
    }
    if (!k || !id) return null
    const videos = await tmdbVideos(k, id)
    return videos[0] ?? null
  }

  const onClick = async (e: React.MouseEvent) => {
    e.stopPropagation()
    e.preventDefault()
    if (loading || notFound) return
    setLoading(true)
    try {
      const v = await resolve()
      if (v) setVideo(v)
      else setNotFound(true)
    } finally {
      setLoading(false)
    }
  }

  return (
    <>
      <button
        onClick={onClick}
        disabled={notFound}
        className={className ?? 'btn-secondary text-xs flex items-center gap-1.5'}
        title={notFound ? t('trailer.none') : t('trailer.watch')}
      >
        {loading ? <Loader2 className="w-4 h-4 animate-spin" /> : <Clapperboard className="w-4 h-4" />}
        <span>{notFound ? t('trailer.none') : t('trailer.watch')}</span>
      </button>
      {video && (
        <TrailerModal videoKey={video.key} title={video.name} onClose={() => setVideo(null)} />
      )}
    </>
  )
}
