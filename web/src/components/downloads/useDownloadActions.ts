import type { Dispatch, MutableRefObject, SetStateAction } from 'react'
import { useTranslation } from 'react-i18next'
import { useConfirm } from '../ConfirmDialog'
import { useToast } from '../Toast'
import {
  DownloadEntry, DownloadPriority, StreamPriority,
  downloadDelete, downloadPause, downloadResume, downloadStopSeed, downloadSetPriority,
  downloadPauseAll, downloadResumeAll, downloadBatchPause, downloadBatchResume, downloadBatchDelete,
  downloadBatchStopSeed,
  streamPause, streamResume, streamSetPriority, streamPauseAll, streamResumeAll, streamSetLimits,
  streamDrop, streamDropBatch,
} from '../../api/client'
import { markDeleted, clearDeleted, type PendingDeletes } from '../../lib/downloadsReconcile'
import { countTorrents } from '../../lib/downloadGroups'

type StatusGroups = {
  downloading: DownloadEntry[]
  paused: DownloadEntry[]
  completed: DownloadEntry[]
  failed: DownloadEntry[]
}

/** Unique non-empty infoHashes → one streamDropBatch (Perf #7). */
async function dropStreamsForEntries(entries: readonly DownloadEntry[]): Promise<void> {
  const hashes = [...new Set(entries.map(d => d.infoHash).filter((h): h is string => Boolean(h)))]
  if (hashes.length === 0) return
  await streamDropBatch(hashes).catch(() => {})
}

