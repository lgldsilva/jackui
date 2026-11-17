import { useCallback, useEffect, useRef, useState } from 'react'
import { favoritesList, folderList, StreamFavorite, FavoriteFolder } from '../api/client'
import { errMessage } from './errMessage'
import { isRevealHidden } from './reveal'
import { useLatestRequest } from './useLatestRequest'

// Probes persist their snapshot to the metadata cache a few seconds after
// they start; re-pull once they have had time to land.
const QUIET_RELOAD_DELAY_MS = 11_000

/**
 * Favorites + folders for FavoritesPage, loaded so the hidden curtain stays
 * hard: every fetch reads the reveal flag at call time (never a value captured
 * by an older render or timer) and its response is dropped when the flag
 * changed or a newer load started while it was in flight. Closing the curtain
 * also empties the lists right away, so nothing revealed stays on screen while
 * (or if) the non-revealed reload fails.
 */
export function useFavoritesData(revealHidden: boolean) {
  const [favs, setFavs] = useState<StreamFavorite[]>([])
  const [folders, setFolders] = useState<FavoriteFolder[]>([])
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState('')
  const loadReq = useLatestRequest()
  const quietReq = useLatestRequest()
  const quietTimer = useRef<ReturnType<typeof setTimeout> | null>(null)

  const load = useCallback(async () => {
    const reveal = isRevealHidden()
    const isLatest = loadReq.begin()
    quietReq.invalidate()
    const current = () => isLatest() && isRevealHidden() === reveal
    setLoading(true)
    setError('')
    try {
      const [favsList, foldersList] = await Promise.all([favoritesList(reveal), folderList(reveal)])
      if (!current()) return
      setFavs(favsList || [])
      setFolders(foldersList || [])
    } catch (e: unknown) {
      if (current()) setError(errMessage(e))
    } finally {
      if (isLatest()) setLoading(false)
    }
  }, [loadReq, quietReq])

  // Silent re-fetch (no loading spinner) — keeps scroll/selection.
  const reloadFavsQuiet = useCallback(async () => {
    const reveal = isRevealHidden()
    const isLatest = quietReq.begin()
    try {
      const favsList = await favoritesList(reveal)
      if (isLatest() && isRevealHidden() === reveal) setFavs(favsList || [])
    } catch { /* keep the current list on a transient failure */ }
  }, [quietReq])

  const cancelQuietReload = useCallback(() => {
    if (quietTimer.current !== null) clearTimeout(quietTimer.current)
    quietTimer.current = null
  }, [])

  const scheduleQuietReload = useCallback(() => {
    cancelQuietReload()
    quietTimer.current = setTimeout(() => {
      quietTimer.current = null
      void reloadFavsQuiet()
    }, QUIET_RELOAD_DELAY_MS)
  }, [cancelQuietReload, reloadFavsQuiet])

  // Load on mount and whenever the curtain flips. The cleanup runs on every
  // flip and on unmount, so a pending delayed reload never outlives the reveal
  // state it was scheduled under.
  useEffect(() => {
    if (!revealHidden) {
      setFavs(prev => (prev.length ? [] : prev))
      setFolders(prev => (prev.length ? [] : prev))
    }
    void load()
    return cancelQuietReload
  }, [revealHidden, load, cancelQuietReload])

  return { favs, setFavs, folders, setFolders, loading, error, load, scheduleQuietReload }
}
