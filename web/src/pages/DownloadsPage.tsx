import { useEffect, useMemo, useRef, useState } from 'react'
import { useNavigate } from 'react-router-dom'
import { useTranslation, Trans } from 'react-i18next'
import {
  Pause, Play, Trash2, CheckCircle2, AlertCircle,
  Activity, Users, Zap, ArrowDownCircle, ArrowUpCircle, Wifi,
  Plus, UploadCloud, Search, X, SlidersHorizontal, AlertTriangle,
  ListFilter, Download, CheckSquare, MoreHorizontal,
  ArrowUp, ArrowDown, ArrowDownWideNarrow,
} from 'lucide-react'
import NavHeader from '../components/NavHeader'
import { Sheet } from '../components/Sheet'
import { useConfirm } from '../components/ConfirmDialog'
import { useToast } from '../components/Toast'
import { usePersistedState } from '../lib/storage'
import { useEnumQueryParam, useQueryParam, useQuerySetter } from '../lib/useQueryState'
import { useScrollRestoration } from '../lib/useScrollRestoration'
import { useRevealHidden } from '../lib/reveal'
import {
  DownloadEntry, DownloadFilterParams, downloadsList, downloadsListFiltered, downloadDelete, downloadPause, downloadResume, downloadStopSeed,
  downloadPauseAll, downloadResumeAll, downloadBatchPause, downloadBatchResume, downloadBatchDelete,
  DownloadPriority, downloadSetPriority, getDownloadsQueueSettings,
  downloadTrackers, downloadCategories,
  downloadsListAll, downloadUsers, DownloadUserEntry,
  TorrentInfo, streamActive, streamPause, streamResume, streamSetPriority,
  streamPauseAll, streamResumeAll, streamGetLimits, streamSetLimits, StreamPriority, streamDrop,
  LocalMount, localMounts, buildLocalHash, SearchResult,
  streamAdd, streamAddTorrentFile, WHOLE_TORRENT_FILE_INDEX
} from '../api/client'
import { formatRate } from '../lib/format'
import { localBrowseHref } from '../lib/localBrowse'
import { newPendingDeletes, markDeleted, clearDeleted, reconcile } from '../lib/downloadsReconcile'
import { applyDownloadSort } from '../lib/downloadSort'
import PromoteModal from '../components/PromoteModal'
import { SelectAllButton } from '../components/SelectAllButton'
import { usePlayer } from '../components/PlayerProvider'
import DownloadInspectModal from '../components/DownloadInspectModal'
import DownloadModal from '../components/DownloadModal'
import AddTorrentModal from '../components/AddTorrentModal'
import { useAuth } from '../auth/AuthContext'
import { StatCard } from '../components/downloads/StatCard'
import { CompletedFilterChips, type CompletedFilterKey } from '../components/downloads/CompletedFilterChips'
import { ActiveTab } from '../components/downloads/ActiveTab'
import { SeedingTab } from '../components/downloads/SeedingTab'
import { NetworkTab } from '../components/downloads/NetworkTab'
import { countTorrents, completedViewCounts } from '../lib/downloadGroups'
import { errMessage } from '../lib/errMessage'

// Re-exported para os testes unitários co-localizados que importam de './DownloadsPage'.
export { countTorrents, groupByHash, completedViewCounts } from '../lib/downloadGroups'


// ═══════════════════════════════════════════════════════════════════════════════
// Premium Downloads & Network Dashboard
// ═══════════════════════════════════════════════════════════════════════════════

type Tab = 'all' | 'downloading' | 'paused' | 'completed' | 'failed' | 'network'
// Allowed tab values for the ?tab= URL param (validated by useEnumQueryParam).
const DOWNLOAD_TABS: readonly Tab[] = ['all', 'downloading', 'paused', 'completed', 'failed', 'network']


