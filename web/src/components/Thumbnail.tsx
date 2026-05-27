import { useState } from 'react'
import { Film, Music, FileVideo } from 'lucide-react'
import { useThumbnail } from '../lib/useThumbnail'
import { detectKind } from '../lib/playable'
import { streamArtURL } from '../api/client'

// Thumbnail renders a mini-poster for a torrent title via the TMDB enrichment
// endpoint. It's the shared building block used by every card-style list in
// the app (search results, favorites, library, playlists, watchlist hits).
//
// Why a component instead of just the hook: most call-sites want the same
// "img-or-fallback-icon-or-skeleton" treatment, so wrapping that here makes
// each page a one-liner. Pages that need custom layout (e.g. ResultCard's
// inline poster next to a title block) can still consume the hook directly.

export type ThumbnailSize = 'sm' | 'md' | 'lg'

interface ThumbnailProps {
  title: string
  // categoryId helps detectKind() pick the right fallback icon when there's
  // no TMDB match (Music vs Film vs generic FileVideo).
  categoryId?: number
  size?: ThumbnailSize
  // When true, render a poster aspect (2:3). When false, render a square
  // 1:1 (useful for music albums; we'd prefer cover art there but TMDB
  // returns posters, so we still aspect-ratio square for music).
  className?: string
  // infoHash, when present, prefers the per-torrent resolved art (embedded
  // poster/cover or a captured frame, persisted server-side after a play) over
  // the title-based TMDB poster. Falls back to TMDB-by-title when the torrent
  // has no resolved art yet (the art endpoint 204s → <img> onError).
  infoHash?: string
}

// Tailwind-stable size classes — keep them static strings so JIT picks them up.
const SIZE_CLASSES: Record<ThumbnailSize, string> = {
  sm: 'w-10 h-[60px] sm:w-12 sm:h-[72px]',   // 40×60 / 48×72 — tight list rows
  md: 'w-12 h-[72px] sm:w-14 sm:h-[84px]',   // 48×72 / 56×84 — search card default
  lg: 'w-16 h-24 sm:w-20 sm:h-[120px]',      // 64×96 / 80×120 — favorites/library
}

const ICON_SIZES: Record<ThumbnailSize, string> = {
  sm: 'w-4 h-4',
  md: 'w-5 h-5',
  lg: 'w-7 h-7',
}

export default function Thumbnail({ title, categoryId = 0, size = 'md', className = '', infoHash }: ThumbnailProps) {
  const { ref, match, loaded } = useThumbnail<HTMLDivElement>(title)
  const [artFailed, setArtFailed] = useState(false)
  const kind = detectKind(title, categoryId)
  const dim = SIZE_CLASSES[size]
  const iconDim = ICON_SIZES[size]
  const FallbackIcon = kind === 'audio' ? Music : (match?.kind === 'tv' ? FileVideo : Film)
  const showArt = !!infoHash && !artFailed

  return (
    <div
      ref={ref}
      className={`${dim} flex-shrink-0 rounded overflow-hidden border border-gray-700 bg-gray-900 relative ${className}`}
      // The tooltip duplicates info shown elsewhere in the card; keep it for
      // accessibility / hover discovery when the title is truncated.
      title={match ? `${match.title}${match.year ? ' (' + match.year + ')' : ''}${match.overview ? ' — ' + match.overview : ''}` : title}
    >
      {match?.posterUrl ? (
        <img
          src={match.posterUrl}
          alt={match.title}
          loading="lazy"
          className="w-full h-full object-cover"
          // If the TMDB poster URL 404s (rare but happens after deletions),
          // swap the <img> for the fallback icon by hiding it — the icon
          // sibling sits underneath with absolute positioning.
          onError={(e) => { (e.currentTarget as HTMLImageElement).style.display = 'none' }}
        />
      ) : null}
      {/* Fallback layer: shown while waiting OR when no match was found.
          Positioned absolute under the <img> so a broken poster falls back
          gracefully via the onError handler above without re-rendering. */}
      <div className={`absolute inset-0 flex items-center justify-center text-gray-600 pointer-events-none ${match?.posterUrl ? 'invisible' : ''}`}>
        {!loaded ? (
          <div className={`${iconDim} animate-pulse rounded bg-gray-800`} />
        ) : (
          <FallbackIcon className={iconDim} />
        )}
      </div>
      {/* Top layer: per-torrent resolved art (poster/cover/frame). Covers the
          TMDB poster + fallback when present; a 204/404 hides it (onError),
          revealing the layers below. Only mounts when we have an info_hash. */}
      {showArt && (
        <img
          src={streamArtURL(infoHash!)}
          alt={title}
          loading="lazy"
          className="absolute inset-0 w-full h-full object-cover"
          onError={() => setArtFailed(true)}
        />
      )}
    </div>
  )
}
