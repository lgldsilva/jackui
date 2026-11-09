import { useEffect, useRef, useState } from 'react'
import { streamAdd, streamMetadata, type SearchResult, type StreamFile } from '../../api/client'
import { resolveTorrentFiles } from './resolveTorrentFiles'

type Msgs = { notYet: string; fallbackFailed: string }

/**
 * Loads a torrent's file list for the download picker while `enabled` (the
 * internal client is the destination). Re-runs when `result`/`enabled`
 * change; a superseded load is discarded, and disabling mid-load clears the
 * spinner. `onResolved` receives the files once (e.g. to seed a selection).
 */
export function useTorrentFiles(
  result: SearchResult | null,
  enabled: boolean,
  msgs: Msgs,
  onResolved: (files: StreamFile[]) => void,
) {
  const [files, setFiles] = useState<StreamFile[] | null>(null)
  const [loading, setLoading] = useState(false)
  const [error, setError] = useState('')
  // Latest callbacks/messages without re-triggering the load on every render.
  const latest = useRef({ msgs, onResolved })
  latest.current = { msgs, onResolved }

  useEffect(() => {
    setFiles(null)
    setError('')
    if (!result || !enabled) {
      setLoading(false)
      return
    }
    let cancelled = false
    setLoading(true)
    resolveTorrentFiles(result, { metadata: streamMetadata, add: streamAdd }, latest.current.msgs)
      .then((res) => {
        if (cancelled) return
        if (res.files) {
          setFiles(res.files)
          latest.current.onResolved(res.files)
        } else {
          setError(res.error)
        }
        setLoading(false)
      })
      .catch(() => { if (!cancelled) setLoading(false) }) // resolveTorrentFiles never rejects
    return () => { cancelled = true }
  }, [result, enabled])

  return { files, loading, error }
}
