import { useState, useCallback, useRef } from 'react'
import {
  SearchResult,
  TorrentInfo,
  streamFileURL,
  downloadLocalFileDirect,
  classifyCategory,
  isLocalHash,
  downloadCreate,
} from '../../api/client'
import { parentDir, filesUnderDir } from '../../lib/treeSelect'

// Groups the player's download entry points (📁↓ folder, "cache on server",
// local Electron download, auto-enqueue-next, AI category classify) and their
// state. Every UI path opens the unified DownloadModal via `playerDownload`.
export function usePlayerDownloads(deps: {
  info: TorrentInfo | null
  result: SearchResult | null
  selectedFile: number
  notifyError: (err: unknown) => void
}) {
  const { info, result, selectedFile, notifyError } = deps

  // Server-side background download state. Loading/success stay false now
  // that the button opens the unified modal (instead of downloading directly) — kept so
  // PlayerControlsPanel's interface isn't touched.
  const [serverDownloadLoading] = useState(false)
  const [serverDownloadSuccess] = useState(false)
  // Nested download modal target (destination + selection); indices pre-selects.
  const [playerDownload, setPlayerDownload] = useState<{ result: SearchResult; indices?: number[] } | null>(null)
  // Local (Electron) download with automatic categorization
  const [localDownloadLoading, setLocalDownloadLoading] = useState(false)
  const [overrideCategory, setOverrideCategory] = useState<string | null>(null)
  const [classifyingCat, setClassifyingCat] = useState(false)

  // Callback ref for the next file's auto-download: the function reads
  // buildDownloadResult+info (changes every render), so we mirror them into a ref
  // so the effect keyed by info doesn't need the callback as a dep.
  const enqueueNextDownloadRef = useRef<(fileIndex: number) => void>(() => {})

  // Builds the SearchResult for the modal from the current info/result. null for
  // local files (no magnet — the torrent client won't take them; they use LocalCacheButton).
  const buildDownloadResult = useCallback((): SearchResult | null => {
    if (!info || isLocalHash(info.infoHash)) return null
    const magnet = result?.magnetUri || `magnet:?xt=urn:btih:${info.infoHash}`
    if (result) return { ...result, magnetUri: magnet, infoHash: info.infoHash, title: result.title || info.name }
    return {
      title: info.name, tracker: '', categoryId: 0, category: '', size: 0, seeders: 0,
      leechers: 0, age: '', magnetUri: magnet, link: '', infoHash: info.infoHash, publishDate: '',
    }
  }, [info, result])

  // "Cache on server": opens the modal pre-selecting the playing file.
  const handleServerDownload = () => {
    const r = buildDownloadResult()
    if (!r) return
    setPlayerDownload({ result: r, indices: selectedFile >= 0 ? [selectedFile] : undefined })
  }

  // 📁↓ per file: download that file's whole folder (recursive). Opens the
  // modal with all of the folder's files pre-selected.
  const downloadFolderFromPlayer = useCallback((file: TorrentInfo['files'][number]) => {
    const r = buildDownloadResult()
    if (!r || !info) return
    const dir = parentDir(file.path)
    const indices = dir ? filesUnderDir(info.files, dir).map(f => f.index) : [file.index]
    setPlayerDownload({ result: r, indices })
  }, [buildDownloadResult, info])

  // Auto-enqueue callback: when the current streaming file finishes downloading,
  // enqueue the next file in the in-torrent queue as a background download.
  // Reuses buildDownloadResult for magnet/tracker/name synthesis.
  const enqueueNextDownload = useCallback((fileIndex: number) => {
    const r = buildDownloadResult()
    if (!r || !info) return
    const f = info.files.find(x => x.index === fileIndex)
    if (!f) return
    downloadCreate({
      infoHash: info.infoHash,
      fileIndex: f.index,
      magnet: r.magnetUri,
      name: r.title,
      filePath: f.path,
      fileSize: f.size,
      tracker: r.tracker || undefined,
    }).catch(() => {
      // Best-effort: never block playback or pollute the console.
      // downloadCreate is idempotent — safe to retry on next poll.
    })
  }, [buildDownloadResult, info])
  // Mirrors the callback into a ref to avoid a stale closure in the effect keyed by info.
  enqueueNextDownloadRef.current = enqueueNextDownload

  // 📁↓ on the folder row (tree): downloads the whole folder, recursively. The
  // node.path is the real path (even in collapsed single-child folders), so
  // filesUnderDir matches everything under it. Opens the modal with those files pre-checked.
  const downloadDirFromPlayer = useCallback((dirPath: string) => {
    const r = buildDownloadResult()
    if (!r || !info) return
    const indices = filesUnderDir(info.files, dirPath).map(f => f.index)
    if (indices.length === 0) return
    setPlayerDownload({ result: r, indices })
  }, [buildDownloadResult, info])

  // 'default' = don't force a category (let the backend categorize). The <select>
  // has a <option value="default">; mapping the state onto it avoids the orphan value
  // (Jackett's raw string, e.g. "Movies/HD", matches no option →
  // React warning + wrong category on the download).
  const effectiveCategory = overrideCategory ?? 'default'

  const handleLocalDownload = async () => {
    if (!info || selectedFile < 0) return
    setLocalDownloadLoading(true)
    try {
      const file = info.files[selectedFile]
      const name = file.path.split('/').pop() || info.name
      const apiPath = streamFileURL(info.infoHash, selectedFile)
      const categoryArg = effectiveCategory === 'default' ? undefined : effectiveCategory
      await downloadLocalFileDirect(apiPath, name, categoryArg)
    } catch (err) {
      notifyError(err)
    } finally {
      setLocalDownloadLoading(false)
    }
  }

  const handleClassifyCategory = async () => {
    if (!info) return
    setClassifyingCat(true)
    try {
      const res = await classifyCategory(info.name, result?.category ? String(result.category) : undefined)
      if (res.category && res.category !== 'other') {
        setOverrideCategory(res.category)
      }
    } catch { /* silent */ }
    setClassifyingCat(false)
  }

  return {
    serverDownloadLoading,
    serverDownloadSuccess,
    playerDownload,
    setPlayerDownload,
    localDownloadLoading,
    overrideCategory,
    setOverrideCategory,
    classifyingCat,
    effectiveCategory,
    enqueueNextDownloadRef,
    buildDownloadResult,
    handleServerDownload,
    downloadFolderFromPlayer,
    downloadDirFromPlayer,
    handleLocalDownload,
    handleClassifyCategory,
  }
}
