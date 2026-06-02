import { useEffect, useRef, useState } from 'react'
import {
  Loader2, Pause, Play, Trash2, CheckCircle2, AlertCircle, Clock,
  Activity, Gauge, Users, Zap, ArrowDownCircle, ArrowUpCircle, Wifi, Server, Info,
  Plus, UploadCloud, Search, X, SlidersHorizontal, HardDrive, AlertTriangle,
  ListFilter, Download, CheckSquare, MoreHorizontal,
} from 'lucide-react'
import NavHeader from '../components/NavHeader'
import { Sheet } from '../components/Sheet'
import { useConfirm } from '../components/ConfirmDialog'
import {
  DownloadEntry, DownloadFilterParams, downloadsList, downloadsListFiltered, downloadDelete, downloadPause, downloadResume, downloadStopSeed,
  downloadPauseAll, downloadResumeAll, downloadBatchPause, downloadBatchResume, downloadBatchDelete,
  downloadTrackers, downloadCategories,
  downloadsListAll, downloadUsers, DownloadUserEntry,
  TorrentInfo, streamActive, streamPause, streamResume, streamSetPriority,
  streamPauseAll, streamResumeAll, streamGetLimits, streamSetLimits, StreamPriority, streamDrop,
  LocalMount, localMounts, buildLocalHash, SearchResult,
  streamAdd, streamAddTorrentFile
} from '../api/client'
import { formatBytes, formatRate, formatDurationShort } from '../lib/format'
import PromoteModal from '../components/PromoteModal'
import { usePlayer } from '../components/PlayerProvider'
import DownloadInspectModal from '../components/DownloadInspectModal'
import DownloadModal from '../components/DownloadModal'
import AddTorrentModal from '../components/AddTorrentModal'
import { useAuth } from '../auth/AuthContext'


// ═══════════════════════════════════════════════════════════════════════════════
// Premium Downloads & Network Dashboard
// ═══════════════════════════════════════════════════════════════════════════════

type Tab = 'all' | 'downloading' | 'paused' | 'completed' | 'failed' | 'network'

