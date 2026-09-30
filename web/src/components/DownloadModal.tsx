import { useState, useEffect, useRef } from 'react'
import { useTranslation } from 'react-i18next'
import { useNavigate } from 'react-router-dom'
import { Download, Loader2, Clock, Server, CheckCircle2 } from 'lucide-react'
import {
  SearchResult, DownloadClient, getClients, downloadTorrent, downloadCreate,
  downloadBatchCreate, buildBatchFiles, isWholeTorrentSelection, WHOLE_TORRENT_FILE_INDEX,
  createParamsWhenFilesUnknown,
  streamAdd, streamMetadata, StreamFile, TorrentInfo,
  dedupCheck, dedupLink, DedupCheckResult,
} from '../api/client'
import { Sheet } from './Sheet'
import { load, save, pushMRU } from '../lib/storage'
import DownloadDestinationPicker from './DownloadDestinationPicker'
import { FileSelectionSection } from './files/FileSelectionSection'
import DedupPrompt from './DedupPrompt'
import { linkableItems, planAfterLink } from '../lib/dedup'
import { errMessage } from '../lib/errMessage'
import { canonicalInfoHash } from '../lib/magnet'
import { defaultSelectedFiles } from '../lib/torrentSelect'

// Sentinel client id for "download inside JackUI itself" (anacrolix → /data),
// as opposed to handing the torrent to an external qBittorrent/Transmission.
const INTERNAL_ID = '__internal__'

// Initial selection of the resolved files: the explicit pre-selection (e.g. the folder
// coming from the player) takes priority, filtered against what actually resolved
// (defensive: cache vs streamAdd); falls back to the heuristic when empty/absent.
function pickInitialSelection(files: StreamFile[], initial?: readonly number[]): Set<number> {
  if (initial) {
    const preset = new Set(files.filter(f => initial.includes(f.index)).map(f => f.index))
    if (preset.size > 0) return preset
  }
  return defaultSelectedFiles(files)
}

type DownloadDest = { destBase: string; destSubdir: string }
const EMPTY_DEST: DownloadDest = { destBase: '', destSubdir: '' }

type SuccessInfo = {
  fileCount: number
  destLabel: string
  category?: string
  external: boolean
}

function queuedFileCount(files: StreamFile[] | null, selection: Set<number>): number {
  if (!files || files.length === 0) return 1
  if (isWholeTorrentSelection(files, selection)) return files.length
  return selection.size
}

function internalDestLabel(dest: DownloadDest, t: TFn): string {
  const parts = [dest.destBase, dest.destSubdir].filter(Boolean)
  return parts.length > 0 ? parts.join('/') : t('downloads.modal.defaultDest')
}

// Minimal translate-fn signature so the module-level helper can format the
// user-facing partial/validation messages with the component's `t`.
type TFn = (key: string, opts?: Record<string, unknown>) => string

