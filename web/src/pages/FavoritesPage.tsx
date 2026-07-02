import { useState, useEffect, useMemo, useRef } from 'react'
import { Heart, Loader2, Trash2, Play, Clock, FileVideo, FolderPlus, Folder, FolderOpen, ChevronRight, ChevronDown, Pencil, Inbox, Download, X, UploadCloud, Search, CheckSquare, Square, Eye, EyeOff, RefreshCw } from 'lucide-react'
import {
  favoritesList, favoriteRemove, StreamFavorite,
  FavoriteFolder, folderList, folderCreate, folderRename, folderDelete, folderSetHidden, favoriteSetFolder,
  streamImport, SearchResult,
} from '../api/client'
import NavHeader from '../components/NavHeader'
import DownloadModal from '../components/DownloadModal'
import PullToRefreshIndicator from '../components/PullToRefreshIndicator'
import Thumbnail from '../components/Thumbnail'
import SeedBadge from '../components/SeedBadge'
import FavoritesSortControl from '../components/FavoritesSortControl'
import TorrentContentsModal from '../components/TorrentContentsModal'
import { useScrollRestoration } from '../lib/useScrollRestoration'
import { Sheet } from '../components/Sheet'
import { useConfirm } from '../components/ConfirmDialog'
import { useAuth } from '../auth/AuthContext'
import { usePullToRefresh } from '../lib/usePullToRefresh'
import { usePlayer } from '../components/PlayerProvider'
import { useRevealHidden } from '../lib/reveal'
import { newTabProps, playHref } from '../lib/cardNav'
import { formatDate } from '../lib/format'
import { SortKey, SortDir, sortFavorites } from '../lib/favSort'

type FolderNode = {
  folder: FavoriteFolder
  children: FolderNode[]
}

// Build a tree from a flat folder list. Each node holds its children sorted
// by position then name. Roots = nodes whose parentId is null.
function buildTree(folders: FavoriteFolder[]): FolderNode[] {
  const byId = new Map<number, FolderNode>()
  folders.forEach(f => byId.set(f.id, { folder: f, children: [] }))
  const roots: FolderNode[] = []
  folders.forEach(f => {
    const node = byId.get(f.id)!
    if (f.parentId == null) roots.push(node)
    else {
      const parent = byId.get(f.parentId)
      if (parent) parent.children.push(node)
      else roots.push(node) // orphaned (parent deleted) → render at root
    }
  })
  return roots
}

// Achata a árvore em uma lista ordenada (DFS) com a profundidade de cada nó —
// usada pelo dropdown de pasta no mobile pra indentar visualmente as subpastas.
function flattenTree(nodes: FolderNode[], depth = 0): { folder: FavoriteFolder; depth: number }[] {
  return nodes.flatMap(node => [
    { folder: node.folder, depth },
    ...flattenTree(node.children, depth + 1),
  ])
}

async function importTorrentB64(files: File[], viewMode: number | null, ALL_VIEW: number): Promise<{ ok: number; fails: string[] }> {
  let ok = 0
  const fails: string[] = []
  for (const file of files) {
    try {
      const buf = await file.arrayBuffer()
      // Byte→binary-string in 32KB chunks. The old char-by-char `bin +=` was
      // O(n²) and stalled (read as "import failed") on real .torrent files.
      const bytes = new Uint8Array(buf)
      let bin = ''
      const CHUNK = 0x8000
      for (let i = 0; i < bytes.length; i += CHUNK) {
        bin += String.fromCodePoint(...bytes.subarray(i, i + CHUNK))
      }
      await streamImport({ torrentB64: btoa(bin), folderId: viewMode === ALL_VIEW ? null : viewMode })
      ok++
    } catch (e: unknown) {
      fails.push(`${file.name}: ${e instanceof Error ? e.message : String(e)}`)
    }
  }
  return { ok, fails }
}

function buildImportMsg(ok: number, failCount: number, firstFail: string | undefined, suffix: string): { kind: 'ok' | 'err'; text: string } {
  if (failCount === 0) {
    const plural = ok === 1 ? '' : 's'
    return { kind: 'ok', text: `${ok} torrent${plural} importado${plural}${suffix}` }
  }
  return { kind: 'err', text: `${ok} ok, ${failCount} falha(s): ${firstFail}${suffix}` }
}

type TreeProps = {
  readonly nodes: FolderNode[]
  readonly depth: number
  readonly selectedId: number | null
  readonly expanded: Set<number>
  readonly editingId: number | null
  readonly onSelect: (id: number | null) => void
  readonly onToggle: (id: number) => void
  readonly onStartEdit: (id: number) => void
  readonly onCommitEdit: (id: number, name: string) => void
  readonly onCancelEdit: () => void
  readonly onDelete: (id: number) => void
  readonly onCreateSub: (parentId: number) => void
  readonly onToggleHidden: (id: number, hidden: boolean) => void
  readonly onDropOnFolder: (folderId: number, favoriteName: string) => void
}