export default function DownloadsPage() {
  const [items, setItems] = useState<DownloadEntry[]>([])
  // Multi-select de downloads concluídos pra batch promote. Set of IDs.
  const [selected, setSelected] = useState<Set<number>>(new Set())
  // Items passados ao modal de promove (null = fechado). Single = [d], batch = [d1, d2, ...]
  const [promoteTargets, setPromoteTargets] = useState<DownloadEntry[] | null>(null)
  // Download sendo inspecionado: o ID vive na URL (?inspect=) para sobreviver a
  // reload/back; o alvo é resolvido a partir de `items` (push p/ Back fechar). Se o
  // item ainda não carregou (poll) ou sumiu, fica null e o modal espera/fecha.
  const [inspectId, setInspectId] = useQueryParam('inspect', '', { replace: false })
  const inspectTarget = useMemo(
    () => (inspectId ? items.find(d => String(d.id) === inspectId) ?? null : null),
    [inspectId, items],
  )
  // Mounts navegáveis — usados pra decidir se Play vai pelo player local
  // (arquivo em mount como /mnt/downloads) ou pelo torrent (em /data/streams).
  // Carregado uma vez; mounts não mudam durante uma sessão.
  const [mounts, setMounts] = useState<LocalMount[]>([])
  const navigate = useNavigate()
  const { playSingle } = usePlayer()
  const [loading, setLoading] = useState(true)
  const [busyID, setBusyID] = useState<number | null>(null)
  const mountedRef = useRef(true)
  // Monotonic load token: the 2s poll + filter/sort changes can have several
  // load()s in flight at once and their responses can land OUT OF ORDER. Only
  // the newest load may apply its result — otherwise an older poll (which still
  // carries a just-deleted row) lands after a newer one and `reconcile` prunes
  // the pending-delete, making the row reappear (the single-user "Remove didn't
  // take"). Each load captures its token and bails if a newer load started.
  const loadSeqRef = useRef(0)
  // Optimistic-delete tracker: IDs the user removed are hidden from poll
  // results until the backend confirms they're gone, so a stale in-flight poll
  // (started before the DELETE landed) can't resurrect a just-deleted row —
  // the "clicked Remove, row came back" race. A ref (not state) so the polling
  // closure always reads the latest set without re-subscribing.
  const pendingDeletesRef = useRef(newPendingDeletes())

  const [activeTab, setActiveTab] = useEnumQueryParam<Tab>('tab', DOWNLOAD_TABS, 'all')
  // Completed-view filter (Todos / Semeando / No disco). Lifted to the page so the
  // chips render ONCE at the top of the tab content — before the active cards —
  // instead of inside SeedingTab (which put them in the MIDDLE of the list,
  // between the active and the completed cards).
  const [completedFilter, setCompletedFilter] = usePersistedState<CompletedFilterKey>('downloads.completedFilter', 'all')

  const [torrents, setTorrents] = useState<TorrentInfo[]>([])
  const [torrentsLoaded, setTorrentsLoaded] = useState(false)
  const [maxActive, setMaxActive] = useState<number>(0)
  const [busyHash, setBusyHash] = useState<string | null>(null)
  const [bulkBusy, setBulkBusy] = useState(false)
  const [bulkSheetOpen, setBulkSheetOpen] = useState(false)
  const confirm = useConfirm()
  const { notify, notifyError } = useToast()
  const { t } = useTranslation()

  const [limitDownKB, setLimitDownKB] = useState<string>('')
  const [limitUpKB, setLimitUpKB] = useState<string>('')
  const [limitsSaving, setLimitsSaving] = useState(false)
  const [limitsMsg, setLimitsMsg] = useState<string>('')

  // ─── Filter & Sort state (na URL: sobrevive a navegação/reload/reabrir) ──────
  // Todos via useQueryParam (mesma assinatura [v,set] do useState) → o efeito de
  // reload abaixo não muda. Multi-set num único handler (botão "Limpar") usa o
  // setQuery atômico, senão cada setter leria um location.search defasado.
  const [filterSearch, setFilterSearch] = useQueryParam('q')
  const [filterStatus, setFilterStatus] = useQueryParam('status')
  const [filterTracker, setFilterTracker] = useQueryParam('tracker')
  const [filterCategory, setFilterCategory] = useQueryParam('cat')
  const [sortCol, setSortCol] = useQueryParam('sort', 'created_at')
  const [sortDir, setSortDir] = useQueryParam('dir', 'desc')
  const setQuery = useQuerySetter()
  const [availableTrackers, setAvailableTrackers] = useState<string[]>([])
  const [availableCategories, setAvailableCategories] = useState<string[]>([])
  const [filtersParam, setFiltersParam] = useQueryParam('filters')
  const showFilters = filtersParam === '1'
  const filterTimeoutRef = useRef<ReturnType<typeof setTimeout>>()
  // Admin mode: toggle between own downloads and all users' downloads
  const { isAdmin, isGuest, user } = useAuth()
  const [usersParam, setUsersParam] = useQueryParam('users')
  const showAllUsers = isAdmin && usersParam === 'all'
  const [availableUsers, setAvailableUsers] = useState<DownloadUserEntry[]>([])
  const [filterUserId, setFilterUserId] = useQueryParam('uid')
  // Global hidden curtain: downloads tied to a hidden favourite folder drop out
  // unless it's open (re-fetch on flip; backend filters by the header).
  const [revealHidden] = useRevealHidden()
  // Restaura a posição de scroll ao voltar/recarregar, assim que a lista carrega.
  useScrollRestoration(!loading)

  // Add Torrent & Magnet Modals State
  const [showAddModal, setShowAddModal] = useState(false)
  const [downloadTarget, setDownloadTarget] = useState<SearchResult | null>(null)
  const [preloadFiles, setPreloadFiles] = useState<File[] | null>(null)
  const [isDraggingPage, setIsDraggingPage] = useState(false)
  const dragCounter = useRef(0)

  const handleDragEnter = (e: React.DragEvent) => {
    e.preventDefault()
    e.stopPropagation()
    dragCounter.current++
    if ((e.dataTransfer?.items?.length ?? 0) > 0) {
      setIsDraggingPage(true)
    }
  }

  const handleDragLeave = (e: React.DragEvent) => {
    e.preventDefault()
    e.stopPropagation()
    dragCounter.current--
    if (dragCounter.current === 0) {
      setIsDraggingPage(false)
    }
  }

  const handleDragOver = (e: React.DragEvent) => {
    e.preventDefault()
    e.stopPropagation()
  }

  const handleDrop = async (e: React.DragEvent) => {
    e.preventDefault()
    e.stopPropagation()
    setIsDraggingPage(false)
    dragCounter.current = 0

    // Verifica magnet arrastado como texto
    const textData = e.dataTransfer.getData('text/plain')
    if (textData?.trim().startsWith('magnet:?')) {
      const magnet = textData.trim()
      setLoading(true)
      try {
        const info = await streamAdd(magnet)
        const synthetic: SearchResult = {
          title: info.name,
          tracker: '',
          categoryId: 0,
          category: '',
          size: info.totalSize,
          seeders: info.seeders || 0,
          leechers: info.peers || 0,
          age: '',
          magnetUri: magnet,
          link: '',
          infoHash: info.infoHash,
          publishDate: '',
        }
        setDownloadTarget(synthetic)
      } catch (err: unknown) {
        notifyError(err)
      } finally {
        setLoading(false)
      }
      return
    }

    if ((e.dataTransfer?.files?.length ?? 0) > 0) {
      const files = Array.from(e.dataTransfer.files)
      const torrentFiles = files.filter(f => f.name.endsWith('.torrent'))
      
      if (torrentFiles.length === 0) {
        notify(t('downloads.page.dropOnlyTorrentOrMagnet'), 'error')
        return
      }

      if (torrentFiles.length === 1) {
        setLoading(true)
        try {
          const info = await streamAddTorrentFile(torrentFiles[0])
          const synthetic: SearchResult = {
            title: info.name,
            tracker: '',
            categoryId: 0,
            category: '',
            size: info.totalSize,
            seeders: info.seeders || 0,
            leechers: info.peers || 0,
            age: '',
            magnetUri: `magnet:?xt=urn:btih:${info.infoHash}`,
            link: '',
            infoHash: info.infoHash,
            publishDate: '',
          }
          setDownloadTarget(synthetic)
        } catch (err: unknown) {
          notifyError(err)
        } finally {
          setLoading(false)
        }
      } else {
        // Múltiplos arquivos
        setPreloadFiles(torrentFiles)
        setShowAddModal(true)
      }
    }
  }

  // ─── Data loading ─────────────────────────────────────────────────────────

  const load = async () => {
    const seq = ++loadSeqRef.current
    try {
      const list = showAllUsers
        ? await downloadsListAll({
            userId: filterUserId || undefined,
            sort: sortCol,
            order: sortDir,
          })
        : await downloadsList()
      // Drop stale/out-of-order responses: only the latest load reconciles.
      if (mountedRef.current && seq === loadSeqRef.current) setItems(reconcile(pendingDeletesRef.current, list))
    } catch (err) {
      notifyError(errMessage(err))
    } finally {
      if (mountedRef.current) setLoading(false)
    }
  }

  const loadFiltered = async () => {
    const seq = ++loadSeqRef.current
    try {
      const params: DownloadFilterParams = {
        status: filterStatus || undefined,
        tracker: filterTracker || undefined,
        category: filterCategory || undefined,
        search: filterSearch || undefined,
        sort: sortCol,
        order: sortDir,
      }
      if (showAllUsers) params.userId = filterUserId || undefined
      const list = showAllUsers
        ? await downloadsListAll(params)
        : await downloadsListFiltered(params)
      if (mountedRef.current && seq === loadSeqRef.current) setItems(reconcile(pendingDeletesRef.current, list))
    } catch (err) {
      notifyError(errMessage(err))
    } finally {
      if (mountedRef.current) setLoading(false)
    }
  }

  // Poll + mutations must respect active filters — the mount-only interval used
  // to always call load() and overwrote a filtered view every ~2s.
  const reloadDownloadsRef = useRef<() => Promise<void>>(async () => {})
  reloadDownloadsRef.current = async () => {
    const hasFilters = !!(filterStatus || filterTracker || filterCategory || filterSearch
      || sortCol !== 'created_at' || sortDir !== 'desc')
    if (hasFilters) await loadFiltered()
    else await load()
  }

  const loadFilterOptions = async () => {
    try {
      const [trackers, cats] = await Promise.all([downloadTrackers(), downloadCategories()])
      if (mountedRef.current) {
        setAvailableTrackers(trackers)
        setAvailableCategories(cats)
      }
    } catch { /* silent */ }
  }

  const loadLimits = async () => {
    try {
      const cur = await streamGetLimits()
      if (!mountedRef.current) return
      setLimitDownKB(cur.down > 0 ? String(Math.round(cur.down / 1024)) : '')
      setLimitUpKB(cur.up > 0 ? String(Math.round(cur.up / 1024)) : '')
    } catch { /* leave empty */ }
  }

  const loadTorrents = async () => {
    try {
      const list = await streamActive()
      if (mountedRef.current) setTorrents(list)
    } catch { /* keep last */ } finally {
      if (mountedRef.current) setTorrentsLoaded(true)
    }
  }

  useEffect(() => {
    mountedRef.current = true
    reloadDownloadsRef.current().catch(() => {}); loadTorrents(); loadLimits(); loadFilterOptions()
    localMounts().then(setMounts).catch(() => {})
    getDownloadsQueueSettings().then(s => setMaxActive(s.maxActive)).catch(() => {})
    // Pula o poll com a aba oculta — cada ciclo refaz streamActive→buildInfo de
    // todos os torrents ativos (caro num pacote multi-arquivo). Retoma ao focar.
    const t = setInterval(() => {
      if (document.hidden) return
      reloadDownloadsRef.current().catch(() => {})
      loadTorrents()
    }, 2000)
    return () => { mountedRef.current = false; clearInterval(t) }
  }, [])

  // Reload when filters/sort change (debounced for search)
  useEffect(() => {
    if (!mountedRef.current) return
    if (filterSearch !== undefined && filterTimeoutRef.current) {
      clearTimeout(filterTimeoutRef.current)
    }
    filterTimeoutRef.current = setTimeout(() => {
      if (mountedRef.current) reloadDownloadsRef.current().catch(() => {})
    }, filterSearch ? 300 : 0)
    return () => { if (filterTimeoutRef.current) clearTimeout(filterTimeoutRef.current) }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [filterStatus, filterTracker, filterCategory, filterSearch, sortCol, sortDir, filterUserId, showAllUsers, revealHidden])

  // Roteia play: se file_path está dentro de algum mount navegável → player
  // local (sem tocar no anacrolix); senão → player do torrent (cache em
  // /data/streams ou ainda baixando). Mantém a UX consistente com os outros
  // pontos do app onde clicar em Play "simplesmente toca".
  const onPlay = (d: DownloadEntry) => {
    const fp = d.filePath
    if (!fp) return
    // Item de torrent INTEIRO: file_path é a PASTA do torrent (não um arquivo)
    // e fileIndex é o sentinel — abre o player sem índice pra ele resolver o
    // arquivo principal e listar os demais.
    if (d.fileIndex === WHOLE_TORRENT_FILE_INDEX) {
      const synthetic: SearchResult = {
        title: d.name || fp,
        tracker: '', categoryId: 0, category: '', size: d.fileSize,
        seeders: 0, leechers: 0, age: '',
        magnetUri: d.magnet,
        link: '', infoHash: d.infoHash, publishDate: '',
      }
      playSingle(synthetic)
      return
    }
    const m = mounts.find(mt => fp === mt.path || fp.startsWith(mt.path + '/'))
    if (m) {
      let rel = fp.slice(m.path.length).replaceAll(/^\/+/g, '')
      // Mounts user_subpath isolam o download fisicamente em /{username}/ E o
      // backend re-escopa pelo subdir do usuário ao resolver. Removemos o
      // prefixo do username aqui pra não duplicar (espelha StripUserScope).
      const uname = user?.username
      if (m.userSubpath && uname && (rel === uname || rel.startsWith(uname + '/'))) {
        rel = rel.slice(uname.length).replaceAll(/^\/+/g, '')
      }
      const hash = buildLocalHash(m.name, rel)
      const synthetic: SearchResult = {
        title: d.name || rel.split('/').pop() || rel,
        tracker: '', categoryId: 0, category: '', size: d.fileSize,
        seeders: 0, leechers: 0, age: '',
        magnetUri: `magnet:?xt=urn:btih:${hash}`,
        link: '', infoHash: hash, publishDate: '',
      }
      playSingle(synthetic, 0)
      return
    }
    // Não está num mount navegável → assume cache (anacrolix). Toca via hash
    // do torrent + fileIndex. Funciona pra downloads em curso E pra completos
    // que ainda não foram movidos pra fora do cache.
    const synthetic: SearchResult = {
      title: d.name || fp.split('/').pop() || fp,
      tracker: '', categoryId: 0, category: '', size: d.fileSize,
      seeders: 0, leechers: 0, age: '',
      magnetUri: d.magnet,
      link: '', infoHash: d.infoHash, publishDate: '',
    }
    playSingle(synthetic, d.fileIndex)
  }

  // Play a STREAMING torrent card (TorrentInfo, no download row). Opens the player
  // by info_hash WITHOUT a file index, so it resolves the main file and lists the
  // rest — the same "ver arquivos + tocar" the whole-torrent download case gets.
  const onTorrentPlay = (t: TorrentInfo) => {
    const synthetic: SearchResult = {
      title: t.name || t.infoHash,
      tracker: '', categoryId: 0, category: '', size: t.totalSize || 0,
      seeders: 0, leechers: 0, age: '',
      magnetUri: `magnet:?xt=urn:btih:${t.infoHash}`,
      link: '', infoHash: t.infoHash, publishDate: '',
    }
    playSingle(synthetic)
  }

  // Returns a handler that opens this download in the local-files browser (at the
  // folder its file lives in), or undefined when the file isn't under a browsable
  // mount (e.g. a cache-only completion) — so the "Abrir no local" button never
  // shows a dead action. Maps file_path → mount + relpath, stripping the per-user
  // subdir like the player does.
  const openLocalFor = (d: DownloadEntry): (() => void) | undefined => {
    const href = localBrowseHref(d.filePath, mounts, user?.username, d.fileIndex === WHOLE_TORRENT_FILE_INDEX)
    return href ? () => navigate(href) : undefined
  }

  // ─── Actions ──────────────────────────────────────────────────────────────

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

  // ─── Derived data ─────────────────────────────────────────────────────────

  // Esconde o card de STREAMING quando existe QUALQUER download row pro mesmo
  // hash — incluindo `completed`. Antes só filtrávamos `downloading|queued`, e
  // ao terminar o download a streaming card voltava a aparecer ao lado da
  // download card (ambas dizendo 4GB/4GB) — duplicata óbvia. Agora a download
  // row é a fonte canônica e a streaming card só aparece pra torrents que NÃO
  // foram enfileirados como background download (puro stream).
  const bgHashes = new Set(items.map(d => d.infoHash))
  const displayTorrents = torrents.filter(t => !bgHashes.has(t.infoHash))

  // Torrent status helpers
  const torrentStatus = (t: TorrentInfo) =>
    t.status || ((t.progress || 0) >= 1 ? 'complete' : 'downloading')

  const activeTorrents = displayTorrents.filter(t => {
    const s = torrentStatus(t)
    return s === 'downloading' || s === 'paused'
  })
  const seedingTorrents = displayTorrents.filter(t => {
    const s = torrentStatus(t)
    return s === 'seeding' || s === 'complete'
  })

  // Ordenação por métrica AO VIVO (velocidade ↓/↑, seeds) é client-side: esses
  // valores não são persistidos, então o backend não os ordena (ORDER BY). As
  // demais chaves (data/nome/...) seguem server-side; aqui a ordem é preservada.
  // As seções/grupos derivam de sortedItems para herdar a ordem escolhida.
  const sortedItems = useMemo(
    () => applyDownloadSort(items, sortCol, sortDir),
    [items, sortCol, sortDir],
  )

  // Per-status download groups (memoized — poll every 2s would otherwise rebuild)
  const downloadsByStatus = useMemo(() => ({
    downloading: sortedItems.filter(d => d.status === 'downloading' || d.status === 'queued'),
    paused:      sortedItems.filter(d => d.status === 'paused'),
    completed:   sortedItems.filter(d => d.status === 'completed'),
    failed:      sortedItems.filter(d => d.status === 'failed'),
  }), [sortedItems])
  const completedDownloads = downloadsByStatus.completed

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
  const queuedDownloads = items.filter(d => d.status === 'queued')
  const doClearQueued = () => doClearByStatus(
    queuedDownloads,
    t('downloads.clear_queued_title'),
    t('downloads.clear_queued_message', { count: queuedDownloads.length }),
  )

  // Stalled: downloading but no progress (downRate === 0 or null)
  const stalledCount = items.filter(
    d => d.status === 'downloading' && (d.downRate ?? 0) === 0 && d.bytesDownloaded < d.fileSize
  ).length

  // Summary stats: Calculated solely from the active torrents list (`torrents`).
  // Since `items` contains individual file rows of the same torrent (which all
  // share the torrent's aggregate down/up rate and peers), summing from `items` or
  // mixing both would cause double-counting. Using `torrents` ensures each active
  // torrent is counted exactly once.
  const totalDown = torrents.reduce((sum, t) => sum + (t.downRate || 0), 0)
  const totalUp = torrents.reduce((sum, t) => sum + (t.upRate || 0), 0)
  const totalPeers = torrents.reduce((sum, t) => sum + (t.peers || 0), 0)
  // Counts are by TORRENT, not by file row (a 778-file pack is 1 torrent) — see
  // countTorrents. Keeps the badges/indicators aligned with the grouped cards.
  const activeCount = activeTorrents.length + countTorrents(downloadsByStatus.downloading)
  const seedingCount = seedingTorrents.length
  // Background downloads actually running vs waiting (downloadsByStatus.downloading
  // groups both for tab counts, so split them here for the "X/N active" indicator).
  const downloadingNowCount = countTorrents(items.filter(d => d.status === 'downloading'))
  const queuedCount = countTorrents(items.filter(d => d.status === 'queued'))
  let queueSubtitle: string | undefined
  if (queuedCount > 0) queueSubtitle = t('downloads.page.queuedCount', { count: queuedCount })
  else if (seedingCount > 0) queueSubtitle = t('downloads.page.seedingCount', { count: seedingCount })
  const activeValue = maxActive > 0
    ? t('downloads.page.activeOfMax', { current: downloadingNowCount, max: maxActive })
    : t('downloads.page.activeCount', { count: activeCount })

  // Tab badge counts
  const tabCounts: Record<Tab, number> = {
    all:         displayTorrents.length + countTorrents(items),
    downloading: activeTorrents.length + countTorrents(downloadsByStatus.downloading),
    paused:      countTorrents(downloadsByStatus.paused),
    completed:   seedingTorrents.length + countTorrents(completedDownloads),
    failed:      countTorrents(downloadsByStatus.failed),
    network:     0,
  }

  // Items for the currently-selected status tab
  const tabDownloads: Record<Tab, DownloadEntry[]> = {
    all:         sortedItems,
    downloading: [...downloadsByStatus.downloading, ...downloadsByStatus.paused, ...downloadsByStatus.failed],
    paused:      downloadsByStatus.paused,
    completed:   completedDownloads,
    failed:      downloadsByStatus.failed,
    network:     [],
  }
  const tabTorrents: Record<Tab, TorrentInfo[]> = {
    all:         displayTorrents,
    downloading: activeTorrents,
    paused:      [],
    completed:   seedingTorrents,
    failed:      [],
    network:     [],
  }

  // Counts for the completed-view filter chips, computed at page level (so the
  // chips can sit at the TOP, above the active cards). Seeding = live torrents
  // not yet on disk + completed groups still seeding; on-disk = completed groups
  // whose torrent is no longer live.
  const { seeding: seedingCountForTab, onDisk: onDiskCountForTab } =
    completedViewCounts(tabDownloads[activeTab], tabTorrents[activeTab])
  const hasCompletedForTab = seedingCountForTab > 0 || onDiskCountForTab > 0
  const effectiveCompletedFilter: CompletedFilterKey = hasCompletedForTab ? completedFilter : 'all'

  // ─── Render ───────────────────────────────────────────────────────────────

  return (
    <section
      onDragEnter={handleDragEnter}
      onDragOver={handleDragOver}
      onDragLeave={handleDragLeave}
      onDrop={handleDrop}
      aria-label={t('downloads.page.sectionAriaLabel')}
      className="relative min-h-screen bg-surface"
    >
      {isDraggingPage && (
        <div className="fixed inset-0 z-50 bg-surface-elevated/80 backdrop-blur-md flex flex-col items-center justify-center border-4 border-dashed border-cyan-500/50 m-4 rounded-3xl pointer-events-none transition-all duration-300 animate-pulse">
          <UploadCloud className="w-16 h-16 text-cyan-400 mb-4 animate-bounce" />
          <h2 className="text-xl font-bold text-text-primary mb-1">{t('downloads.page.dropOverlayTitle')}</h2>
          <p className="text-sm text-text-secondary">{t('downloads.page.dropOverlaySubtitle')}</p>
        </div>
      )}

      <NavHeader />
      <main className="max-w-5xl mx-auto px-4 py-6 flex flex-col gap-6">
        {/* ═══════════════ Summary Dashboard ═══════════════ */}
        <div className="grid grid-cols-2 sm:grid-cols-4 gap-3">
          <StatCard
            icon={<ArrowDownCircle className="w-5 h-5" />}
            label={t('downloads.page.statDownload')}
            value={formatRate(totalDown)}
            gradient="from-emerald-500/20 to-teal-500/10"
            iconColor="text-emerald-400"
            pulse={totalDown > 0}
          />
          <StatCard
            icon={<ArrowUpCircle className="w-5 h-5" />}
            label={t('downloads.page.statUpload')}
            value={formatRate(totalUp)}
            gradient="from-violet-500/20 to-purple-500/10"
            iconColor="text-violet-400"
            pulse={totalUp > 0}
          />
          <StatCard
            icon={<Users className="w-5 h-5" />}
            label={t('downloads.page.statPeers')}
            value={String(totalPeers)}
            gradient="from-blue-500/20 to-cyan-500/10"
            iconColor="text-blue-400"
          />
          <StatCard
            icon={<Activity className="w-5 h-5" />}
            label={t('downloads.page.statQueue')}
            value={activeValue}
            subtitle={queueSubtitle}
            gradient="from-amber-500/20 to-orange-500/10"
            iconColor="text-amber-400"
          />
        </div>

        {/* ═══════════════ Global Action Toolbar ═══════════════ */}
        <div className="flex items-center justify-between gap-3 flex-wrap">
          {/* Quick stats strip */}
          <div className="flex items-center gap-3 text-xs text-text-secondary flex-wrap">
            {downloadsByStatus.downloading.length > 0 && (
              <span className="flex items-center gap-1">
                <Download className="w-3.5 h-3.5 text-cyan-400" />
                <Trans i18nKey="downloads.page.quickDownloading" values={{ count: downloadsByStatus.downloading.length }} components={{ b: <span className="text-text-primary font-medium" /> }} />
              </span>
            )}
            {downloadsByStatus.paused.length > 0 && (
              <span className="flex items-center gap-1">
                <Pause className="w-3.5 h-3.5 text-text-secondary" />
                <Trans i18nKey="downloads.page.quickPaused" values={{ count: downloadsByStatus.paused.length }} components={{ b: <span className="text-text-primary font-medium" /> }} />
              </span>
            )}
            {completedDownloads.length > 0 && (
              <span className="flex items-center gap-1">
                <CheckCircle2 className="w-3.5 h-3.5 text-green-400" />
                <Trans i18nKey="downloads.page.quickCompleted" values={{ count: completedDownloads.length }} components={{ b: <span className="text-text-primary font-medium" /> }} />
              </span>
            )}
            {downloadsByStatus.failed.length > 0 && (
              <span className="flex items-center gap-1">
                <AlertCircle className="w-3.5 h-3.5 text-red-400" />
                <Trans i18nKey="downloads.page.quickFailed" values={{ count: downloadsByStatus.failed.length }} components={{ b: <span className="text-text-primary font-medium" /> }} />
              </span>
            )}
            {stalledCount > 0 && (
              <span className="flex items-center gap-1 text-amber-400">
                <AlertTriangle className="w-3.5 h-3.5" />
                <Trans i18nKey="downloads.page.quickStalled" values={{ count: stalledCount }} components={{ b: <span className="font-medium" /> }} />
              </span>
            )}
          </div>

          {/* Global controls */}
          {!isGuest && (
            <>
              {/* Desktop: ações inline */}
              <div className="hidden sm:flex items-center gap-2">
                <button
                  onClick={doResumeAll}
                  disabled={bulkBusy}
                  title={t('downloads.page.startAllTitle')}
                  className="flex items-center gap-1.5 text-xs bg-emerald-500/10 hover:bg-emerald-500/20 disabled:opacity-50 text-emerald-700 dark:text-emerald-300 border border-emerald-500/30 px-3 py-1.5 rounded-lg transition-colors"
                >
                  <Play className="w-3 h-3" /> {t('downloads.page.startAll')}
                </button>
                <button
                  onClick={doPauseAll}
                  disabled={bulkBusy}
                  title={t('downloads.page.pauseAllTitle')}
                  className="flex items-center gap-1.5 text-xs bg-surface-secondary hover:bg-surface-tertiary disabled:opacity-50 text-text-primary border border-default px-3 py-1.5 rounded-lg transition-colors"
                >
                  <Pause className="w-3 h-3" /> {t('downloads.page.pauseAll')}
                </button>
                {completedDownloads.length > 0 && (
                  <button
                    onClick={doRemoveCompleted}
                    disabled={bulkBusy}
                    title={t('downloads.page.removeCompletedBtnTitle')}
                    className="flex items-center gap-1.5 text-xs bg-red-500/10 hover:bg-red-500/20 disabled:opacity-50 text-red-700 dark:text-red-300 border border-red-500/30 px-3 py-1.5 rounded-lg transition-colors"
                  >
                    <Trash2 className="w-3 h-3" /> {t('downloads.page.removeCompleted')}
                  </button>
                )}
                {downloadsByStatus.failed.length > 0 && (
                  <button
                    onClick={doClearFailed}
                    disabled={bulkBusy}
                    title={t('downloads.clear_failed_title')}
                    className="flex items-center gap-1.5 text-xs bg-red-500/10 hover:bg-red-500/20 disabled:opacity-50 text-red-700 dark:text-red-300 border border-red-500/30 px-3 py-1.5 rounded-lg transition-colors"
                  >
                    <Trash2 className="w-3 h-3" /> {t('downloads.clear_failed')} ({downloadsByStatus.failed.length})
                  </button>
                )}
                {queuedDownloads.length > 0 && (
                  <button
                    onClick={doClearQueued}
                    disabled={bulkBusy}
                    title={t('downloads.clear_queued_title')}
                    className="flex items-center gap-1.5 text-xs bg-red-500/10 hover:bg-red-500/20 disabled:opacity-50 text-red-700 dark:text-red-300 border border-red-500/30 px-3 py-1.5 rounded-lg transition-colors"
                  >
                    <Trash2 className="w-3 h-3" /> {t('downloads.clear_queued')} ({queuedDownloads.length})
                  </button>
                )}
              </div>
              {/* Mobile: agrupadas num Sheet de "Ações" */}
              <button
                onClick={() => setBulkSheetOpen(true)}
                disabled={bulkBusy}
                className="sm:hidden flex items-center gap-1.5 text-xs px-3 min-h-[44px] rounded-lg border border-default bg-surface-secondary text-text-primary disabled:opacity-50"
              >
                <MoreHorizontal className="w-4 h-4" /> {t('downloads.page.actions')}
              </button>
            </>
          )}
        </div>

        {/* ═══════════════ Filters Bar ═══════════════ */}
        <div className="flex flex-col gap-2">
          <div className="flex items-center gap-2 flex-wrap">
            <div className="relative flex-1 min-w-[200px]">
              <Search className="absolute left-3 top-1/2 -translate-y-1/2 w-4 h-4 text-text-muted" />
              <input
                type="text"
                value={filterSearch}
                onChange={e => setFilterSearch(e.target.value)}
                placeholder={t('downloads.page.searchPlaceholder')}
                className="w-full bg-surface-secondary/80 border border-default rounded-lg pl-9 pr-3 py-2 text-sm text-text-primary placeholder-gray-500 focus:outline-none focus:border-cyan-500/50 transition-colors"
              />
              {filterSearch && (
                <button onClick={() => setFilterSearch('')} className="absolute right-3 top-1/2 -translate-y-1/2 text-text-muted hover:text-text-primary">
                  <X className="w-3.5 h-3.5" />
                </button>
              )}
            </div>
            <button
              onClick={() => setFiltersParam(showFilters ? '' : '1')}
              className={`flex items-center gap-1.5 text-xs px-3 py-2 rounded-lg border transition-colors ${
                showFilters || filterStatus || filterTracker || filterCategory
                  ? 'bg-cyan-500/10 border-cyan-500/30 text-cyan-700 dark:text-cyan-300'
                  : 'bg-surface-secondary border-default text-text-secondary hover:text-text-primary'
              }`}
            >
              <SlidersHorizontal className="w-3.5 h-3.5" />
              {t('downloads.page.filters')}
              {(filterStatus || filterTracker || filterCategory) && (
                <span className="w-1.5 h-1.5 rounded-full bg-cyan-400" />
              )}
            </button>
          </div>

          {showFilters && (
            <div className="bg-surface-secondary/40 border border-default/50 rounded-xl p-3 flex flex-col gap-3">
              {/* Filtros — selects preenchem a largura no mobile (flex-1) e ficam
                  no tamanho natural no desktop. */}
              <div className="flex flex-wrap items-center gap-2">
                <select
                  value={filterStatus}
                  onChange={e => setFilterStatus(e.target.value)}
                  className="flex-1 min-w-[140px] sm:flex-none bg-surface border border-default rounded-lg px-3 py-1.5 text-xs text-text-primary focus:outline-none focus:border-cyan-500/50"
                >
                  <option value="">{t('downloads.page.filterAllStatus')}</option>
                  <option value="downloading">{t('downloads.page.statusDownloading')}</option>
                  <option value="paused">{t('downloads.page.statusPaused')}</option>
                  <option value="queued">{t('downloads.page.statusQueued')}</option>
                  <option value="completed">{t('downloads.page.statusCompleted')}</option>
                  <option value="failed">{t('downloads.page.statusFailed')}</option>
                </select>
                {availableTrackers.length > 0 && (
                  <select
                    value={filterTracker}
                    onChange={e => setFilterTracker(e.target.value)}
                    className="flex-1 min-w-[140px] sm:flex-none bg-surface border border-default rounded-lg px-3 py-1.5 text-xs text-text-primary focus:outline-none focus:border-cyan-500/50"
                  >
                    <option value="">{t('downloads.page.filterAllTrackers')}</option>
                    {availableTrackers.map(tr => (
                      <option key={tr} value={tr}>{tr}</option>
                    ))}
                  </select>
                )}
                {availableCategories.length > 0 && (
                  <select
                    value={filterCategory}
                    onChange={e => setFilterCategory(e.target.value)}
                    className="flex-1 min-w-[140px] sm:flex-none bg-surface border border-default rounded-lg px-3 py-1.5 text-xs text-text-primary focus:outline-none focus:border-cyan-500/50"
                  >
                    <option value="">{t('downloads.page.filterAllCategories')}</option>
                    {availableCategories.map(c => (
                      <option key={c} value={c}>{c}</option>
                    ))}
                  </select>
                )}
                {showAllUsers && availableUsers.length > 0 && (
                  <select
                    value={filterUserId}
                    onChange={e => setFilterUserId(e.target.value)}
                    className="flex-1 min-w-[140px] sm:flex-none bg-surface border border-default rounded-lg px-3 py-1.5 text-xs text-text-primary focus:outline-none focus:border-cyan-500/50"
                  >
                    <option value="">{t('downloads.page.filterAllUsers')}</option>
                    {availableUsers.map(u => (
                      <option key={u.id} value={String(u.id)}>{u.username}</option>
                    ))}
                  </select>
                )}
              </div>

              {/* Ordenar — grupo próprio, separado por divisória; Limpar à direita. */}
              <div className="flex items-center gap-2 flex-wrap border-t border-default/50 pt-3">
                <span className="text-xs text-text-muted flex items-center gap-1.5 flex-shrink-0">
                  <ArrowDownWideNarrow className="w-3.5 h-3.5" /> {t('downloads.page.sort')}
                </span>
                <select
                  value={sortCol}
                  onChange={e => setSortCol(e.target.value)}
                  className="flex-1 min-w-[140px] sm:flex-none bg-surface border border-default rounded-lg px-3 py-1.5 text-xs text-text-primary focus:outline-none focus:border-cyan-500/50"
                >
                  <option value="created_at">{t('downloads.page.sortDate')}</option>
                  <option value="name">{t('downloads.page.sortName')}</option>
                  <option value="size">{t('downloads.page.sortSize')}</option>
                  <option value="progress">{t('downloads.page.sortProgress')}</option>
                  <option value="downRate">{t('downloads.page.sortDownSpeed')}</option>
                  <option value="upRate">{t('downloads.page.sortUpSpeed')}</option>
                  <option value="seeders">{t('downloads.page.sortSeeds')}</option>
                  <option value="status">{t('downloads.page.sortStatus')}</option>
                  <option value="tracker">{t('downloads.page.sortTracker')}</option>
                  <option value="category">{t('downloads.page.sortCategory')}</option>
                </select>
                <button
                  onClick={() => setSortDir(sortDir === 'asc' ? 'desc' : 'asc')}
                  title={sortDir === 'asc' ? t('downloads.page.ascending') : t('downloads.page.descending')}
                  aria-label={t('downloads.page.invertOrder')}
                  className="flex-shrink-0 bg-surface border border-default rounded-lg px-2 py-1.5 text-text-primary hover:text-cyan-600 dark:hover:text-cyan-300 hover:border-cyan-500/40 transition-colors"
                >
                  {sortDir === 'asc' ? <ArrowUp className="w-3.5 h-3.5" /> : <ArrowDown className="w-3.5 h-3.5" />}
                </button>
                <button
                  onClick={() => setQuery({ status: null, tracker: null, cat: null, q: null, uid: null, sort: null, dir: null })}
                  className="ml-auto text-xs text-text-muted hover:text-text-primary px-2 py-1 flex-shrink-0"
                >
                  {t('downloads.page.clear')}
                </button>
              </div>
            </div>
          )}
        </div>

        {/* ═══════════════ Tabs & Actions ═══════════════ */}
        <div className="flex items-center justify-between border-b border-default/60 flex-wrap gap-3">
          <div className="flex items-center gap-0.5 overflow-x-auto">
            {([
              { key: 'all'         as Tab, label: t('downloads.page.tabAll'),         icon: <ListFilter className="w-3.5 h-3.5" /> },
              { key: 'downloading' as Tab, label: t('downloads.page.tabDownloading'), icon: <Zap className="w-3.5 h-3.5" /> },
              { key: 'paused'      as Tab, label: t('downloads.page.tabPaused'),      icon: <Pause className="w-3.5 h-3.5" /> },
              { key: 'completed'   as Tab, label: t('downloads.page.tabCompleted'),   icon: <CheckSquare className="w-3.5 h-3.5" /> },
              { key: 'failed'      as Tab, label: t('downloads.page.tabFailed'),      icon: <AlertCircle className="w-3.5 h-3.5" /> },
              { key: 'network'     as Tab, label: t('downloads.page.tabNetwork'),     icon: <Wifi className="w-3.5 h-3.5" /> },
            ]).map(tab => (
              <button
                key={tab.key}
                onClick={() => setActiveTab(tab.key)}
                className={`
                  flex items-center gap-1.5 px-3 py-2.5 text-xs font-medium whitespace-nowrap
                  border-b-2 transition-all duration-200
                  ${activeTab === tab.key
                    ? 'border-emerald-400 text-emerald-400'
                    : 'border-transparent text-text-secondary hover:text-text-primary hover:border-strong'}
                `}
              >
                {tab.icon}
                {tab.label}
                {tabCounts[tab.key] > 0 && (
                  <span className={`text-[10px] px-1.5 py-0.5 rounded-full font-semibold min-w-[18px] text-center ${tabBadgeClass(activeTab, tab.key)}`}>
                    {tabCounts[tab.key]}
                  </span>
                )}
              </button>
            ))}
          </div>
          <div className="flex items-center gap-2">
            {isAdmin && (
              <button
                onClick={() => {
                  if (showAllUsers) { setQuery({ users: null, uid: null }) } // desligar: limpa users + uid órfão
                  else { setUsersParam('all'); downloadUsers().then(setAvailableUsers).catch(() => {}) }
                }}
                className={`flex items-center gap-1.5 text-xs px-4 py-2 rounded-xl font-semibold transition-all duration-200 mb-2 md:mb-0 ${
                  showAllUsers
                    ? 'bg-violet-500 hover:bg-violet-600 text-white shadow-lg shadow-violet-500/10'
                    : 'bg-surface-secondary border border-default text-text-secondary hover:text-text-primary'
                }`}
              >
                <Users className="w-4 h-4" />
                {showAllUsers ? t('downloads.page.allUsers') : t('downloads.page.myDownloads')}
              </button>
            )}
            {!isGuest && (
              <button
                onClick={() => {
                  setPreloadFiles(null)
                  setShowAddModal(true)
                }}
                className="flex items-center gap-1.5 text-xs bg-cyan-500 hover:bg-cyan-600 text-white px-4 py-2 rounded-xl font-semibold transition-all duration-200 shadow-lg shadow-cyan-500/10 mb-2 md:mb-0"
              >
                <Plus className="w-4 h-4" /> {t('downloads.page.addTorrentMagnet')}
              </button>
            )}
          </div>
        </div>

        {/* ═══════════════ Tab Content ═══════════════ */}
        <div className="min-h-[300px]">
          {activeTab !== 'network' && (
            <>
              {/* Completed-view filter — at the TOP, so it heads the whole list
                  instead of sitting between the active and completed cards. */}
              {torrentsLoaded && hasCompletedForTab && (
                <div className="mb-4">
                  <CompletedFilterChips
                    value={completedFilter}
                    onChange={setCompletedFilter}
                    seedingN={seedingCountForTab}
                    onDiskN={onDiskCountForTab}
                  />
                </div>
              )}
              {/* Active / downloading torrents — hidden when filtering "No disco". */}
              {tabTorrents[activeTab].length > 0 && effectiveCompletedFilter !== 'ondisk' && (
                <ActiveTab
                  torrents={tabTorrents[activeTab]}
                  downloads={[]}
                  torrentsLoaded={torrentsLoaded}
                  loading={false}
                  busyHash={busyHash}
                  busyID={null}
                  onTorrentPause={onTorrentPause}
                  onTorrentResume={onTorrentResume}
                  onTorrentPriority={onTorrentPriority}
                  onTorrentDelete={onTorrentDelete}
                  onTorrentPlay={onTorrentPlay}
                  onPause={onPause}
                  onResume={onResume}
                  onDelete={onDelete}
                  onPlay={onPlay}
                  onInspect={(d: DownloadEntry) => setInspectId(String(d.id))}
                openLocalFor={openLocalFor}
                />
              )}
              {/* Background downloads for this tab */}
              <SeedingTab
                torrents={[]}
                downloads={tabDownloads[activeTab]}
                completedFilter={effectiveCompletedFilter}
                torrentsLoaded={torrentsLoaded}
                busyHash={busyHash}
                busyID={busyID}
                selected={selected}
                onToggleSelected={(id: number) => setSelected(prev => {
                  const next = new Set(prev)
                  if (next.has(id)) next.delete(id); else next.add(id)
                  return next
                })}
                onTorrentPause={onTorrentPause}
                onTorrentResume={onTorrentResume}
                onTorrentPriority={onTorrentPriority}
                onTorrentDelete={onTorrentDelete}
                onTorrentPlay={onTorrentPlay}
                onPause={onPause}
                onResume={onResume}
                onDelete={onDelete}
                onPromote={onPromote}
                onStopSeed={onStopSeed}
                onPromoteMany={onPromoteMany}
                onDeleteMany={onDeleteMany}
                onStopSeedMany={onStopSeedMany}
                onRetryMany={onRetryMany}
                onSetPriority={onSetPriority}
                onPlay={onPlay}
                onInspect={(d: DownloadEntry) => setInspectId(String(d.id))}
                openLocalFor={openLocalFor}
                loading={loading}
              />
            </>
          )}
          {activeTab === 'network' && (
            <NetworkTab
              limitDownKB={limitDownKB}
              limitUpKB={limitUpKB}
              setLimitDownKB={setLimitDownKB}
              setLimitUpKB={setLimitUpKB}
              limitsSaving={limitsSaving}
              limitsMsg={limitsMsg}
              onSaveLimits={onSaveLimits}
              totalDown={totalDown}
              totalUp={totalUp}
              totalPeers={totalPeers}
            />
          )}
        </div>
      </main>

      {/* Modal de promove — navegador de subpastas + nova pasta + keep-seeding.
          Aceita single (1 item) ou batch (N selecionados). Backend cria subdirs
          inexistentes via os.MkdirAll. */}
      <PromoteModal
        items={promoteTargets}
        onClose={() => setPromoteTargets(null)}
        onPromoted={onPromoted}
      />

      {/* Modal de inspeção detalhada de download (com recheck, files list e stop seed) */}
      <DownloadInspectModal
        download={inspectTarget}
        onClose={() => setQuery({ inspect: null }, { replace: true })}
        siblings={inspectTarget ? items.filter(i => i.infoHash === inspectTarget.infoHash) : []}
        onAdopted={() => { reloadDownloadsRef.current().catch(() => {}) }}
        onMutated={(updated) => {
          setItems(prev => prev.map(item => item.id === updated.id ? updated : item))
        }}
        onDeleted={() => {
          setItems(prev => prev.filter(item => item.id !== inspectTarget?.id))
          setQuery({ inspect: null }, { replace: true })
        }}
        onPromote={onPromote}
        onPlay={onPlay}
      />

      {/* Modal para adicionar torrents por arquivo drag & drop ou link magnet */}
      <AddTorrentModal
        isOpen={showAddModal}
        onClose={() => setShowAddModal(false)}
        preloadFiles={preloadFiles}
        onAdded={(result) => {
          setDownloadTarget(result)
        }}
      />

      {/* Modal para configurar download de um único torrent resolvido da busca ou arrastado */}
      <DownloadModal
        result={downloadTarget}
        onClose={() => {
          setDownloadTarget(null)
          reloadDownloadsRef.current().catch(() => {})
          loadTorrents().catch(() => {})
        }}
      />

      {/* Ações globais (mobile) */}
      <Sheet
        open={bulkSheetOpen}
        onClose={() => setBulkSheetOpen(false)}
        title={t('downloads.page.actions')}
        size="sm"
      >
        <div className="flex flex-col gap-2">
          <button
            onClick={() => { setBulkSheetOpen(false); void doResumeAll() }}
            disabled={bulkBusy}
            className="flex items-center gap-2 min-h-[48px] px-4 rounded-lg bg-emerald-500/10 text-emerald-700 dark:text-emerald-300 border border-emerald-500/30 disabled:opacity-50"
          >
            <Play className="w-4 h-4" /> {t('downloads.page.startAll')}
          </button>
          <button
            onClick={() => { setBulkSheetOpen(false); void doPauseAll() }}
            disabled={bulkBusy}
            className="flex items-center gap-2 min-h-[48px] px-4 rounded-lg bg-surface-tertiary/60 text-text-primary border border-default disabled:opacity-50"
          >
            <Pause className="w-4 h-4" /> {t('downloads.page.pauseAll')}
          </button>
          {completedDownloads.length > 0 && (
            <button
              onClick={() => { setBulkSheetOpen(false); void doRemoveCompleted() }}
              disabled={bulkBusy}
              className="flex items-center gap-2 min-h-[48px] px-4 rounded-lg bg-red-500/10 text-red-700 dark:text-red-300 border border-red-500/30 disabled:opacity-50"
            >
              <Trash2 className="w-4 h-4" /> {t('downloads.page.removeCompletedCount', { count: completedDownloads.length })}
            </button>
          )}
          {downloadsByStatus.failed.length > 0 && (
            <button
              onClick={() => { setBulkSheetOpen(false); void doClearFailed() }}
              disabled={bulkBusy}
              className="flex items-center gap-2 min-h-[48px] px-4 rounded-lg bg-red-500/10 text-red-700 dark:text-red-300 border border-red-500/30 disabled:opacity-50"
            >
              <Trash2 className="w-4 h-4" /> {t('downloads.clear_failed')} ({downloadsByStatus.failed.length})
            </button>
          )}
          {queuedDownloads.length > 0 && (
            <button
              onClick={() => { setBulkSheetOpen(false); void doClearQueued() }}
              disabled={bulkBusy}
              className="flex items-center gap-2 min-h-[48px] px-4 rounded-lg bg-red-500/10 text-red-700 dark:text-red-300 border border-red-500/30 disabled:opacity-50"
            >
              <Trash2 className="w-4 h-4" /> {t('downloads.clear_queued')} ({queuedDownloads.length})
            </button>
          )}
        </div>
      </Sheet>


      {/* Barra flutuante de bulk actions, só aparece com seleção ativa. */}
      {selected.size > 0 && (
        <div
          style={{ bottom: 'calc(1rem + env(safe-area-inset-bottom, 0px))' }}
          className="fixed left-1/2 -translate-x-1/2 z-40 flex items-center gap-2 bg-surface-secondary border border-cyan-500/40 shadow-2xl rounded-full px-4 py-2 backdrop-blur"
        >
          <span className="text-sm text-text-primary font-medium whitespace-nowrap">{t('downloads.page.selectedCount', { count: selected.size })}</span>
          <div className="w-px h-5 bg-surface-tertiary" />
          <button
            onClick={onBatchPause}
            disabled={bulkBusy}
            className="flex items-center gap-1 text-xs bg-surface-tertiary/60 hover:bg-surface-tertiary disabled:opacity-50 text-text-primary px-3 py-1 rounded-full transition-colors"
          >
            <Pause className="w-3 h-3" /> {t('downloads.page.pause')}
          </button>
          <button
            onClick={onBatchResume}
            disabled={bulkBusy}
            className="flex items-center gap-1 text-xs bg-emerald-500/10 hover:bg-emerald-500/20 disabled:opacity-50 text-emerald-700 dark:text-emerald-300 px-3 py-1 rounded-full transition-colors"
          >
            <Play className="w-3 h-3" /> {t('downloads.page.resume')}
          </button>
          <button
            onClick={onPromoteSelected}
            className="flex items-center gap-1 text-xs bg-cyan-500/20 hover:bg-cyan-500/30 text-cyan-700 dark:text-cyan-300 px-3 py-1 rounded-full transition-colors"
          >
            <ArrowUpCircle className="w-3 h-3" />
            {t('downloads.page.promote')}
          </button>
          <button
            onClick={onBatchDelete}
            disabled={bulkBusy}
            className="flex items-center gap-1 text-xs bg-red-500/10 hover:bg-red-500/20 disabled:opacity-50 text-red-700 dark:text-red-300 px-3 py-1 rounded-full transition-colors"
          >
            <Trash2 className="w-3 h-3" /> {t('downloads.page.remove')}
          </button>
          <div className="w-px h-5 bg-surface-tertiary" />
          <SelectAllButton
            allSelected={items.length > 0 && selected.size === items.length}
            onToggle={handleToggleSelectAll}
          />
        </div>
      )}
    </section>
  )
}

// ═══════════════════════════════════════════════════════════════════════════════
// Shared helpers
// ═══════════════════════════════════════════════════════════════════════════════

function tabBadgeClass(activeTab: Tab, tabKey: string): string {
  if (activeTab === tabKey) return 'bg-emerald-500/20 text-emerald-700 dark:text-emerald-300'
  if (tabKey === 'failed') return 'bg-red-500/20 text-red-400'
  return 'bg-surface-tertiary text-text-secondary'
}
