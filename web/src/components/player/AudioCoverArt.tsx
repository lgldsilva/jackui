import { useEffect, useState } from 'react'
import { Volume2 } from 'lucide-react'
import { TorrentInfo, streamArtworkURL, streamArtURL, resolveArt, isLocalHash, parseLocalHash, localAudioCoverURL } from '../../api/client'

// audioCoverURL picks the cover source: a LOCAL file serves the EMBEDDED cover (dedicated
// headerless route via ?token=); torrent uses the per-file extracted art. Both
// answer 204 when there's no image (the <img> onError hides it).
export function audioCoverURL(info: TorrentInfo, selectedFile: number, mediaToken: string): string {
  if (isLocalHash(info.infoHash)) {
    const loc = parseLocalHash(info.infoHash)
    if (loc) return localAudioCoverURL(loc.mount, loc.path, mediaToken || undefined)
  }
  return streamArtworkURL(info.infoHash, selectedFile, mediaToken || undefined)
}

// AudioCoverArt: album cover behind the audio player. Fallback when the file
// has NO embedded image: for torrents, it triggers the server's art chain
// (embedded → TMDB → web search, music-aware) and shows whatever resolves; local
// files resolve the fallback server-side, so here they just hide on a miss.
export function AudioCoverArt({ info, selectedFile, mediaToken }: {
  readonly info: TorrentInfo | null
  readonly selectedFile: number
  readonly mediaToken: string
}) {
  const [fallbackSrc, setFallbackSrc] = useState('')
  const [hidden, setHidden] = useState(false)
  useEffect(() => { setFallbackSrc(''); setHidden(false) }, [info?.infoHash, selectedFile])
  if (!info) return null

  const handleError = async () => {
    if (fallbackSrc || isLocalHash(info.infoHash)) { setHidden(true); return }
    const src = await resolveArt(info.infoHash, -1, info.name).catch(() => null)
    if (src) setFallbackSrc(streamArtURL(info.infoHash))
    else setHidden(true)
  }

  const url = fallbackSrc || audioCoverURL(info, selectedFile, mediaToken)
  return (
    <div className="absolute inset-0 flex items-center justify-center bg-gradient-to-br from-gray-800 to-gray-900 pointer-events-none">
      <Volume2 className="absolute w-12 h-12 text-text-muted" />
      {!hidden && (
        <img
          key={url}
          src={url}
          alt=""
          className="relative max-h-full max-w-full object-contain rounded shadow-2xl"
          onError={handleError}
        />
      )}
    </div>
  )
}
