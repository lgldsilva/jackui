import { useRef, useState, type MutableRefObject } from 'react'
import { errMessage } from '../../lib/errMessage'
import { useMountedRef } from '../../lib/useMountedRef'
import { useVisiblePolling } from '../../lib/useVisiblePolling'

const POLL_MS = 2000

type DownloadsPoll = {
  /** Message of the last failed list load; null once a load succeeds again. */
  readonly loadError: string | null
  /** Reload the list (poll, filters, mutations). Never rejects. */
  readonly reloadDownloadsRef: MutableRefObject<() => Promise<void>>
}

/**
 * Owns the Downloads list refresh loop and its failure state.
 *
 * `reload` must REJECT on failure. The failure is kept as a single inline
 * state instead of a toast: with the backend unreachable the 2s poll fails on
 * every cycle, and a toast per cycle buried the page under identical errors.
 * `pollExtra` runs on the same cadence (the active-torrents list).
 */
export function useDownloadsPoll(reload: () => Promise<void>, pollExtra: () => unknown): DownloadsPoll {
  const [loadError, setLoadError] = useState<string | null>(null)
  const mountedRef = useMountedRef()

  const reloadDownloadsRef = useRef<() => Promise<void>>(async () => {})
  reloadDownloadsRef.current = async () => {
    try {
      await reload()
      if (mountedRef.current) setLoadError(null)
    } catch (err) {
      if (mountedRef.current) setLoadError(errMessage(err))
    }
  }

  useVisiblePolling(() => Promise.all([reloadDownloadsRef.current(), pollExtra()]), POLL_MS)

  return { loadError, reloadDownloadsRef }
}
