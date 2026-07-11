import type { Dispatch, MutableRefObject, SetStateAction } from 'react'
import { useTranslation } from 'react-i18next'
import { useConfirm } from '../ConfirmDialog'
import { useToast } from '../Toast'
import {
  DownloadEntry, DownloadPriority, StreamPriority,
  downloadDelete, downloadPause, downloadResume, downloadStopSeed, downloadSetPriority,
  downloadPauseAll, downloadResumeAll, downloadBatchPause, downloadBatchResume, downloadBatchDelete,
  streamPause, streamResume, streamSetPriority, streamPauseAll, streamResumeAll, streamSetLimits, streamDrop,
} from '../../api/client'
import { markDeleted, clearDeleted, type PendingDeletes } from '../../lib/downloadsReconcile'

type StatusGroups = {
  downloading: DownloadEntry[]
  paused: DownloadEntry[]
  completed: DownloadEntry[]
  failed: DownloadEntry[]
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
      await downloadPause(id).catch(() => {}) // pausa antes de remover
      // Play de um download cria uma sessão de stream/transcode no anacrolix pro
      // MESMO hash, separada da row. Sem derrubá-la, o torrent "tocado" reaparece
      // como card de Streaming após o delete (sintoma: "permaneceu mesmo após excluir").
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
  // Abre o modal de promove (single ou batch). Single: passa só esse item;
  // batch: passa todos os selected. UI faz o resto.
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
    if (!await confirm({ title: t('downloads.page.removeDownloadsTitle'), message: t('downloads.page.removeDownloadsMessage', { count: ids.length }), confirmLabel: t('downloads.page.remove'), destructive: true })) return
    setBulkBusy(true)
    await runBatchDelete(ids, targets)
    setBulkBusy(false)
  }

  // runBatchDelete is the shared optimistic-delete flow for batch + per-torrent
  // removal: hide the rows, pause + drop stream sessions, fire the batch DELETE,
  // then surface any IDs the backend reported as failed (instead of letting the
  // poll silently re-show them).
  const runBatchDelete = async (ids: number[], targets: DownloadEntry[]) => {
    markDeleted(pendingDeletesRef.current, ids)
    setItems(prev => prev.filter(x => !ids.includes(x.id)))
    try {
      await downloadBatchPause(ids).catch(() => {}) // pausa todos antes de remover
      // Encerra qualquer sessão de stream/transcode aberta pelo Play (ver onDelete).
      await Promise.all(targets.map(d => d.infoHash ? streamDrop(d.infoHash).catch(() => {}) : Promise.resolve()))
      const res = await downloadBatchDelete(ids)
      const failed = res.failed ?? []
      if (failed.length > 0) {
        clearDeleted(pendingDeletesRef.current, failed) // let the survivors come back into view
        notify(t('downloads.page.removeFailed', { count: failed.length, ids: failed.join(', #') }), 'error')
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
    // Limpa seleção dos que deram certo
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
    try { await downloadStopSeed(id); await reloadDownloadsRef.current(); await loadTorrents() }
    finally { setBusyID(null) }
  }

  // ── Ações no nível do torrent (grupo de arquivos do mesmo infoHash) ──
  const onPromoteMany = (ds: DownloadEntry[]) => { if (ds.length > 0) setPromoteTargets(ds) }
  const onDeleteMany = async (ds: DownloadEntry[]) => {
    const ids = ds.map(d => d.id)
    if (ids.length === 0) return
    if (!await confirm({ title: t('downloads.page.removeTorrentTitle'), message: t('downloads.page.removeTorrentFilesMessage', { count: ids.length }), confirmLabel: t('downloads.page.remove'), destructive: true })) return
    setBulkBusy(true)
    await runBatchDelete(ids, ds)
    setBulkBusy(false)
  }
  const onStopSeedMany = async (ds: DownloadEntry[]) => {
    if (ds.length === 0) return
    if (!await confirm({ title: t('downloads.page.stopSeedTitle'), message: t('downloads.page.stopSeedManyMessage', { count: ds.length }), confirmLabel: t('downloads.page.stop'), destructive: true })) return
    setBulkBusy(true)
    try { await Promise.all(ds.map(d => downloadStopSeed(d.id).catch(() => {}))); await reloadDownloadsRef.current(); await loadTorrents() }
    finally { setBulkBusy(false) }
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
    try { await streamDrop(hash); await loadTorrents() } finally { setBusyHash(null) }
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

  // Ações em lote globais (reusadas pela barra inline do desktop e pelo Sheet
  // de "Ações" do mobile).
  const doResumeAll = async () => {
    setBulkBusy(true)
    try { await Promise.all([downloadResumeAll(), streamResumeAll()]); await reloadDownloadsRef.current() }
    finally { setBulkBusy(false) }
  }
  const doPauseAll = async () => {
    setBulkBusy(true)
    try { await Promise.all([downloadPauseAll(), streamPauseAll()]); await reloadDownloadsRef.current() }
    finally { setBulkBusy(false) }
  }
  const doRemoveCompleted = async () => {
    const ok = await confirm({
      title: t('downloads.page.removeCompletedTitle'),
      message: t('downloads.page.removeCompletedMessage', { count: completedDownloads.length }),
      confirmLabel: t('downloads.page.remove'),
      destructive: true,
    })
    if (!ok) return
    setBulkBusy(true)
    try {
      // Encerra sessões de seed/stream antes de remover as rows concluídas.
      await Promise.all(completedDownloads.map(d => d.infoHash ? streamDrop(d.infoHash).catch(() => {}) : Promise.resolve()))
      await downloadBatchDelete(completedDownloads.map(d => d.id)); await reloadDownloadsRef.current(); await loadTorrents()
    }
    finally { setBulkBusy(false) }
  }
  // Limpeza em massa por status — "limpar falhados" e "limpar fila". Caso de
  // uso: o antigo "Baixar tudo" (1 row POR arquivo) podia entupir a fila com
  // centenas de itens; isto remove o lixo em 1 clique sem caçar checkbox.
  const doClearByStatus = async (targets: DownloadEntry[], title: string, message: string) => {
    if (targets.length === 0) return
    const ok = await confirm({ title, message, confirmLabel: t('downloads.clear_confirm'), destructive: true })
    if (!ok) return
    setBulkBusy(true)
    try {
      await downloadBatchDelete(targets.map(d => d.id)); await reloadDownloadsRef.current(); await loadTorrents()
    } finally { setBulkBusy(false) }
  }
  const doClearFailed = () => doClearByStatus(
    downloadsByStatus.failed,
    t('downloads.clear_failed_title'),
    t('downloads.clear_failed_message', { count: downloadsByStatus.failed.length }),
  )
  const doClearQueued = () => doClearByStatus(
    queuedDownloads,
    t('downloads.clear_queued_title'),
    t('downloads.clear_queued_message', { count: queuedDownloads.length }),
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
