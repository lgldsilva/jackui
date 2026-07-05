import { useCallback, useEffect, useMemo, useRef, useState } from 'react'
import { useTranslation, Trans } from 'react-i18next'
import { useSearchParams } from 'react-router-dom'
import { useQuerySetter } from '../lib/useQueryState'
import {
  ChevronDown,
  HardDrive,
  HardDriveDownload,
  ArrowDown,
  ArrowUp,
  FolderSync,
  CopyCheck,
  Upload,
  Search,
  X,
  RefreshCw,
} from 'lucide-react'
import NavHeader from '../components/NavHeader'
import { usePersistedState } from '../lib/storage'
import { usePlayer } from '../components/PlayerProvider'
import { useAuth } from '../auth/AuthContext'
import { useConfirm } from '../components/ConfirmDialog'
import { DuplicatesModal } from '../components/local/DuplicatesModal'
import { Sheet } from '../components/Sheet'
import { BatchActionBar } from '../components/BatchActionBar'
import LocalPromoteModal from '../components/LocalPromoteModal'
import ReclassifyFolderModal from '../components/ReclassifyFolderModal'
import MoveFolderModal from '../components/MoveFolderModal'
import RenameModal from '../components/RenameModal'
import CleanEmptyButton from '../components/local/CleanEmptyButton'
import { MountBadge, MountSpaceLabel } from '../components/local/MountBadge'
import { Breadcrumbs } from '../components/local/Breadcrumbs'
import { EntryRow } from '../components/local/EntryRow'
import { isVideo, isAudio, formatCount } from '../components/local/entryFormat'
import { useIncrementalReveal } from '../components/player/useIncrementalReveal'
import {
  LocalEntry,
  LocalMount,
  SearchResult,
  PlaylistItem,
  AdminUser,
  buildLocalHash,
  localList,
  localWalk,
  localMounts,
  localDelete,
  localCleanEmptyDirs,
  localSetFolderLock,
  localCacheFolder,
  localPlayBatch,
  localUpload,
  adminListUsers,
  setLocalViewAsUser,
  localSetHidden,
  localListHidden,
} from '../api/client'
import { useRevealHidden } from '../lib/reveal'
import { useTransfers } from '../lib/transfers'
import FileProgressBar from '../components/FileProgressBar'
import { mergePromoteFiles } from './localPromote'
import FilePreviewModal from '../components/FilePreviewModal'
import { isViewable, detectViewerKind } from '../components/viewer/viewerKind'
import { previewRawURL } from '../api/preview'
import { matchesEntryStatus, type LocalStatusFilter } from '../lib/localFilter'
import { errMessage } from '../lib/errMessage'

type SortKey = 'name' | 'size' | 'date'
type KindFilter = 'all' | 'video' | 'audio' | 'other'

