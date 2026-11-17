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
 * (or if) the non-revealed reload fails. Handlers that patch the lists after
 * an awaited mutation go through `mutate` for the same reason.
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

  // Runs a mutation, then its local patch of favs/folders. The response was
  // produced for the reveal state the request was sent under, so if the curtain
  // flipped meanwhile the patch is skipped (it could put a hidden item back, or
  // keep one that the mutation just moved under the curtain) and the lists are
  // re-fetched for the current state instead.
  const mutate = useCallback(async <T,>(run: () => Promise<T>, patch: (result: T) => void): Promise<T> => {
    const reveal = isRevealHidden()
    const result = await run()
    if (isRevealHidden() === reveal) patch(result)
    else void load()
    return result
  }, [load])

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

  return { favs, setFavs, folders, setFolders, loading, error, load, reloadFavsQuiet, scheduleQuietReload, mutate }
}
