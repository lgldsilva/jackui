import { useCallback, useEffect, useMemo, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { LocalEntry, localDelete, localWalk } from '../../api/client'
import { useConfirm } from '../ConfirmDialog'
import { mergePromoteFiles } from '../../pages/localPromote'
import { errMessage } from '../../lib/errMessage'
import { formatCount } from './entryFormat'

type Setter = React.Dispatch<React.SetStateAction<string>>

// Multi-selection / batch: selection mode, selected set and the batch
// operations (delete, promote). Leaves selection mode when switching mount/folder.
export function useLocalSelection(
  entries: LocalEntry[],
  visible: LocalEntry[],
  activeMount: string,
  path: string,
  refresh: () => void,
  setError: Setter,
  setPromoteEntries: React.Dispatch<React.SetStateAction<LocalEntry[]>>,
) {
  const { t } = useTranslation()
  const confirm = useConfirm()
  const [selectMode, setSelectMode] = useState(false)
  const [selected, setSelected] = useState<Set<string>>(new Set())
  const [batchRunning, setBatchRunning] = useState(false)
  const [batchMoveOpen, setBatchMoveOpen] = useState(false)

  // Leave selection mode when switching mount/folder — avoids an accidental
  // cross-folder batch (the APIs are scoped to a relative mount+path).
  useEffect(() => {
    setSelectMode(false)
    setSelected(new Set())
  }, [activeMount, path])

  const selectedEntries = useMemo(
    () => entries.filter((e) => selected.has(e.path)),
    [entries, selected],
  )

  const clearSelection = () => { setSelectMode(false); setSelected(new Set()) }
  const toggleSelect = useCallback((e: LocalEntry) => setSelected((prev) => {
    const next = new Set(prev)
    if (next.has(e.path)) next.delete(e.path)
    else next.add(e.path)
    return next
  }), [])
  const enterSelect = useCallback((e: LocalEntry) => { setSelectMode(true); setSelected(new Set([e.path])) }, [])
  // "Select all" acts on the visible list (respects the current filter/search).
  const selectAllVisible = () => setSelected(new Set(visible.map((e) => e.path)))

  const runBatchDelete = async () => {
    if (selectedEntries.length === 0) return
    const ok = await confirm({
      title: t('local.delete.title'),
      message: t('local.delete.batchMessage', { items: formatCount(selectedEntries.length, t) }),
      confirmLabel: t('local.delete.confirm'),
      destructive: true,
    })
    if (!ok) return
    setBatchRunning(true)
    setError('')
    const results = await Promise.allSettled(selectedEntries.map((e) => localDelete(activeMount, e.path)))
    const failed = results.filter((r) => r.status === 'rejected').length
    if (failed > 0) setError(t('local.delete.batchFailed', { failed, total: selectedEntries.length }))
    setBatchRunning(false)
    clearSelection()
    refresh()
  }

  // Expand selected directories into their media files via localWalk, capped to a
  // few concurrent walks so a large multi-folder selection doesn't fan out into
  // dozens of simultaneous server calls.
  const expandDirsToMediaFiles = async (dirs: LocalEntry[]): Promise<LocalEntry[]> => {
    const out: LocalEntry[] = []
    const concurrency = 3
    let i = 0
    const worker = async () => {
      while (i < dirs.length) {
        const dir = dirs[i++]
        const r = await localWalk(activeMount, dir.path, true) // media_only
        out.push(...r.entries.filter((e) => !e.isDir))
      }
    }
    await Promise.all(Array.from({ length: Math.min(concurrency, dirs.length) }, worker))
    return out
  }

  // Batch promote = a single modal for ALL the selected files
  // (destination + AI rename chosen once, one call only). Selected folders
  // are walked (localWalk, media_only) into their media files
  // BEFORE opening the modal; mixed selection joins the loose files + the ones
  // inside the folders, deduplicated by path.
  const runBatchPromote = async () => {
    const looseFiles = selectedEntries.filter((e) => !e.isDir)
    const dirs = selectedEntries.filter((e) => e.isDir)
    if (looseFiles.length === 0 && dirs.length === 0) return

    setBatchRunning(true)
    setError('')
    try {
      const expanded = dirs.length > 0 ? await expandDirsToMediaFiles(dirs) : []
      const files = mergePromoteFiles(looseFiles, expanded)
      if (files.length === 0) {
        setError(t('local.promote.noMediaInFolders'))
        return
      }
      setPromoteEntries(files)
    } catch (e: unknown) {
      const msg = errMessage(e)
      setError(msg)
    } finally {
      setBatchRunning(false)
    }
  }

  return {
    selectMode, setSelectMode, selected, setSelected, selectedEntries,
    batchRunning, batchMoveOpen, setBatchMoveOpen,
    clearSelection, toggleSelect, enterSelect, selectAllVisible,
    runBatchDelete, runBatchPromote,
  }
}
