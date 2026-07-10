import { useState, useEffect } from 'react'
import {
  SearchResult,
  TorrentInfo,
  pickTorrentSource,
  favoriteAdd,
  favoriteRemove,
  favoritesList,
} from '../../api/client'

// Favorite state for the currently-playing torrent: loads the initial flag when
// info arrives (match by infoHash, fall back to name) and toggles add/remove.
// Auto-favorite after N minutes of playback lives in the progress handler (it
// owns the accumulated watch time), not here.
export function useFavorites(info: TorrentInfo | null, result: SearchResult | null) {
  const [isFavorite, setIsFavorite] = useState(false)

  // Load initial favorite state when torrent info arrives. Match by infoHash
  // first (precise — same content always returns same hash) and fall back to
  // name only when needed. The old version matched ONLY by name, which broke
  // when `info.name` (anacrolix torrent.Name()) differed from the favorite's
  // stored name (which was the search result title at favorite-time) — common
  // for torrents whose name has trailing periods, encoded characters, etc.
  useEffect(() => {
    if (!info) return
    favoritesList()
      .then(list => setIsFavorite(list.some(f =>
        (f.infoHash?.toLowerCase() === info.infoHash?.toLowerCase())
        || f.name === info.name
      )))
      .catch(() => {})
  }, [info?.name, info?.infoHash])

  const toggleFavorite = async () => {
    if (!info) return
    const next = !isFavorite
    setIsFavorite(next)
    try {
      if (next) {
        // We have the real source URL via `result.magnetUri || result.link` (pickTorrentSource).
        // If the result came from a search, magnetUri is set; if from /library, the magnet was already saved.
        // Fallback to inferred magnet (no trackers) ONLY if nothing else available.
        const magnet = (result ? pickTorrentSource(result) : '')
          || (info.infoHash ? `magnet:?xt=urn:btih:${info.infoHash}` : '')
        await favoriteAdd(info.name, info.infoHash, magnet, 'manual')
      } else {
        await favoriteRemove(info.name)
      }
    } catch {
      setIsFavorite(!next) // revert on error
    }
  }

  return { isFavorite, setIsFavorite, toggleFavorite }
}
