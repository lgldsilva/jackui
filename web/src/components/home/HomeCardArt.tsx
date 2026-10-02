import { useEffect, useState } from 'react'
import { Film, Music, FileVideo } from 'lucide-react'
import { useThumbnail } from '../../lib/useThumbnail'
import { detectKind } from '../../lib/playable'
import { shouldMountArtImg, withArtBust } from '../../lib/artPresence'
import { streamArtURL } from '../../api/client'

// HomeCardArt fills a home rail card with the same layered art the rest of
// the app gets from Thumbnail: per-torrent resolved art on top, TMDB poster
// by title underneath, kind icon as the last resort. The rails used to mount
// a bare streamArt <img> and went blank grey on a 204 (no art resolved yet);
// a resolved-miss now still shows the title's TMDB poster instead.
//
// The root renders absolute-inset-0, so the parent keeps owning the aspect
// ratio box and any sibling overlays (play hover, progress bar) stack above.

type HomeCardArtProps = {
  readonly title: string
  readonly infoHash: string
  /** resolveArtBatch presence: true mounts the art GET, false skips it, undefined = legacy try. */
  readonly hasArt?: boolean
  /** Cache-buster for hashes that gained art mid-session (defeats a cached 204). */
  readonly bust?: number
  /**
   * Library's artMode pattern: while the batch hasn't hard-failed, only mount
   * the art GET once presence is KNOWN — an eager GET 204s faster than the
   * batch answers and would poison the card with artFailed before the result.
   */
  readonly requireKnown?: boolean
}

export default function HomeCardArt({ title, infoHash, hasArt, bust, requireKnown }: HomeCardArtProps) {
  const { ref, match, loaded } = useThumbnail<HTMLDivElement>(title)
  const [artFailed, setArtFailed] = useState(false)
  // A miss recorded before the batch answered (or under a previous presence)
  // must not stick once this hash gains art: reset on any presence/bust change,
  // not only when the hash itself changes.
  useEffect(() => { setArtFailed(false) }, [infoHash, hasArt, bust])
  const kind = detectKind(title)
  let FallbackIcon: typeof Film
  if (kind === 'audio') {
    FallbackIcon = Music
  } else if (match?.kind === 'tv') {
    FallbackIcon = FileVideo
  } else {
    FallbackIcon = Film
  }
  const showArt = shouldMountArtImg({ infoHash, hasArt, artFailed, requireKnown })

  return (
    <div ref={ref} className="absolute inset-0">
      {match?.posterUrl ? (
        <img
          src={match.posterUrl}
          alt=""
          loading="lazy"
          className="w-full h-full object-cover"
          // Broken TMDB URL (rare): hide and reveal the icon layer below.
          onError={(e) => { e.currentTarget.style.display = 'none' }}
        />
      ) : null}
      <div className={`absolute inset-0 flex items-center justify-center text-text-muted pointer-events-none ${match?.posterUrl ? 'invisible' : ''}`}>
        {loaded ? (
          <FallbackIcon className="w-10 h-10 opacity-60" />
        ) : (
          <div className="w-10 h-10 animate-pulse rounded bg-surface-secondary" />
        )}
      </div>
      {showArt ? (
        <img
          src={withArtBust(streamArtURL(infoHash), bust)}
          alt=""
          loading="lazy"
          className="absolute inset-0 w-full h-full object-cover"
          // 204/404 on the torrent art reveals the TMDB poster below.
          onError={() => setArtFailed(true)}
        />
      ) : null}
    </div>
  )
}