export default function DownloadsPage() {
  const [items, setItems] = useState<DownloadEntry[]>([])
  // Multi-select de downloads concluídos pra batch promote. Set of IDs.
  const [selected, setSelected] = useState<Set<number>>(new Set())
  // Items passados ao modal de promove (null = fechado). Single = [d], batch = [d1, d2, ...]
  const [promoteTargets, setPromoteTargets] = useState<DownloadEntry[] | null>(null)
  // Download sendo inspecionado no modal de detalhes (null = fechado)
  const [inspectTarget, setInspectTarget] = useState<DownloadEntry | null>(null)
  // Mounts navegáveis — usados pra decidir se Play vai pelo player local
  // (arquivo em mount como /mnt/downloads) ou pelo torrent (em /data/streams).
  // Carregado uma vez; mounts não mudam durante uma sessão.
  const [mounts, setMounts] = useState<LocalMount[]>([])
  const { playSingle } = usePlayer()
  const [loading, setLoading] = useState(true)
  const [busyID, setBusyID] = useState<number | null>(null)
  const mountedRef = useRef(true)

  const [activeTab, setActiveTab] = useState<Tab>('all')

  const [torrents, setTorrents] = useState<TorrentInfo[]>([])
  const [torrentsLoaded, setTorrentsLoaded] = useState(false)
  const [busyHash, setBusyHash] = useState<string | null>(null)
  const [bulkBusy, setBulkBusy] = useState(false)
  const [bulkSheetOpen, setBulkSheetOpen] = useState(false)
  const confirm = useConfirm()

  const [limitDownKB, setLimitDownKB] = useState<string>('')
  const [limitUpKB, setLimitUpKB] = useState<string>('')
  const [limitsSaving, setLimitsSaving] = useState(false)
  const [limitsMsg, setLimitsMsg] = useState<string>('')

  // ─── Filter & Sort state ────────────────────────────────────────────────────
  const [filterSearch, setFilterSearch] = useState('')
  const [filterStatus, setFilterStatus] = useState('')
  const [filterTracker, setFilterTracker] = useState('')
  const [filterCategory, setFilterCategory] = useState('')
  const [sortCol] = useState('created_at')
  const [sortDir] = useState('desc')
  const [availableTrackers, setAvailableTrackers] = useState<string[]>([])
  const [availableCategories, setAvailableCategories] = useState<string[]>([])
  const [showFilters, setShowFilters] = useState(false)
  const filterTimeoutRef = useRef<ReturnType<typeof setTimeout>>()
  // Admin mode: toggle between own downloads and all users' downloads
  const { isAdmin, isGuest } = useAuth()
  const [showAllUsers, setShowAllUsers] = useState(false)
  const [availableUsers, setAvailableUsers] = useState<DownloadUserEntry[]>([])
  const [filterUserId, setFilterUserId] = useState('')

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
    try {
      const list = showAllUsers
        ? await downloadsListAll({
            userId: filterUserId || undefined,
            sort: sortCol,
            order: sortDir,
          })
        : await downloadsList()
      if (mountedRef.current) setItems(list)
    } catch { /* silent */ } finally {
      if (mountedRef.current) setLoading(false)
    }
  }

  const loadFiltered = async () => {
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
      if (mountedRef.current) setItems(list)
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
    const t = setInterval(() => { load(); loadTorrents() }, 2000)
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
  }, [filterStatus, filterTracker, filterCategory, filterSearch, sortCol, sortDir, filterUserId, showAllUsers])

  // Roteia play: se file_path está dentro de algum mount navegável → player
  // local (sem tocar no anacrolix); senão → player do torrent (cache em
  // /data/streams ou ainda baixando). Mantém a UX consistente com os outros
  // pontos do app onde clicar em Play "simplesmente toca".
  const onPlay = (d: DownloadEntry) => {
    const fp = d.filePath
    if (!fp) return
    const m = mounts.find(mt => fp === mt.path || fp.startsWith(mt.path + '/'))
    if (m) {
      const rel = fp.slice(m.path.length).replaceAll(/^\/+/g, '')
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

  // ─── Actions ──────────────────────────────────────────────────────────────

  const onPause = async (id: number) => {
    setBusyID(id)
    try { await downloadPause(id); await load() } finally { setBusyID(null) }
  }
  const onResume = async (id: number) => {
    setBusyID(id)
    try { await downloadResume(id); await load() } finally { setBusyID(null) }
  }
  const onDelete = async (id: number) => {
    if (!await confirm({ title: 'Remover download?', message: 'Parar e remover este download da fila? A sessão de streaming/transcode aberta pelo Play também é encerrada.', confirmLabel: 'Remover', destructive: true })) return
    const target = items.find(x => x.id === id)
    setBusyID(id)
    try {
      await downloadPause(id).catch(() => {}) // pausa antes de remover
      // Play de um download cria uma sessão de stream/transcode no anacrolix pro
      // MESMO hash, separada da row. Sem derrubá-la, o torrent "tocado" reaparece
      // como card de Streaming após o delete (sintoma: "permaneceu mesmo após excluir").
      if (target?.infoHash) await streamDrop(target.infoHash).catch(() => {})
      await downloadDelete(id)
      await load(); await loadTorrents()
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
    try {
      await downloadBatchPause(ids).catch(() => {}) // pausa todos antes de remover
      // Encerra qualquer sessão de stream/transcode aberta pelo Play (ver onDelete).
      await Promise.all(targets.map(d => d.infoHash ? streamDrop(d.infoHash).catch(() => {}) : Promise.resolve()))
      await downloadBatchDelete(ids)
      await load(); await loadTorrents()
      setSelected(new Set())
    } finally { setBulkBusy(false) }
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

  // Per-status download groups
  const downloadsByStatus = {
    downloading: items.filter(d => d.status === 'downloading' || d.status === 'queued'),
    paused:      items.filter(d => d.status === 'paused'),
    completed:   items.filter(d => d.status === 'completed'),
    failed:      items.filter(d => d.status === 'failed'),
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

  // Stalled: downloading but no progress (downRate === 0 or null)
  const stalledCount = items.filter(
    d => d.status === 'downloading' && (d.downRate ?? 0) === 0 && d.bytesDownloaded < d.fileSize
  ).length

  // Summary stats
  const totalDown = torrents.reduce((sum, t) => sum + (t.downRate || 0), 0)
    + items.filter(d => d.status === 'downloading').reduce((sum, d) => sum + (d.downRate || 0), 0)
  const totalUp = torrents.reduce((sum, t) => sum + (t.upRate || 0), 0)
  const totalPeers = torrents.reduce((sum, t) => sum + (t.peers || 0), 0)
  const activeCount = activeTorrents.length + downloadsByStatus.downloading.length
  const seedingCount = seedingTorrents.length

  // Tab badge counts
  const tabCounts: Record<Tab, number> = {
    all:         displayTorrents.length + items.length,
    downloading: activeTorrents.length + downloadsByStatus.downloading.length,
    paused:      downloadsByStatus.paused.length,
    completed:   seedingTorrents.length + completedDownloads.length,
    failed:      downloadsByStatus.failed.length,
    network:     0,
  }

  // Items for the currently-selected status tab
  const tabDownloads: Record<Tab, DownloadEntry[]> = {
    all:         items,
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

  // ─── Render ───────────────────────────────────────────────────────────────

  return (
    <section
      onDragEnter={handleDragEnter}
      onDragOver={handleDragOver}
      onDragLeave={handleDragLeave}
      onDrop={handleDrop}
      aria-label="Gerenciador de downloads — arraste arquivos .torrent ou links magnet"
      className="relative min-h-screen bg-gray-900"
    >
      {isDraggingPage && (
        <div className="fixed inset-0 z-50 bg-gray-950/80 backdrop-blur-md flex flex-col items-center justify-center border-4 border-dashed border-cyan-500/50 m-4 rounded-3xl pointer-events-none transition-all duration-300 animate-pulse">
          <UploadCloud className="w-16 h-16 text-cyan-400 mb-4 animate-bounce" />
          <h2 className="text-xl font-bold text-gray-100 mb-1">Solte seus arquivos .torrent aqui!</h2>
          <p className="text-sm text-gray-400">ou links magnet arrastados para iniciar o carregamento</p>
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
            value={`${activeCount} ativo${activeCount === 1 ? '' : 's'}`}
            subtitle={seedingCount > 0 ? `${seedingCount} semeando` : undefined}
            gradient="from-amber-500/20 to-orange-500/10"
            iconColor="text-amber-400"
          />
        </div>

        {/* ═══════════════ Global Action Toolbar ═══════════════ */}
        <div className="flex items-center justify-between gap-3 flex-wrap">
          {/* Quick stats strip */}
          <div className="flex items-center gap-3 text-xs text-gray-400 flex-wrap">
            {downloadsByStatus.downloading.length > 0 && (
              <span className="flex items-center gap-1">
                <Download className="w-3.5 h-3.5 text-cyan-400" />
                <span className="text-gray-200 font-medium">{downloadsByStatus.downloading.length}</span> baixando
              </span>
            )}
            {downloadsByStatus.paused.length > 0 && (
              <span className="flex items-center gap-1">
                <Pause className="w-3.5 h-3.5 text-gray-400" />
                <span className="text-gray-200 font-medium">{downloadsByStatus.paused.length}</span> pausados
              </span>
            )}
            {completedDownloads.length > 0 && (
              <span className="flex items-center gap-1">
                <CheckCircle2 className="w-3.5 h-3.5 text-green-400" />
                <span className="text-gray-200 font-medium">{completedDownloads.length}</span> concluídos
              </span>
            )}
            {downloadsByStatus.failed.length > 0 && (
              <span className="flex items-center gap-1">
                <AlertCircle className="w-3.5 h-3.5 text-red-400" />
                <span className="text-gray-200 font-medium">{downloadsByStatus.failed.length}</span> com erro
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
                  className="flex items-center gap-1.5 text-xs bg-emerald-500/10 hover:bg-emerald-500/20 disabled:opacity-50 text-emerald-300 border border-emerald-500/30 px-3 py-1.5 rounded-lg transition-colors"
                >
                  <Play className="w-3 h-3" /> Iniciar todos
                </button>
                <button
                  onClick={doPauseAll}
                  disabled={bulkBusy}
                  title="Pausar todos os downloads e torrents ativos"
                  className="flex items-center gap-1.5 text-xs bg-gray-800 hover:bg-gray-700 disabled:opacity-50 text-gray-300 border border-gray-700 px-3 py-1.5 rounded-lg transition-colors"
                >
                  <Pause className="w-3 h-3" /> Pausar todos
                </button>
                {completedDownloads.length > 0 && (
                  <button
                    onClick={doRemoveCompleted}
                    disabled={bulkBusy}
                    title="Remover da fila todos os downloads concluídos (arquivos não são apagados)"
                    className="flex items-center gap-1.5 text-xs bg-red-500/10 hover:bg-red-500/20 disabled:opacity-50 text-red-300 border border-red-500/30 px-3 py-1.5 rounded-lg transition-colors"
                  >
                    <Trash2 className="w-3 h-3" /> Remover concluídos
                  </button>
                )}
              </div>
              {/* Mobile: agrupadas num Sheet de "Ações" */}
              <button
                onClick={() => setBulkSheetOpen(true)}
                disabled={bulkBusy}
                className="sm:hidden flex items-center gap-1.5 text-xs px-3 min-h-[44px] rounded-lg border border-gray-700 bg-gray-800 text-gray-300 disabled:opacity-50"
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
              <Search className="absolute left-3 top-1/2 -translate-y-1/2 w-4 h-4 text-gray-500" />
              <input
                type="text"
                value={filterSearch}
                onChange={e => setFilterSearch(e.target.value)}
                placeholder="Buscar por nome ou caminho..."
                className="w-full bg-gray-800/80 border border-gray-700 rounded-lg pl-9 pr-3 py-2 text-sm text-gray-200 placeholder-gray-500 focus:outline-none focus:border-cyan-500/50 transition-colors"
              />
              {filterSearch && (
                <button onClick={() => setFilterSearch('')} className="absolute right-3 top-1/2 -translate-y-1/2 text-gray-500 hover:text-gray-300">
                  <X className="w-3.5 h-3.5" />
                </button>
              )}
            </div>
            <button
              onClick={() => setShowFilters(!showFilters)}
              className={`flex items-center gap-1.5 text-xs px-3 py-2 rounded-lg border transition-colors ${
                showFilters || filterStatus || filterTracker || filterCategory
                  ? 'bg-cyan-500/10 border-cyan-500/30 text-cyan-300'
                  : 'bg-gray-800 border-gray-700 text-gray-400 hover:text-gray-200'
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
            <div className="flex items-center gap-2 flex-wrap bg-gray-800/40 border border-gray-700/50 rounded-xl p-3">
              <select
                value={filterStatus}
                onChange={e => setFilterStatus(e.target.value)}
                className="bg-gray-900 border border-gray-700 rounded-lg px-3 py-1.5 text-xs text-gray-200 focus:outline-none focus:border-cyan-500/50"
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
                  className="bg-gray-900 border border-gray-700 rounded-lg px-3 py-1.5 text-xs text-gray-200 focus:outline-none focus:border-cyan-500/50"
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
                  className="bg-gray-900 border border-gray-700 rounded-lg px-3 py-1.5 text-xs text-gray-200 focus:outline-none focus:border-cyan-500/50"
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
                  className="bg-gray-900 border border-gray-700 rounded-lg px-3 py-1.5 text-xs text-gray-200 focus:outline-none focus:border-cyan-500/50"
                >
                  <option value="">Todos os usuários</option>
                  {availableUsers.map(u => (
                    <option key={u.id} value={String(u.id)}>{u.username}</option>
                  ))}
                </select>
              )}
              <button
                onClick={() => { setFilterStatus(''); setFilterTracker(''); setFilterCategory(''); setFilterSearch(''); setFilterUserId('') }}
                className="text-xs text-gray-500 hover:text-gray-300 px-2 py-1"
              >
                Limpar filtros
              </button>
            </div>
          )}
        </div>

        {/* ═══════════════ Tabs & Actions ═══════════════ */}
        <div className="flex items-center justify-between border-b border-gray-700/60 flex-wrap gap-3">
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
                    : 'border-transparent text-gray-400 hover:text-gray-200 hover:border-gray-600'}
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
                onClick={() => { setShowAllUsers(!showAllUsers); if (!showAllUsers) downloadUsers().then(setAvailableUsers).catch(() => {}) }}
                className={`flex items-center gap-1.5 text-xs px-4 py-2 rounded-xl font-semibold transition-all duration-200 mb-2 md:mb-0 ${
                  showAllUsers
                    ? 'bg-violet-500 hover:bg-violet-600 text-white shadow-lg shadow-violet-500/10'
                    : 'bg-gray-800 border border-gray-700 text-gray-400 hover:text-gray-200'
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
                className="flex items-center gap-1.5 text-xs bg-cyan-500 hover:bg-cyan-600 text-gray-900 px-4 py-2 rounded-xl font-semibold transition-all duration-200 shadow-lg shadow-cyan-500/10 mb-2 md:mb-0"
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
              {/* Active / downloading torrents */}
              {tabTorrents[activeTab].length > 0 && (
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
                  onPause={onPause}
                  onResume={onResume}
                  onDelete={onDelete}
                  onPlay={onPlay}
                  onInspect={setInspectTarget}
                />
              )}
              {/* Background downloads for this tab */}
              <SeedingTab
                torrents={[]}
                downloads={tabDownloads[activeTab]}
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
                onPause={onPause}
                onResume={onResume}
                onDelete={onDelete}
                onPromote={onPromote}
                onStopSeed={onStopSeed}
                onPlay={onPlay}
                onInspect={setInspectTarget}
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
        onClose={() => setInspectTarget(null)}
        onMutated={(updated) => {
          setItems(prev => prev.map(item => item.id === updated.id ? updated : item))
        }}
        onDeleted={() => {
          setItems(prev => prev.filter(item => item.id !== inspectTarget?.id))
          setInspectTarget(null)
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
            className="flex items-center gap-2 min-h-[48px] px-4 rounded-lg bg-emerald-500/10 text-emerald-300 border border-emerald-500/30 disabled:opacity-50"
          >
            <Play className="w-4 h-4" /> Iniciar todos
          </button>
          <button
            onClick={() => { setBulkSheetOpen(false); void doPauseAll() }}
            disabled={bulkBusy}
            className="flex items-center gap-2 min-h-[48px] px-4 rounded-lg bg-gray-700/60 text-gray-200 border border-gray-700 disabled:opacity-50"
          >
            <Pause className="w-4 h-4" /> Pausar todos
          </button>
          {completedDownloads.length > 0 && (
            <button
              onClick={() => { setBulkSheetOpen(false); void doRemoveCompleted() }}
              disabled={bulkBusy}
              className="flex items-center gap-2 min-h-[48px] px-4 rounded-lg bg-red-500/10 text-red-300 border border-red-500/30 disabled:opacity-50"
            >
              <Trash2 className="w-4 h-4" /> Remover concluídos ({completedDownloads.length})
            </button>
          )}
        </div>
      </Sheet>


      {/* Barra flutuante de bulk actions, só aparece com seleção ativa. */}
      {selected.size > 0 && (
        <div
          style={{ bottom: 'calc(1rem + env(safe-area-inset-bottom, 0px))' }}
          className="fixed left-1/2 -translate-x-1/2 z-40 flex items-center gap-2 bg-gray-800 border border-cyan-500/40 shadow-2xl rounded-full px-4 py-2 backdrop-blur"
        >
          <span className="text-sm text-gray-200 font-medium whitespace-nowrap">{selected.size} selecionado{selected.size === 1 ? '' : 's'}</span>
          <div className="w-px h-5 bg-gray-700" />
          <button
            onClick={onBatchPause}
            disabled={bulkBusy}
            className="flex items-center gap-1 text-xs bg-gray-700/60 hover:bg-gray-700 disabled:opacity-50 text-gray-300 px-3 py-1 rounded-full transition-colors"
          >
            <Pause className="w-3 h-3" /> Pausar
          </button>
          <button
            onClick={onBatchResume}
            disabled={bulkBusy}
            className="flex items-center gap-1 text-xs bg-emerald-500/10 hover:bg-emerald-500/20 disabled:opacity-50 text-emerald-300 px-3 py-1 rounded-full transition-colors"
          >
            <Play className="w-3 h-3" /> Retomar
          </button>
          <button
            onClick={onPromoteSelected}
            className="flex items-center gap-1 text-xs bg-cyan-500/20 hover:bg-cyan-500/30 text-cyan-300 px-3 py-1 rounded-full transition-colors"
          >
            <ArrowUpCircle className="w-3 h-3" />
            Promover
          </button>
          <button
            onClick={onBatchDelete}
            disabled={bulkBusy}
            className="flex items-center gap-1 text-xs bg-red-500/10 hover:bg-red-500/20 disabled:opacity-50 text-red-300 px-3 py-1 rounded-full transition-colors"
          >
            <Trash2 className="w-3 h-3" /> Remover
          </button>
          <div className="w-px h-5 bg-gray-700" />
          <button
            onClick={handleToggleSelectAll}
            className="text-xs text-gray-400 hover:text-gray-200 px-1"
          >
            {selected.size === items.length ? 'desmarcar' : 'todos'}
          </button>
          <button
            onClick={() => setSelected(new Set())}
            className="text-xs text-gray-400 hover:text-gray-200 px-1"
          >
            limpar
          </button>
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
      relative overflow-hidden rounded-xl border border-gray-700/50
      bg-gradient-to-br ${gradient} backdrop-blur-sm
      p-4 flex flex-col gap-1
    `}>
      <div className="flex items-center gap-2">
        <span className={`${iconColor} ${pulse ? 'animate-pulse' : ''}`}>{icon}</span>
        <span className="text-xs text-gray-400 uppercase tracking-wider font-medium">{label}</span>
      </div>
      <span className="text-xl font-bold text-gray-100 tracking-tight">{value}</span>
      {subtitle && <span className="text-xs text-gray-500">{subtitle}</span>}
    </div>
  )
}

// ═══════════════════════════════════════════════════════════════════════════════
// ActiveTab — downloading/queued torrents + background downloads
// ═══════════════════════════════════════════════════════════════════════════════

function ActiveTab({ torrents, downloads, torrentsLoaded, loading, busyHash, busyID,
  onTorrentPause, onTorrentResume, onTorrentPriority, onTorrentDelete,
  onPause, onResume, onDelete, onPlay, onInspect,
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
  readonly onPause: (id: number) => void
  readonly onResume: (id: number) => void
  readonly onDelete: (id: number) => void
  readonly onPlay: (d: DownloadEntry) => void
  readonly onInspect: (d: DownloadEntry) => void
}) {
  const empty = torrents.length === 0 && downloads.length === 0 && torrentsLoaded && !loading
  const isLoading = (!torrentsLoaded || (loading && downloads.length === 0)) && torrents.length === 0 && downloads.length === 0

  return (
    <div className="flex flex-col gap-4">
      {isLoading && (
        <div className="flex items-center gap-2 text-gray-400 py-12 justify-center">
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
        />
      ))}
    </div>
  )
}

// ═══════════════════════════════════════════════════════════════════════════════
// SeedingTab — seeding/complete torrents + completed downloads
// ═══════════════════════════════════════════════════════════════════════════════

function SeedingTab({ torrents, downloads, torrentsLoaded, busyHash, busyID,
  onTorrentPause, onTorrentResume, onTorrentPriority, onTorrentDelete,
  onPause, onResume, onDelete, onPromote, onStopSeed,
  selected, onToggleSelected, onPlay, onInspect, loading,
}: {
  readonly torrents: TorrentInfo[]
  readonly downloads: DownloadEntry[]
  readonly torrentsLoaded: boolean
  readonly busyHash: string | null
  readonly busyID: number | null
  readonly onTorrentPause: (h: string) => void
  readonly onTorrentResume: (h: string) => void
  readonly onTorrentPriority: (h: string, p: StreamPriority) => void
  readonly onTorrentDelete: (h: string) => void
  readonly onPause: (id: number) => void
  readonly onResume: (id: number) => void
  readonly onDelete: (id: number) => void
  readonly onPromote: (d: DownloadEntry) => void
  readonly onStopSeed: (id: number, name: string) => void
  readonly selected: Set<number>
  readonly onToggleSelected: (id: number) => void
  readonly onPlay: (d: DownloadEntry) => void
  readonly onInspect: (d: DownloadEntry) => void
  readonly loading?: boolean
}) {
  const empty = torrents.length === 0 && downloads.length === 0 && !loading

  return (
    <div className="flex flex-col gap-4">
      {!torrentsLoaded && (
        <div className="flex items-center gap-2 text-gray-400 py-12 justify-center">
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

      {torrents.map(t => (
        <TorrentCard
          key={t.infoHash}
          t={t}
          busy={busyHash === t.infoHash}
          onPause={() => onTorrentPause(t.infoHash)}
          onResume={() => onTorrentResume(t.infoHash)}
          onPriority={(p) => onTorrentPriority(t.infoHash, p)}
          onDelete={() => onTorrentDelete(t.infoHash)}
        />
      ))}

      {downloads.map(d => (
        <DownloadCard
          key={d.id}
          d={d}
          live={torrents.find(t => t.infoHash === d.infoHash)}
          busy={busyID === d.id}
          selected={selected.has(d.id)}
          onToggleSelected={() => onToggleSelected(d.id)}
          onPause={() => onPause?.(d.id)}
          onResume={() => onResume?.(d.id)}
          onDelete={() => onDelete(d.id)}
          onPromote={() => onPromote(d)}
          onStopSeed={() => onStopSeed(d.id, d.name || d.filePath)}
          onPlay={() => onPlay(d)}
          onInspect={() => onInspect(d)}
        />
      ))}
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
      <div className="rounded-xl border border-gray-700/50 bg-gradient-to-br from-gray-800/80 to-gray-900/80 backdrop-blur-sm p-6">
        <h3 className="text-sm font-semibold text-gray-300 uppercase tracking-wider flex items-center gap-2 mb-5">
          <Wifi className="w-4 h-4 text-cyan-400" />
          Monitoramento em Tempo Real
        </h3>
        <div className="grid grid-cols-1 sm:grid-cols-3 gap-6">
          <div className="flex flex-col gap-1">
            <span className="text-xs text-gray-500">Download atual</span>
            <span className="text-2xl font-bold text-emerald-400">{formatRate(totalDown)}</span>
          </div>
          <div className="flex flex-col gap-1">
            <span className="text-xs text-gray-500">Upload atual</span>
            <span className="text-2xl font-bold text-violet-400">{formatRate(totalUp)}</span>
          </div>
          <div className="flex flex-col gap-1">
            <span className="text-xs text-gray-500">Peers conectados</span>
            <span className="text-2xl font-bold text-blue-400">{totalPeers}</span>
          </div>
        </div>
      </div>

      {/* Bandwidth limits form */}
      <div className="rounded-xl border border-gray-700/50 bg-gradient-to-br from-gray-800/60 to-gray-900/60 backdrop-blur-sm p-6">
        <h3 className="text-sm font-semibold text-gray-300 uppercase tracking-wider flex items-center gap-2 mb-5">
          <Gauge className="w-4 h-4 text-amber-400" />
          Limites de Velocidade
        </h3>
        <p className="text-xs text-gray-500 mb-4">
          Defina limites em KB/s. Deixe em branco ou 0 para ilimitado.
        </p>
        <div className="grid grid-cols-1 sm:grid-cols-2 gap-4 mb-5">
          <div className="flex flex-col gap-2">
            <label className="text-xs text-gray-400 flex items-center gap-1.5">
              <ArrowDownCircle className="w-3.5 h-3.5 text-emerald-400" />
              Limite de download (KB/s)
            </label>
            <input
              type="number"
              min={0}
              placeholder="Ilimitado"
              value={limitDownKB}
              onChange={e => setLimitDownKB(e.target.value)}
              className="bg-gray-900/80 border border-gray-700 rounded-lg px-3 py-2.5 text-gray-100 text-sm focus:outline-none focus:border-emerald-500 focus:ring-1 focus:ring-emerald-500/30 transition-all"
            />
          </div>
          <div className="flex flex-col gap-2">
            <label className="text-xs text-gray-400 flex items-center gap-1.5">
              <ArrowUpCircle className="w-3.5 h-3.5 text-violet-400" />
              Limite de upload (KB/s)
            </label>
            <input
              type="number"
              min={0}
              placeholder="Ilimitado"
              value={limitUpKB}
              onChange={e => setLimitUpKB(e.target.value)}
              className="bg-gray-900/80 border border-gray-700 rounded-lg px-3 py-2.5 text-gray-100 text-sm focus:outline-none focus:border-violet-500 focus:ring-1 focus:ring-violet-500/30 transition-all"
            />
          </div>
        </div>
        <div className="flex items-center gap-3">
          <button
            onClick={onSaveLimits}
            disabled={limitsSaving}
            className="flex items-center gap-2 text-sm bg-emerald-500/20 hover:bg-emerald-500/30 disabled:opacity-50 text-emerald-300 border border-emerald-500/40 px-5 py-2 rounded-lg transition-all font-medium"
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
      <div className="text-gray-700 mb-3">{icon}</div>
      <h3 className="text-lg font-semibold text-gray-400 mb-1">{title}</h3>
      <p className="text-sm text-gray-600 max-w-md">{description}</p>
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
}

function TorrentCard({ t, busy, onPause, onResume, onPriority, onDelete }: TorrentCardProps) {
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
    borderClass = 'border-gray-600/50 hover:border-gray-500/60'
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
      bg-gradient-to-br from-gray-800/80 to-gray-900/60 backdrop-blur-sm
      p-4 flex flex-col gap-3 transition-all duration-300
    `}>
      {/* Top row: name + badges */}
      <div className="flex items-start justify-between gap-3">
        <div className="min-w-0 flex-1">
          <h3 className="font-semibold text-gray-100 truncate text-sm" title={t.name}>{t.name || t.infoHash}</h3>
          <p className="text-[11px] text-gray-600 truncate mt-0.5 font-mono" title={t.infoHash}>{t.infoHash}</p>
        </div>
        <div className="flex items-center gap-2 flex-shrink-0">
          <KindBadge kind="streaming" />
          <TorrentStatusBadge status={status} />
        </div>
      </div>

      {/* Live rate chips — destaque pro Down/Up/Peers de CADA torrent. Eram
          mostrados em text-xs no rodapé, passavam batido (sintoma "não sei a
          velocidade de cada um"). Agora ficam em fonte maior + chip dedicado.
          Quando tudo zerado (ex.: pausado), só os peers ainda aparecem. */}
      <div className="flex items-center gap-2 flex-wrap text-sm">
        <span
          className={`flex items-center gap-1 px-2 py-0.5 rounded-full font-mono tabular-nums ${
            t.downRate > 0 ? 'bg-emerald-500/15 text-emerald-300 border border-emerald-500/30' : 'text-gray-500'
          }`}
          title="Velocidade de download deste torrent"
        >
          <ArrowDownCircle className="w-3.5 h-3.5" />
          {formatRate(t.downRate)}
        </span>
        <span
          className={`flex items-center gap-1 px-2 py-0.5 rounded-full font-mono tabular-nums ${
            t.upRate > 0 ? 'bg-violet-500/15 text-violet-300 border border-violet-500/30' : 'text-gray-500'
          }`}
          title="Velocidade de upload deste torrent"
        >
          <ArrowUpCircle className="w-3.5 h-3.5" />
          {formatRate(t.upRate)}
        </span>
        <span
          className="flex items-center gap-1 px-2 py-0.5 rounded-full bg-blue-500/10 text-blue-300 border border-blue-500/20 font-mono tabular-nums"
          title="Peers conectados / Seeders no swarm"
        >
          <Users className="w-3.5 h-3.5" />
          {t.peers}{(t.seeders ?? 0) > 0 && <span className="text-gray-500"> / {t.seeders}</span>}
        </span>
      </div>

      {/* Progress bar */}
      <div>
        <div className="h-2 bg-gray-900/80 rounded-full overflow-hidden">
          <div
            className={`h-full rounded-full bg-gradient-to-r ${barGradient} transition-all duration-500 ease-out`}
            style={{ width: `${pct.toFixed(1)}%` }}
          />
        </div>
        {/* Stats row — bytes/% + ETA. Velocidades subiram para os chips acima. */}
        <div className="flex items-center justify-between mt-2 text-xs text-gray-400 gap-3 flex-wrap">
          <span className="text-gray-300 font-medium">
            {formatBytes(Math.round((t.totalSize || 0) * (t.progress || 0)))} / {formatBytes(t.totalSize)}
            <span className="text-gray-500 ml-1">({pct.toFixed(1)}%)</span>
          </span>
          {eta && (
            <span className="flex items-center gap-1 text-gray-500" title="ETA">
              <Clock className="w-3 h-3" /> {eta}
            </span>
          )}
        </div>
      </div>

      {/* Action bar */}
      <div className="flex items-center gap-2 flex-wrap pt-1">
        {isPaused ? (
          <ActionButton onClick={onResume} disabled={busy} variant="success" icon={<Play className="w-3.5 h-3.5" />} label="Retomar" title="Retoma o torrent de onde parou" />
        ) : (
          <ActionButton onClick={onPause} disabled={busy || isComplete} variant="neutral" icon={<Pause className="w-3.5 h-3.5" />} label="Pausar" title="Pausa o torrent (retomável; o que já baixou fica no cache)" />
        )}

        <label className="flex items-center gap-1.5 text-xs text-gray-400">
          <span className="text-gray-500">Prioridade:</span>
          <select
            value={priority}
            onChange={e => onPriority(e.target.value as StreamPriority)}
            disabled={busy}
            className="bg-gray-800 border border-gray-700 rounded-lg px-2 py-1 text-gray-100 text-xs disabled:opacity-50 focus:outline-none focus:border-emerald-500 transition-colors cursor-pointer"
          >
            <option value="low">Baixa</option>
            <option value="normal">Normal</option>
            <option value="high">Alta</option>
          </select>
        </label>

        {busy && <Loader2 className="w-3.5 h-3.5 animate-spin text-gray-500" />}
        <ActionButton onClick={onDelete} disabled={busy} variant="danger" icon={<Trash2 className="w-3.5 h-3.5" />} label="Parar" title="Encerra e remove o torrent do streaming (diferente de Pausar; o cache já baixado não é apagado)" className="ml-auto" />
      </div>
    </div>
  )
}

function downloadBorderClass(completed: boolean, failed: boolean, paused: boolean): string {
  if (completed) return 'border-green-500/30 hover:border-green-500/50'
  if (failed) return 'border-red-500/30 hover:border-red-500/50'
  if (paused) return 'border-gray-600/50 hover:border-gray-500/60'
  return 'border-cyan-500/30 hover:border-cyan-500/50'
}

function downloadBarGradient(completed: boolean, failed: boolean, paused: boolean): string {
  if (completed) return 'from-green-500 to-emerald-400'
  if (failed) return 'from-red-500 to-rose-400'
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
  readonly onToggleSelected?: () => void
  readonly onPause: () => void
  readonly onResume: () => void
  readonly onDelete: () => void
  readonly onPromote?: () => void
  readonly onStopSeed?: () => void
  readonly onPlay?: () => void
  readonly onInspect?: () => void
}

function DownloadCard({ d, live, busy, selected, onToggleSelected, onPause, onResume, onDelete, onPromote, onStopSeed, onPlay, onInspect }: DownloadCardProps) {
  const { isGuest } = useAuth()
  const pct = Math.max(0, Math.min(1, d.progress || 0)) * 100
  const isCompleted = d.status === 'completed'
  const isFailed = d.status === 'failed'
  const isPaused = d.status === 'paused'
  const isActive = d.status === 'downloading' || d.status === 'queued'
  const isStalled = d.status === 'downloading' && (d.downRate ?? 0) === 0 && d.bytesDownloaded < d.fileSize

  const etaText = computeETA(d)
  const borderClass = downloadBorderClass(isCompleted, isFailed, isPaused)
  const barGradient = downloadBarGradient(isCompleted, isFailed, isPaused)

  return (
    <div className={`
      relative overflow-hidden rounded-xl border ${borderClass}
      bg-gradient-to-br from-gray-800/80 to-gray-900/60 backdrop-blur-sm
      p-4 flex flex-col gap-3 transition-all duration-300
    `}>
      {/* Top row */}
      <div className="flex items-start justify-between gap-3">
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
          <div className="flex items-center gap-2">
            <h3 className="font-semibold text-gray-100 truncate text-sm" title={d.name}>{d.name || d.filePath}</h3>
            {d.username && (
              <span className="flex-shrink-0 text-[10px] px-1.5 py-0.5 rounded-md bg-violet-500/15 text-violet-300 border border-violet-500/30 font-medium">
                {d.username}
              </span>
            )}
          </div>
          <p className="text-[11px] text-gray-600 truncate mt-0.5" title={d.filePath}>{d.filePath}</p>
        </div>
        <div className="flex items-center gap-2 flex-shrink-0">
          <KindBadge kind="server" />
          {isStalled && (
            <span className="inline-flex items-center gap-1 text-[10px] px-2 py-0.5 rounded-md border font-medium bg-amber-500/15 text-amber-300 border-amber-500/30" title="Download sem progresso — sem peers ou bloqueado">
              <AlertTriangle className="w-3 h-3" /> Travado
            </span>
          )}
          {d.status === 'completed' && (
            <span className="inline-flex items-center gap-1 text-[10px] px-2 py-0.5 rounded-md border font-medium bg-emerald-500/15 text-emerald-300 border-emerald-500/30">
              <HardDrive className="w-3 h-3 text-emerald-400" /> no disco
            </span>
          )}
          <DownloadStatusBadge status={d.status} />
        </div>
      </div>

      {/* Live activity chips — só quando o anacrolix tem o torrent ativo (ou
          baixando, ou seedando depois de concluído). Mesmo formato visual do
          TorrentCard pra consistência. */}
      {live && (live.downRate > 0 || live.upRate > 0 || live.peers > 0) && (
        <div className="flex items-center gap-2 flex-wrap text-sm">
          <span
            className={`flex items-center gap-1 px-2 py-0.5 rounded-full font-mono tabular-nums ${
              live.downRate > 0 ? 'bg-emerald-500/15 text-emerald-300 border border-emerald-500/30' : 'text-gray-500'
            }`}
            title="Download deste torrent"
          >
            <ArrowDownCircle className="w-3.5 h-3.5" />
            {formatRate(live.downRate)}
          </span>
          <span
            className={`flex items-center gap-1 px-2 py-0.5 rounded-full font-mono tabular-nums ${
              live.upRate > 0 ? 'bg-violet-500/15 text-violet-300 border border-violet-500/30' : 'text-gray-500'
            }`}
            title="Upload deste torrent (seedando)"
          >
            <ArrowUpCircle className="w-3.5 h-3.5" />
            {formatRate(live.upRate)}
          </span>
          <span
            className="flex items-center gap-1 px-2 py-0.5 rounded-full bg-blue-500/10 text-blue-300 border border-blue-500/20 font-mono tabular-nums"
            title="Peers conectados / Seeders no swarm"
          >
            <Users className="w-3.5 h-3.5" />
            {live.peers}{(live.seeders ?? 0) > 0 && <span className="text-gray-500"> / {live.seeders}</span>}
          </span>
        </div>
      )}

      {/* Progress bar */}
      <div>
        <div className="h-2 bg-gray-900/80 rounded-full overflow-hidden">
          <div
            className={`h-full rounded-full bg-gradient-to-r ${barGradient} transition-all duration-500 ease-out`}
            style={{ width: `${pct.toFixed(1)}%` }}
          />
        </div>
        <div className="flex items-center justify-between mt-2 text-xs text-gray-400">
          <span className="text-gray-300 font-medium">
            {formatBytes(d.bytesDownloaded)} / {formatBytes(d.fileSize)}
            <span className="text-gray-500 ml-1">({pct.toFixed(1)}%)</span>
          </span>
          {!isCompleted && !isFailed && etaText && (
            <span className="flex items-center gap-1 text-gray-500">
              <Clock className="w-3 h-3" /> {etaText}
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
        <div className="flex items-start gap-2 text-xs text-red-300 bg-red-500/10 border border-red-500/20 rounded-lg px-3 py-2">
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
      <span className="inline-flex items-center gap-1 text-[10px] font-bold uppercase tracking-wider px-2 py-0.5 rounded-md bg-gradient-to-r from-emerald-500/20 to-teal-500/20 text-emerald-300 border border-emerald-500/30">
        <Activity className="w-2.5 h-2.5" />
        Streaming
      </span>
    )
  }
  return (
    <span className="inline-flex items-center gap-1 text-[10px] font-bold uppercase tracking-wider px-2 py-0.5 rounded-md bg-gradient-to-r from-cyan-500/20 to-blue-500/20 text-cyan-300 border border-cyan-500/30">
      <Server className="w-2.5 h-2.5" />
      Servidor
    </span>
  )
}

function TorrentStatusBadge({ status }: { readonly status: NonNullable<TorrentInfo['status']> }) {
  const map: Record<NonNullable<TorrentInfo['status']>, { label: string; cls: string; icon: React.ReactNode }> = {
    downloading: { label: 'Baixando',  cls: 'bg-emerald-500/15 text-emerald-300 border-emerald-500/30', icon: <Loader2 className="w-3 h-3 animate-spin" /> },
    paused:      { label: 'Pausado',   cls: 'bg-gray-500/15 text-gray-300 border-gray-500/30',          icon: <Pause className="w-3 h-3" /> },
    seeding:     { label: 'Semeando',  cls: 'bg-violet-500/15 text-violet-300 border-violet-500/30',    icon: <ArrowUpCircle className="w-3 h-3" /> },
    complete:    { label: 'Completo',  cls: 'bg-green-500/15 text-green-300 border-green-500/30',       icon: <CheckCircle2 className="w-3 h-3" /> },
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
    queued:      { label: 'Na fila',     cls: 'bg-gray-700/50 text-gray-300 border-gray-600/50',         icon: <Clock className="w-3 h-3" /> },
    downloading: { label: 'Baixando',    cls: 'bg-cyan-500/15 text-cyan-300 border-cyan-500/30',         icon: <Loader2 className="w-3 h-3 animate-spin" /> },
    completed:   { label: 'Concluído',   cls: 'bg-green-500/15 text-green-300 border-green-500/30',      icon: <CheckCircle2 className="w-3 h-3" /> },
    failed:      { label: 'Falhou',      cls: 'bg-red-500/15 text-red-300 border-red-500/30',            icon: <AlertCircle className="w-3 h-3" /> },
    paused:      { label: 'Pausado',     cls: 'bg-gray-500/15 text-gray-300 border-gray-500/30',         icon: <Pause className="w-3 h-3" /> },
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
    success: 'bg-emerald-500/10 hover:bg-emerald-500/20 text-emerald-300 border-emerald-500/30',
    danger:  'bg-red-500/10 hover:bg-red-500/20 text-red-300 border-red-500/30',
    neutral: 'bg-gray-700/60 hover:bg-gray-700 text-gray-300 border-gray-600/60',
    info:    'bg-blue-500/10 hover:bg-blue-500/20 text-blue-300 border-blue-500/30',
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
  if (activeTab === tabKey) return 'bg-emerald-500/20 text-emerald-300'
  if (tabKey === 'failed') return 'bg-red-500/20 text-red-400'
  return 'bg-gray-700 text-gray-400'
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
