import { useCallback } from 'react'
import { useTranslation, Trans } from 'react-i18next'
import { LocalEntry, localDelete, localCleanEmptyDirs, localSetFolderLock, localCacheFolder } from '../../api/client'
import { useConfirm } from '../ConfirmDialog'

type Setter = React.Dispatch<React.SetStateAction<string>>

// Operações por-item/pasta sobre o mount atual: apagar, limpar pastas vazias,
// fixar/soltar (.keep) e cachear pasta remota. Todas reportam erro/aviso via os
// setters da página e re-listam ao concluir.
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
  // scope 'here' = recursivo a partir da pasta atual; 'root' = desde a raiz do
  // mount. Pastas "mantidas" (.keep) sobrevivem em ambos. Arquivos não são tocados.
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

  // Fixa/solta uma pasta (.keep) pra que o "limpar vazias" a mantenha mesmo sem
  // arquivos. Sem confirm — é reversível e inofensivo.
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

  // Cacheia a pasta inteira (recursivo) num clique — só aparece em mount
  // remoto (rclone/NFS/CIFS). O LRU do cache cuida do tamanho: copia tudo e vai
  // soltando os mais frios conforme novos chegam (favoritos/downloads ficam
  // protegidos), então uma série grande não estoura o cache.
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