export default function LocalPage() {
  const { t } = useTranslation()
  const [searchParams] = useSearchParams()
  const setQuery = useQuerySetter()
  const [mounts, setMounts] = useState<LocalMount[]>([])
  const activeMount = searchParams.get('mount') || ''
  const path = searchParams.get('path') || ''
  const [entries, setEntries] = useState<LocalEntry[]>([])
  const [loading, setLoading] = useState(false)
  const [error, setError] = useState('')
  const [notice, setNotice] = useState('')
  const [showDuplicates, setShowDuplicates] = useState(false)
  const { playSingle, playPlaylist } = usePlayer()
  const [kind, setKind] = usePersistedState<KindFilter>('local.kind', 'all')
  const [statusFilter, setStatusFilter] = usePersistedState<LocalStatusFilter>('local.status', 'all')
  const [sortKey, setSortKey] = usePersistedState<SortKey>('local.sortKey', 'name')
  const [sortDir, setSortDir] = usePersistedState<'asc' | 'desc'>('local.sortDir', 'asc')

  // Files queued for the promote modal: 1 (single, via the row action) or many
  // (batch selection). The modal applies one destination + AI choice to all in
  // a single call — no more one-modal-per-file walk.
  const [promoteEntries, setPromoteEntries] = useState<LocalEntry[]>([])
  const [reclassifyItem, setReclassifyItem] = useState<LocalEntry | null>(null)
  const [moveItem, setMoveItem] = useState<LocalEntry | null>(null)
  const [renameItem, setRenameItem] = useState<LocalEntry | null>(null)
  // Viewer universal pra arquivos não-reproduzíveis (NFO/imagem/PDF/CBZ/zip/EPUB)
  const [previewEntry, setPreviewEntry] = useState<LocalEntry | null>(null)
  const confirm = useConfirm()

  // Hidden curtain (global easter egg): hidden entries drop from the list unless
  // it's open. hiddenSet (paths in the active mount) flags which rows are hidden
  // so the row shows a "Mostrar" action + indicator while revealed.
  const [revealHidden] = useRevealHidden()
  const [hiddenSet, setHiddenSet] = useState<Set<string>>(new Set())

  // Busca textual por nome (filtra a lista visível) + seleção múltipla / lote.
  const [search, setSearch] = useState('')
  const [mountSheetOpen, setMountSheetOpen] = useState(false)
  const [selectMode, setSelectMode] = useState(false)
  const [selected, setSelected] = useState<Set<string>>(new Set())
  const [batchRunning, setBatchRunning] = useState(false)
  const [batchMoveOpen, setBatchMoveOpen] = useState(false)

  // Upload state: tracks the in-flight transfer for the progress banner. The
  // AbortController lets the user cancel mid-stream; the hidden <input> is reset
  // after each pick so re-selecting the same file fires onChange again.
  const [upload, setUpload] = useState<{ name: string; loaded: number; total: number } | null>(null)
  const [uploadError, setUploadError] = useState('')
  const uploadAbortRef = useRef<AbortController | null>(null)
  const fileInputRef = useRef<HTMLInputElement | null>(null)

  // useCallback: os handlers passados a cada EntryRow precisam de referência
  // estável pra que o React.memo da row funcione — sem isso, um render do pai
  // (progresso de upload, notice, seleção de OUTRA row) recriaria os handlers e
  // re-renderizaria TODAS as linhas.
  const updateNavigation = useCallback((newMount: string, newPath: string, replace = false) => {
    // Atomic two-key update (mount + path) via the shared helper, which merges over
    // the live query so an active ?play= is preserved.
    setQuery({ mount: newMount || null, path: newPath || null }, { replace })
  }, [setQuery])

  const { isGuest, isAdmin } = useAuth()
  // Admin "view as user": '' = own space. When set, every /api/local/* call
  // carries ?user= (the backend re-checks the admin role before honoring it).
  const [viewAsUser, setViewAsUser] = useState('')
  const [adminUsers, setAdminUsers] = useState<AdminUser[]>([])
  const activeMountObj = useMemo(() => mounts.find((m) => m.name === activeMount), [mounts, activeMount])
  const canViewAsUser = isAdmin && !!activeMountObj?.userSubpath
  const canManipulate = !isGuest && activeMount.toLowerCase() === 'meus downloads'

  // Folders always show (so navigation never gets filtered away); the kind
  // filter + sort apply within each group, folders kept on top.
  const visible = useMemo(() => {
    const q = search.trim().toLowerCase()
    // O filtro de status (baixando/concluído) aplica a pastas E arquivos — mas só
    // quando ≠ 'all', pra que no default a navegação por pastas siga livre.
    const keep = (e: LocalEntry) =>
      (!q || e.name.toLowerCase().includes(q)) && matchesEntryStatus(e, statusFilter)
    const dirs = entries.filter((e) => e.isDir && keep(e))
    let files = entries.filter((e) => !e.isDir && keep(e))
    if (kind === 'video') files = files.filter((e) => isVideo(e.name))
    else if (kind === 'audio') files = files.filter((e) => isAudio(e.name))
    else if (kind === 'other') files = files.filter((e) => !isVideo(e.name) && !isAudio(e.name))

    const cmp = (a: LocalEntry, b: LocalEntry) => {
      let r = 0
      if (sortKey === 'name') r = a.name.localeCompare(b.name, undefined, { numeric: true })
      else if (sortKey === 'size') r = a.size - b.size
      else r = new Date(a.modTime).getTime() - new Date(b.modTime).getTime()
      return sortDir === 'asc' ? r : -r
    }
    return [...dirs.sort(cmp), ...files.sort(cmp)]
  }, [entries, kind, statusFilter, sortKey, sortDir, search])

  // Windowing: renderiza a lista em LOTES e revela mais ao rolar até o fim
  // (sentinela + IntersectionObserver) — igual Search/Favorites. resetKey volta ao
  // 1º lote quando muda a pasta/mount ou os filtros/ordenação/busca.
  const reveal = useIncrementalReveal(
    visible.length,
    `${activeMount}|${path}|${kind}|${statusFilter}|${sortKey}|${sortDir}|${search}`,
  )

  const toggleSort = (key: SortKey) => {
    if (sortKey === key) setSortDir((d) => (d === 'asc' ? 'desc' : 'asc'))
    else { setSortKey(key); setSortDir(key === 'name' ? 'asc' : 'desc') }
  }

  // Sair do modo seleção ao trocar de mount/pasta — evita lote acidental
  // cross-folder (as APIs são scoped a mount+path relativo).
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
  // "Selecionar tudo" age sobre a lista visível (respeita filtro/busca atuais).
  const selectAllVisible = () => setSelected(new Set(visible.map((e) => e.path)))

  // reqSeq guards against out-of-order responses: when the user navigates
  // quickly (or the initial mount load is still in flight), two localList calls
  // race and the slower one could overwrite the newer result — showing stale or
  // empty content and a flash. Only the latest request is allowed to commit.
  const reqSeq = useRef(0)

  const refresh = useCallback(() => {
    if (!activeMount) return
    // Sync the client module's view-as state BEFORE any local call so list +
    // subsequent play/thumb/move/delete all hit the selected user's space.
    setLocalViewAsUser(viewAsUser)
    const seq = ++reqSeq.current
    setLoading(true)
    setError('')
    localList(activeMount, path)
      .then((data) => {
        if (seq !== reqSeq.current) return
        setEntries(data)
      })
      .catch((e: unknown) => {
        if (seq !== reqSeq.current) return
        const msg = errMessage(e)
        setError(msg)
        setEntries([])
      })
      .finally(() => {
        if (seq === reqSeq.current) setLoading(false)
      })
  }, [activeMount, path, viewAsUser])

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

  // Promover em lote = um único modal para TODOS os arquivos selecionados
  // (destino + renomeação IA escolhidos uma vez, uma chamada só). Pastas
  // selecionadas são varridas (localWalk, media_only) em seus arquivos de mídia
  // ANTES de abrir o modal; seleção mista junta os arquivos soltos + os de
  // dentro das pastas, deduplicados por path.
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

  useEffect(() => {
    localMounts()
      .then(setMounts)
      .catch((e: unknown) => {
        const msg = errMessage(e)
        setError(msg)
      })
  }, [])

  // Auto-seleciona o primeiro mount sempre que NENHUM está selecionado — no land
  // inicial E ao re-clicar "Local" na nav (que vai pra /local sem ?mount=, zerando
  // o activeMount). Tem que ser REATIVO, não só no load: no mobile o seletor de
  // mount vive DENTRO do bloco `{activeMount && ...}`, então um activeMount vazio
  // escondia os mounts por completo ("clico no Local 2x e some os mounts"). Manter
  // sempre um mount selecionado evita esse estado morto. Loop não acontece: ao
  // selecionar, activeMount deixa de ser vazio e o efeito vira no-op.
  useEffect(() => {
    if (mounts.length > 0 && !activeMount) {
      updateNavigation(mounts[0].name, '', true)
    }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [mounts, activeMount])

  useEffect(() => {
    setNotice('') // stale "N folders removed" shouldn't linger across navigation
    refresh()
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [activeMount, path, viewAsUser, revealHidden])

  // Async move/promote now run server-side and report to the Transfers dock; when
  // the last running transfer finishes, re-list so the browser reflects the new
  // layout (the source is gone, the destination has the file).
  const { transfers } = useTransfers()
  const prevRunningTransfers = useRef(0)
  useEffect(() => {
    const running = transfers.filter((t) => t.status === 'running').length
    if (prevRunningTransfers.current > 0 && running === 0) refresh()
    prevRunningTransfers.current = running
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [transfers])

  // Which entries in this mount are hidden — flags them + offers "Mostrar" while
  // the curtain is open (closed → they're filtered server-side, empty set is ok).
  const loadHidden = useCallback(() => {
    if (!activeMount) { setHiddenSet(new Set()); return }
    localListHidden()
      .then((paths) => setHiddenSet(new Set(paths.filter((p) => p.mount === activeMount).map((p) => p.path))))
      .catch(() => setHiddenSet(new Set()))
  }, [activeMount])
  useEffect(() => {
    loadHidden()
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [activeMount, revealHidden])

  const handleToggleHidden = useCallback(async (e: LocalEntry) => {
    await localSetHidden(activeMount, e.path, !hiddenSet.has(e.path))
    loadHidden()
    refresh()
  }, [activeMount, hiddenSet, loadHidden, refresh])

  // Load the user list once for the admin "view as user" selector.
  useEffect(() => {
    if (!isAdmin) return
    adminListUsers().then(setAdminUsers).catch(() => setAdminUsers([]))
  }, [isAdmin])

  // Reset the view-as override when leaving the page so other views (e.g. the
  // player opening a local "continue watching" item) aren't silently scoped to
  // another user.
  useEffect(() => {
    return () => setLocalViewAsUser('')
  }, [])

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
  }, [activeMount, confirm, t, refresh])

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
  }, [activeMount, refresh, t])

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

  const handleUploadPick = async (e: React.ChangeEvent<HTMLInputElement>) => {
    const file = e.target.files?.[0]
    // Reset the input so picking the same file again re-fires onChange.
    e.target.value = ''
    if (!file || !activeMount) return

    const controller = new AbortController()
    uploadAbortRef.current = controller
    setUploadError('')
    setUpload({ name: file.name, loaded: 0, total: file.size })
    setLocalViewAsUser(viewAsUser) // keep admin "view as user" scoping consistent
    try {
      await localUpload(
        activeMount,
        path,
        file,
        (loaded, total) => setUpload({ name: file.name, loaded, total }),
        controller.signal,
      )
      setUpload(null)
      refresh()
    } catch (err: any) {
      if (controller.signal.aborted) {
        setUploadError(t('local.upload.canceled'))
      } else {
        setUploadError(err?.response?.data?.error || err?.message || t('local.errors.upload'))
      }
      setUpload(null)
    } finally {
      uploadAbortRef.current = null
    }
  }

  const handleSelectMount = (name: string) => {
    updateNavigation(name, '')
    setViewAsUser('') // back to own space when switching mounts
  }

  const handleViewAsUser = (username: string) => {
    setLocalViewAsUser(username) // module state — takes effect on the next call
    setViewAsUser(username)
    updateNavigation(activeMount, '') // jump to the root of the selected user's space
  }

  const handleEntryClick = useCallback((e: LocalEntry) => {
    if (e.isDir) {
      updateNavigation(activeMount, e.path)
      return
    }
    if (!activeMount) return
    // Não-reproduzível mas visualizável (NFO/imagem/PDF/CBZ/zip/EPUB) → abre o
    // viewer universal em vez de ser um clique morto.
    if (!e.isPlayable) {
      if (isViewable(e.name)) setPreviewEntry(e)
      return
    }
    // Routes the file through the main PlayerProvider/PlayerModal via a
    // synthetic SearchResult com pseudo-hash `local-...` (mount+path codificados).
    // Resultado: o player completo abre — legendas embedded, sidecar .srt/.vtt,
    // OpenSubtitles auto, escolha persistida, tudo. As funções do client (streamProbe,
    // streamSidecars, subtitlesAuto, etc.) detectam o prefixo e roteiam pra
    // /api/local/* sem mudar PlayerModal.
    //
    // Os irmãos playable da MESMA modalidade (vídeo↔vídeo, áudio↔áudio), na ordem
    // exibida (`visible`), viram uma playlist implícita — assim ⏮⏭ navegam entre
    // os episódios/faixas da pasta. Cada arquivo local mantém seu próprio
    // pseudo-hash (que o player já toca sozinho); sem isso o player recebia só 1
    // arquivo e os botões de próximo/anterior ficavam inertes.
    const clickedIsVideo = isVideo(e.name)
    const siblings = visible.filter(
      (x) => !x.isDir && x.isPlayable && (clickedIsVideo ? isVideo(x.name) : isAudio(x.name)),
    )
    if (siblings.length > 1) {
      const items: PlaylistItem[] = siblings.map((x, pos) => {
        const h = buildLocalHash(activeMount, x.path)
        return {
          id: pos, playlistId: 0, position: pos, title: x.name,
          magnet: `magnet:?xt=urn:btih:${h}`, infoHash: h, fileIndex: 0, addedAt: '',
        }
      })
      const start = Math.max(0, siblings.findIndex((x) => x.path === e.path))
      // Pre-warm the resolution (direct-vs-HLS + URL) of EVERY track in the folder
      // in ONE batch call, instead of one GET /api/local/play (ffprobe) per track
      // when the player navigates/auto-advances. Best-effort (never blocks play).
      void localPlayBatch(activeMount, siblings.map((x) => x.path)).catch(() => {})
      const folderName = path ? path.split('/').pop() || path : activeMount
      // expand=true: arquivos locais abrem o player MAXIMIZADO (não o dock de
      // áudio minimizado) — o usuário clicou pra ver/ouvir a experiência cheia.
      playPlaylist(folderName, items, start, true)
      return
    }
    const hash = buildLocalHash(activeMount, e.path)
    const synthetic: SearchResult = {
      title: e.name,
      tracker: '',
      categoryId: 0,
      category: '',
      size: e.size,
      seeders: 0,
      leechers: 0,
      age: '',
      magnetUri: `magnet:?xt=urn:btih:${hash}`,
      link: '',
      infoHash: hash,
      publishDate: '',
    }
    playSingle(synthetic, 0, undefined, true)
  }, [activeMount, path, visible, playSingle, playPlaylist, updateNavigation])

  const promoteOne = useCallback((entry: LocalEntry) => setPromoteEntries([entry]), [])

  return (
    <div className="h-screen bg-surface flex flex-col overflow-hidden">
      <NavHeader />
      <main className="flex-1 min-h-0 max-w-7xl 2xl:max-w-[min(95vw,1600px)] mx-auto w-full px-4 py-6 flex flex-col md:flex-row gap-4 md:gap-6">
        {/* Sidebar — desktop é coluna fixa à esquerda. No mobile some por completo
            (hidden) e dá lugar a um dropdown de mount na barra do breadcrumb, que
            não rouba altura nem força scroll horizontal de chips. */}
        <aside className="hidden md:block md:w-56 flex-shrink-0 md:overflow-y-auto">
          <h2 className="text-xs uppercase tracking-wider text-text-muted mb-2 md:mb-3">
            {t('local.mounts')}
          </h2>
          {mounts.length === 0 ? (
            <><p className="text-sm text-text-muted">
              <Trans i18nKey="local.noMountsConfigured" components={{ c: <code /> }} />
            </p>
            <code className="block mt-2 p-2 bg-surface-secondary rounded text-xs">
                external:{'\n'}  mounts:{'\n'}    - name: HD Externo{'\n'}      path: /mnt/external
              </code></>
          ) : (
            <ul className="flex flex-col gap-1 space-y-1">
              {mounts.map((m) => {
                const active = m.name === activeMount
                return (
                  <li key={m.name} className="flex-shrink-0">
                    <button
                      onClick={() => handleSelectMount(m.name)}
                      className={`w-full flex items-center gap-2 px-3 py-2 rounded-lg text-sm transition-colors whitespace-nowrap ${
                        active
                          ? 'bg-green-500/10 text-green-400 border border-green-500/30'
                          : 'text-text-primary hover:bg-surface-secondary border border-transparent'
                      }`}
                    >
                      <HardDrive className="w-4 h-4 flex-shrink-0" />
                      <span className="truncate">{m.name}</span>
                      <MountBadge m={m} />
                    </button>
                    <MountSpaceLabel m={m} />
                  </li>
                )
              })}
            </ul>
          )}

          {canViewAsUser && (
            <div className="mt-5 md:mt-6">
              <h2 className="text-xs uppercase tracking-wider text-text-muted mb-2">{t('local.viewAs')}</h2>
              <select
                value={viewAsUser}
                onChange={(e) => handleViewAsUser(e.target.value)}
                className="w-full px-3 py-2 rounded-lg text-sm bg-surface-secondary border border-default text-text-primary focus:border-green-500/50 focus:outline-none"
              >
                <option value="">{t('local.mySpace')}</option>
                {adminUsers.map((u) => (
                  <option key={u.id} value={u.username}>{u.username}</option>
                ))}
              </select>
              {viewAsUser && (
                <p className="mt-1.5 text-[11px] text-amber-400/80">
                  <Trans i18nKey="local.viewingSpaceOf" values={{ user: viewAsUser }} components={{ b: <strong /> }} />
                </p>
              )}
            </div>
          )}
        </aside>

        {/* Content */}
        <section className="flex-1 min-w-0 min-h-0 flex flex-col gap-4">
          {activeMount && (
            <div className="flex-shrink-0 flex flex-wrap items-center gap-2">
              <div className="flex items-center gap-2 min-w-0 flex-1 max-md:basis-full">
                {/* Dropdown de mount — só no mobile (a sidebar some em <md) */}
                <button
                  onClick={() => setMountSheetOpen(true)}
                  className="md:hidden flex-shrink-0 flex items-center gap-1.5 px-2.5 min-h-[40px] rounded-lg bg-surface-secondary border border-default text-sm text-text-primary max-w-[45vw]"
                >
                  <HardDrive className="w-4 h-4 text-green-400 flex-shrink-0" />
                  <span className="truncate">{activeMount}</span>
                  <ChevronDown className="w-4 h-4 text-text-muted flex-shrink-0" />
                </button>
                <Breadcrumbs mountName={activeMount} path={path} onNavigate={(p) => updateNavigation(activeMount, p)} />
              </div>
              {/* Botões de ação agrupados: no mobile quebram juntos para a linha
                  de baixo (antes encavalavam no breadcrumb); inline no desktop. */}
              <div className="flex items-center gap-2 flex-shrink-0">
              <button
                onClick={refresh}
                disabled={loading}
                title={t('local.reloadTitle')}
                className="flex-shrink-0 inline-flex items-center gap-1.5 text-sm bg-surface-tertiary/60 hover:bg-surface-tertiary disabled:opacity-50 text-text-primary border border-strong px-3 py-1.5 rounded-lg transition-colors font-medium"
              >
                <RefreshCw className={`w-4 h-4 ${loading ? 'animate-spin' : ''}`} />
                <span className="hidden sm:inline">{t('local.reload')}</span>
              </button>
              {activeMountObj?.cacheable && (
                <button
                  onClick={requestCacheFolder}
                  title={t('local.cacheFolderTitle')}
                  className="flex-shrink-0 inline-flex items-center gap-1.5 text-sm bg-green-500/15 hover:bg-green-500/25 text-green-400 border border-green-500/30 px-3 py-1.5 rounded-lg transition-colors font-medium"
                >
                  <HardDriveDownload className="w-4 h-4" />
                  <span className="hidden sm:inline">{t('local.cacheFolder')}</span>
                </button>
              )}
              {(canManipulate || isAdmin) && (
                <>
                  <input
                    ref={fileInputRef}
                    type="file"
                    accept=".mkv,.mp4,.m4v,.avi,.mov,.webm,.ts,.m2ts,.mpg,.mpeg,.wmv,.flv,.ogv,.3gp,.srt,.vtt,.ass,.ssa,.sub"
                    onChange={handleUploadPick}
                    className="hidden"
                  />
                  <button
                    onClick={() => fileInputRef.current?.click()}
                    disabled={!!upload}
                    title={t('local.uploadTitle')}
                    className="flex-shrink-0 inline-flex items-center gap-1.5 text-sm bg-green-500/15 hover:bg-green-500/25 disabled:opacity-50 text-green-400 border border-green-500/30 px-3 py-1.5 rounded-lg transition-colors font-medium"
                  >
                    <Upload className="w-4 h-4" />
                    <span className="hidden sm:inline">{t('local.uploadButton')}</span>
                  </button>
                  <CleanEmptyButton atRoot={!path} onClean={requestCleanEmptyDirs} />
                  <button
                    onClick={() => setShowDuplicates(true)}
                    title={t('local.duplicatesTitle')}
                    className="flex-shrink-0 inline-flex items-center gap-1.5 text-sm bg-surface-tertiary/60 hover:bg-surface-tertiary text-text-primary border border-strong px-3 py-1.5 rounded-lg transition-colors font-medium"
                  >
                    <CopyCheck className="w-4 h-4" />
                    <span className="hidden sm:inline">{t('local.duplicatesButton')}</span>
                  </button>
                </>
              )}
              {isAdmin && (
                <button
                  onClick={() => setReclassifyItem({ name: path ? path.split('/').pop() || path : activeMount, path, isDir: true, size: 0, modTime: '', isPlayable: false })}
                  title={t('local.reclassifyTitle')}
                  className="flex-shrink-0 inline-flex items-center gap-1.5 text-sm bg-purple-500/15 hover:bg-purple-500/25 text-purple-400 border border-purple-500/30 px-3 py-1.5 rounded-lg transition-colors font-medium"
                >
                  <FolderSync className="w-4 h-4" />
                  <span className="hidden sm:inline">{t('local.reclassifyButton')}</span>
                </button>
              )}
              </div>
            </div>
          )}

          {/* Banner de progresso do upload (streaming direto pro disco no backend) —
              mesmo componente FileProgressBar usado no dock global de Transferências. */}
          {upload && (
            <div className="flex-shrink-0 bg-surface-secondary border border-green-500/30 rounded-xl p-3">
              <FileProgressBar
                label={upload.name}
                status="running"
                bytesDone={upload.loaded}
                bytesTotal={upload.total}
                onCancel={() => uploadAbortRef.current?.abort()}
              />
            </div>
          )}

          {uploadError && (
            <div className="flex-shrink-0 bg-amber-500/10 border border-amber-500/30 text-amber-400 rounded-xl px-4 py-2.5 text-sm flex items-center justify-between gap-2">
              <span>{uploadError}</span>
              <button onClick={() => setUploadError('')} className="text-amber-400/70 hover:text-amber-500 dark:hover:text-amber-300">
                <X className="w-4 h-4" />
              </button>
            </div>
          )}

          {/* Toolbar: busca + selecionar; chips de tipo + ordenação (flex-shrink-0
              pra ficar fixa enquanto a lista abaixo rola). */}
          {activeMount && entries.length > 0 && (
            <div className="flex-shrink-0 flex flex-col gap-2">
              <div className="flex items-center gap-2">
                <div className="relative flex-1 min-w-0">
                  <Search className="absolute left-3 top-1/2 -translate-y-1/2 w-4 h-4 text-text-muted pointer-events-none" />
                  <input
                    type="text"
                    value={search}
                    onChange={(e) => setSearch(e.target.value)}
                    placeholder={t('local.searchPlaceholder')}
                    className="w-full bg-surface-secondary border border-default rounded-lg pl-9 pr-8 py-2 text-base sm:text-sm text-text-primary placeholder-gray-500 focus:outline-none focus:border-green-500/50"
                  />
                  {search && (
                    <button
                      onClick={() => setSearch('')}
                      aria-label={t('local.clearSearch')}
                      className="absolute right-2 top-1/2 -translate-y-1/2 text-text-muted hover:text-text-primary p-1"
                    >
                      <X className="w-3.5 h-3.5" />
                    </button>
                  )}
                </div>
                {(canManipulate || isAdmin) && !selectMode && (
                  <button
                    onClick={() => setSelectMode(true)}
                    className="flex-shrink-0 text-sm px-3 min-h-[44px] sm:min-h-0 sm:py-1.5 rounded-lg border border-default text-text-primary hover:bg-surface-tertiary transition-colors"
                  >
                    {t('local.select')}
                  </button>
                )}
              </div>
              {/* Dois grupos rotulados (Tipo / Ordenar). No mobile empilham
                  (flex-col) com rótulo visível em cada um — antes os chips dos
                  dois grupos se misturavam numa mesma linha-que-quebra, sem
                  rótulo, e ficava confuso. No desktop voltam pra uma linha. */}
              <div className="flex flex-col sm:flex-row sm:flex-wrap sm:items-center gap-2 text-xs">
                <div className="flex flex-wrap items-center gap-2">
                  <span className="text-text-muted sm:hidden mr-0.5">{t('local.typeLabel')}</span>
                  {(['all', 'video', 'audio', 'other'] as KindFilter[]).map((k) => (
                    <button
                      key={k}
                      onClick={() => setKind(k)}
                      className={`px-2.5 py-1 rounded-full border transition-colors ${
                        kind === k
                          ? 'bg-green-500/15 text-green-400 border-green-500/40'
                          : 'text-text-secondary border-default hover:border-strong'
                      }`}
                    >
                      {t(`local.kind.${k}`)}
                    </button>
                  ))}
                </div>
                <span className="mx-1 h-4 w-px bg-surface-tertiary hidden sm:block" />
                <div className="flex flex-wrap items-center gap-2">
                  <span className="text-text-muted sm:hidden mr-0.5">{t('local.statusLabel')}</span>
                  {(['all', 'downloading', 'done'] as LocalStatusFilter[]).map((s) => (
                    <button
                      key={s}
                      onClick={() => setStatusFilter(s)}
                      className={`px-2.5 py-1 rounded-full border transition-colors ${
                        statusFilter === s
                          ? 'bg-green-500/15 text-green-400 border-green-500/40'
                          : 'text-text-secondary border-default hover:border-strong'
                      }`}
                    >
                      {t(`local.status.${s}`)}
                    </button>
                  ))}
                </div>
                <span className="mx-1 h-4 w-px bg-surface-tertiary hidden sm:block" />
                <div className="flex flex-wrap items-center gap-2">
                  <span className="text-text-muted mr-0.5">{t('local.sortLabel')}</span>
                  {(['name', 'size', 'date'] as SortKey[]).map((k) => (
                    <button
                      key={k}
                      onClick={() => toggleSort(k)}
                      className={`flex items-center gap-1 px-2.5 py-1 rounded-full border transition-colors ${
                        sortKey === k
                          ? 'bg-surface-tertiary text-text-primary border-strong'
                          : 'text-text-secondary border-default hover:border-strong'
                      }`}
                    >
                      {t(`local.sort.${k}`)}
                      {sortKey === k && (sortDir === 'asc' ? <ArrowUp className="w-3 h-3" /> : <ArrowDown className="w-3 h-3" />)}
                    </button>
                  ))}
                </div>
              </div>
            </div>
          )}

          {error && (
            <div className="bg-red-500/10 border border-red-500/30 text-red-400 rounded-xl p-4 text-sm">
              {error}
            </div>
          )}

          {notice && (
            <div className="bg-emerald-500/10 border border-emerald-500/30 text-emerald-700 dark:text-emerald-300 rounded-xl px-4 py-2.5 text-sm flex items-center justify-between gap-3">
              <span>{notice}</span>
              <button onClick={() => setNotice('')} className="text-emerald-400/70 hover:text-emerald-500 dark:hover:text-emerald-300 text-xs">{t('local.close')}</button>
            </div>
          )}

          {loading && (
            <div className="text-text-muted text-sm">{t('local.loading')}</div>
          )}

          {!loading && !error && activeMount && visible.length === 0 && (
            <div className="text-text-muted text-sm">
              {entries.length === 0 ? t('local.emptyFolder') : t('local.noFilterMatch')}
            </div>
          )}

          {!loading && visible.length > 0 && (
            <ul className={`flex-1 min-h-0 overflow-y-auto divide-y divide-default bg-surface-secondary/50 rounded-xl border border-default ${selectMode ? 'pb-20' : ''}`}>
              {visible.slice(0, reveal.visible).map((e) => (
                <EntryRow
                  key={e.path}
                  entry={e}
                  mount={activeMount}
                  selectMode={selectMode}
                  selected={selected.has(e.path)}
                  canManipulate={canManipulate}
                  isAdmin={isAdmin}
                  onOpen={handleEntryClick}
                  onEnterSelect={enterSelect}
                  onToggleSelect={toggleSelect}
                  onRename={setRenameItem}
                  onPromote={promoteOne}
                  onReclassify={setReclassifyItem}
                  onMove={setMoveItem}
                  onLock={handleToggleLock}
                  onDelete={requestDelete}
                  hidden={hiddenSet.has(e.path)}
                  onToggleHidden={handleToggleHidden}
                />
              ))}
              {/* Sentinela do windowing: revela mais um lote ao rolar até aqui;
                  o botão é o fallback (clique) — mesmo padrão de Search/Favorites. */}
              {reveal.hasMore && (
                <li className="px-2 pt-1 pb-2">
                  <div ref={reveal.sentinelRef}>
                    <button
                      onClick={reveal.showMore}
                      className="w-full flex items-center justify-center gap-1.5 rounded-lg bg-surface-tertiary/60 py-2 text-xs text-text-secondary hover:text-text-primary transition-colors"
                    >
                      <ChevronDown className="w-3.5 h-3.5" />
                      {t('player.files.showMore', { count: reveal.remaining, total: visible.length })}
                    </button>
                  </div>
                </li>
              )}
            </ul>
          )}

          {/* Viewer universal — arquivo local não-reproduzível clicado */}
          {previewEntry && activeMount && (() => {
            // Irmãs imagens da pasta viram navegação ←/→ no viewer.
            const imageSiblings = visible.filter(x => !x.isDir && detectViewerKind(x.name) === 'image')
            const imageStart = Math.max(0, imageSiblings.findIndex(x => x.path === previewEntry.path))
            return (
              <FilePreviewModal
                infoHash={buildLocalHash(activeMount, previewEntry.path)}
                fileIdx={0}
                filePath={previewEntry.path}
                fileSize={previewEntry.size}
                imageItems={imageSiblings.map(x => ({ label: x.path, url: previewRawURL(buildLocalHash(activeMount, x.path), 0) }))}
                imageStart={imageStart}
                onClose={() => setPreviewEntry(null)}
              />
            )
          })()}

          {/* Modal de Promoção — individual (1) ou lote (N) num único fluxo */}
          <LocalPromoteModal
            mount={activeMount}
            entries={promoteEntries}
            onClose={() => setPromoteEntries([])}
            onPromoted={() => { refresh(); clearSelection() }}
          />

          {/* Modal de Reclassificação em lote via IA */}
          <ReclassifyFolderModal
            mount={activeMount}
            entry={reclassifyItem}
            onClose={() => setReclassifyItem(null)}
            onDone={() => { setReclassifyItem(null); refresh() }}
          />

          {/* Modal de renomear arquivo/pasta in-place */}
          <RenameModal
            mount={activeMount}
            entry={renameItem}
            onClose={() => setRenameItem(null)}
            onRenamed={refresh}
          />

          {/* Modal de Mover entre mounts — individual (moveItem) ou lote (selectedEntries) */}
          <MoveFolderModal
            mount={activeMount}
            entry={moveItem}
            entries={batchMoveOpen ? selectedEntries : undefined}
            onClose={() => {
              setMoveItem(null)
              if (batchMoveOpen) { setBatchMoveOpen(false); clearSelection() }
            }}
            onMoved={refresh}
          />

          {/* Modal de limpar duplicados por conteúdo (pasta atual, recursivo) */}
          {showDuplicates && activeMount && (
            <DuplicatesModal
              mount={activeMount}
              path={path}
              onClose={() => setShowDuplicates(false)}
              onDeleted={(n) => {
                setNotice(n > 0 ? t('local.duplicates.removedNotice', { count: n }) : t('local.duplicates.noneRemoved'))
                refresh()
              }}
            />
          )}
        </section>
      </main>

      {/* Barra de ações em lote (modo seleção) */}
      {selectMode && (
        <BatchActionBar
          count={selected.size}
          onCancel={clearSelection}
          allSelected={visible.length > 0 && selected.size === visible.length}
          onSelectAll={selected.size === visible.length ? () => setSelected(new Set()) : selectAllVisible}
          canMove={isAdmin}
          canPromote={canManipulate || isAdmin}
          onDelete={runBatchDelete}
          onMove={() => setBatchMoveOpen(true)}
          onPromote={runBatchPromote}
          running={batchRunning}
        />
      )}

      {/* Dropdown de mounts (mobile) */}
      <Sheet
        open={mountSheetOpen}
        onClose={() => setMountSheetOpen(false)}
        title={t('local.mounts')}
        icon={<HardDrive className="w-4 h-4 text-green-400 flex-shrink-0" />}
        size="sm"
      >
        <ul className="space-y-1">
          {mounts.map((m) => (
            <li key={m.name}>
              <button
                onClick={() => { handleSelectMount(m.name); setMountSheetOpen(false) }}
                className={`w-full flex items-center gap-2 px-3 min-h-[44px] rounded-lg text-sm transition-colors ${
                  m.name === activeMount
                    ? 'bg-green-500/10 text-green-400 border border-green-500/30'
                    : 'text-text-primary hover:bg-surface-tertiary border border-transparent'
                }`}
              >
                <HardDrive className="w-4 h-4 flex-shrink-0" />
                <span className="truncate">{m.name}</span>
                <MountBadge m={m} />
              </button>
              <MountSpaceLabel m={m} />
            </li>
          ))}
        </ul>
        {canViewAsUser && (
          <div className="mt-4 pt-4 border-t border-default">
            <h3 className="text-xs uppercase tracking-wider text-text-muted mb-2">{t('local.viewAs')}</h3>
            <select
              value={viewAsUser}
              onChange={(e) => { handleViewAsUser(e.target.value); setMountSheetOpen(false) }}
              className="w-full px-3 py-2 rounded-lg text-base bg-surface-secondary border border-default text-text-primary focus:border-green-500/50 focus:outline-none"
            >
              <option value="">{t('local.mySpace')}</option>
              {adminUsers.map((u) => (
                <option key={u.id} value={u.username}>{u.username}</option>
              ))}
            </select>
          </div>
        )}
      </Sheet>
    </div>
  )
}