async function downloadInternal(
  result: SearchResult,
  files: StreamFile[] | null,
  selectedFiles: Set<number>,
  streamAdd: (source: string) => Promise<any>,
  downloadCreate: (opts: any) => Promise<any>,
  t: TFn,
  dest: DownloadDest = EMPTY_DEST,
): Promise<string | null> {
  let magnet = result.magnetUri || (result.infoHash ? `magnet:?xt=urn:btih:${result.infoHash}` : '')
  let infoHash = result.infoHash || canonicalInfoHash(result.infoHash, magnet)
  if ((!infoHash || !magnet) && result.link) {
    const info = await streamAdd(result.link)
    infoHash = info.infoHash
    magnet = result.link
  }
  if (!infoHash || !magnet) throw new Error('missing magnet/infoHash — cannot download internally')

  if ((files?.length ?? 0) > 0) {
    const all = files ?? []
    const picks = all.filter(f => selectedFiles.has(f.index))
    if (picks.length === 0) throw new Error(t('downloads.modal.selectAtLeastOne'))
    // All files selected → ONE "whole torrent" row (fileIndex=-2):
    // anacrolix downloads the whole torrent via file priorities, not N downloads.
    // A 778-file pack becomes 1 row → ends the explosion that inflated the
    // list and made /api/downloads slow. A subset falls to the batch (1 row
    // per chosen file), preserving granularity.
    if (isWholeTorrentSelection(all, selectedFiles)) {
      await downloadCreate({
        infoHash, fileIndex: WHOLE_TORRENT_FILE_INDEX, magnet, name: result.title,
        filePath: '', fileSize: all.reduce((s, f) => s + (f.size || 0), 0),
        tracker: result.tracker || undefined, category: result.category || undefined,
        destBase: dest.destBase || undefined, destSubdir: dest.destSubdir || undefined,
      })
      return null
    }
    // ONE batch request (before: 1 POST per file). The backend inserts everything in an
    // all-or-nothing transaction — no more partial success to report.
    const res = await downloadBatchCreate({
      infoHash, magnet, name: result.title,
      tracker: result.tracker || undefined, category: result.category || undefined,
      destBase: dest.destBase || undefined, destSubdir: dest.destSubdir || undefined,
      files: buildBatchFiles(picks),
    })
    if (res.created.length !== picks.length) {
      return t('downloads.modal.partialQueued', { created: res.created.length, total: picks.length })
    }
  } else {
    // Without a file list: auto-pick on the backend (pickBestFile). Never fileIndex 0
    // (usually .nfo spam) — see createParamsWhenFilesUnknown + tests.
    await downloadCreate(createParamsWhenFilesUnknown({
      infoHash, magnet, name: result.title,
      tracker: result.tracker || undefined, category: result.category || undefined,
      destBase: dest.destBase || undefined, destSubdir: dest.destSubdir || undefined,
    }))
  }
  return null
}

type DownloadModalProps = {
  readonly result: SearchResult | null
  readonly onClose: () => void
  // Optional pre-selection (e.g. "download this folder" in the player passes the folder's
  // indices). When absent, the modal applies the defaultSelected heuristic.
  readonly initialFileIndices?: readonly number[]
  // Nested modal (inside the player): doesn't lock scroll again (the player already
  // holds the lock; useScrollLock isn't refcounted) and raises the z-index.
  readonly nested?: boolean
}

const KEY_CLIENT = 'lastClientId'
const KEY_PATH = 'lastSavePath'
const KEY_RECENT_PATHS = 'recentSavePaths'

