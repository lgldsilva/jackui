import { useEffect, useMemo, useRef, useState } from 'react'
import { useNavigate } from 'react-router-dom'
import { useTranslation } from 'react-i18next'
import {
  Loader2, Pause, Play, Trash2, CheckCircle2, AlertCircle, Clock,
  Activity, Gauge, Users, Zap, ArrowDownCircle, ArrowUpCircle, Wifi, Server, Info,
  Plus, UploadCloud, Search, X, SlidersHorizontal, HardDrive, AlertTriangle,
  ListFilter, Download, CheckSquare, MoreHorizontal, ChevronDown, ChevronRight, Folder,
  ArrowUp, ArrowDown, ArrowDownWideNarrow,
} from 'lucide-react'
import NavHeader from '../components/NavHeader'
import { Sheet } from '../components/Sheet'
import { useConfirm } from '../components/ConfirmDialog'
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
import { formatRate, formatDurationShort, formatBytesPair } from '../lib/format'
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


// ═══════════════════════════════════════════════════════════════════════════════
// Premium Downloads & Network Dashboard
// ═══════════════════════════════════════════════════════════════════════════════

type Tab = 'all' | 'downloading' | 'paused' | 'completed' | 'failed' | 'network'
// Allowed tab values for the ?tab= URL param (validated by useEnumQueryParam).
const DOWNLOAD_TABS: readonly Tab[] = ['all', 'downloading', 'paused', 'completed', 'failed', 'network']