// useDownloadActions — every download/torrent mutation handler the page wires to
// its cards, toolbar and bulk bars. Kept out of DownloadsPage so the page reads
// as composition; state, refs and the loaders it drives are injected as deps.
export function useDownloadActions(deps: {
  readonly items: DownloadEntry[]
  readonly setItems: Dispatch<SetStateAction<DownloadEntry[]>>
  readonly selected: Set<number>
  readonly setSelected: Dispatch<SetStateAction<Set<number>>>
  readonly setBusyID: (v: number | null) => void
  readonly setBusyHash: (v: string | null) => void
  readonly setBulkBusy: (v: boolean) => void
  readonly setPromoteTargets: (v: DownloadEntry[] | null) => void
  readonly pendingDeletesRef: MutableRefObject<PendingDeletes>
  readonly reloadDownloadsRef: MutableRefObject<() => Promise<void>>
  readonly loadTorrents: () => Promise<void>
  readonly loadLimits: () => Promise<void>
  readonly mountedRef: MutableRefObject<boolean>
  readonly limitDownKB: string
  readonly limitUpKB: string
  readonly setLimitsSaving: (v: boolean) => void
  readonly setLimitsMsg: (v: string) => void
  readonly completedDownloads: DownloadEntry[]
  readonly downloadsByStatus: StatusGroups
  readonly queuedDownloads: DownloadEntry[]
}) {
  const {
    items, setItems, selected, setSelected, setBusyID, setBusyHash, setBulkBusy, setPromoteTargets,
    pendingDeletesRef, reloadDownloadsRef, loadTorrents, loadLimits, mountedRef,
    limitDownKB, limitUpKB, setLimitsSaving, setLimitsMsg,
    completedDownloads, downloadsByStatus, queuedDownloads,
  } = deps
  const confirm = useConfirm()
  const { notify, notifyError } = useToast()
  const { t } = useTranslation()

  const onPause = async (id: number) => {
    setBusyID(id)
    try { await downloadPause(id); await reloadDownloadsRef.current() } finally { setBusyID(null) }
  }
  const onResume = async (id: number) => {
    setBusyID(id)
    try { await downloadResume(id); await reloadDownloadsRef.current() } finally { setBusyID(null) }
  }
  const onSetPriority = async (id: number, priority: DownloadPriority) => {
    setBusyID(id)
    try { await downloadSetPriority(id, priority); await reloadDownloadsRef.current() } finally { setBusyID(null) }
  }
  const onDelete = async (id: number) => {
    if (!await confirm({ title: t('downloads.page.removeDownloadTitle'), message: t('downloads.page.removeDownloadMessage'), confirmLabel: t('downloads.page.remove'), destructive: true })) return
    const target = items.find(x => x.id === id)
    setBusyID(id)
    // OPTIMISTIC: hide the row immediately and shield it from in-flight polls.
    // The DELETE is authoritative + idempotent on the backend, so once it
    // resolves the row is gone for good; until then a stale 2s poll must not
    // re-show it.
    markDeleted(pendingDeletesRef.current, [id])
    setItems(prev => prev.filter(x => x.id !== id))
    try {
      await downloadPause(id).catch(() => {}) // pause before removing
      // Playing a download creates a stream/transcode session on anacrolix for the
      // SAME hash, separate from the row. Without dropping it, the "played" torrent reappears
      // as a Streaming card after the delete (symptom: "it stayed even after deleting").
      if (target?.infoHash) await streamDrop(target.infoHash).catch(() => {})
      await downloadDelete(id)
      await reloadDownloadsRef.current(); await loadTorrents()
    } catch (err) {
      // The DELETE genuinely failed (network/500) — un-hide the row so the user
      // sees reality instead of a silently-vanished item, and surface the error.
      clearDeleted(pendingDeletesRef.current, [id])
      await reloadDownloadsRef.current().catch(() => {})
      notifyError(err)
    } finally { setBusyID(null) }
  }
  // Opens the promote modal (single or batch). Single: passes just this item;
  // batch: passes all the selected ones. The UI does the rest.
  const onPromote = (d: DownloadEntry) => {
    setPromoteTargets([d])
  }
  const onPromoteSelected = () => {
    const targets = items.filter(d => selected.has(d.id) && d.status === 'completed')
    if (targets.length === 0) return
    setPromoteTargets(targets)
  }

  const onBatchPause = async () => {
    const ids = items.filter(d => selected.has(d.id) && (d.status === 'downloading' || d.status === 'queued')).map(d => d.id)
    if (ids.length === 0) return
    setBulkBusy(true)
    try { await downloadBatchPause(ids); await reloadDownloadsRef.current(); setSelected(new Set()) } finally { setBulkBusy(false) }
  }

  const onBatchResume = async () => {
    const ids = items.filter(d => selected.has(d.id) && d.status === 'paused').map(d => d.id)
    if (ids.length === 0) return
    setBulkBusy(true)
    try { await downloadBatchResume(ids); await reloadDownloadsRef.current(); setSelected(new Set()) } finally { setBulkBusy(false) }
  }

  const onBatchDelete = async () => {
    const targets = items.filter(d => selected.has(d.id))
    const ids = targets.map(d => d.id)
    if (ids.length === 0) return
    const torrentCount = countTorrents(targets)
    if (!await confirm({ title: t('downloads.page.removeDownloadsTitle'), message: t('downloads.page.removeDownloadsMessage', { count: torrentCount, fileCount: ids.length }), confirmLabel: t('downloads.page.remove'), destructive: true })) return
    setBulkBusy(true)
    try { await runBatchDelete(ids, targets) } finally { setBulkBusy(false) }
  }

  // runBatchDelete is the shared optimistic-delete flow for batch + per-torrent
  // removal: hide the rows, pause + drop stream sessions, fire the batch DELETE,
  // then surface any IDs the backend reported as failed (instead of letting the
  // poll silently re-show them).
  const runBatchDelete = async (ids: number[], targets: DownloadEntry[]) => {
    markDeleted(pendingDeletesRef.current, ids)
    setItems(prev => prev.filter(x => !ids.includes(x.id)))
    try {
      await downloadBatchPause(ids).catch(() => {}) // pause all before removing
      // Ends the stream/transcode sessions opened by Play — 1 batch (Perf #7).
      await dropStreamsForEntries(targets)
      const res = await downloadBatchDelete(ids)
      const failed = res.failed ?? []
      if (failed.length > 0) {
        clearDeleted(pendingDeletesRef.current, failed) // let the survivors come back into view
        const failedTargets = targets.filter(d => failed.includes(d.id))
        notify(t('downloads.page.removeFailed', { count: countTorrents(failedTargets), ids: failed.join(', #') }), 'error')
      }
      await reloadDownloadsRef.current(); await loadTorrents()
      setSelected(new Set())
    } catch (err) {
      clearDeleted(pendingDeletesRef.current, ids)
      await reloadDownloadsRef.current().catch(() => {})
      notifyError(err)
    }
  }

  const handleToggleSelectAll = () => {
    const next = selected.size === items.length ? new Set<number>() : new Set(items.map(d => d.id))
    setSelected(next)
  }
  const onPromoted = (result: { promoted: DownloadEntry[]; failed: { id: number; error: string }[] }) => {
    setPromoteTargets(null)
    if (result.failed.length > 0) {
      notify(t('downloads.page.promoteResult', {
        promoted: result.promoted.length,
        failed: result.failed.length,
        details: result.failed.map(f => `#${f.id}: ${f.error}`).join('; '),
      }), 'error')
    }
    // Clears the selection of the ones that succeeded
    if (result.promoted.length > 0) {
      const ok = new Set(result.promoted.map(d => d.id))
      setSelected(prev => {
        const next = new Set(prev)
        ok.forEach(id => next.delete(id))
        return next
      })
    }
    reloadDownloadsRef.current().catch(() => {})
    loadTorrents().catch(() => {})
  }
  const onStopSeed = async (id: number, name: string) => {
    if (!await confirm({ title: t('downloads.page.stopSeedTitle'), message: t('downloads.page.stopSeedMessage', { name }), confirmLabel: t('downloads.page.stop'), destructive: true })) return
    setBusyID(id)
    // OPTIMISTIC (same mechanism as delete): stop-seed now REMOVES the row on
    // the backend, so hide it right away and shield against stale 2s polls; on error
    // restore it so the user sees reality.
    markDeleted(pendingDeletesRef.current, [id])
    setItems(prev => prev.filter(x => x.id !== id))
    try {
      await downloadStopSeed(id)
      await reloadDownloadsRef.current(); await loadTorrents()
    } catch (err) {
      clearDeleted(pendingDeletesRef.current, [id])
      await reloadDownloadsRef.current().catch(() => {})
      notifyError(err)
    } finally { setBusyID(null) }
  }

  // ── Torrent-level actions (group of files with the same infoHash) ──
  const onPromoteMany = (ds: DownloadEntry[]) => { if (ds.length > 0) setPromoteTargets(ds) }
  const onDeleteMany = async (ds: DownloadEntry[]) => {
    const ids = ds.map(d => d.id)
    if (ids.length === 0) return
    if (!await confirm({ title: t('downloads.page.removeTorrentTitle'), message: t('downloads.page.removeTorrentFilesMessage', { count: ids.length }), confirmLabel: t('downloads.page.remove'), destructive: true })) return
    setBulkBusy(true)
    try { await runBatchDelete(ids, ds) } finally { setBulkBusy(false) }
  }
  const onStopSeedMany = async (ds: DownloadEntry[]) => {
    if (ds.length === 0) return
    if (!await confirm({ title: t('downloads.page.stopSeedTitle'), message: t('downloads.page.stopSeedManyMessage', { count: ds.length }), confirmLabel: t('downloads.page.stop'), destructive: true })) return
    setBulkBusy(true)
    const ids = ds.map(d => d.id)
    // OPTIMISTIC: the batch removes the rows on the backend; hide right away and undo only
    // the ones the server reports as failed.
    markDeleted(pendingDeletesRef.current, ids)
    setItems(prev => prev.filter(x => !ids.includes(x.id)))
    try {
      // Auto-chunked below the server cap; a rejection means the whole set
      // failed, otherwise `failed` lists the rows we couldn't stop. Surface it
      // instead of the old swallow-and-pretend-success.
      const res = await downloadBatchStopSeed(ids).catch(() => null)
      const failed = res?.failed ?? []
      const failedCount = res === null ? ds.length : failed.length
      if (failedCount > 0) {
        const failedIDs = res === null ? ids : failed
        clearDeleted(pendingDeletesRef.current, failedIDs)
        notify(t('downloads.page.stopSeedFailed', { count: failedCount }), 'error')
      }
      await reloadDownloadsRef.current()
      await loadTorrents()
    } finally { setBulkBusy(false) }
  }
  const onRetryMany = async (ds: DownloadEntry[]) => {
    const ids = ds.filter(d => d.status === 'failed').map(d => d.id)
    if (ids.length === 0) return
    setBulkBusy(true)
    try { await downloadBatchResume(ids); await reloadDownloadsRef.current() } finally { setBulkBusy(false) }
  }

  const onTorrentPause = async (hash: string) => {
    setBusyHash(hash)
    try { await streamPause(hash); await loadTorrents() } finally { setBusyHash(null) }
  }
  const onTorrentResume = async (hash: string) => {
    setBusyHash(hash)
    try { await streamResume(hash); await loadTorrents() } finally { setBusyHash(null) }
  }
  const onTorrentPriority = async (hash: string, priority: StreamPriority) => {
    setBusyHash(hash)
    try { await streamSetPriority(hash, priority); await loadTorrents() } finally { setBusyHash(null) }
  }
  const onTorrentDelete = async (hash: string) => {
    if (!await confirm({ title: t('downloads.page.removeTorrentTitle'), message: t('downloads.page.removeStreamingTorrentMessage'), confirmLabel: t('downloads.page.remove'), destructive: true })) return
    setBusyHash(hash)
    try {
      await streamDrop(hash)
      await loadTorrents()
    } catch (err) {
      // The backend now replies 409 with the reason when someone is still
      // watching (viewer lease) — the toast shows why instead of failing
      // silently.
      notifyError(err)
    } finally { setBusyHash(null) }
  }
  const onSaveLimits = async () => {
    setLimitsSaving(true); setLimitsMsg('')
    try {
      const down = limitDownKB.trim() === '' ? 0 : Math.max(0, Math.round(Number(limitDownKB) * 1024))
      const up = limitUpKB.trim() === '' ? 0 : Math.max(0, Math.round(Number(limitUpKB) * 1024))
      if (!Number.isFinite(down) || !Number.isFinite(up)) { setLimitsMsg(t('downloads.page.invalidValues')); return }
      await streamSetLimits({ down, up })
      setLimitsMsg(t('downloads.page.limitsApplied'))
      await loadLimits()
      globalThis.setTimeout(() => { if (mountedRef.current) setLimitsMsg('') }, 2500)
    } catch { setLimitsMsg(t('downloads.page.saveFailed')) } finally { setLimitsSaving(false) }
  }

  // Global batch actions (reused by the desktop's inline bar and the mobile
  // "Actions" Sheet).
  const doResumeAll = async () => {
    setBulkBusy(true)
    try {
      // streamPauseAll/resumeAll are admin-only (shared swarm); non-admins still
      // pause their download-queue rows. Ignore 403 so the bulk action succeeds.
      await Promise.all([downloadResumeAll(), streamResumeAll().catch(() => {})])
      await reloadDownloadsRef.current()
    }
    finally { setBulkBusy(false) }
  }
  const doPauseAll = async () => {
    setBulkBusy(true)
    try {
      await Promise.all([downloadPauseAll(), streamPauseAll().catch(() => {})])
      await reloadDownloadsRef.current()
    }
    finally { setBulkBusy(false) }
  }
  const doRemoveCompleted = async () => {
    const ok = await confirm({
      title: t('downloads.page.removeCompletedTitle'),
      message: t('downloads.page.removeCompletedMessage', { count: countTorrents(completedDownloads), fileCount: completedDownloads.length }),
      confirmLabel: t('downloads.page.remove'),
      destructive: true,
    })
    if (!ok) return
    setBulkBusy(true)
    try { await runBatchDelete(completedDownloads.map(d => d.id), completedDownloads) } finally { setBulkBusy(false) }
  }
  // Bulk cleanup by status — "clear failed" and "clear queue". Use case: the old
  // "Download all" (1 row PER file) could clog the queue with
  // hundreds of items; this removes the junk in 1 click without hunting checkboxes.
  const doClearByStatus = async (targets: DownloadEntry[], title: string, message: string) => {
    if (targets.length === 0) return
    const ok = await confirm({ title, message, confirmLabel: t('downloads.clear_confirm'), destructive: true })
    if (!ok) return
    setBulkBusy(true)
    try { await runBatchDelete(targets.map(d => d.id), targets) } finally { setBulkBusy(false) }
  }
  const doClearFailed = () => doClearByStatus(
    downloadsByStatus.failed,
    t('downloads.clear_failed_title'),
    t('downloads.clear_failed_message', { count: countTorrents(downloadsByStatus.failed), fileCount: downloadsByStatus.failed.length }),
  )
  const doClearQueued = () => doClearByStatus(
    queuedDownloads,
    t('downloads.clear_queued_title'),
    t('downloads.clear_queued_message', { count: countTorrents(queuedDownloads), fileCount: queuedDownloads.length }),
  )

  const onToggleSelected = (id: number) => setSelected(prev => {
    const next = new Set(prev)
    if (next.has(id)) next.delete(id); else next.add(id)
    return next
  })

  return {
    onPause, onResume, onSetPriority, onDelete, onPromote, onPromoteSelected,
    onBatchPause, onBatchResume, onBatchDelete, handleToggleSelectAll, onPromoted,
    onStopSeed, onPromoteMany, onDeleteMany, onStopSeedMany, onRetryMany,
    onTorrentPause, onTorrentResume, onTorrentPriority, onTorrentDelete, onSaveLimits,
    doResumeAll, doPauseAll, doRemoveCompleted, doClearFailed, doClearQueued, onToggleSelected,
  }
}