function FolderTree(p: TreeProps) {
  return (
    <ul className="flex flex-col gap-0.5">
      {p.nodes.map(node => {
        const isOpen = p.expanded.has(node.folder.id)
        const isSelected = p.selectedId === node.folder.id
        const isEditing = p.editingId === node.folder.id
        return (
            <li key={node.folder.id}>
            <button
              type="button"
              className={`group flex items-center gap-1 px-2 py-1 rounded-md text-sm transition-colors w-full text-left ${
                isSelected ? 'bg-pink-500/15 text-pink-700 dark:text-pink-200 border border-pink-500/30' : 'text-text-primary hover:bg-surface-secondary border border-transparent'
              }`}
              style={{ paddingLeft: `${depthIndent(p.depth)}px` }}
              onDragOver={e => { e.preventDefault(); e.dataTransfer.dropEffect = 'move' }}
              onDrop={e => {
                e.preventDefault()
                const name = e.dataTransfer.getData('text/x-favorite-name')
                if (name) p.onDropOnFolder(node.folder.id, name)
              }}
              onClick={() => p.onSelect(node.folder.id)}
            >
              {node.children.length > 0 ? (
                <button onClick={() => p.onToggle(node.folder.id)} className="text-text-muted hover:text-text-primary">
                  {isOpen ? <ChevronDown className="w-3 h-3" /> : <ChevronRight className="w-3 h-3" />}
                </button>
              ) : (
                <span className="w-3" />
              )}
              {isOpen ? <FolderOpen className="w-3.5 h-3.5 text-pink-400" /> : <Folder className="w-3.5 h-3.5 text-text-muted" />}
              {node.folder.hidden && <EyeOff className="w-3 h-3 text-amber-400 flex-shrink-0" aria-label="pasta oculta" />}
              {isEditing ? (
                <input
                  autoFocus
                  defaultValue={node.folder.name}
                  onBlur={e => p.onCommitEdit(node.folder.id, e.currentTarget.value)}
                  onKeyDown={e => {
                    if (e.key === 'Enter') p.onCommitEdit(node.folder.id, e.currentTarget.value)
                    if (e.key === 'Escape') p.onCancelEdit()
                  }}
                  className="flex-1 bg-surface border border-default rounded px-1 text-xs text-text-primary focus:outline-none focus:border-pink-500"
                />
              ) : (
                <button
                  onClick={() => p.onSelect(node.folder.id)}
                  onDoubleClick={() => p.onStartEdit(node.folder.id)}
                  className="flex-1 min-w-0 text-left truncate"
                  title={node.folder.name}
                >
                  {node.folder.name}
                </button>
              )}
              <div className="max-sm:opacity-100 opacity-0 group-hover:opacity-100 flex items-center gap-0.5 transition-opacity">
                <button onClick={() => p.onCreateSub(node.folder.id)} title="Subpasta" className="p-0.5 text-text-muted hover:text-text-primary">
                  <FolderPlus className="w-3 h-3" />
                </button>
                <button onClick={() => p.onStartEdit(node.folder.id)} title="Renomear" className="p-0.5 text-text-muted hover:text-text-primary">
                  <Pencil className="w-3 h-3" />
                </button>
                <button onClick={() => p.onToggleHidden(node.folder.id, !node.folder.hidden)} title={node.folder.hidden ? 'Mostrar pasta' : 'Ocultar pasta'} className="p-0.5 text-text-muted hover:text-amber-400">
                  {node.folder.hidden ? <Eye className="w-3 h-3" /> : <EyeOff className="w-3 h-3" />}
                </button>
                <button onClick={() => p.onDelete(node.folder.id)} title="Excluir" className="p-0.5 text-text-muted hover:text-red-400">
                  <Trash2 className="w-3 h-3" />
                </button>
              </div>
            </button>
            {isOpen && node.children.length > 0 && (
              <FolderTree {...p} nodes={node.children} depth={p.depth + 1} />
            )}
          </li>
        )
      })}
    </ul>
  )
}

const depthIndent = (depth: number) => 8 + depth * 14

function rootFolderClass(viewMode: number | null, dropOnRoot: boolean): string {
  if (viewMode === null) return 'bg-pink-500/15 text-pink-700 dark:text-pink-200 border border-pink-500/30'
  if (dropOnRoot) return 'bg-pink-500/20 border border-pink-500/50 text-pink-700 dark:text-pink-100'
  return 'text-text-primary hover:bg-surface-secondary border border-transparent'
}

function pageTitle(viewMode: number | null, ALL_VIEW: number, folders: FavoriteFolder[]): string {
  if (viewMode === ALL_VIEW) return 'Todos os favoritos'
  if (viewMode === null) return 'Sem pasta'
  return folders.find(f => f.id === viewMode)?.name || 'Favoritos'
}

function renderFavsContent(loading: boolean, error: string, filteredFavs: StreamFavorite[], viewMode: number | null, ALL_VIEW: number, _folders: FavoriteFolder[]): JSX.Element | null {
  if (loading) {
    return <div className="flex items-center justify-center py-20 text-text-muted">
      <Loader2 className="w-8 h-8 animate-spin" />
    </div>
  }
  if (error) {
    return <div className="card text-red-400 text-sm">Erro: {error}</div>
  }
  if (filteredFavs.length === 0) {
    const insideFolder = viewMode !== ALL_VIEW
    return <div className="flex flex-col items-center justify-center py-20 text-text-muted">
      <Heart className="w-16 h-16 mb-4 opacity-30" />
      <p className="text-xl font-medium">Nenhum favorito {insideFolder ? 'nessa pasta' : 'ainda'}</p>
      <p className="text-sm mt-2 text-center max-w-md">
        {viewMode === ALL_VIEW
          ? 'Abra um torrent no player e clique no ♥ no canto superior.'
          : 'Arraste favoritos da view "Todos" pra esta pasta.'}
      </p>
    </div>
  }
  return null
}