// errMessage extracts a human-readable message from an unknown thrown value
// (axios error with a JSON {error} body, a plain Error, or anything else).
function errMessage(err: unknown): string {
  const ax = err as { response?: { data?: { error?: string } }; message?: string }
  return ax?.response?.data?.error || ax?.message || String(err)
}

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
      } catch (err: any) {
        alert(`Erro ao processar magnet: ${err.message || err}`)
      } finally {
        setLoading(false)
      }
      return
    }

    if ((e.dataTransfer?.files?.length ?? 0) > 0) {
      const files = Array.from(e.dataTransfer.files)
      const torrentFiles = files.filter(f => f.name.endsWith('.torrent'))
      
      if (torrentFiles.length === 0) {
        alert('Por favor, arraste apenas arquivos com a extensão .torrent ou links magnet')
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
        } catch (err: any) {
          alert(`Erro ao carregar torrent: ${err.message || err}`)
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
    } catch { /* silent */ } finally {
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
    } catch { /* silent */ } finally {
      if (mountedRef.current) setLoading(false)
    }
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
    load(); loadTorrents(); loadLimits(); loadFilterOptions()
    localMounts().then(setMounts).catch(() => {})
    getDownloadsQueueSettings().then(s => setMaxActive(s.maxActive)).catch(() => {})
    // Pula o poll com a aba oculta — cada ciclo refaz streamActive→buildInfo de
    // todos os torrents ativos (caro num pacote multi-arquivo). Retoma ao focar.
    const t = setInterval(() => { if (document.hidden) return; load(); loadTorrents() }, 2000)
    return () => { mountedRef.current = false; clearInterval(t) }
  }, [])

  // Reload when filters/sort change (debounced for search)
  useEffect(() => {
    if (!mountedRef.current) return
    if (filterSearch !== undefined && filterTimeoutRef.current) {
      clearTimeout(filterTimeoutRef.current)
    }
    filterTimeoutRef.current = setTimeout(() => {
      if (mountedRef.current) {
        const hasFilters = filterStatus || filterTracker || filterCategory || filterSearch
        if (hasFilters || sortCol !== 'created_at' || sortDir !== 'desc') {
          loadFiltered()
        } else {
          load()
        }
      }
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
    try { await downloadPause(id); await load() } finally { setBusyID(null) }
  }
  const onResume = async (id: number) => {
    setBusyID(id)
    try { await downloadResume(id); await load() } finally { setBusyID(null) }
  }
  const onSetPriority = async (id: number, priority: DownloadPriority) => {
    setBusyID(id)
    try { await downloadSetPriority(id, priority); await load() } finally { setBusyID(null) }
  }
  const onDelete = async (id: number) => {
    if (!await confirm({ title: 'Remover download?', message: 'Parar e remover este download da fila? A sessão de streaming/transcode aberta pelo Play também é encerrada.', confirmLabel: 'Remover', destructive: true })) return
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
      await load(); await loadTorrents()
    } catch (err) {
      // The DELETE genuinely failed (network/500) — un-hide the row so the user
      // sees reality instead of a silently-vanished item, and surface the error.
      clearDeleted(pendingDeletesRef.current, [id])
      await load().catch(() => {})
      alert(`Erro ao remover download: ${errMessage(err)}`)
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
    try { await downloadBatchPause(ids); await load(); setSelected(new Set()) } finally { setBulkBusy(false) }
  }

  const onBatchResume = async () => {
    const ids = items.filter(d => selected.has(d.id) && d.status === 'paused').map(d => d.id)
    if (ids.length === 0) return
    setBulkBusy(true)
    try { await downloadBatchResume(ids); await load(); setSelected(new Set()) } finally { setBulkBusy(false) }
  }

  const onBatchDelete = async () => {
    const targets = items.filter(d => selected.has(d.id))
    const ids = targets.map(d => d.id)
    if (ids.length === 0) return
    if (!await confirm({ title: 'Remover downloads?', message: `Parar e remover ${ids.length} download(s) da lista? Sessões de streaming/transcode abertas pelo Play também são encerradas.`, confirmLabel: 'Remover', destructive: true })) return
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
        alert(`Falha ao remover ${failed.length} download(s) (#${failed.join(', #')}).`)
      }
      await load(); await loadTorrents()
      setSelected(new Set())
    } catch (err) {
      clearDeleted(pendingDeletesRef.current, ids)
      await load().catch(() => {})
      alert(`Erro ao remover downloads: ${errMessage(err)}`)
    }
  }

  const handleToggleSelectAll = () => {
    const next = selected.size === items.length ? new Set<number>() : new Set(items.map(d => d.id))
    setSelected(next)
  }
  const onPromoted = (result: { promoted: DownloadEntry[]; failed: { id: number; error: string }[] }) => {
    setPromoteTargets(null)
    if (result.failed.length > 0) {
      alert(`${result.promoted.length} promovido(s), ${result.failed.length} falha(s):\n` +
        result.failed.map(f => `#${f.id}: ${f.error}`).join('\n'))
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
    void load()
    void loadTorrents()
  }
  const onStopSeed = async (id: number, name: string) => {
    if (!await confirm({ title: 'Parar de seedar?', message: `Parar de seedar "${name}"? O arquivo permanece no lugar.`, confirmLabel: 'Parar', destructive: true })) return
    setBusyID(id)
    try { await downloadStopSeed(id); await load(); await loadTorrents() }
    finally { setBusyID(null) }
  }

  // ── Ações no nível do torrent (grupo de arquivos do mesmo infoHash) ──
  const onPromoteMany = (ds: DownloadEntry[]) => { if (ds.length > 0) setPromoteTargets(ds) }
  const onDeleteMany = async (ds: DownloadEntry[]) => {
    const ids = ds.map(d => d.id)
    if (ids.length === 0) return
    if (!await confirm({ title: 'Remover torrent?', message: `Remover ${ids.length} arquivo(s) deste torrent da lista? Os arquivos no disco NÃO são apagados.`, confirmLabel: 'Remover', destructive: true })) return
    setBulkBusy(true)
    await runBatchDelete(ids, ds)
    setBulkBusy(false)
  }
  const onStopSeedMany = async (ds: DownloadEntry[]) => {
    if (ds.length === 0) return
    if (!await confirm({ title: 'Parar de seedar?', message: `Parar de seedar ${ds.length} arquivo(s) deste torrent? Os arquivos permanecem no lugar.`, confirmLabel: 'Parar', destructive: true })) return
    setBulkBusy(true)
    try { await Promise.all(ds.map(d => downloadStopSeed(d.id).catch(() => {}))); await load(); await loadTorrents() }
    finally { setBulkBusy(false) }
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
    if (!await confirm({ title: 'Remover torrent?', message: 'Parar e remover este torrent da fila de streaming?', confirmLabel: 'Remover', destructive: true })) return
    setBusyHash(hash)
    try { await streamDrop(hash); await loadTorrents() } finally { setBusyHash(null) }
  }
  const onSaveLimits = async () => {
    setLimitsSaving(true); setLimitsMsg('')
    try {
      const down = limitDownKB.trim() === '' ? 0 : Math.max(0, Math.round(Number(limitDownKB) * 1024))
      const up = limitUpKB.trim() === '' ? 0 : Math.max(0, Math.round(Number(limitUpKB) * 1024))
      if (!Number.isFinite(down) || !Number.isFinite(up)) { setLimitsMsg('Valores inválidos'); return }
      await streamSetLimits({ down, up })
      setLimitsMsg('Limites aplicados')
      await loadLimits()
      globalThis.setTimeout(() => { if (mountedRef.current) setLimitsMsg('') }, 2500)
    } catch { setLimitsMsg('Falha ao salvar') } finally { setLimitsSaving(false) }
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
  const sortedItems = applyDownloadSort(items, sortCol, sortDir)

  // Per-status download groups
  const downloadsByStatus = {
    downloading: sortedItems.filter(d => d.status === 'downloading' || d.status === 'queued'),
    paused:      sortedItems.filter(d => d.status === 'paused'),
    completed:   sortedItems.filter(d => d.status === 'completed'),
    failed:      sortedItems.filter(d => d.status === 'failed'),
  }
  const completedDownloads = downloadsByStatus.completed

  // Ações em lote globais (reusadas pela barra inline do desktop e pelo Sheet
  // de "Ações" do mobile).
  const doResumeAll = async () => {
    setBulkBusy(true)
    try { await Promise.all([downloadResumeAll(), streamResumeAll()]); await load() }
    finally { setBulkBusy(false) }
  }
  const doPauseAll = async () => {
    setBulkBusy(true)
    try { await Promise.all([downloadPauseAll(), streamPauseAll()]); await load() }
    finally { setBulkBusy(false) }
  }
  const doRemoveCompleted = async () => {
    const ok = await confirm({
      title: 'Remover concluídos?',
      message: `Remover ${completedDownloads.length} download(s) concluído(s)? Os arquivos no disco NÃO serão apagados.`,
      confirmLabel: 'Remover',
      destructive: true,
    })
    if (!ok) return
    setBulkBusy(true)
    try {
      // Encerra sessões de seed/stream antes de remover as rows concluídas.
      await Promise.all(completedDownloads.map(d => d.infoHash ? streamDrop(d.infoHash).catch(() => {}) : Promise.resolve()))
      await downloadBatchDelete(completedDownloads.map(d => d.id)); await load(); await loadTorrents()
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
      await downloadBatchDelete(targets.map(d => d.id)); await load(); await loadTorrents()
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

  // Summary stats
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
  if (queuedCount > 0) queueSubtitle = `${queuedCount} na fila`
  else if (seedingCount > 0) queueSubtitle = `${seedingCount} semeando`
  const activePlural = activeCount === 1 ? '' : 's'
  const activeValue = maxActive > 0
    ? `${downloadingNowCount}/${maxActive} ativos`
    : `${activeCount} ativo${activePlural}`

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
      aria-label="Gerenciador de downloads — arraste arquivos .torrent ou links magnet"
      className="relative min-h-screen bg-surface"
    >
      {isDraggingPage && (
        <div className="fixed inset-0 z-50 bg-surface-elevated/80 backdrop-blur-md flex flex-col items-center justify-center border-4 border-dashed border-cyan-500/50 m-4 rounded-3xl pointer-events-none transition-all duration-300 animate-pulse">
          <UploadCloud className="w-16 h-16 text-cyan-400 mb-4 animate-bounce" />
          <h2 className="text-xl font-bold text-text-primary mb-1">Solte seus arquivos .torrent aqui!</h2>
          <p className="text-sm text-text-secondary">ou links magnet arrastados para iniciar o carregamento</p>
        </div>
      )}

      <NavHeader />
      <main className="max-w-5xl mx-auto px-4 py-6 flex flex-col gap-6">
        {/* ═══════════════ Summary Dashboard ═══════════════ */}
        <div className="grid grid-cols-2 sm:grid-cols-4 gap-3">
          <StatCard
            icon={<ArrowDownCircle className="w-5 h-5" />}
            label="Download"
            value={formatRate(totalDown)}
            gradient="from-emerald-500/20 to-teal-500/10"
            iconColor="text-emerald-400"
            pulse={totalDown > 0}
          />
          <StatCard
            icon={<ArrowUpCircle className="w-5 h-5" />}
            label="Upload"
            value={formatRate(totalUp)}
            gradient="from-violet-500/20 to-purple-500/10"
            iconColor="text-violet-400"
            pulse={totalUp > 0}
          />
          <StatCard
            icon={<Users className="w-5 h-5" />}
            label="Peers"
            value={String(totalPeers)}
            gradient="from-blue-500/20 to-cyan-500/10"
            iconColor="text-blue-400"
          />
          <StatCard
            icon={<Activity className="w-5 h-5" />}
            label="Fila"
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
                <span className="text-text-primary font-medium">{downloadsByStatus.downloading.length}</span> baixando
              </span>
            )}
            {downloadsByStatus.paused.length > 0 && (
              <span className="flex items-center gap-1">
                <Pause className="w-3.5 h-3.5 text-text-secondary" />
                <span className="text-text-primary font-medium">{downloadsByStatus.paused.length}</span> pausados
              </span>
            )}
            {completedDownloads.length > 0 && (
              <span className="flex items-center gap-1">
                <CheckCircle2 className="w-3.5 h-3.5 text-green-400" />
                <span className="text-text-primary font-medium">{completedDownloads.length}</span> concluídos
              </span>
            )}
            {downloadsByStatus.failed.length > 0 && (
              <span className="flex items-center gap-1">
                <AlertCircle className="w-3.5 h-3.5 text-red-400" />
                <span className="text-text-primary font-medium">{downloadsByStatus.failed.length}</span> com erro
              </span>
            )}
            {stalledCount > 0 && (
              <span className="flex items-center gap-1 text-amber-400">
                <AlertTriangle className="w-3.5 h-3.5" />
                <span className="font-medium">{stalledCount}</span> travados
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
                  title="Iniciar todos os downloads e torrents pausados"
                  className="flex items-center gap-1.5 text-xs bg-emerald-500/10 hover:bg-emerald-500/20 disabled:opacity-50 text-emerald-700 dark:text-emerald-300 border border-emerald-500/30 px-3 py-1.5 rounded-lg transition-colors"
                >
                  <Play className="w-3 h-3" /> Iniciar todos
                </button>
                <button
                  onClick={doPauseAll}
                  disabled={bulkBusy}
                  title="Pausar todos os downloads e torrents ativos"
                  className="flex items-center gap-1.5 text-xs bg-surface-secondary hover:bg-surface-tertiary disabled:opacity-50 text-text-primary border border-default px-3 py-1.5 rounded-lg transition-colors"
                >
                  <Pause className="w-3 h-3" /> Pausar todos
                </button>
                {completedDownloads.length > 0 && (
                  <button
                    onClick={doRemoveCompleted}
                    disabled={bulkBusy}
                    title="Remover da fila todos os downloads concluídos (arquivos não são apagados)"
                    className="flex items-center gap-1.5 text-xs bg-red-500/10 hover:bg-red-500/20 disabled:opacity-50 text-red-700 dark:text-red-300 border border-red-500/30 px-3 py-1.5 rounded-lg transition-colors"
                  >
                    <Trash2 className="w-3 h-3" /> Remover concluídos
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
                <MoreHorizontal className="w-4 h-4" /> Ações
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
                placeholder="Buscar por nome ou caminho..."
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
              Filtros
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
                  <option value="">Todos os status</option>
                  <option value="downloading">Baixando</option>
                  <option value="paused">Pausado</option>
                  <option value="queued">Na fila</option>
                  <option value="completed">Concluído</option>
                  <option value="failed">Falhou</option>
                </select>
                {availableTrackers.length > 0 && (
                  <select
                    value={filterTracker}
                    onChange={e => setFilterTracker(e.target.value)}
                    className="flex-1 min-w-[140px] sm:flex-none bg-surface border border-default rounded-lg px-3 py-1.5 text-xs text-text-primary focus:outline-none focus:border-cyan-500/50"
                  >
                    <option value="">Todos os trackers</option>
                    {availableTrackers.map(t => (
                      <option key={t} value={t}>{t}</option>
                    ))}
                  </select>
                )}
                {availableCategories.length > 0 && (
                  <select
                    value={filterCategory}
                    onChange={e => setFilterCategory(e.target.value)}
                    className="flex-1 min-w-[140px] sm:flex-none bg-surface border border-default rounded-lg px-3 py-1.5 text-xs text-text-primary focus:outline-none focus:border-cyan-500/50"
                  >
                    <option value="">Todas as categorias</option>
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
                    <option value="">Todos os usuários</option>
                    {availableUsers.map(u => (
                      <option key={u.id} value={String(u.id)}>{u.username}</option>
                    ))}
                  </select>
                )}
              </div>

              {/* Ordenar — grupo próprio, separado por divisória; Limpar à direita. */}
              <div className="flex items-center gap-2 flex-wrap border-t border-default/50 pt-3">
                <span className="text-xs text-text-muted flex items-center gap-1.5 flex-shrink-0">
                  <ArrowDownWideNarrow className="w-3.5 h-3.5" /> Ordenar
                </span>
                <select
                  value={sortCol}
                  onChange={e => setSortCol(e.target.value)}
                  className="flex-1 min-w-[140px] sm:flex-none bg-surface border border-default rounded-lg px-3 py-1.5 text-xs text-text-primary focus:outline-none focus:border-cyan-500/50"
                >
                  <option value="created_at">Data</option>
                  <option value="name">Nome</option>
                  <option value="size">Tamanho</option>
                  <option value="progress">Progresso</option>
                  <option value="downRate">Velocidade ↓</option>
                  <option value="upRate">Velocidade ↑</option>
                  <option value="seeders">Seeds</option>
                  <option value="status">Status</option>
                  <option value="tracker">Tracker</option>
                  <option value="category">Categoria</option>
                </select>
                <button
                  onClick={() => setSortDir(sortDir === 'asc' ? 'desc' : 'asc')}
                  title={sortDir === 'asc' ? 'Crescente' : 'Decrescente'}
                  aria-label="Inverter ordem"
                  className="flex-shrink-0 bg-surface border border-default rounded-lg px-2 py-1.5 text-text-primary hover:text-cyan-600 dark:hover:text-cyan-300 hover:border-cyan-500/40 transition-colors"
                >
                  {sortDir === 'asc' ? <ArrowUp className="w-3.5 h-3.5" /> : <ArrowDown className="w-3.5 h-3.5" />}
                </button>
                <button
                  onClick={() => setQuery({ status: null, tracker: null, cat: null, q: null, uid: null, sort: null, dir: null })}
                  className="ml-auto text-xs text-text-muted hover:text-text-primary px-2 py-1 flex-shrink-0"
                >
                  Limpar
                </button>
              </div>
            </div>
          )}
        </div>

        {/* ═══════════════ Tabs & Actions ═══════════════ */}
        <div className="flex items-center justify-between border-b border-default/60 flex-wrap gap-3">
          <div className="flex items-center gap-0.5 overflow-x-auto">
            {([
              { key: 'all'         as Tab, label: 'Todos',      icon: <ListFilter className="w-3.5 h-3.5" /> },
              { key: 'downloading' as Tab, label: 'Baixando',   icon: <Zap className="w-3.5 h-3.5" /> },
              { key: 'paused'      as Tab, label: 'Pausados',   icon: <Pause className="w-3.5 h-3.5" /> },
              { key: 'completed'   as Tab, label: 'Concluídos', icon: <CheckSquare className="w-3.5 h-3.5" /> },
              { key: 'failed'      as Tab, label: 'Erro',       icon: <AlertCircle className="w-3.5 h-3.5" /> },
              { key: 'network'     as Tab, label: 'Rede',       icon: <Wifi className="w-3.5 h-3.5" /> },
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
                {showAllUsers ? 'Todos usuários' : 'Meus downloads'}
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
                <Plus className="w-4 h-4" /> Adicionar Torrent / Magnet
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
        onAdopted={() => { void load() }}
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
          void load()
          void loadTorrents()
        }}
      />

      {/* Ações globais (mobile) */}
      <Sheet
        open={bulkSheetOpen}
        onClose={() => setBulkSheetOpen(false)}
        title="Ações"
        size="sm"
      >
        <div className="flex flex-col gap-2">
          <button
            onClick={() => { setBulkSheetOpen(false); void doResumeAll() }}
            disabled={bulkBusy}
            className="flex items-center gap-2 min-h-[48px] px-4 rounded-lg bg-emerald-500/10 text-emerald-700 dark:text-emerald-300 border border-emerald-500/30 disabled:opacity-50"
          >
            <Play className="w-4 h-4" /> Iniciar todos
          </button>
          <button
            onClick={() => { setBulkSheetOpen(false); void doPauseAll() }}
            disabled={bulkBusy}
            className="flex items-center gap-2 min-h-[48px] px-4 rounded-lg bg-surface-tertiary/60 text-text-primary border border-default disabled:opacity-50"
          >
            <Pause className="w-4 h-4" /> Pausar todos
          </button>
          {completedDownloads.length > 0 && (
            <button
              onClick={() => { setBulkSheetOpen(false); void doRemoveCompleted() }}
              disabled={bulkBusy}
              className="flex items-center gap-2 min-h-[48px] px-4 rounded-lg bg-red-500/10 text-red-700 dark:text-red-300 border border-red-500/30 disabled:opacity-50"
            >
              <Trash2 className="w-4 h-4" /> Remover concluídos ({completedDownloads.length})
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
          <span className="text-sm text-text-primary font-medium whitespace-nowrap">{selected.size} selecionado{selected.size === 1 ? '' : 's'}</span>
          <div className="w-px h-5 bg-surface-tertiary" />
          <button
            onClick={onBatchPause}
            disabled={bulkBusy}
            className="flex items-center gap-1 text-xs bg-surface-tertiary/60 hover:bg-surface-tertiary disabled:opacity-50 text-text-primary px-3 py-1 rounded-full transition-colors"
          >
            <Pause className="w-3 h-3" /> Pausar
          </button>
          <button
            onClick={onBatchResume}
            disabled={bulkBusy}
            className="flex items-center gap-1 text-xs bg-emerald-500/10 hover:bg-emerald-500/20 disabled:opacity-50 text-emerald-700 dark:text-emerald-300 px-3 py-1 rounded-full transition-colors"
          >
            <Play className="w-3 h-3" /> Retomar
          </button>
          <button
            onClick={onPromoteSelected}
            className="flex items-center gap-1 text-xs bg-cyan-500/20 hover:bg-cyan-500/30 text-cyan-700 dark:text-cyan-300 px-3 py-1 rounded-full transition-colors"
          >
            <ArrowUpCircle className="w-3 h-3" />
            Promover
          </button>
          <button
            onClick={onBatchDelete}
            disabled={bulkBusy}
            className="flex items-center gap-1 text-xs bg-red-500/10 hover:bg-red-500/20 disabled:opacity-50 text-red-700 dark:text-red-300 px-3 py-1 rounded-full transition-colors"
          >
            <Trash2 className="w-3 h-3" /> Remover
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
// StatCard — one cell in the top summary dashboard
// ═══════════════════════════════════════════════════════════════════════════════

function StatCard({ icon, label, value, subtitle, gradient, iconColor, pulse }: {
  readonly icon: React.ReactNode
  readonly label: string
  readonly value: string
  readonly subtitle?: string
  readonly gradient: string
  readonly iconColor: string
  readonly pulse?: boolean
}) {
  return (
    <div className={`
      relative overflow-hidden rounded-xl border border-default/50
      bg-gradient-to-br ${gradient} backdrop-blur-sm
      p-4 flex flex-col gap-1
    `}>
      <div className="flex items-center gap-2">
        <span className={`${iconColor} ${pulse ? 'animate-pulse' : ''}`}>{icon}</span>
        <span className="text-xs text-text-secondary uppercase tracking-wider font-medium">{label}</span>
      </div>
      <span className="text-xl font-bold text-text-primary tracking-tight">{value}</span>
      {subtitle && <span className="text-xs text-text-muted">{subtitle}</span>}
    </div>
  )
}

// ═══════════════════════════════════════════════════════════════════════════════
// ActiveTab — downloading/queued torrents + background downloads
// ═══════════════════════════════════════════════════════════════════════════════

function ActiveTab({ torrents, downloads, torrentsLoaded, loading, busyHash, busyID,
  onTorrentPause, onTorrentResume, onTorrentPriority, onTorrentDelete, onTorrentPlay,
  onPause, onResume, onDelete, onPlay, onInspect, openLocalFor,
}: {
  readonly torrents: TorrentInfo[]
  readonly downloads: DownloadEntry[]
  readonly torrentsLoaded: boolean
  readonly loading: boolean
  readonly busyHash: string | null
  readonly busyID: number | null
  readonly onTorrentPause: (h: string) => void
  readonly onTorrentResume: (h: string) => void
  readonly onTorrentPriority: (h: string, p: StreamPriority) => void
  readonly onTorrentDelete: (h: string) => void
  readonly onTorrentPlay: (t: TorrentInfo) => void
  readonly onPause: (id: number) => void
  readonly onResume: (id: number) => void
  readonly onDelete: (id: number) => void
  readonly onPlay: (d: DownloadEntry) => void
  readonly onInspect: (d: DownloadEntry) => void
  readonly openLocalFor: (d: DownloadEntry) => (() => void) | undefined
}) {
  const empty = torrents.length === 0 && downloads.length === 0 && torrentsLoaded && !loading
  const isLoading = (!torrentsLoaded || (loading && downloads.length === 0)) && torrents.length === 0 && downloads.length === 0

  return (
    <div className="flex flex-col gap-4">
      {isLoading && (
        <div className="flex items-center gap-2 text-text-secondary py-12 justify-center">
          <Loader2 className="w-5 h-5 animate-spin" />
          <span className="text-sm">Carregando...</span>
        </div>
      )}

      {empty && (
        <EmptyState
          icon={<Zap className="w-12 h-12" />}
          title="Nenhuma transferência ativa"
          description={'Inicie um streaming ou use o botão "Baixar no Servidor" no player para enfileirar downloads.'}
        />
      )}

      {/* Streaming torrents */}
      {torrents.map(t => (
        <TorrentCard
          key={t.infoHash}
          t={t}
          busy={busyHash === t.infoHash}
          onPause={() => onTorrentPause(t.infoHash)}
          onResume={() => onTorrentResume(t.infoHash)}
          onPriority={(p) => onTorrentPriority(t.infoHash, p)}
          onDelete={() => onTorrentDelete(t.infoHash)}
          onPlay={() => onTorrentPlay(t)}
        />
      ))}

      {/* Background downloads */}
      {downloads.map(d => (
        <DownloadCard
          key={d.id}
          d={d}
          live={torrents.find(t => t.infoHash === d.infoHash)}
          busy={busyID === d.id}
          onPause={() => onPause(d.id)}
          onResume={() => onResume(d.id)}
          onDelete={() => onDelete(d.id)}
          onPlay={() => onPlay(d)}
          onInspect={() => onInspect(d)}
          onOpenLocal={openLocalFor(d)}
        />
      ))}
    </div>
  )
}

// CompletedGroup bundles all files of one torrent (same infoHash). The name is
// kept generic ("Group") since groupByHash now feeds every lifecycle section
// (downloading/queued/paused), not just completed.
type CompletedGroup = {
  key: string
  name: string
  files: DownloadEntry[]
  seeding: boolean
}

// Ordena arquivos do MESMO torrent em ordem natural (numérica) pelo caminho, pra
// que episódios fiquem S01E01, S01E02, … S01E10 em vez de ordem alfabética crua
// ou de chegada. A ordem ENTRE grupos segue o sort global (ordem de chegada da
// lista já ordenada pelo backend).
function naturalFileCompare(a: DownloadEntry, b: DownloadEntry): number {
  return (a.filePath || a.name).localeCompare(b.filePath || b.name, undefined, { numeric: true, sensitivity: 'base' })
}

// countTorrents counts DISTINCT torrents in a set of download rows. A multi-file
// torrent is N rows (one per file) but ONE torrent — the badges/counters must
// count torrents, not files, so a 778-file pack reads as 1 (matching the grouped
// card view). Hashless rows (pre-metadata) count individually, mirroring the
// backend grpKey id-fallback.
export function countTorrents(rows: readonly DownloadEntry[]): number {
  const seen = new Set<string>()
  for (const d of rows) seen.add(d.infoHash || `id:${d.id}`)
  return seen.size
}

// groupByHash groups downloads by infoHash, preserving first-seen order, with no
// status filter — so it works for ANY lifecycle section (Baixando/Fila/Pausados/
// Concluídos). A single-file torrent and a whole-torrent (-2) item each land in a
// group of one (rendered as a plain card, no wrapper). `seeding` is true when the
// torrent is still live in the streamer. Pure + exported for unit tests.
export function groupByHash(items: readonly DownloadEntry[], torrents: readonly TorrentInfo[]): CompletedGroup[] {
  const byKey = new Map<string, CompletedGroup>()
  const order: string[] = []
  for (const d of items) {
    const key = d.infoHash || `id:${d.id}`
    let g = byKey.get(key)
    if (!g) {
      g = { key, name: d.name || d.filePath, files: [], seeding: !!d.infoHash && torrents.some(t => t.infoHash === d.infoHash) }
      byKey.set(key, g)
      order.push(key)
    }
    g.files.push(d)
  }
  // Episódios em ordem dentro de cada torrent multi-arquivo.
  for (const g of byKey.values()) {
    if (g.files.length > 1) g.files.sort(naturalFileCompare)
  }
  return order.map(k => byKey.get(k) as CompletedGroup)
}

// groupCompleted is the completed-only view kept for completedViewCounts: same
// grouping, the caller pre-filters to status==='completed'.
function groupCompleted(items: readonly DownloadEntry[], torrents: readonly TorrentInfo[]): CompletedGroup[] {
  return groupByHash(items, torrents)
}

// groupProgress aggregates a multi-file group's progress: sum(bytes_downloaded) /
// sum(file_size), clamped to [0,1]. A group with no known sizes reports 0.
function groupProgress(g: CompletedGroup): { downloaded: number; total: number; pct: number } {
  let downloaded = 0
  let total = 0
  for (const f of g.files) {
    downloaded += f.bytesDownloaded || 0
    total += f.fileSize || 0
  }
  const pct = total > 0 ? Math.max(0, Math.min(1, downloaded / total)) * 100 : 0
  return { downloaded, total, pct }
}

// completedViewCounts derives the chip counts for the completed view: "seeding"
// = live torrents not yet on a completed row + completed groups still seeding;
// "onDisk" = completed groups whose torrent is no longer live. Pure + exported so
// the filter-chip behaviour is unit-testable without rendering the page.
export function completedViewCounts(
  downloads: readonly DownloadEntry[],
  torrents: readonly TorrentInfo[],
): { seeding: number; onDisk: number } {
  const completed = downloads.filter(d => d.status === 'completed')
  const groups = groupCompleted(completed, torrents)
  const streamingOnly = torrents.filter(t => !completed.some(d => d.infoHash === t.infoHash))
  return {
    seeding: streamingOnly.length + groups.filter(g => g.seeding).length,
    onDisk: groups.filter(g => !g.seeding).length,
  }
}

// CompletedGroupActions — the promote/stop-seed/remove-all controls for a
// completed (or seeding) multi-file group. Extracted so DownloadGroupCard stays
// presentation-only and each lifecycle section supplies the right action set.
function CompletedGroupActions({ onPromote, onStopSeed, onDelete, busy }: {
  readonly onPromote: () => void
  readonly onStopSeed?: () => void
  readonly onDelete: () => void
  readonly busy: boolean
}) {
  return (
    <>
      <button onClick={onPromote} disabled={busy} title="Promover todos" className="p-1.5 rounded-lg text-cyan-400 hover:bg-cyan-500/10 disabled:opacity-50">
        <ArrowUpCircle className="w-4 h-4" />
      </button>
      {onStopSeed && (
        <button onClick={onStopSeed} disabled={busy} title="Parar de seedar todos" className="p-1.5 rounded-lg text-text-secondary hover:bg-surface-tertiary disabled:opacity-50">
          <Pause className="w-4 h-4" />
        </button>
      )}
      <button onClick={onDelete} disabled={busy} title="Remover torrent da lista" className="p-1.5 rounded-lg text-red-400 hover:bg-red-500/10 disabled:opacity-50">
        <Trash2 className="w-4 h-4" />
      </button>
    </>
  )
}

// ActiveGroupActions — torrent-level pause/resume + remove-all for an in-progress
// multi-file group (Baixando/Fila/Pausados). Pause/resume act on the whole
// torrent by infoHash; remove drops every file row.
function ActiveGroupActions({ paused, onPause, onResume, onDelete, busy }: {
  readonly paused: boolean
  readonly onPause: () => void
  readonly onResume: () => void
  readonly onDelete: () => void
  readonly busy: boolean
}) {
  return (
    <>
      {paused ? (
        <button onClick={onResume} disabled={busy} title="Retomar torrent" className="p-1.5 rounded-lg text-emerald-400 hover:bg-emerald-500/10 disabled:opacity-50">
          <Play className="w-4 h-4" />
        </button>
      ) : (
        <button onClick={onPause} disabled={busy} title="Pausar torrent" className="p-1.5 rounded-lg text-text-secondary hover:bg-surface-tertiary disabled:opacity-50">
          <Pause className="w-4 h-4" />
        </button>
      )}
      <button onClick={onDelete} disabled={busy} title="Remover torrent da lista" className="p-1.5 rounded-lg text-red-400 hover:bg-red-500/10 disabled:opacity-50">
        <Trash2 className="w-4 h-4" />
      </button>
    </>
  )
}

// DownloadGroupCard — collapsible header for a multi-file torrent (2+ files), with
// an aggregate progress bar and a slot for torrent-level actions. Single-file and
// whole-torrent groups render their lone card directly (no wrapper). Children are
// the per-file DownloadCards (shown when expanded).
function DownloadGroupCard({
  group, expanded, onToggle, actions, children,
}: {
  readonly group: CompletedGroup
  readonly expanded: boolean
  readonly onToggle: () => void
  readonly actions: React.ReactNode
  readonly children: React.ReactNode
}) {
  const prog = groupProgress(group)
  const showBar = !group.files.every(f => f.status === 'completed')
  return (
    <div className="rounded-xl border border-default/50 bg-surface-secondary/40 overflow-hidden">
      <div className="flex items-center gap-2 p-3">
        <button onClick={onToggle} className="flex items-center gap-2 min-w-0 flex-1 text-left">
          {expanded ? <ChevronDown className="w-4 h-4 flex-shrink-0 text-text-secondary" /> : <ChevronRight className="w-4 h-4 flex-shrink-0 text-text-secondary" />}
          <Folder className="w-4 h-4 flex-shrink-0 text-emerald-400" />
          <span className="font-semibold text-text-primary text-sm truncate" title={group.name}>{group.name}</span>
          <span className="text-[11px] text-text-muted flex-shrink-0">{group.files.length} arquivos</span>
          {group.seeding && (
            <span className="inline-flex items-center gap-1 text-[10px] px-1.5 py-0.5 rounded-md border font-medium bg-emerald-500/15 text-emerald-700 dark:text-emerald-300 border-emerald-500/30 flex-shrink-0">
              <ArrowUpCircle className="w-3 h-3" />Semeando
            </span>
          )}
        </button>
        <div className="flex items-center gap-1.5 flex-shrink-0">{actions}</div>
      </div>
      {showBar && (
        <div className="px-3 pb-2 -mt-1">
          <div className="h-1.5 rounded-full bg-surface-tertiary/60 overflow-hidden">
            <div className="h-full rounded-full bg-gradient-to-r from-cyan-500 to-blue-500 transition-all" style={{ width: `${prog.pct}%` }} />
          </div>
          <div className="text-[10px] text-text-muted mt-1">
            {formatBytesPair(prog.downloaded, prog.total)} · {Math.round(prog.pct)}%
          </div>
        </div>
      )}
      {expanded && <div className="flex flex-col gap-2 px-3 pb-3 pl-6 border-l-2 border-default/50 ml-3">{children}</div>}
    </div>
  )
}

// GroupHeader — small section label above a download group (Baixando/Na fila/…).
type CompletedFilterKey = 'all' | 'seeding' | 'ondisk'

// Filtro da aba de concluídos: ver tudo, só o que está semeando ao vivo, ou só
// o que está parado no disco. Top-level para evitar componente-no-pai (S6478).
function CompletedFilterChips({ value, onChange, seedingN, onDiskN }: {
  readonly value: CompletedFilterKey
  readonly onChange: (v: CompletedFilterKey) => void
  readonly seedingN: number
  readonly onDiskN: number
}) {
  const opts: { key: CompletedFilterKey; label: string }[] = [
    { key: 'all', label: 'Todos' },
    { key: 'seeding', label: `Semeando (${seedingN})` },
    { key: 'ondisk', label: `No disco (${onDiskN})` },
  ]
  return (
    <div className="flex items-center gap-1.5 flex-wrap">
      {opts.map(o => (
        <button
          key={o.key}
          onClick={() => onChange(o.key)}
          className={`text-xs px-3 py-1.5 rounded-lg border transition-colors ${value === o.key
            ? 'bg-emerald-500/20 text-emerald-700 dark:text-emerald-300 border-emerald-500/40'
            : 'bg-surface-secondary text-text-secondary border-default hover:text-text-primary'}`}
        >
          {o.label}
        </button>
      ))}
    </div>
  )
}

function GroupHeader({ icon, label, color }: { readonly icon: React.ReactNode; readonly label: string; readonly color: string }) {
  return (
    <div className={`flex items-center gap-2 text-xs font-medium uppercase tracking-wider px-1 ${color}`}>
      {icon}{label}
    </div>
  )
}

// ═══════════════════════════════════════════════════════════════════════════════
// SeedingTab — seeding/complete torrents + completed downloads
// Sub-divided by lifecycle: Baixando agora / Na fila / Semeando / No disco / Pausados.
// ═══════════════════════════════════════════════════════════════════════════════

function SeedingTab({ torrents, downloads, completedFilter, torrentsLoaded, busyHash, busyID,
  onTorrentPause, onTorrentResume, onTorrentPriority, onTorrentDelete, onTorrentPlay,
  onPause, onResume, onDelete, onPromote, onStopSeed, onSetPriority,
  onPromoteMany, onDeleteMany, onStopSeedMany,
  selected, onToggleSelected, onPlay, onInspect, openLocalFor, loading,
}: {
  readonly torrents: TorrentInfo[]
  readonly downloads: DownloadEntry[]
  readonly completedFilter: CompletedFilterKey
  readonly torrentsLoaded: boolean
  readonly busyHash: string | null
  readonly busyID: number | null
  readonly onTorrentPause: (h: string) => void
  readonly onTorrentResume: (h: string) => void
  readonly onTorrentPriority: (h: string, p: StreamPriority) => void
  readonly onTorrentDelete: (h: string) => void
  readonly onTorrentPlay: (t: TorrentInfo) => void
  readonly onPause: (id: number) => void
  readonly onResume: (id: number) => void
  readonly onDelete: (id: number) => void
  readonly onPromote: (d: DownloadEntry) => void
  readonly onStopSeed: (id: number, name: string) => void
  readonly onPromoteMany: (ds: DownloadEntry[]) => void
  readonly onDeleteMany: (ds: DownloadEntry[]) => void
  readonly onStopSeedMany: (ds: DownloadEntry[]) => void
  readonly onSetPriority: (id: number, p: DownloadPriority) => void
  readonly selected: Set<number>
  readonly onToggleSelected: (id: number) => void
  readonly onPlay: (d: DownloadEntry) => void
  readonly onInspect: (d: DownloadEntry) => void
  readonly openLocalFor: (d: DownloadEntry) => (() => void) | undefined
  readonly loading?: boolean
}) {
  const [expandedGroups, setExpandedGroups] = useState<Set<string>>(new Set())
  const toggleGroup = (key: string) => setExpandedGroups(prev => {
    const next = new Set(prev)
    if (next.has(key)) next.delete(key); else next.add(key)
    return next
  })
  const empty = torrents.length === 0 && downloads.length === 0 && !loading

  // Quantos downloads compartilham cada infoHash → distingue torrent multi-arquivo
  // (>1) de single-file. Vale pra TODAS as seções (baixando/fila/concluídos/…),
  // então o arquivo "que ficou sem baixar" também aparece com o nome do episódio.
  const dlCountByHash = new Map<string, number>()
  for (const d of downloads) {
    if (d.infoHash) dlCountByHash.set(d.infoHash, (dlCountByHash.get(d.infoHash) ?? 0) + 1)
  }

  const renderDownloadCard = (d: DownloadEntry) => (
    <DownloadCard
      key={d.id}
      d={d}
      live={torrents.find(t => t.infoHash === d.infoHash)}
      busy={busyID === d.id}
      selected={selected.has(d.id)}
      multiFile={!!d.infoHash && (dlCountByHash.get(d.infoHash) ?? 0) > 1}
      onToggleSelected={() => onToggleSelected(d.id)}
      onPause={() => onPause?.(d.id)}
      onResume={() => onResume?.(d.id)}
      onDelete={() => onDelete(d.id)}
      onPromote={() => onPromote(d)}
      onStopSeed={() => onStopSeed(d.id, d.name || d.filePath)}
      onPlay={() => onPlay(d)}
      onInspect={() => onInspect(d)}
      onSetPriority={(p) => onSetPriority(d.id, p)}
      onOpenLocal={openLocalFor(d)}
    />
  )

  // groupShell wraps a multi-file group in the collapsible card; a group of one
  // (single-file OR whole-torrent -2) renders its lone card with no wrapper, so
  // the torrent stays the unit without doubling chrome.
  const groupShell = (g: CompletedGroup, actions: React.ReactNode) => {
    if (g.files.length === 1) return renderDownloadCard(g.files[0])
    return (
      <DownloadGroupCard
        key={g.key}
        group={g}
        expanded={expandedGroups.has(g.key)}
        onToggle={() => toggleGroup(g.key)}
        actions={actions}
      >
        {g.files.map(renderDownloadCard)}
      </DownloadGroupCard>
    )
  }

  // Completed/seeding group: promote / stop-seed (only while live) / remove all.
  const renderCompletedGroup = (g: CompletedGroup) => groupShell(g, (
    <CompletedGroupActions
      onPromote={() => onPromoteMany(g.files)}
      onStopSeed={g.seeding ? () => onStopSeedMany(g.files) : undefined}
      onDelete={() => onDeleteMany(g.files)}
      busy={g.files.some(f => busyID === f.id)}
    />
  ))

  // In-progress group (Baixando/Fila/Pausados): pause/resume the whole torrent and
  // remove every file. Pause/resume fan out to the per-file download rows (the unit
  // the backend aggregates by (user, info_hash)), so the group's status reflects in
  // each row. A group is "paused" when ALL its files are.
  const renderActiveGroup = (g: CompletedGroup) => {
    const allPaused = g.files.every(f => f.status === 'paused')
    return groupShell(g, (
      <ActiveGroupActions
        paused={allPaused}
        onPause={() => g.files.forEach(f => onPause(f.id))}
        onResume={() => g.files.forEach(f => onResume(f.id))}
        onDelete={() => onDeleteMany(g.files)}
        busy={g.files.some(f => busyID === f.id)}
      />
    ))
  }

  // Group downloads by lifecycle so the list is legible. Completed files are
  // grouped per torrent (infoHash) and split into Seeding (live) vs On-disk.
  // Streaming-only torrents (no download row) keep their TorrentCard. Headers
  // show only when more than one group is non-empty.
  // Each lifecycle section is grouped per torrent (infoHash): a multi-file torrent
  // is ONE card, a single-file / whole-torrent (-2) item a card of one. Counts and
  // headers count GROUPS (torrents), not file rows.
  const downloadingGroups = groupByHash(downloads.filter(d => d.status === 'downloading'), torrents)
  const queuedGroups = groupByHash(downloads.filter(d => d.status === 'queued'), torrents)
  const otherGroups = groupByHash(downloads.filter(d => d.status === 'paused' || d.status === 'failed'), torrents)
  const completed = downloads.filter(d => d.status === 'completed')
  const completedGroups = groupCompleted(completed, torrents)
  const seedingGroups = completedGroups.filter(g => g.seeding)
  const onDiskGroups = completedGroups.filter(g => !g.seeding)
  // Streaming torrents that aren't backed by a completed download row.
  const streamingOnly = torrents.filter(t => !completed.some(d => d.infoHash === t.infoHash))
  const seedingCount = streamingOnly.length + seedingGroups.length

  const sectionCount = [downloadingGroups.length, queuedGroups.length, seedingCount, onDiskGroups.length, otherGroups.length]
    .filter(n => n > 0).length
  const showHeaders = sectionCount > 1
  // Filtro Semeando/No disco: 'seeding' isola os que estão semeando ao vivo,
  // 'ondisk' os parados no disco, 'all' mostra tudo (incluindo baixando/fila/pausados).
  const hasCompleted = seedingCount > 0 || onDiskGroups.length > 0
  // Sem concluídos, o filtro não se aplica (não esconde baixando/fila/pausados).
  const cf = hasCompleted ? completedFilter : 'all'
  const showSeeding = cf !== 'ondisk'
  const showOnDisk = cf !== 'seeding'
  const showOthers = cf === 'all'

  return (
    <div className="flex flex-col gap-4">
      {!torrentsLoaded && (
        <div className="flex items-center gap-2 text-text-secondary py-12 justify-center">
          <Loader2 className="w-5 h-5 animate-spin" />
          <span className="text-sm">Carregando...</span>
        </div>
      )}

      {torrentsLoaded && empty && (
        <EmptyState
          icon={<ArrowUpCircle className="w-12 h-12" />}
          title="Nada semeando ou completo"
          description="Torrents concluídos e em seed aparecerão aqui."
        />
      )}

      {/* Baixando agora */}
      {showOthers && downloadingGroups.length > 0 && (
        <>
          {showHeaders && <GroupHeader icon={<Loader2 className="w-3.5 h-3.5" />} label={`Baixando agora (${downloadingGroups.length})`} color="text-cyan-400" />}
          {downloadingGroups.map(renderActiveGroup)}
        </>
      )}

      {/* Na fila */}
      {showOthers && queuedGroups.length > 0 && (
        <>
          {showHeaders && <GroupHeader icon={<Clock className="w-3.5 h-3.5" />} label={`Na fila (${queuedGroups.length})`} color="text-text-muted" />}
          {queuedGroups.map(renderActiveGroup)}
        </>
      )}

      {/* Semeando (torrents de streaming + grupos completed com torrent live) */}
      {showSeeding && seedingCount > 0 && (
        <>
          {showHeaders && <GroupHeader icon={<ArrowUpCircle className="w-3.5 h-3.5" />} label="Semeando" color="text-emerald-400" />}
          {streamingOnly.map(t => (
            <TorrentCard
              key={t.infoHash}
              t={t}
              busy={busyHash === t.infoHash}
              onPause={() => onTorrentPause(t.infoHash)}
              onResume={() => onTorrentResume(t.infoHash)}
              onPriority={(p) => onTorrentPriority(t.infoHash, p)}
              onDelete={() => onTorrentDelete(t.infoHash)}
              onPlay={() => onTorrentPlay(t)}
            />
          ))}
          {seedingGroups.map(renderCompletedGroup)}
        </>
      )}

      {/* No disco (concluído, seed parado) */}
      {showOnDisk && onDiskGroups.length > 0 && (
        <>
          {showHeaders && <GroupHeader icon={<HardDrive className="w-3.5 h-3.5" />} label="No disco" color="text-text-muted" />}
          {onDiskGroups.map(renderCompletedGroup)}
        </>
      )}

      {/* Pausados / falhos */}
      {showOthers && otherGroups.length > 0 && (
        <>
          {showHeaders && <GroupHeader icon={<Pause className="w-3.5 h-3.5" />} label={`Pausados / erro (${otherGroups.length})`} color="text-text-muted" />}
          {otherGroups.map(renderActiveGroup)}
        </>
      )}
    </div>
  )
}

// ═══════════════════════════════════════════════════════════════════════════════
// NetworkTab — bandwidth limit controls
// ═══════════════════════════════════════════════════════════════════════════════

function NetworkTab({ limitDownKB, limitUpKB, setLimitDownKB, setLimitUpKB,
  limitsSaving, limitsMsg, onSaveLimits, totalDown, totalUp, totalPeers,
}: {
  readonly limitDownKB: string
  readonly limitUpKB: string
  readonly setLimitDownKB: (v: string) => void
  readonly setLimitUpKB: (v: string) => void
  readonly limitsSaving: boolean
  readonly limitsMsg: string
  readonly onSaveLimits: () => void
  readonly totalDown: number
  readonly totalUp: number
  readonly totalPeers: number
}) {
  return (
    <div className="flex flex-col gap-6">
      {/* Live network overview */}
      <div className="rounded-xl border border-default/50 bg-card dark:bg-gradient-to-br dark:from-gray-800/80 dark:to-gray-900/80 backdrop-blur-sm p-6">
        <h3 className="text-sm font-semibold text-text-primary uppercase tracking-wider flex items-center gap-2 mb-5">
          <Wifi className="w-4 h-4 text-cyan-400" />
          Monitoramento em Tempo Real
        </h3>
        <div className="grid grid-cols-1 sm:grid-cols-3 gap-6">
          <div className="flex flex-col gap-1">
            <span className="text-xs text-text-muted">Download atual</span>
            <span className="text-2xl font-bold text-emerald-400">{formatRate(totalDown)}</span>
          </div>
          <div className="flex flex-col gap-1">
            <span className="text-xs text-text-muted">Upload atual</span>
            <span className="text-2xl font-bold text-violet-400">{formatRate(totalUp)}</span>
          </div>
          <div className="flex flex-col gap-1">
            <span className="text-xs text-text-muted">Peers conectados</span>
            <span className="text-2xl font-bold text-blue-400">{totalPeers}</span>
          </div>
        </div>
      </div>

      {/* Bandwidth limits form */}
      <div className="rounded-xl border border-default/50 bg-card dark:bg-gradient-to-br dark:from-gray-800/60 dark:to-gray-900/60 backdrop-blur-sm p-6">
        <h3 className="text-sm font-semibold text-text-primary uppercase tracking-wider flex items-center gap-2 mb-5">
          <Gauge className="w-4 h-4 text-amber-400" />
          Limites de Velocidade
        </h3>
        <p className="text-xs text-text-muted mb-4">
          Defina limites em KB/s. Deixe em branco ou 0 para ilimitado.
        </p>
        <div className="grid grid-cols-1 sm:grid-cols-2 gap-4 mb-5">
          <div className="flex flex-col gap-2">
            <label className="text-xs text-text-secondary flex items-center gap-1.5">
              <ArrowDownCircle className="w-3.5 h-3.5 text-emerald-400" />
              Limite de download (KB/s)
            </label>
            <input
              type="number"
              min={0}
              placeholder="Ilimitado"
              value={limitDownKB}
              onChange={e => setLimitDownKB(e.target.value)}
              className="bg-surface/80 border border-default rounded-lg px-3 py-2.5 text-text-primary text-sm focus:outline-none focus:border-emerald-500 focus:ring-1 focus:ring-emerald-500/30 transition-all"
            />
          </div>
          <div className="flex flex-col gap-2">
            <label className="text-xs text-text-secondary flex items-center gap-1.5">
              <ArrowUpCircle className="w-3.5 h-3.5 text-violet-400" />
              Limite de upload (KB/s)
            </label>
            <input
              type="number"
              min={0}
              placeholder="Ilimitado"
              value={limitUpKB}
              onChange={e => setLimitUpKB(e.target.value)}
              className="bg-surface/80 border border-default rounded-lg px-3 py-2.5 text-text-primary text-sm focus:outline-none focus:border-violet-500 focus:ring-1 focus:ring-violet-500/30 transition-all"
            />
          </div>
        </div>
        <div className="flex items-center gap-3">
          <button
            onClick={onSaveLimits}
            disabled={limitsSaving}
            className="flex items-center gap-2 text-sm bg-emerald-500/20 hover:bg-emerald-500/30 disabled:opacity-50 text-emerald-700 dark:text-emerald-300 border border-emerald-500/40 px-5 py-2 rounded-lg transition-all font-medium"
          >
            {limitsSaving && <Loader2 className="w-4 h-4 animate-spin" />}
            Aplicar limites
          </button>
          {limitsMsg && (
            <span className={`text-sm font-medium ${limitsMsg.includes('aplicados') ? 'text-emerald-400' : 'text-red-400'}`}>
              {limitsMsg}
            </span>
          )}
        </div>
      </div>
    </div>
  )
}

// ═══════════════════════════════════════════════════════════════════════════════
// EmptyState
// ═══════════════════════════════════════════════════════════════════════════════

function EmptyState({ icon, title, description }: { readonly icon: React.ReactNode; readonly title: string; readonly description: string }) {
  return (
    <div className="flex flex-col items-center justify-center py-16 text-center">
      <div className="text-text-muted mb-3">{icon}</div>
      <h3 className="text-lg font-semibold text-text-secondary mb-1">{title}</h3>
      <p className="text-sm text-text-muted max-w-md">{description}</p>
    </div>
  )
}

// ═══════════════════════════════════════════════════════════════════════════════
// TorrentCard — Premium redesigned streaming torrent card
// ═══════════════════════════════════════════════════════════════════════════════

type TorrentCardProps = {
  readonly t: TorrentInfo
  readonly busy: boolean
  readonly onPause: () => void
  readonly onResume: () => void
  readonly onPriority: (p: StreamPriority) => void
  readonly onDelete: () => void
  readonly onPlay?: () => void
}

function TorrentCard({ t, busy, onPause, onResume, onPriority, onDelete, onPlay }: TorrentCardProps) {
  const pct = Math.max(0, Math.min(1, t.progress || 0)) * 100
  const status = t.status || (pct >= 100 ? 'complete' : 'downloading')
  const isPaused = status === 'paused'
  const isSeeding = status === 'seeding'
  const isComplete = status === 'complete'

  const eta = computeTorrentETA(t)
  const priority: StreamPriority = (t.priority as StreamPriority) || 'normal'

  // Card border/glow color based on state
  let borderClass: string
  if (isSeeding) {
    borderClass = 'border-violet-500/30 hover:border-violet-500/50'
  } else if (isPaused) {
    borderClass = 'border-strong/50 hover:border-strong/60'
  } else if (isComplete) {
    borderClass = 'border-green-500/30 hover:border-green-500/50'
  } else {
    borderClass = 'border-emerald-500/30 hover:border-emerald-500/50'
  }

  // Gradient bar colors
  let barGradient: string
  if (isComplete) {
    barGradient = 'from-green-500 to-emerald-400'
  } else if (isPaused) {
    barGradient = 'from-gray-600 to-gray-500'
  } else if (isSeeding) {
    barGradient = 'from-violet-500 to-indigo-400'
  } else {
    barGradient = 'from-emerald-500 to-teal-400'
  }

  return (
    <div className={`
      relative overflow-hidden rounded-xl border ${borderClass}
      bg-card dark:bg-gradient-to-br dark:from-gray-800/80 dark:to-gray-900/60 backdrop-blur-sm
      p-4 flex flex-col gap-3 transition-all duration-300
    `}>
      {/* Top row: name + badges */}
      <div className="min-w-0">
        <h3 className="font-semibold text-text-primary text-sm leading-snug [overflow-wrap:anywhere]" title={t.name}>
          {t.name || t.infoHash}
        </h3>
        <div className="flex items-center gap-1.5 mt-1.5 flex-wrap">
          <KindBadge kind="streaming" />
          <TorrentStatusBadge status={status} />
        </div>
        <p className="text-[11px] text-text-muted truncate mt-0.5 font-mono" title={t.infoHash}>{t.infoHash}</p>
      </div>

      {/* Live rate chips — destaque pro Down/Up/Peers de CADA torrent. Eram
          mostrados em text-xs no rodapé, passavam batido (sintoma "não sei a
          velocidade de cada um"). Agora ficam em fonte maior + chip dedicado.
          Quando tudo zerado (ex.: pausado), só os peers ainda aparecem. */}
      <div className="flex items-center gap-2 flex-wrap text-sm">
        <span
          className={`flex items-center gap-1 px-2 py-0.5 rounded-full font-mono tabular-nums ${
            t.downRate > 0 ? 'bg-emerald-500/15 text-emerald-700 dark:text-emerald-300 border border-emerald-500/30' : 'text-text-muted'
          }`}
          title="Velocidade de download deste torrent"
        >
          <ArrowDownCircle className="w-3.5 h-3.5" />
          {formatRate(t.downRate)}
        </span>
        <span
          className={`flex items-center gap-1 px-2 py-0.5 rounded-full font-mono tabular-nums ${
            t.upRate > 0 ? 'bg-violet-500/15 text-violet-700 dark:text-violet-300 border border-violet-500/30' : 'text-text-muted'
          }`}
          title="Velocidade de upload deste torrent"
        >
          <ArrowUpCircle className="w-3.5 h-3.5" />
          {formatRate(t.upRate)}
        </span>
        <span
          className="flex items-center gap-1 px-2 py-0.5 rounded-full bg-blue-500/10 text-blue-700 dark:text-blue-300 border border-blue-500/20 font-mono tabular-nums"
          title="Peers conectados / Seeders no swarm"
        >
          <Users className="w-3.5 h-3.5" />
          {t.peers}{(t.seeders ?? 0) > 0 && <span className="text-text-muted"> / {t.seeders}</span>}
        </span>
      </div>

      {/* Progress bar */}
      <div>
        <div className="h-2 bg-surface-tertiary dark:bg-surface/80 rounded-full overflow-hidden">
          <div
            className={`h-full rounded-full bg-gradient-to-r ${barGradient} transition-all duration-500 ease-out`}
            style={{ width: `${pct.toFixed(1)}%` }}
          />
        </div>
        {/* Stats row — bytes/% + ETA. Velocidades subiram para os chips acima. */}
        <div className="flex items-center justify-between mt-2 text-xs text-text-secondary gap-3 flex-wrap">
          <span className="text-text-primary font-medium">
            {formatBytesPair(Math.round((t.totalSize || 0) * (t.progress || 0)), t.totalSize)}
            <span className="text-text-muted ml-1">({pct.toFixed(1)}%)</span>
          </span>
          {eta && (
            <span className="flex items-center gap-1 text-text-muted" title="ETA">
              <Clock className="w-3 h-3" /> {eta}
            </span>
          )}
        </div>
      </div>

      {/* Action bar */}
      <div className="flex items-center gap-2 flex-wrap pt-1">
        {/* Ver arquivos / tocar: abre o player pelo info_hash (resolve o arquivo
            principal e lista os demais). Disponível assim que há algo no cache —
            inclusive quando completo/semeando, que antes só tinha pausar/parar. */}
        {onPlay && t.progress > 0 && (
          <ActionButton onClick={onPlay} disabled={busy} variant="success" icon={<Play className="w-3.5 h-3.5 fill-current" />} label="Ver arquivos" title="Abre o player: toca o arquivo principal e lista os demais do torrent" />
        )}
        {isPaused ? (
          <ActionButton onClick={onResume} disabled={busy} variant="success" icon={<Play className="w-3.5 h-3.5" />} label="Retomar" title="Retoma o torrent de onde parou" />
        ) : (
          <ActionButton onClick={onPause} disabled={busy || isComplete} variant="neutral" icon={<Pause className="w-3.5 h-3.5" />} label="Pausar" title="Pausa o torrent (retomável; o que já baixou fica no cache)" />
        )}

        <label className="flex items-center gap-1.5 text-xs text-text-secondary">
          <span className="text-text-muted">Prioridade:</span>
          <select
            value={priority}
            onChange={e => onPriority(e.target.value as StreamPriority)}
            disabled={busy}
            className="bg-surface-secondary border border-default rounded-lg px-2 py-1 text-text-primary text-xs disabled:opacity-50 focus:outline-none focus:border-emerald-500 transition-colors cursor-pointer"
          >
            <option value="low">Baixa</option>
            <option value="normal">Normal</option>
            <option value="high">Alta</option>
          </select>
        </label>

        {busy && <Loader2 className="w-3.5 h-3.5 animate-spin text-text-muted" />}
        <ActionButton onClick={onDelete} disabled={busy} variant="danger" icon={<Trash2 className="w-3.5 h-3.5" />} label="Parar" title="Encerra e remove o torrent do streaming (diferente de Pausar; o cache já baixado não é apagado)" className="ml-auto" />
      </div>
    </div>
  )
}

function downloadBorderClass(completed: boolean, failed: boolean, paused: boolean, moving = false): string {
  if (completed) return 'border-green-500/30 hover:border-green-500/50'
  if (failed) return 'border-red-500/30 hover:border-red-500/50'
  if (moving) return 'border-amber-500/30 hover:border-amber-500/50'
  if (paused) return 'border-strong/50 hover:border-strong/60'
  return 'border-cyan-500/30 hover:border-cyan-500/50'
}

function downloadBarGradient(completed: boolean, failed: boolean, paused: boolean, moving = false): string {
  if (completed) return 'from-green-500 to-emerald-400'
  if (failed) return 'from-red-500 to-rose-400'
  if (moving) return 'from-amber-500 to-yellow-400'
  if (paused) return 'from-gray-600 to-gray-500'
  return 'from-cyan-500 to-blue-400'
}

// ═══════════════════════════════════════════════════════════════════════════════
// DownloadCard — Premium redesigned background download card
// ═══════════════════════════════════════════════════════════════════════════════

type DownloadCardProps = {
  readonly d: DownloadEntry
  readonly live?: TorrentInfo
  readonly busy: boolean
  readonly selected?: boolean
  /** True quando o download é UM arquivo de um torrent multi-arquivo (há irmãos
      com o mesmo infoHash). Aí o título mostra o NOME DO ARQUIVO, não o nome do
      torrent — senão todos os episódios de "Euphoria" aparecem idênticos. */
  readonly multiFile?: boolean
  readonly onToggleSelected?: () => void
  readonly onPause: () => void
  readonly onResume: () => void
  readonly onDelete: () => void
  readonly onPromote?: () => void
  readonly onStopSeed?: () => void
  readonly onPlay?: () => void
  readonly onInspect?: () => void
  readonly onSetPriority?: (priority: DownloadPriority) => void
  /** Opens the file in the local browser; undefined when it isn't under a mount. */
  readonly onOpenLocal?: () => void
}

// PriorityBadge shows the queue priority on a download card. Hidden for the
// default (normal) so it only draws attention when the user has tuned it.
function PriorityBadge({ priority }: { readonly priority?: DownloadPriority }) {
  if (!priority || priority === 'normal') return null
  const cls = priority === 'high'
    ? 'bg-orange-500/15 text-orange-700 dark:text-orange-300 border-orange-500/30'
    : 'bg-blue-500/15 text-blue-700 dark:text-blue-300 border-blue-500/30'
  return (
    <span className={`inline-flex items-center gap-1 text-[10px] px-2 py-0.5 rounded-md border font-medium ${cls}`} title="Prioridade na fila">
      <ArrowUpCircle className="w-3 h-3" />{priority === 'high' ? 'Alta' : 'Baixa'}
    </span>
  )
}

function DownloadCard({ d, live, busy, selected, multiFile, onToggleSelected, onPause, onResume, onDelete, onPromote, onStopSeed, onPlay, onInspect, onSetPriority, onOpenLocal }: DownloadCardProps) {
  const { isGuest } = useAuth()
  const { t } = useTranslation()
  // Item de torrent INTEIRO (sentinel): UM card com progresso agregado.
  const isWholeTorrent = d.fileIndex === WHOLE_TORRENT_FILE_INDEX
  const wholeFileCount = live?.files?.length ?? 0
  // Em torrent multi-arquivo o `name` é o nome do torrent (igual pra todos os
  // arquivos), então o que distingue é o basename do filePath (ex: o episódio).
  const fileBase = d.filePath ? d.filePath.split('/').pop() || '' : ''
  const titleText = multiFile && fileBase ? fileBase : (d.name || d.filePath)
  // Subtítulo: no multi-arquivo mostra o torrent (contexto, já que o título virou
  // o arquivo); no single-arquivo mantém o caminho como antes.
  const subtitleText = multiFile && fileBase ? d.name : d.filePath
  const pct = Math.max(0, Math.min(1, d.progress || 0)) * 100
  const isCompleted = d.status === 'completed'
  const isFailed = d.status === 'failed'
  const isPaused = d.status === 'paused'
  const isMoving = d.status === 'moving'
  const isActive = d.status === 'downloading' || d.status === 'queued'
  const isStalled = d.status === 'downloading' && (d.downRate ?? 0) === 0 && d.bytesDownloaded < d.fileSize

  const etaText = computeETA(d)
  const borderClass = downloadBorderClass(isCompleted, isFailed, isPaused, isMoving)
  const barGradient = downloadBarGradient(isCompleted, isFailed, isPaused, isMoving)

  return (
    <div className={`
      relative overflow-hidden rounded-xl border ${borderClass}
      bg-card dark:bg-gradient-to-br dark:from-gray-800/80 dark:to-gray-900/60 backdrop-blur-sm
      p-4 flex flex-col gap-3 transition-all duration-300
    `}>
      {/* Top row */}
      <div className="flex items-start gap-3">
        {onToggleSelected && (
          <input
            type="checkbox"
            checked={!!selected}
            onChange={onToggleSelected}
            className="mt-1 accent-cyan-500 flex-shrink-0"
            title="Selecionar para ações em lote"
          />
        )}
        <div className="min-w-0 flex-1">
          <h3 className="font-semibold text-text-primary text-sm leading-snug [overflow-wrap:anywhere]" title={titleText}>
            {titleText}
          </h3>
          <div className="flex items-center gap-1.5 mt-1.5 flex-wrap">
            <KindBadge kind="server" />
            <DownloadStatusBadge status={d.status} />
            {isWholeTorrent && (
              <span className="inline-flex items-center gap-1 text-[10px] px-2 py-0.5 rounded-md border font-medium bg-cyan-500/15 text-cyan-700 dark:text-cyan-300 border-cyan-500/30" title={t('downloads.whole_torrent_badge')}>
                <Folder className="w-3 h-3" />
                {t('downloads.whole_torrent_badge')}
                {wholeFileCount > 0 && <> · {t('downloads.whole_torrent_files', { count: wholeFileCount })}</>}
              </span>
            )}
            {d.status === 'queued' && (d.queuePosition ?? 0) > 0 && (
              <span className="text-[10px] px-1.5 py-0.5 rounded-md bg-surface-tertiary/50 text-text-secondary border border-strong/50 font-medium" title="Posição na fila">
                {d.queuePosition}º na fila
              </span>
            )}
            <PriorityBadge priority={d.priority} />
            {isStalled && (
              <span className="inline-flex items-center gap-1 text-[10px] px-2 py-0.5 rounded-md border font-medium bg-amber-500/15 text-amber-700 dark:text-amber-300 border-amber-500/30" title="Sem progresso — sem peers/seeds. Após o limite vai pro fim da fila.">
                <AlertTriangle className="w-3 h-3" /> Sem seed{(d.stalls ?? 0) > 0 ? ` (${d.stalls}×)` : ''}
              </span>
            )}
            {d.status === 'completed' && (
              <span className="inline-flex items-center gap-1 text-[10px] px-2 py-0.5 rounded-md border font-medium bg-emerald-500/15 text-emerald-700 dark:text-emerald-300 border-emerald-500/30">
                <HardDrive className="w-3 h-3 text-emerald-400" /> no disco
              </span>
            )}
            {d.username && (
              <span className="text-[10px] px-1.5 py-0.5 rounded-md bg-violet-500/15 text-violet-700 dark:text-violet-300 border border-violet-500/30 font-medium">
                {d.username}
              </span>
            )}
          </div>
          {subtitleText && <p className="text-[11px] text-text-muted truncate mt-0.5" title={subtitleText}>{subtitleText}</p>}
        </div>
      </div>

      {/* Live activity chips — só quando o anacrolix tem o torrent ativo (ou
          baixando, ou seedando depois de concluído). Mesmo formato visual do
          TorrentCard pra consistência. */}
      {live && (live.downRate > 0 || live.upRate > 0 || live.peers > 0) && (
        <div className="flex items-center gap-2 flex-wrap text-sm">
          <span
            className={`flex items-center gap-1 px-2 py-0.5 rounded-full font-mono tabular-nums ${
              live.downRate > 0 ? 'bg-emerald-500/15 text-emerald-700 dark:text-emerald-300 border border-emerald-500/30' : 'text-text-muted'
            }`}
            title="Download deste torrent"
          >
            <ArrowDownCircle className="w-3.5 h-3.5" />
            {formatRate(live.downRate)}
          </span>
          <span
            className={`flex items-center gap-1 px-2 py-0.5 rounded-full font-mono tabular-nums ${
              live.upRate > 0 ? 'bg-violet-500/15 text-violet-700 dark:text-violet-300 border border-violet-500/30' : 'text-text-muted'
            }`}
            title="Upload deste torrent (seedando)"
          >
            <ArrowUpCircle className="w-3.5 h-3.5" />
            {formatRate(live.upRate)}
          </span>
          <span
            className="flex items-center gap-1 px-2 py-0.5 rounded-full bg-blue-500/10 text-blue-700 dark:text-blue-300 border border-blue-500/20 font-mono tabular-nums"
            title="Peers conectados / Seeders no swarm"
          >
            <Users className="w-3.5 h-3.5" />
            {live.peers}{(live.seeders ?? 0) > 0 && <span className="text-text-muted"> / {live.seeders}</span>}
          </span>
        </div>
      )}

      {/* Progress bar */}
      <div>
        <div className="h-2 bg-surface-tertiary dark:bg-surface/80 rounded-full overflow-hidden">
          <div
            className={`h-full rounded-full bg-gradient-to-r ${barGradient} transition-all duration-500 ease-out`}
            style={{ width: `${pct.toFixed(1)}%` }}
          />
        </div>
        <div className="flex items-center justify-between mt-2 text-xs text-text-secondary">
          <span className="text-text-primary font-medium">
            {formatBytesPair(d.bytesDownloaded, d.fileSize)}
            <span className="text-text-muted ml-1">({pct.toFixed(1)}%)</span>
          </span>
          {!isCompleted && !isFailed && etaText && (
            <span className="flex items-center gap-1 text-text-muted">
              <Clock className="w-3 h-3" /> {etaText}
            </span>
          )}
          {isMoving && (
            <span className="flex items-center gap-1 text-amber-600 dark:text-amber-300 text-xs font-medium" title="Baixou tudo; agora movendo os arquivos para o destino final (veja o progresso no painel de Transferências).">
              <Loader2 className="w-3 h-3 animate-spin" /> Movendo arquivos…
            </span>
          )}
          {isCompleted && (
            <span className="flex items-center gap-1 text-green-400 text-xs font-medium">
              <CheckCircle2 className="w-3 h-3" /> Concluído
            </span>
          )}
        </div>
      </div>

      {/* Error banner */}
      {isFailed && d.error && (
        <div className="flex items-start gap-2 text-xs text-red-700 dark:text-red-300 bg-red-500/10 border border-red-500/20 rounded-lg px-3 py-2">
          <AlertCircle className="w-3.5 h-3.5 flex-shrink-0 mt-0.5" />
          <span className="break-all">{d.error}</span>
        </div>
      )}

      {/* Action bar */}
      <div className="flex items-center gap-2 pt-1 flex-wrap">
        {onPlay && !isFailed && d.bytesDownloaded > 0 && (
          <ActionButton
            onClick={onPlay}
            disabled={busy}
            variant="success"
            icon={<Play className="w-3.5 h-3.5 fill-current" />}
            label="Tocar"
          />
        )}
        {!isGuest && isActive && (
          <ActionButton onClick={onPause} disabled={busy} variant="neutral" icon={<Pause className="w-3.5 h-3.5" />} label="Pausar" title="Pausa o download (retomável; mantém o que já baixou)" />
        )}
        {!isGuest && (isPaused || isFailed) && (
          <ActionButton onClick={onResume} disabled={busy} variant="info" icon={<Play className="w-3.5 h-3.5" />} label={isFailed ? 'Tentar novamente' : 'Resumir'} />
        )}
        {!isGuest && isActive && onSetPriority && (
          <label className="flex items-center gap-1.5 text-xs text-text-secondary">
            <span className="text-text-muted">Prioridade:</span>
            <select
              value={d.priority || 'normal'}
              onChange={e => onSetPriority(e.target.value as DownloadPriority)}
              disabled={busy}
              className="bg-surface-secondary border border-default rounded-lg px-2 py-1 text-text-primary text-xs disabled:opacity-50 focus:outline-none focus:border-cyan-500 transition-colors cursor-pointer"
            >
              <option value="low">Baixa</option>
              <option value="normal">Normal</option>
              <option value="high">Alta</option>
            </select>
          </label>
        )}
        {!isGuest && isCompleted && onPromote && (
          <ActionButton
            onClick={onPromote}
            disabled={busy}
            variant="info"
            icon={<ArrowUpCircle className="w-3.5 h-3.5" />}
            label="Promover"
          />
        )}
        {!isGuest && isCompleted && onStopSeed && (
          <ActionButton
            onClick={onStopSeed}
            disabled={busy}
            variant="neutral"
            icon={<Pause className="w-3.5 h-3.5" />}
            label="Parar de seedar"
          />
        )}
        {isCompleted && onOpenLocal && (
          <ActionButton
            onClick={onOpenLocal}
            disabled={busy}
            variant="info"
            icon={<Folder className="w-3.5 h-3.5" />}
            label="Abrir no local"
            title="Abre o navegador de arquivos na pasta deste download"
          />
        )}
        {onInspect && (
          <ActionButton
            onClick={onInspect}
            disabled={busy}
            variant="neutral"
            icon={<Info className="w-3.5 h-3.5" />}
            label="Detalhes"
          />
        )}
        {!isGuest && (
          <ActionButton
            onClick={onDelete}
            disabled={busy}
            variant="danger"
            icon={<Trash2 className="w-3.5 h-3.5" />}
            label={isCompleted ? 'Remover da lista' : 'Cancelar'}
            className="ml-auto"
          />
        )}
      </div>
    </div>
  )
}

// ═══════════════════════════════════════════════════════════════════════════════
// Shared sub-components
// ═══════════════════════════════════════════════════════════════════════════════

function KindBadge({ kind }: { readonly kind: 'streaming' | 'server' }) {
  if (kind === 'streaming') {
    return (
      <span className="inline-flex items-center gap-1 text-[10px] font-bold uppercase tracking-wider px-2 py-0.5 rounded-md bg-gradient-to-r from-emerald-500/20 to-teal-500/20 text-emerald-700 dark:text-emerald-300 border border-emerald-500/30">
        <Activity className="w-2.5 h-2.5" />
        Streaming
      </span>
    )
  }
  return (
    <span className="inline-flex items-center gap-1 text-[10px] font-bold uppercase tracking-wider px-2 py-0.5 rounded-md bg-gradient-to-r from-cyan-500/20 to-blue-500/20 text-cyan-700 dark:text-cyan-300 border border-cyan-500/30">
      <Server className="w-2.5 h-2.5" />
      Servidor
    </span>
  )
}

function TorrentStatusBadge({ status }: { readonly status: NonNullable<TorrentInfo['status']> }) {
  const map: Record<NonNullable<TorrentInfo['status']>, { label: string; cls: string; icon: React.ReactNode }> = {
    downloading: { label: 'Baixando',  cls: 'bg-emerald-500/15 text-emerald-700 dark:text-emerald-300 border-emerald-500/30', icon: <Loader2 className="w-3 h-3 animate-spin" /> },
    paused:      { label: 'Pausado',   cls: 'bg-gray-500/15 text-text-primary border-strong/30',          icon: <Pause className="w-3 h-3" /> },
    seeding:     { label: 'Semeando',  cls: 'bg-violet-500/15 text-violet-700 dark:text-violet-300 border-violet-500/30',    icon: <ArrowUpCircle className="w-3 h-3" /> },
    complete:    { label: 'Completo',  cls: 'bg-green-500/15 text-green-700 dark:text-green-300 border-green-500/30',       icon: <CheckCircle2 className="w-3 h-3" /> },
  }
  const s = map[status]
  return (
    <span className={`inline-flex items-center gap-1 text-[10px] px-2 py-0.5 rounded-md border font-medium ${s.cls}`}>
      {s.icon} {s.label}
    </span>
  )
}

function DownloadStatusBadge({ status }: { readonly status: DownloadEntry['status'] }) {
  const map: Record<DownloadEntry['status'], { label: string; cls: string; icon: React.ReactNode }> = {
    queued:      { label: 'Na fila',     cls: 'bg-surface-tertiary/50 text-text-primary border-strong/50',         icon: <Clock className="w-3 h-3" /> },
    downloading: { label: 'Baixando',    cls: 'bg-cyan-500/15 text-cyan-700 dark:text-cyan-300 border-cyan-500/30',         icon: <Loader2 className="w-3 h-3 animate-spin" /> },
    moving:      { label: 'Movendo',     cls: 'bg-amber-500/15 text-amber-700 dark:text-amber-300 border-amber-500/30',      icon: <Loader2 className="w-3 h-3 animate-spin" /> },
    completed:   { label: 'Concluído',   cls: 'bg-green-500/15 text-green-700 dark:text-green-300 border-green-500/30',      icon: <CheckCircle2 className="w-3 h-3" /> },
    failed:      { label: 'Falhou',      cls: 'bg-red-500/15 text-red-700 dark:text-red-300 border-red-500/30',            icon: <AlertCircle className="w-3 h-3" /> },
    paused:      { label: 'Pausado',     cls: 'bg-gray-500/15 text-text-primary border-strong/30',         icon: <Pause className="w-3 h-3" /> },
  }
  const s = map[status]
  return (
    <span className={`inline-flex items-center gap-1 text-[10px] px-2 py-0.5 rounded-md border font-medium ${s.cls}`}>
      {s.icon} {s.label}
    </span>
  )
}

function ActionButton({ onClick, disabled, variant, icon, label, className = '', title }: {
  readonly onClick: () => void
  readonly disabled: boolean
  readonly variant: 'success' | 'danger' | 'neutral' | 'info'
  readonly icon: React.ReactNode
  readonly label: string
  readonly className?: string
  readonly title?: string
}) {
  const styles: Record<typeof variant, string> = {
    success: 'bg-emerald-500/10 hover:bg-emerald-500/20 text-emerald-700 dark:text-emerald-300 border-emerald-500/30',
    danger:  'bg-red-500/10 hover:bg-red-500/20 text-red-700 dark:text-red-300 border-red-500/30',
    neutral: 'bg-surface-tertiary/60 hover:bg-surface-tertiary text-text-primary border-strong/60',
    info:    'bg-blue-500/10 hover:bg-blue-500/20 text-blue-700 dark:text-blue-300 border-blue-500/30',
  }
  return (
    <button
      onClick={onClick}
      disabled={disabled}
      title={title}
      className={`
        flex items-center gap-1.5 text-xs border px-3 py-1.5 rounded-lg
        disabled:opacity-50 transition-all duration-200 font-medium
        ${styles[variant]} ${className}
      `}
    >
      {icon} {label}
    </button>
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

// ═══════════════════════════════════════════════════════════════════════════════
// ETA helpers
// ═══════════════════════════════════════════════════════════════════════════════

function computeTorrentETA(t: TorrentInfo): string {
  if (!t.totalSize || !t.downRate || t.downRate <= 0) return ''
  const done = (t.progress || 0) * t.totalSize
  const remaining = t.totalSize - done
  if (remaining <= 0) return ''
  const sec = remaining / t.downRate
  if (!Number.isFinite(sec) || sec <= 0) return ''
  return `~${formatDurationShort(sec)}`
}

function computeETA(d: DownloadEntry): string {
  // Prefer backend-computed ETA (more accurate — uses live swarm rate)
  if (d.eta && d.eta > 0) {
    return `~${formatDurationShort(d.eta)} restantes`
  }
  if (!d.startedAt || d.fileSize <= 0 || d.bytesDownloaded <= 0) return ''
  if (d.bytesDownloaded >= d.fileSize) return ''
  const startMs = new Date(d.startedAt).getTime()
  if (!Number.isFinite(startMs) || startMs <= 0) return ''
  const elapsedSec = (Date.now() - startMs) / 1000
  if (elapsedSec < 2) return ''
  const rate = d.bytesDownloaded / elapsedSec
  if (rate <= 0) return ''
  const remainingSec = (d.fileSize - d.bytesDownloaded) / rate
  if (!Number.isFinite(remainingSec) || remainingSec <= 0) return ''
  return `~${formatDurationShort(remainingSec)} restantes`
}