export default function DownloadModal({ result, onClose, initialFileIndices, nested }: DownloadModalProps) {
  const { t } = useTranslation()
  const navigate = useNavigate()
  const [clients, setClients] = useState<DownloadClient[]>([])
  const [selectedClientId, setSelectedClientId] = useState('')
  const [savePath, setSavePath] = useState('')
  const [recentPaths, setRecentPaths] = useState<string[]>([])
  const [loading, setLoading] = useState(false)
  const [error, setError] = useState('')
  const [success, setSuccess] = useState(false)
  const [successInfo, setSuccessInfo] = useState<SuccessInfo | null>(null)
  const [showRecent, setShowRecent] = useState(false)
  // Torrent files preview — when destination = internal JackUI, the picker shows
  // the list for the user to check/uncheck what to download. null = still loading
  // or external destination (an external client downloads everything, no way to filter).
  const [files, setFiles] = useState<StreamFile[] | null>(null)
  const [filesLoading, setFilesLoading] = useState(false)
  const [filesError, setFilesError] = useState('')
  const [selectedFiles, setSelectedFiles] = useState<Set<number>>(new Set())
  // Chosen destination for the internal download (#16); empty = default dir.
  const [dest, setDest] = useState<{ destBase: string; destSubdir: string }>({ destBase: '', destSubdir: '' })
  const pathInputRef = useRef<HTMLInputElement>(null)
  // clientsLoaded avoids the modal flash while the client list loads.
  const [clientsLoaded, setClientsLoaded] = useState(false)
  // Cross-torrent dedup (#23): set when /dedup-check finds files the user already
  // has → the DedupPrompt asks whether to link them instead of re-downloading.
  const [dedup, setDedup] = useState<DedupCheckResult | null>(null)

  useEffect(() => {
    if (!result) return

    setClientsLoaded(false)
    setError('')
    setSuccess(false)
    setSuccessInfo(null)
    setFiles(null)
    setFilesError('')
    setSelectedFiles(new Set())
    setDedup(null)
    setRecentPaths(load<string[]>(KEY_RECENT_PATHS, []))
    setSavePath(load<string>(KEY_PATH, ''))

    getClients()
      .then((data) => {
        setClients(data)
        // Priority: last-used > default external > internal (always available)
        const lastId = load<string>(KEY_CLIENT, '')
        const lastValid = lastId === INTERNAL_ID || data.some((c) => c.id === lastId)
        const fallback = data.find((c) => c.default)?.id || INTERNAL_ID
        setSelectedClientId(lastValid ? lastId : fallback)
      })
      .catch(() => {
        // Even with no external clients, internal download is always possible.
        setClients([])
        setSelectedClientId(INTERNAL_ID)
      })
      .finally(() => setClientsLoaded(true))
  }, [result])

  // Loads the file list when the destination becomes internal. Tries the metadata
  // cache first (instant if the torrent was played/downloaded before);
  // if empty, does streamAdd to resolve. Failures become a friendly notice — the user
  // can still click Confirm and the worker tries to resolve on the backend.
  useEffect(() => {
    if (!result || selectedClientId !== INTERNAL_ID) {
      setFiles(null)
      return
    }
    let cancelled = false
    setFilesLoading(true)
    setFilesError('')
    const magnet = result.magnetUri || (result.infoHash ? `magnet:?xt=urn:btih:${result.infoHash}` : '')
    const infoHash = result.infoHash || canonicalInfoHash(result.infoHash, magnet)
    const sourceForAdd = magnet || result.link || ''
    ;(async () => {
      let info: TorrentInfo | null = null
      if (infoHash) info = await streamMetadata(infoHash)
      if (!info && sourceForAdd) {
        try { info = await streamAdd(sourceForAdd) } catch (e) {
          if (!cancelled) setFilesError((e as Error)?.message || t('downloads.modal.metadataResolveFailed'))
        }
      }
      if (cancelled) return
      if ((info?.files?.length ?? 0) > 0) {
        const resolved = info!.files ?? []
        setFiles(resolved)
        setSelectedFiles(pickInitialSelection(resolved, initialFileIndices))
      } else if (!filesError) {
        setFilesError(t('downloads.modal.metadataNotYet'))
      }
      setFilesLoading(false)
    })()
    return () => { cancelled = true }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [result, selectedClientId])

  // finishInternal enqueues the internal download for `selection`. Clears the
  // dedup prompt either way so an error surfaces in the modal.
  const finishInternal = async (filesArg: StreamFile[] | null, selection: Set<number>) => {
    const err = await downloadInternal(result!, filesArg, selection, streamAdd, downloadCreate, t, dest)
    setDedup(null)
    if (err) { setError(err); return }
    save(KEY_CLIENT, INTERNAL_ID)
    setSuccessInfo({
      fileCount: queuedFileCount(filesArg, selection),
      destLabel: internalDestLabel(dest, t),
      category: result!.category || undefined,
      external: false,
    })
    setSuccess(true)
  }

  const handleDownload = async () => {
    if (!result) return
    setLoading(true)
    setError('')
    try {
      if (selectedClientId === INTERNAL_ID) {
        // Probe for files already on disk before enqueuing. A match opens the
        // DedupPrompt (the enqueue then happens there); a probe error or no match
        // falls straight through to the normal download.
        const magnet = result.magnetUri || (result.infoHash ? `magnet:?xt=urn:btih:${result.infoHash}` : '')
        const dr = magnet ? await dedupCheck(magnet).catch(() => null) : null
        if (dr && dr.matches.length > 0) { setDedup(dr); return }
        await finishInternal(files, selectedFiles)
      } else {
        await downloadTorrent(selectedClientId, result.magnetUri || '', result.link || '', savePath || undefined)
        save(KEY_CLIENT, selectedClientId)
        if (savePath.trim()) { save(KEY_PATH, savePath.trim()); pushMRU(KEY_RECENT_PATHS, savePath.trim()) }
        setSuccessInfo({
          fileCount: 1,
          destLabel: savePath.trim() || t('downloads.modal.clientDefault'),
          category: result.category || undefined,
          external: true,
        })
        setSuccess(true)
      }
    } catch (err: unknown) {
      setError(errMessage(err))
    } finally {
      setLoading(false)
    }
  }

  // DedupPrompt → "use existing": link the mount-backed matches, then enqueue
  // only what's still missing (or nothing if it's all already here).
  const handleUseExisting = async () => {
    if (!result || !dedup) return
    setLoading(true)
    setError('')
    try {
      const plan = planAfterLink(!!files, [...selectedFiles], dedup.matches, dedup.totalFiles)
      // No per-file list to exclude the linked files from → linking here would
      // leave the whole-torrent download re-fetching them from the swarm. Skip the
      // link and just download (the worker still auto-links local-certain matches).
      if (plan.kind === 'whole') {
        await finishInternal(files, selectedFiles)
        return
      }
      const magnet = result.magnetUri || (result.infoHash ? `magnet:?xt=urn:btih:${result.infoHash}` : '')
      const infoHash = result.infoHash || canonicalInfoHash(result.infoHash, magnet)
      const items = linkableItems(dedup.matches)
      if (items.length > 0) await dedupLink({ infoHash, magnet, name: result.title, items })
      if (plan.kind === 'files') {
        await finishInternal(files, new Set(plan.indices))
      } else {
        save(KEY_CLIENT, INTERNAL_ID)
        setDedup(null)
        setSuccessInfo({
          fileCount: 0,
          destLabel: internalDestLabel(dest, t),
          category: result.category || undefined,
          external: false,
        })
        setSuccess(true)
      }
    } catch (err: unknown) {
      setDedup(null)
      setError(errMessage(err))
    } finally {
      setLoading(false)
    }
  }

  // DedupPrompt → "download anyway": ignore the matches, enqueue as usual.
  const handleDownloadAll = async () => {
    setLoading(true)
    setError('')
    try {
      await finishInternal(files, selectedFiles)
    } catch (err: unknown) {
      setDedup(null)
      setError(errMessage(err))
    } finally {
      setLoading(false)
    }
  }

  const pickRecentPath = (p: string) => {
    setSavePath(p)
    setShowRecent(false)
    pathInputRef.current?.focus()
  }

  if (!result) return null
  // Still deciding (loading clients) — don't flash the modal.
  if (!clientsLoaded) return null
  // Dedup: the torrent has files the user already has → ask before downloading.
  if (dedup) {
    return (
      <DedupPrompt
        matches={dedup.matches}
        totalFiles={dedup.totalFiles}
        busy={loading}
        onUseExisting={handleUseExisting}
        onDownloadAll={handleDownloadAll}
        onCancel={() => setDedup(null)}
      />
    )
  }

  return (
    <Sheet
      open
      onClose={onClose}
      size="lg"
      lockScroll={!nested}
      zClass={nested ? 'z-[70]' : undefined}
      title={t('downloads.modal.title')}
      icon={<Download className="w-4 h-4 text-green-500 flex-shrink-0" />}
      footer={
        success ? (
          <div className="flex gap-3">
            <button onClick={onClose} className="btn-secondary flex-1">
              {t('common.dismiss')}
            </button>
            <button
              onClick={() => { onClose(); navigate('/downloads') }}
              className="btn-primary flex-1"
            >
              {t('downloads.modal.viewDownloads')}
            </button>
          </div>
        ) : (
        <div className="flex gap-3">
          <button onClick={onClose} className="btn-secondary flex-1">
            {t('downloads.modal.cancel')}
          </button>
          <button
            onClick={handleDownload}
            disabled={loading || !selectedClientId}
            className="btn-primary flex-1 flex items-center justify-center gap-2 disabled:opacity-50"
          >
            {loading ? <Loader2 className="w-4 h-4 animate-spin" /> : <Download className="w-4 h-4" />}
            {t('downloads.modal.confirm')}
          </button>
        </div>
        )
      }
    >
      <div className="flex flex-col gap-4">
          <div className="bg-surface rounded-lg p-3">
            <p className="text-sm text-text-primary line-clamp-2">{result.title}</p>
            <p className="text-xs text-text-muted mt-1">{result.tracker}</p>
          </div>

          <div>
            <label htmlFor="download-client" className="block text-sm font-medium text-text-primary mb-1.5">
              {t('downloads.modal.destination')}
            </label>
            <select
              id="download-client"
              value={selectedClientId}
              onChange={(e) => setSelectedClientId(e.target.value)}
              className="input-field"
            >
              <option value={INTERNAL_ID}>{t('downloads.modal.internalOption')}</option>
              {clients.map((c) => (
                <option key={c.id} value={c.id}>
                  {c.name} ({c.type})
                </option>
              ))}
            </select>
            {selectedClientId === INTERNAL_ID && (
              <p className="text-[11px] text-text-muted mt-1 flex items-center gap-1">
                <Server className="w-3 h-3" />
                {t('downloads.modal.internalHint')}
              </p>
            )}
          </div>

          {/* File picker — internal destination only. External hands the whole
              torrent to the client, no way to filter files. */}
          {selectedClientId === INTERNAL_ID && (
            <div>
              {filesLoading && (
                <div className="flex items-center gap-2 text-xs text-text-muted py-2">
                  <Loader2 className="w-3.5 h-3.5 animate-spin" />
                  {t('downloads.modal.loadingMetadata')}
                </div>
              )}
              {!filesLoading && filesError && !files && (
                <p className="text-xs text-amber-400 bg-amber-500/10 border border-amber-500/20 rounded px-2 py-1.5">
                  {filesError}
                </p>
              )}
              {(files?.length ?? 0) > 0 && (
                <FileSelectionSection
                  files={files ?? []}
                  selected={selectedFiles}
                  onChange={setSelectedFiles}
                />
              )}
            </div>
          )}

          {/* Destination picker — internal download only (#16). */}
          {selectedClientId === INTERNAL_ID && (
            <DownloadDestinationPicker onChange={setDest} />
          )}

          <div className={`relative ${selectedClientId === INTERNAL_ID ? 'hidden' : ''}`}>
            <label className="block text-sm font-medium text-text-primary mb-1.5">
              {t('downloads.modal.destFolder')}{' '}
              <span className="text-text-muted font-normal">{t('downloads.modal.optional')}</span>
            </label>
            <div className="relative">
              <input
                ref={pathInputRef}
                type="text"
                value={savePath}
                onChange={(e) => setSavePath(e.target.value)}
                onFocus={() => setShowRecent(recentPaths.length > 0)}
                onBlur={() => setTimeout(() => setShowRecent(false), 150)}
                placeholder="/downloads/filmes"
                className="input-field pr-10"
              />
              {recentPaths.length > 0 && (
                <button
                  type="button"
                  onMouseDown={(e) => { e.preventDefault(); setShowRecent(s => !s) }}
                  className="absolute right-3 top-1/2 -translate-y-1/2 text-text-muted hover:text-text-primary"
                  title={t('downloads.modal.recentFolders')}
                >
                  <Clock className="w-4 h-4" />
                </button>
              )}
            </div>

            {showRecent && recentPaths.length > 0 && (
              <div className="absolute z-10 left-0 right-0 mt-1 bg-surface border border-default rounded-lg shadow-xl max-h-48 overflow-y-auto">
                {recentPaths.map((p) => (
                  <button
                    key={p}
                    type="button"
                    onMouseDown={(e) => { e.preventDefault(); pickRecentPath(p) }}
                    className="w-full text-left px-3 py-2 text-sm text-text-primary hover:bg-surface-secondary transition-colors truncate"
                    title={p}
                  >
                    {p}
                  </button>
                ))}
              </div>
            )}
          </div>

          {error && (
            <div className="bg-red-500/10 border border-red-500/30 text-red-400 text-sm rounded-lg p-3">
              {error}
            </div>
          )}
          {success && successInfo && (
            <div className="bg-green-500/10 border border-green-500/30 text-green-400 text-sm rounded-lg p-3 flex flex-col gap-2" role="status">
              <div className="flex items-center gap-2 font-medium">
                <CheckCircle2 className="w-4 h-4 flex-shrink-0" />
                {t('downloads.modal.success')}
              </div>
              <ul className="text-xs text-green-300/90 space-y-1 pl-6 list-disc">
                {successInfo.fileCount > 0 && (
                  <li>{t('downloads.modal.successFiles', { count: successInfo.fileCount })}</li>
                )}
                <li>{t('downloads.modal.successDest', { dest: successInfo.destLabel })}</li>
                {successInfo.category && (
                  <li>{t('downloads.modal.successCategory', { category: successInfo.category })}</li>
                )}
              </ul>
            </div>
          )}
      </div>
    </Sheet>
  )
}