export default function FavoritesPage() {
  const { isAdmin } = useAuth()
  const [favs, setFavs] = useState<StreamFavorite[]>([])
  const [folders, setFolders] = useState<FavoriteFolder[]>([])
  const [loading, setLoading] = useState(true)
  useScrollRestoration(!loading)
  const [error, setError] = useState('')
  // The hidden curtain is now GLOBAL: the easter egg lives on the header logo
  // (7 taps) and reveals hidden folders/favourites here as well as Continue
  // Watching, downloads and local. Session-only — see lib/reveal.
  const [revealHidden] = useRevealHidden()
  const { playSingle } = usePlayer()
  const confirm = useConfirm()
  // Favorito sendo enviado ao modal de download (destino + seleção de arquivos).
  const [downloadTarget, setDownloadTarget] = useState<SearchResult | null>(null)
  // Dropdown de pasta no mobile (a sidebar é hidden md:block — sem isto não dá
  // pra trocar de pasta no celular).
  const [folderSheetOpen, setFolderSheetOpen] = useState(false)

  // Tree UI state
  const [selectedFolderId, setSelectedFolderId] = useState<number | null>(null)
  const ALL_VIEW = -1 as number  // sentinel for "Todos" view (no filter)
  const [viewMode, setViewMode] = useState<number | null>(ALL_VIEW)
  const [expanded, setExpanded] = useState<Set<number>>(new Set())
  const [editingId, setEditingId] = useState<number | null>(null)
  const [dropOnRoot, setDropOnRoot] = useState(false) // visual flag for drop indicator on "root" zone
  const [contentsTarget, setContentsTarget] = useState<SearchResult | null>(null)
  const newFolderInput = useRef<HTMLInputElement>(null)
  const [creatingRoot, setCreatingRoot] = useState(false)

  // Search
  const [searchQuery, setSearchQuery] = useState('')

  // Sort order (date/name/seeds/size + direction). Default mirrors the legacy
  // behaviour: most recently added first.
  const [sortBy, setSortBy] = useState<SortKey>('date')
  const [sortDir, setSortDir] = useState<SortDir>('desc')

  // Silent re-fetch (no loading spinner) — keeps scroll/selection. Used after a
  // bulk seed refresh so sorting by seeds picks up the freshly probed numbers.
  const reloadFavsQuiet = async () => {
    try {
      const favsList = await favoritesList(revealHidden)
      setFavs(favsList || [])
    } catch { /* keep the current list on a transient failure */ }
  }

  // "Atualizar seeds": bump this counter to make every SeedBadge in the current
  // view re-probe the swarm at once. The backend dedupes + caps to 3 concurrent
  // probes, so firing one per visible card is safe.
  const [seedRefresh, setSeedRefresh] = useState(0)
  const [seedRefreshing, setSeedRefreshing] = useState(false)
  const refreshSeeds = () => {
    setSeedRefresh(n => n + 1)
    setSeedRefreshing(true)
    // Visual ack only — each badge owns its own probing spinner afterwards.
    setTimeout(() => setSeedRefreshing(false), 1500)
    // Probes persist the snapshot to the metadata cache; re-pull a bit later so
    // a "sort by seeds" reflects the new counts without a manual refresh.
    setTimeout(() => { void reloadFavsQuiet() }, 11000)
  }

  // Filter favorites by current view AND search query, then sort.
  const filteredFavs = useMemo(() => {
    let list = favs
    if (viewMode !== ALL_VIEW) {
      if (viewMode === null) list = list.filter(f => f.folderId === null)
      else list = list.filter(f => f.folderId === viewMode)
    }
    if (searchQuery.trim()) {
      const q = searchQuery.toLowerCase()
      list = list.filter(f => f.name.toLowerCase().includes(q))
    }
    return sortFavorites(list, sortBy, sortDir)
  }, [favs, viewMode, searchQuery, sortBy, sortDir])

  const load = async () => {
    setLoading(true)
    setError('')
    try {
      const [favsList, foldersList] = await Promise.all([favoritesList(revealHidden), folderList(revealHidden)])
      setFavs(favsList || [])
      setFolders(foldersList || [])
    } catch (e: unknown) {
      setError(e instanceof Error ? e.message : String(e))
    } finally {
      setLoading(false)
    }
  }

  // Infinite scroll
  const PAGE_SIZE = 40
  const [visible, setVisible] = useState(PAGE_SIZE)
  const sentinelRef = useRef<HTMLDivElement>(null)

  // IntersectionObserver for infinite scroll
  useEffect(() => {
    const el = sentinelRef.current
    if (!el) return
    const observer = new IntersectionObserver(([entry]) => {
      if (entry.isIntersecting) {
        setVisible(prev => Math.min(prev + PAGE_SIZE, filteredFavs.length))
      }
    }, { rootMargin: '200px' })
    observer.observe(el)
    return () => observer.disconnect()
  }, [filteredFavs.length])

  // Load on mount and whenever the hidden curtain is toggled (re-fetch with/without
  // the hidden folders + their favourites).
  // eslint-disable-next-line react-hooks/exhaustive-deps
  useEffect(() => { load() }, [revealHidden])

  const handleToggleHidden = async (id: number, hidden: boolean) => {
    await folderSetHidden(id, hidden)
    // Quando esconde a pasta selecionada sem o modo revelado, volta pra raiz.
    if (hidden && !revealHidden && viewMode === id) { setSelectedFolderId(null); setViewMode(ALL_VIEW) }
    load()
  }

  const ptr = usePullToRefresh({ onRefresh: load, disabled: loading })

  const tree = useMemo(() => buildTree(folders), [folders])

  const handleCreateRoot = async () => {
    if (!newFolderInput.current) return
    const name = newFolderInput.current.value.trim()
    if (!name) { setCreatingRoot(false); return }
    const f = await folderCreate(name, null)
    setFolders([...folders, f])
    setCreatingRoot(false)
    if (newFolderInput.current) newFolderInput.current.value = ''
  }

  // ─── Import (magnet / .torrent) ──────────────────────────────────────────
  const [showImport, setShowImport] = useState(false)
  const [magnetInput, setMagnetInput] = useState('')
  const [importing, setImporting] = useState(false)
  const [importMsg, setImportMsg] = useState<{ kind: 'ok' | 'err'; text: string } | null>(null)
  const [dragOverDrop, setDragOverDrop] = useState(false)

  const importMagnets = async () => {
    const lines = magnetInput.split('\n').map(l => l.trim()).filter(Boolean)
    if (lines.length === 0) return
    setImporting(true)
    setImportMsg(null)
    let ok = 0
    const fails: string[] = []
    for (const magnet of lines) {
      try {
        await streamImport({ magnet, folderId: viewMode === ALL_VIEW ? null : viewMode })
        ok++
      } catch (e: unknown) {
        fails.push(e instanceof Error ? e.message : String(e))
      }
    }
    setImporting(false)
    setMagnetInput('')
    let msg: { kind: 'ok' | 'err'; text: string }
    if (fails.length === 0) {
      const plural = ok === 1 ? '' : 's'
      msg = { kind: 'ok', text: `${ok} torrent${plural} importado${plural}` }
    } else {
      msg = { kind: 'err', text: `${ok} ok, ${fails.length} falha(s): ${fails[0]}` }
    }
    setImportMsg(msg)
    await load()
  }

  const importTorrentFiles = async (files: File[]) => {
    const torrents = files.filter(f => f.name.toLowerCase().endsWith('.torrent'))
    const skipped = files.length - torrents.length
    if (torrents.length === 0) {
      setImportMsg({ kind: 'err', text: 'Selecione ao menos um arquivo .torrent' })
      return
    }
    setImporting(true)
    setImportMsg(null)
    const { ok, fails } = await importTorrentB64(torrents, viewMode, ALL_VIEW)
    setImporting(false)
    const pluralSuffix = skipped === 1 ? '' : 's'
    const suffix = skipped > 0 ? ` (${skipped} ignorado${pluralSuffix} — não .torrent)` : ''
    setImportMsg(buildImportMsg(ok, fails.length, fails[0], suffix))
    await load()
  }

  const handleRemove = async (name: string) => {
    const ok = await confirm({ title: 'Remover favorito', message: `Remover "${name}" dos favoritos?`, confirmLabel: 'Remover', destructive: true })
    if (!ok) return
    await favoriteRemove(name)
    setFavs(favs.filter(f => f.name !== name))
  }

  // favToResult builds the SearchResult shape the player/contents modal expect.
  const favToResult = (f: StreamFavorite): SearchResult => ({
    title: f.name, tracker: '', categoryId: 0, category: '', size: 0, seeders: 0, leechers: 0,
    age: '', magnetUri: f.magnet, link: '', infoHash: f.infoHash, publishDate: '',
  })

  const favHasValidMagnet = (f: StreamFavorite) =>
    !!f.magnet && (f.magnet.startsWith('magnet:') || f.magnet.startsWith('http'))

  const handleFavDragStart = (e: React.DragEvent, favName: string) => {
    e.dataTransfer.setData('text/x-favorite-name', favName)
    e.dataTransfer.effectAllowed = 'move'
    const ghost = document.createElement('div')
    ghost.textContent = favName
    ghost.style.cssText = 'position:fixed;top:-1000px;left:0;max-width:220px;overflow:hidden;text-overflow:ellipsis;white-space:nowrap;padding:6px 12px;background:#16a34a;color:#fff;font-size:12px;border-radius:8px;box-shadow:0 4px 12px rgba(0,0,0,.4);pointer-events:none'
    document.body.appendChild(ghost)
    e.dataTransfer.setDragImage(ghost, 12, 12)
    setTimeout(() => ghost.remove(), 0)
  }

  const playFavorite = (f: StreamFavorite) => {
    if (!favHasValidMagnet(f)) {
      alert('Magnet inválido nesse favorito. Refavorite via busca para reabilitar Play.')
      return
    }
    playSingle(favToResult(f))
  }

  // "Baixar": abre o modal unificado (destino + seleção de arquivos/árvore),
  // como na busca. Antes baixava o torrent inteiro direto, sem perguntar nada.
  const downloadFavorite = (fav: StreamFavorite) => {
    if (!favHasValidMagnet(fav)) {
      alert('Magnet inválido nesse favorito. Refavorite via busca para reabilitar o download.')
      return
    }
    setDownloadTarget(favToResult(fav))
  }

  const openContents = (f: StreamFavorite) => {
    if (!favHasValidMagnet(f)) {
      alert('Magnet inválido nesse favorito. Refavorite via busca para reabilitar Play.')
      return
    }
    setContentsTarget(favToResult(f))
  }

  const handleCreateSub = async (parentId: number) => {
    const name = prompt('Nome da subpasta:')
    if (!name) return
    const f = await folderCreate(name, parentId)
    setFolders([...folders, f])
    setExpanded(new Set(expanded).add(parentId))
  }

  const handleRename = async (id: number, name: string) => {
    setEditingId(null)
    const trimmed = name.trim()
    if (!trimmed) return
    await folderRename(id, trimmed)
    setFolders(folders.map(f => f.id === id ? { ...f, name: trimmed } : f))
  }

  const handleDeleteFolder = async (id: number) => {
    const target = folders.find(f => f.id === id)
    const ok = await confirm({ title: 'Excluir pasta', message: `Excluir pasta "${target?.name}"? Favoritos dentro voltam pra raiz.`, confirmLabel: 'Excluir', destructive: true })
    if (!ok) return
    await folderDelete(id)
    setFolders(folders.filter(f => f.id !== id))
    // Favoritos: server fez SET NULL, recarrego pra refletir
    const fresh = await favoritesList()
    setFavs(fresh || [])
    if (selectedFolderId === id) setViewMode(ALL_VIEW)
  }

  const handleDropOnFolder = async (folderId: number | null, favoriteName: string) => {
    setDropOnRoot(false)
    await favoriteSetFolder(favoriteName, folderId)
    setFavs(favs.map(f => f.name === favoriteName ? { ...f, folderId } : f))
  }

  // Versões prompt-based pro sheet do mobile (sem a sidebar/edição inline do
  // desktop): criar pasta raiz e renomear via prompt nativo (usável no iOS).
  const handleCreateRootPrompt = async () => {
    const name = prompt('Nome da nova pasta:')?.trim()
    if (!name) return
    const f = await folderCreate(name, null)
    setFolders([...folders, f])
  }
  const handleRenamePrompt = (folder: FavoriteFolder) => {
    const name = prompt('Renomear pasta:', folder.name)
    if (name != null) void handleRename(folder.id, name)
  }

  // ─── Multi-select (move several favorites to a folder at once) ─────────────
  const [selected, setSelected] = useState<Set<string>>(new Set())
  const toggleSelected = (name: string) => {
    setSelected(prev => {
      const next = new Set(prev)
      if (next.has(name)) {
        next.delete(name)
      } else {
        next.add(name)
      }
      return next
    })
  }
  const clearSelection = () => setSelected(new Set())
  const moveSelectedToFolder = async (folderId: number | null) => {
    const names = [...selected]
    await Promise.all(names.map(n => favoriteSetFolder(n, folderId).catch(() => {})))
    setFavs(favs.map(f => selected.has(f.name) ? { ...f, folderId } : f))
    clearSelection()
  }

  return (
    <div className="min-h-screen bg-surface flex flex-col">
      <PullToRefreshIndicator pull={ptr.pull} progress={ptr.progress} refreshing={ptr.refreshing} />
      <NavHeader />

      <main className="flex-1 max-w-7xl 2xl:max-w-[min(95vw,1600px)] mx-auto w-full px-4 py-6 flex flex-col md:flex-row gap-4">
        {/* Sidebar — folder tree (oculta no mobile pra não comprimir o conteúdo) */}
        <aside className="w-64 flex-shrink-0 hidden md:block">
          <div className="flex items-center justify-between mb-2">
            <h2 className="text-xs uppercase tracking-wider text-text-muted cursor-default select-none" title={revealHidden ? 'Pastas ocultas visíveis' : undefined}>
              Pastas{revealHidden && <Eye className="inline w-3 h-3 ml-1 text-amber-400" aria-label="ocultas visíveis" />}
            </h2>
            <button
              onClick={() => setCreatingRoot(true)}
              title="Nova pasta"
              className="p-1 text-text-muted hover:text-pink-400"
            >
              <FolderPlus className="w-4 h-4" />
            </button>
          </div>

          {/* Special views */}
          <ul className="flex flex-col gap-0.5 mb-2">
            <li>
              <button
                onClick={() => { setViewMode(ALL_VIEW); setSelectedFolderId(null) }}
                onKeyDown={e => { if (e.key === 'Enter' || e.key === ' ') { e.preventDefault(); setViewMode(ALL_VIEW); setSelectedFolderId(null) } }}
                className={`w-full flex items-center gap-2 px-2 py-1 rounded-md text-sm transition-colors ${
                  viewMode === ALL_VIEW ? 'bg-pink-500/15 text-pink-700 dark:text-pink-200 border border-pink-500/30' : 'text-text-primary hover:bg-surface-secondary border border-transparent'
                }`}
              >
                <Heart className="w-3.5 h-3.5 fill-current" />
                Todos
                <span className="ml-auto text-[10px] text-text-muted">{favs.length}</span>
              </button>
            </li>
            <li>
              <button
                onDragOver={e => { e.preventDefault(); setDropOnRoot(true) }}
                onDragLeave={() => setDropOnRoot(false)}
                onDrop={e => {
                  e.preventDefault()
                  const name = e.dataTransfer.getData('text/x-favorite-name')
                  if (name) handleDropOnFolder(null, name)
                }}
                onClick={() => { setViewMode(null); setSelectedFolderId(null) }}
                onKeyDown={e => { if (e.key === 'Enter' || e.key === ' ') { e.preventDefault(); setViewMode(null); setSelectedFolderId(null) } }}
                className={`w-full flex items-center gap-2 px-2 py-1 rounded-md text-sm transition-colors ${rootFolderClass(viewMode, dropOnRoot)}`}
              >
                <Inbox className="w-3.5 h-3.5" />
                Sem pasta
                <span className="ml-auto text-[10px] text-text-muted">{favs.filter(f => f.folderId == null).length}</span>
              </button>
            </li>
          </ul>

          {/* New root folder input */}
          {creatingRoot && (
            <div className="mb-2">
              <input
                ref={newFolderInput}
                autoFocus
                placeholder="Nome da pasta"
                onBlur={handleCreateRoot}
                onKeyDown={e => {
                  if (e.key === 'Enter') handleCreateRoot()
                  if (e.key === 'Escape') setCreatingRoot(false)
                }}
                className="w-full bg-surface border border-default rounded px-2 py-1 text-xs text-text-primary focus:outline-none focus:border-pink-500"
              />
            </div>
          )}

          {/* Folder tree */}
          <FolderTree
            nodes={tree}
            depth={0}
            selectedId={selectedFolderId}
            expanded={expanded}
            editingId={editingId}
            onSelect={id => { setSelectedFolderId(id); setViewMode(id) }}
            onToggle={id => setExpanded(prev => {
              const next = new Set(prev)
              if (next.has(id)) next.delete(id); else next.add(id)
              return next
            })}
            onStartEdit={setEditingId}
            onCommitEdit={handleRename}
            onCancelEdit={() => setEditingId(null)}
            onDelete={handleDeleteFolder}
            onCreateSub={handleCreateSub}
            onToggleHidden={handleToggleHidden}
            onDropOnFolder={(fid, name) => handleDropOnFolder(fid, name)}
          />
        </aside>

        {/* Main — favorites grid */}
        <section className="flex-1 min-w-0">
          {/* Dropdown de pasta — só no mobile (a sidebar é hidden md:block). */}
          <button
            onClick={() => setFolderSheetOpen(true)}
            className="md:hidden w-full flex items-center gap-2 px-3 min-h-[44px] mb-3 rounded-lg bg-surface-secondary border border-default text-sm text-text-primary"
          >
            <Folder className="w-4 h-4 text-pink-400 flex-shrink-0" />
            <span className="truncate flex-1 text-left">{pageTitle(viewMode, ALL_VIEW, folders)}</span>
            <ChevronDown className="w-4 h-4 text-text-muted flex-shrink-0" />
          </button>
          <div className="flex items-center justify-between flex-wrap gap-3 mb-4">
            <div className="flex items-center gap-3">
              <Heart className="w-5 h-5 text-pink-400 fill-current" />
              <h1 className="text-lg font-semibold text-text-primary">{pageTitle(viewMode, ALL_VIEW, folders)}</h1>
              {!loading && (
                <span className="text-xs text-text-muted bg-surface-secondary border border-default px-2 py-0.5 rounded-full">
                  {filteredFavs.length} item{filteredFavs.length === 1 ? '' : 's'}
                </span>
              )}
              {isAdmin && (
                <span className="text-[10px] uppercase bg-yellow-500/20 text-yellow-400 border border-yellow-500/30 px-2 py-0.5 rounded">
                  Admin · vê todos
                </span>
              )}
            </div>
            <div className="flex items-center gap-3">
              {filteredFavs.length > 0 && (
                <button
                  onClick={refreshSeeds}
                  disabled={seedRefreshing}
                  title="Reverificar seeds de todos os favoritos nesta visão"
                  className="flex items-center gap-1.5 text-xs bg-surface-secondary hover:bg-surface-tertiary text-text-primary border border-default px-3 py-1.5 rounded-lg transition-colors disabled:opacity-50"
                >
                  <RefreshCw className={`w-3.5 h-3.5 ${seedRefreshing ? 'animate-spin' : ''}`} />
                  Atualizar seeds
                </button>
              )}
              <button
                onClick={() => { setShowImport(true); setImportMsg(null) }}
                className="flex items-center gap-1.5 text-xs bg-pink-500/15 hover:bg-pink-500/25 text-pink-700 dark:text-pink-200 border border-pink-500/30 px-3 py-1.5 rounded-lg transition-colors"
              >
                <Download className="w-3.5 h-3.5" />
                Importar torrent
              </button>
              <p className="text-xs text-text-muted hidden lg:block">Arraste favoritos pra pastas na lateral pra organizar.</p>
            </div>
          </div>

          {/* Search bar */}
          <div className="flex items-center gap-2 mb-4">
            <div className="relative flex-1">
              <Search className="absolute left-3 top-1/2 -translate-y-1/2 text-text-muted w-4 h-4" />
              <input
                type="text"
                value={searchQuery}
                onChange={e => { setSearchQuery(e.target.value); setVisible(PAGE_SIZE) }}
                placeholder="Buscar nos favoritos…"
                className="w-full bg-surface-secondary border border-default rounded-lg pl-9 pr-3 py-2 text-sm text-text-primary focus:outline-none focus:border-pink-500"
              />
              {searchQuery && (
                <button
                  onClick={() => { setSearchQuery(''); setVisible(PAGE_SIZE) }}
                  className="absolute right-3 top-1/2 -translate-y-1/2 text-text-muted hover:text-text-primary"
                >
                  <X className="w-4 h-4" />
                </button>
              )}
            </div>
            {/* Sort: criterion + direction. setVisible reset keeps the infinite
                scroll honest when the order (and thus the first page) changes. */}
            <FavoritesSortControl
              sortBy={sortBy}
              sortDir={sortDir}
              onSortBy={k => { setSortBy(k); setVisible(PAGE_SIZE) }}
              onToggleDir={() => { setSortDir(d => d === 'asc' ? 'desc' : 'asc'); setVisible(PAGE_SIZE) }}
            />
            <button
              onClick={() => {
                if (selected.size === filteredFavs.length) {
                  clearSelection()
                } else {
                  setSelected(new Set(filteredFavs.map(f => f.name)))
                }
              }}
              className="flex items-center gap-1.5 text-xs bg-surface-tertiary hover:bg-surface-tertiary text-text-primary px-3 py-2 rounded-lg transition-colors flex-shrink-0"
              title={selected.size === filteredFavs.length ? 'Desmarcar todos' : 'Selecionar todos'}
            >
              {selected.size === filteredFavs.length ? <Square className="w-3.5 h-3.5" /> : <CheckSquare className="w-3.5 h-3.5" />}
              {selected.size === filteredFavs.length ? 'Limpar' : 'Selecionar'}
            </button>
          </div>

          {(() => {
            const fallback = renderFavsContent(loading, error, filteredFavs, viewMode, ALL_VIEW, folders)
            if (fallback) return fallback
            const shown = filteredFavs.slice(0, visible)
            return <>
              <div className="grid grid-cols-1 md:grid-cols-2 xl:grid-cols-3 gap-4">
              {shown.map(fav => (
                <div
                  key={fav.name}
                  role="button"
                  tabIndex={0}
                  draggable
                  onDragStart={e => handleFavDragStart(e, fav.name)}
                  {...newTabProps(playHref(fav.infoHash), () => playFavorite(fav))}
                  onKeyDown={e => { if (e.key === 'Enter') playFavorite(fav) }}
                  className={`card flex flex-col gap-2 group cursor-grab active:cursor-grabbing relative w-full text-left ${
                    selected.has(fav.name) ? 'ring-2 ring-green-500' : ''
                  }`}
                >
                  {/* Multi-select checkbox — pick several, then move all to a
                      folder via the action bar. Stops propagation so it doesn't
                      start a drag/play. */}
                  <input
                    type="checkbox"
                    checked={selected.has(fav.name)}
                    onChange={() => toggleSelected(fav.name)}
                    onClick={e => e.stopPropagation()}
                    title="Selecionar"
                    className={`absolute top-2 left-2 z-10 w-4 h-4 accent-green-500 cursor-pointer ${
                      selected.size > 0 ? 'opacity-100' : 'max-sm:opacity-100 opacity-0 group-hover:opacity-100'
                    }`}
                  />
                  <div className="flex items-start gap-2 pl-6">
                    {/* Lazy TMDB poster — falls back to a Film/Music icon when no match. */}
                    <Thumbnail title={fav.name} size="md" infoHash={fav.infoHash} />
                    <h3 className="text-sm font-medium text-text-primary line-clamp-2 flex-1" title={fav.name}>
                      <FileVideo className="w-3.5 h-3.5 inline mr-1.5 text-text-muted" />
                      {fav.name}
                    </h3>
                    <button
                      onClick={(e) => { e.stopPropagation(); handleRemove(fav.name) }}
                      title="Remover dos favoritos"
                      className="text-text-muted hover:text-red-400 transition-colors max-sm:opacity-100 opacity-0 group-hover:opacity-100 flex-shrink-0"
                    >
                      <Trash2 className="w-4 h-4" />
                    </button>
                  </div>

                  <div className="flex items-center gap-3 text-xs text-text-muted flex-wrap">
                    <span className="flex items-center gap-1">
                      <Clock className="w-3 h-3" />
                      {formatDate(fav.favoritedAt)}
                    </span>
                    <SeedBadge infoHash={fav.infoHash} magnet={fav.magnet} refreshSignal={seedRefresh} />
                    <span className={`text-[10px] px-1.5 py-0.5 rounded ${
                      fav.reason === 'auto-5min'
                        ? 'bg-blue-500/20 text-blue-700 dark:text-blue-300 border border-blue-500/30'
                        : 'bg-pink-500/20 text-pink-700 dark:text-pink-300 border border-pink-500/30'
                    }`}>
                      {fav.reason === 'auto-5min' ? 'Auto (5min)' : 'Manual'}
                    </span>
                    {fav.folderId != null && (
                      <span className="text-[10px] px-1.5 py-0.5 rounded bg-surface-secondary text-text-secondary border border-default flex items-center gap-1">
                        <Folder className="w-2.5 h-2.5" />
                        {folders.find(f => f.id === fav.folderId)?.name || '?'}
                      </span>
                    )}
                  </div>

                  <div className="flex gap-1.5 mt-auto pt-2 border-t border-default">
                    <button
                      onClick={e => { e.stopPropagation(); playFavorite(fav) }}
                      disabled={!fav.magnet}
                      title={fav.magnet ? 'Play (usa magnet salvo)' : 'Magnet não salvo — refavorite'}
                      className={`flex items-center gap-1 text-xs px-2.5 py-1.5 rounded-lg flex-1 justify-center transition-colors ${
                        fav.magnet
                          ? 'bg-green-500/20 hover:bg-green-500/30 text-green-700 dark:text-green-300 border border-green-500/30'
                          : 'bg-surface-tertiary/30 text-text-muted cursor-not-allowed'
                      }`}
                    >
                      <Play className="w-3.5 h-3.5" />
                      Play
                    </button>
                    {/* Baixar — abre o modal unificado (destino + seleção de
                        arquivos/árvore), igual à busca/histórico. */}
                    <button
                      onClick={e => { e.stopPropagation(); downloadFavorite(fav) }}
                      disabled={!fav.magnet}
                      title="Baixar (escolher destino e arquivos)"
                      className={`flex items-center justify-center text-xs px-2.5 py-1.5 rounded-lg transition-colors ${
                        fav.magnet
                          ? 'bg-blue-500/15 hover:bg-blue-500/25 text-blue-700 dark:text-blue-300 border border-blue-500/30'
                          : 'bg-surface-tertiary/30 text-text-muted cursor-not-allowed'
                      }`}
                    >
                      <Download className="w-3.5 h-3.5" />
                    </button>
                    {/* Details/contents — view files + torrent details without
                        committing to play (consistent with search/history). */}
                    <button
                      onClick={e => { e.stopPropagation(); openContents(fav) }}
                      disabled={!fav.magnet}
                      title="Ver conteúdo e detalhes"
                      className={`flex items-center justify-center text-xs px-2.5 py-1.5 rounded-lg transition-colors ${
                        fav.magnet
                          ? 'bg-surface-tertiary/40 hover:bg-surface-tertiary/70 text-text-primary border border-default'
                          : 'bg-surface-tertiary/30 text-text-muted cursor-not-allowed'
                      }`}
                    >
                      <FolderOpen className="w-3.5 h-3.5" />
                    </button>
                    {/* Move to folder — touch-friendly alternative to drag-and-drop
                        (HTML5 DnD doesn't work on touch). Native <select> is fully
                        usable on iOS. Only shown when folders exist. */}
                    {folders.length > 0 && (
                      <select
                        value={fav.folderId ?? ''}
                        onClick={e => e.stopPropagation()}
                        onChange={e => handleDropOnFolder(e.target.value === '' ? null : Number(e.target.value), fav.name)}
                        title="Mover para pasta"
                        className="text-xs px-2 py-1.5 rounded-lg bg-surface-tertiary/40 text-text-primary border border-default focus:outline-none focus:border-green-500 cursor-pointer max-w-[45%]"
                      >
                        <option value="">Raiz (sem pasta)</option>
                        {folders.map(f => (
                          <option key={f.id} value={f.id}>{f.name}</option>
                        ))}
                      </select>
                    )}
                  </div>
                </div>
              ))}
            </div>
              {visible < filteredFavs.length && (
                <div ref={sentinelRef} className="h-12 flex items-center justify-center text-text-muted text-xs">
                  <Loader2 className="w-4 h-4 animate-spin mr-2" />
                  Carregando mais…
                </div>
              )}
              {visible >= filteredFavs.length && filteredFavs.length > PAGE_SIZE && (
                <p className="text-center text-text-muted text-xs py-4">Todos os {filteredFavs.length} itens carregados.</p>
              )}
            </>
          })()}
        </section>
      </main>

      {/* Import modal — paste magnet(s) or drop a .torrent file.
          Usa o Sheet (mesmo padrão dos demais modais): centraliza certo no
          desktop/Safari e vira bottom-sheet no mobile. */}
      <Sheet
        open={showImport}
        onClose={() => { if (!importing) setShowImport(false) }}
        size="lg"
        icon={<Download className="w-4 h-4 text-pink-400 flex-shrink-0" />}
        title={
          <>
            Importar torrent
            {viewMode !== ALL_VIEW && (
              <span className="text-[10px] text-text-secondary font-normal ml-1">
                → {folders.find(f => f.id === viewMode)?.name || 'pasta'}
              </span>
            )}
          </>
        }
      >
        <div className="flex flex-col gap-4">
          {/* Magnet textarea — one per line for batch */}
          <div>
            <label htmlFor="import-magnet" className="text-xs text-text-secondary mb-1 block">Magnet link (um por linha pra importar vários)</label>
            <textarea
              id="import-magnet"
              value={magnetInput}
              onChange={e => setMagnetInput(e.target.value)}
              placeholder="magnet:?xt=urn:btih:..."
              rows={3}
              className="w-full bg-surface border border-default rounded-lg px-3 py-2 text-sm text-text-primary font-mono resize-y focus:border-pink-500 focus:outline-none"
            />
            <button
              onClick={importMagnets}
              disabled={importing || !magnetInput.trim()}
              className="mt-2 w-full flex items-center justify-center gap-2 text-sm bg-pink-500/20 hover:bg-pink-500/30 text-pink-700 dark:text-pink-200 border border-pink-500/30 px-3 py-2 rounded-lg transition-colors disabled:opacity-40"
            >
              {importing ? <Loader2 className="w-4 h-4 animate-spin" /> : <Download className="w-4 h-4" />}
              Importar magnet{magnetInput.split('\n').filter(l => l.trim()).length > 1 ? 's' : ''}
            </button>
          </div>

          <div className="flex items-center gap-2 text-[10px] text-text-muted uppercase tracking-wider">
            <div className="flex-1 h-px bg-surface-tertiary" /> ou <div className="flex-1 h-px bg-surface-tertiary" />
          </div>

          {/* .torrent dropzone */}
          <label
            onDragOver={e => { e.preventDefault(); setDragOverDrop(true) }}
            onDragLeave={() => setDragOverDrop(false)}
            onDrop={e => {
              e.preventDefault()
              setDragOverDrop(false)
              const fs = Array.from(e.dataTransfer.files || [])
              if (fs.length) importTorrentFiles(fs)
            }}
            className={`flex flex-col items-center justify-center gap-2 border-2 border-dashed rounded-xl py-10 cursor-pointer transition-colors ${
              dragOverDrop ? 'border-pink-500 bg-pink-500/10' : 'border-default hover:border-strong'
            }`}
          >
            <UploadCloud className="w-7 h-7 text-text-muted" />
            <span className="text-sm text-text-secondary">Arraste arquivos .torrent ou clique pra escolher (vários)</span>
            <input
              type="file"
              accept=".torrent"
              multiple
              className="hidden"
              onChange={e => { const fs = Array.from(e.target.files || []); if (fs.length) importTorrentFiles(fs) }}
            />
          </label>

          {importMsg && (
            <p className={`text-sm ${importMsg.kind === 'ok' ? 'text-green-400' : 'text-red-400'}`}>
              {importMsg.text}
            </p>
          )}
        </div>
      </Sheet>

        {/* Multi-select action bar — appears when ≥1 favorite is checked. */}
      {selected.size > 0 && (
        <div className="fixed bottom-4 left-1/2 -translate-x-1/2 z-40 flex items-center gap-3 bg-surface-secondary border border-default rounded-full shadow-2xl px-4 py-2 safe-bottom">
          <span className="text-sm text-text-primary whitespace-nowrap">{selected.size} selecionado{selected.size === 1 ? '' : 's'}</span>
          <select
            defaultValue=""
            onChange={e => { moveSelectedToFolder(e.target.value === '' ? null : Number(e.target.value)); e.target.value = '' }}
            className="bg-surface border border-default rounded-lg text-sm text-text-primary px-2 py-1 focus:outline-none focus:border-green-500"
          >
            <option value="" disabled>Mover para…</option>
            <option value="">Raiz (sem pasta)</option>
            {folders.map(f => (
              <option key={f.id} value={f.id}>{f.name}</option>
            ))}
          </select>
          <button
            onClick={async () => {
              const names = [...selected]
              const ok = await confirm({ title: 'Excluir favoritos', message: `Remover ${names.length} favorito${names.length === 1 ? '' : 's'} selecionado${names.length === 1 ? '' : 's'}?`, confirmLabel: 'Excluir', destructive: true })
              if (!ok) return
              await Promise.all(names.map(n => favoriteRemove(n).catch(() => {})))
              setFavs(favs.filter(f => !selected.has(f.name)))
              clearSelection()
            }}
            className="flex items-center gap-1 text-xs text-red-400 hover:text-red-500 dark:hover:text-red-300 px-2 py-1"
            title="Excluir selecionados"
          >
            <Trash2 className="w-3.5 h-3.5" />
            Excluir
          </button>
          <button onClick={clearSelection} title="Limpar seleção" className="text-text-secondary hover:text-text-primary">
            <X className="w-4 h-4" />
          </button>
        </div>
      )}

      {/* Contents/details modal — files + torrent details without playing. */}
      <TorrentContentsModal
        result={contentsTarget}
        onClose={() => setContentsTarget(null)}
        onPlayFile={(r, fileIdx) => { setContentsTarget(null); playSingle(r, fileIdx) }}
        onDownload={(r) => { setContentsTarget(null); setDownloadTarget(r) }}
      />

      {/* Download modal — destino + seleção de arquivos (árvore), igual à busca. */}
      <DownloadModal result={downloadTarget} onClose={() => setDownloadTarget(null)} />

      {/* Dropdown de pastas no mobile — navega entre pastas sem a sidebar. */}
      <Sheet
        open={folderSheetOpen}
        onClose={() => setFolderSheetOpen(false)}
        title={<>Pastas{revealHidden && <Eye className="inline w-3.5 h-3.5 ml-1 text-amber-400" aria-label="ocultas visíveis" />}</>}
        icon={<Folder className="w-4 h-4 text-pink-400 flex-shrink-0" />}
        size="sm"
      >
        {/* Criar/editar/excluir categorias direto no mobile (a sidebar com isso
            é hidden md:block). */}
        <button
          onClick={handleCreateRootPrompt}
          className="w-full flex items-center justify-center gap-2 mb-2 px-3 min-h-[44px] rounded-lg text-sm bg-pink-500/15 text-pink-700 dark:text-pink-200 border border-pink-500/30 hover:bg-pink-500/25 transition-colors"
        >
          <FolderPlus className="w-4 h-4 flex-shrink-0" />
          Nova pasta
        </button>
        <ul className="flex flex-col gap-1">
          <li>
            <button
              onClick={() => { setViewMode(ALL_VIEW); setSelectedFolderId(null); setFolderSheetOpen(false) }}
              className={`w-full flex items-center gap-2 px-3 min-h-[44px] rounded-lg text-sm transition-colors ${
                viewMode === ALL_VIEW ? 'bg-pink-500/15 text-pink-700 dark:text-pink-200 border border-pink-500/30' : 'text-text-primary hover:bg-surface-tertiary border border-transparent'
              }`}
            >
              <Heart className="w-4 h-4 fill-current flex-shrink-0" />
              <span className="flex-1 text-left">Todos</span>
              <span className="text-[10px] text-text-muted">{favs.length}</span>
            </button>
          </li>
          <li>
            <button
              onClick={() => { setViewMode(null); setSelectedFolderId(null); setFolderSheetOpen(false) }}
              className={`w-full flex items-center gap-2 px-3 min-h-[44px] rounded-lg text-sm transition-colors ${
                viewMode === null ? 'bg-pink-500/15 text-pink-700 dark:text-pink-200 border border-pink-500/30' : 'text-text-primary hover:bg-surface-tertiary border border-transparent'
              }`}
            >
              <Inbox className="w-4 h-4 flex-shrink-0" />
              <span className="flex-1 text-left">Sem pasta</span>
              <span className="text-[10px] text-text-muted">{favs.filter(f => f.folderId == null).length}</span>
            </button>
          </li>
          {flattenTree(tree).map(({ folder, depth }) => (
            <li key={folder.id} className={`flex items-center rounded-lg transition-colors ${
              viewMode === folder.id ? 'bg-pink-500/15 border border-pink-500/30' : 'border border-transparent hover:bg-surface-tertiary'
            }`}>
              <button
                onClick={() => { setViewMode(folder.id); setSelectedFolderId(folder.id); setFolderSheetOpen(false) }}
                className={`flex-1 min-w-0 flex items-center gap-2 min-h-[44px] text-sm text-left ${
                  viewMode === folder.id ? 'text-pink-700 dark:text-pink-200' : 'text-text-primary'
                }`}
                style={{ paddingLeft: `${12 + depth * 16}px` }}
              >
                <Folder className="w-4 h-4 text-text-muted flex-shrink-0" />
                <span className="flex-1 text-left truncate">{folder.name}</span>
                {folder.hidden && <EyeOff className="w-3.5 h-3.5 text-amber-400 flex-shrink-0" aria-label="pasta oculta" />}
                <span className="text-[10px] text-text-muted">{favs.filter(f => f.folderId === folder.id).length}</span>
              </button>
              {/* Ações da categoria — ocultar / subpasta / renomear / excluir.
                  Pastas ocultas só aparecem aqui com o modo revelado ativo. */}
              <button onClick={() => void handleToggleHidden(folder.id, !folder.hidden)} title={folder.hidden ? 'Mostrar pasta' : 'Ocultar pasta'} className="p-2 text-text-muted hover:text-amber-400 flex-shrink-0">
                {folder.hidden ? <Eye className="w-4 h-4" /> : <EyeOff className="w-4 h-4" />}
              </button>
              <button onClick={() => void handleCreateSub(folder.id)} title="Nova subpasta" className="p-2 text-text-muted hover:text-pink-400 flex-shrink-0">
                <FolderPlus className="w-4 h-4" />
              </button>
              <button onClick={() => handleRenamePrompt(folder)} title="Renomear" className="p-2 text-text-muted hover:text-text-primary flex-shrink-0">
                <Pencil className="w-4 h-4" />
              </button>
              <button onClick={() => void handleDeleteFolder(folder.id)} title="Excluir" className="p-2 pr-3 text-text-muted hover:text-red-400 flex-shrink-0">
                <Trash2 className="w-4 h-4" />
              </button>
            </li>
          ))}
        </ul>
      </Sheet>
    </div>
  )
}
