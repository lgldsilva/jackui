import { useCallback } from 'react'
import { useTranslation, Trans } from 'react-i18next'
import { LocalEntry, localDelete, localCleanEmptyDirs, localSetFolderLock, localCacheFolder } from '../../api/client'
import { useConfirm } from '../ConfirmDialog'

type Setter = React.Dispatch<React.SetStateAction<string>>

// Per-item/folder operations on the current mount: delete, clean empty folders,
// pin/release (.keep) and cache remote folder. All report error/notice via the
// page's setters and re-list on completion.
export function useLocalOps(activeMount: string, path: string, refresh: () => void, setError: Setter, setNotice: Setter) {
  const { t } = useTranslation()
  const confirm = useConfirm()

  const requestDelete = useCallback(async (item: LocalEntry) => {
    if (!activeMount) return
    const ok = await confirm({
      title: t('local.delete.title'),
      message: (
        <Trans
          i18nKey="local.delete.singleMessage"
          values={{ name: item.name, kind: item.isDir ? t('local.delete.dir') : t('local.delete.file') }}
          components={{ hl: <span className="text-red-400 font-medium" />, note: <span className="block mt-2 text-xs text-amber-400/80" /> }}
        />
      ),
      confirmLabel: t('local.delete.confirm'),
      destructive: true,
    })
    if (!ok) return
    setError('')
    try {
      await localDelete(activeMount, item.path)
      refresh()
    } catch (e: any) {
      setError(e?.response?.data?.error || e.message || t('local.errors.deleteFile'))
    }
  }, [activeMount, confirm, t, refresh, setError])

  // Remove empty subfolders left behind after promoting/moving files. Low risk
  // (only deletes truly-empty dirs), so a light confirm is enough.
  // scope 'here' = recursive from the current folder; 'root' = from the mount's
  // root. "Kept" folders (.keep) survive in both. Files are never touched.
  const requestCleanEmptyDirs = async (scope: 'here' | 'root') => {
    if (!activeMount) return
    const target = scope === 'root' ? '' : path
    const ok = await confirm({
      title: t('local.clean.confirmTitle'),
      message: <Trans i18nKey="local.clean.confirmMessage" values={{ target: target || activeMount }} components={{ hl: <span className="text-text-primary font-medium" /> }} />,
      confirmLabel: t('local.clean.confirmLabel'),
    })
    if (!ok) return
    setError('')
    setNotice('')
    try {
      const { cleaned } = await localCleanEmptyDirs(activeMount, target)
      setNotice(cleaned > 0 ? t('local.clean.removedNotice', { count: cleaned }) : t('local.clean.noneFound'))
      refresh()
    } catch (e: any) {
      setError(e?.response?.data?.error || e.message || t('local.errors.cleanEmpty'))
    }
  }

  // Pins/releases a folder (.keep) so "clean empty" keeps it even without
  // files. No confirm — it's reversible and harmless.
  const handleToggleLock = useCallback(async (entry: LocalEntry) => {
    if (!activeMount) return
    setError('')
    try {
      await localSetFolderLock(activeMount, entry.path, !entry.locked)
      refresh()
    } catch (e: any) {
      setError(e?.response?.data?.error || e.message || t('local.errors.toggleLock'))
    }
  }, [activeMount, refresh, t, setError])

  // Caches the whole folder (recursive) in one click — only shows on remote
  // mounts (rclone/NFS/CIFS). The cache LRU handles size: copies everything and
  // gradually evicts the coldest as new ones arrive (favorites/downloads stay
  // protected), so a big series doesn't blow the cache.
  const requestCacheFolder = async () => {
    if (!activeMount) return
    setError(''); setNotice('')
    try {
      const { queued } = await localCacheFolder(activeMount, path)
      setNotice(queued > 0
        ? t('local.cache.queuedNotice', { count: queued })
        : t('local.cache.noMedia'))
    } catch (e: any) {
      setError(e?.response?.data?.error || e.message || t('local.errors.cacheFolder'))
    }
  }

  return { requestDelete, requestCleanEmptyDirs, handleToggleLock, requestCacheFolder }
}
