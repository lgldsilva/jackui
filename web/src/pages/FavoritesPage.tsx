import { useState, useEffect, useMemo, useRef } from 'react'
import { Heart, Loader2, Trash2, Play, Clock, FileVideo, FolderPlus, Folder, FolderOpen, ChevronRight, ChevronDown, Pencil, Inbox, Download, X, UploadCloud } from 'lucide-react'
import {
  favoritesList, favoriteRemove, StreamFavorite,
  FavoriteFolder, folderList, folderCreate, folderRename, folderDelete, favoriteSetFolder,
  streamImport, SearchResult,
} from '../api/client'
import NavHeader from '../components/NavHeader'
import PullToRefreshIndicator from '../components/PullToRefreshIndicator'
import Thumbnail from '../components/Thumbnail'
import SeedBadge from '../components/SeedBadge'
import TorrentContentsModal from '../components/TorrentContentsModal'
import { useAuth } from '../auth/AuthContext'
import { usePullToRefresh } from '../lib/usePullToRefresh'
import { usePlayer } from '../components/PlayerProvider'
import { formatDate } from '../lib/format'

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
            <div
              className={`group flex items-center gap-1 px-2 py-1 rounded-md text-sm transition-colors ${
                isSelected ? 'bg-pink-500/15 text-pink-200 border border-pink-500/30' : 'text-gray-300 hover:bg-gray-800 border border-transparent'
              }`}
              style={{ paddingLeft: `${depthIndent(p.depth)}px` }}
              onDragOver={e => { e.preventDefault(); e.dataTransfer.dropEffect = 'move' }}
              onDrop={e => {
                e.preventDefault()
                const name = e.dataTransfer.getData('text/x-favorite-name')
                if (name) p.onDropOnFolder(node.folder.id, name)
              }}
            >
              {node.children.length > 0 ? (
                <button onClick={() => p.onToggle(node.folder.id)} className="text-gray-500 hover:text-gray-200">
                  {isOpen ? <ChevronDown className="w-3 h-3" /> : <ChevronRight className="w-3 h-3" />}
                </button>
              ) : (
                <span className="w-3" />
              )}
              {isOpen ? <FolderOpen className="w-3.5 h-3.5 text-pink-400" /> : <Folder className="w-3.5 h-3.5 text-gray-500" />}
              {isEditing ? (
                <input
                  autoFocus
                  defaultValue={node.folder.name}
                  onBlur={e => p.onCommitEdit(node.folder.id, e.currentTarget.value)}
                  onKeyDown={e => {
                    if (e.key === 'Enter') p.onCommitEdit(node.folder.id, e.currentTarget.value)
                    if (e.key === 'Escape') p.onCancelEdit()
                  }}
                  className="flex-1 bg-gray-900 border border-gray-700 rounded px-1 text-xs text-gray-100 focus:outline-none focus:border-pink-500"
                />
              ) : (
                <button
                  onClick={() => p.onSelect(node.folder.id)}
                  onDoubleClick={() => p.onStartEdit(node.folder.id)}
                  className="flex-1 text-left truncate"
                  title={node.folder.name}
                >
                  {node.folder.name}
                </button>
              )}
              <div className="max-sm:opacity-100 opacity-0 group-hover:opacity-100 flex items-center gap-0.5 transition-opacity">
                <button onClick={() => p.onCreateSub(node.folder.id)} title="Subpasta" className="p-0.5 text-gray-500 hover:text-gray-200">
                  <FolderPlus className="w-3 h-3" />
                </button>
                <button onClick={() => p.onStartEdit(node.folder.id)} title="Renomear" className="p-0.5 text-gray-500 hover:text-gray-200">
                  <Pencil className="w-3 h-3" />
                </button>
                <button onClick={() => p.onDelete(node.folder.id)} title="Excluir" className="p-0.5 text-gray-500 hover:text-red-400">
                  <Trash2 className="w-3 h-3" />
                </button>
              </div>
            </div>
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
  if (viewMode === null) return 'bg-pink-500/15 text-pink-200 border border-pink-500/30'
  if (dropOnRoot) return 'bg-pink-500/20 border border-pink-500/50 text-pink-100'
  return 'text-gray-300 hover:bg-gray-800 border border-transparent'
}

function pageTitle(viewMode: number | null, ALL_VIEW: number, folders: FavoriteFolder[]): string {
  if (viewMode === ALL_VIEW) return 'Todos os favoritos'
  if (viewMode === null) return 'Sem pasta'
  return folders.find(f => f.id === viewMode)?.name || 'Favoritos'
}

function renderFavsContent(loading: boolean, error: string, filteredFavs: StreamFavorite[], viewMode: number | null, ALL_VIEW: number, _folders: FavoriteFolder[]): JSX.Element | null {
  if (loading) {
    return <div className="flex items-center justify-center py-20 text-gray-500">
      <Loader2 className="w-8 h-8 animate-spin" />
    </div>
  }
  if (error) {
    return <div className="card text-red-400 text-sm">Erro: {error}</div>
  }
  if (filteredFavs.length === 0) {
    const insideFolder = viewMode !== ALL_VIEW
    return <div className="flex flex-col items-center justify-center py-20 text-gray-500">
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
  const [error, setError] = useState('')
  const { playSingle } = usePlayer()

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

  // favToResult builds the SearchResult shape the player/contents modal expect.
  // Favorites only persist name/magnet/infoHash, so tracker/category stay empty
  // (the details modal hides the rows it has no data for).
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

  // Card click opens the contents/details modal (files + torrent details)
  // WITHOUT playing — matching the search/history flow. The Play button stays
  // the quick direct path.
  const openContents = (f: StreamFavorite) => {
    if (!favHasValidMagnet(f)) {
      alert('Magnet inválido nesse favorito. Refavorite via busca para reabilitar Play.')
      return
    }
    setContentsTarget(favToResult(f))
  }

  const load = async () => {
    setLoading(true)
    setError('')
    try {
      const [favsList, foldersList] = await Promise.all([favoritesList(), folderList()])
      setFavs(favsList || [])
      setFolders(foldersList || [])
    } catch (e: unknown) {
      setError(e instanceof Error ? e.message : String(e))
    } finally {
      setLoading(false)
    }
  }
  useEffect(() => { load() }, [])

  const ptr = usePullToRefresh({ onRefresh: load, disabled: loading })

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
    let ok = 0
    const fails: string[] = []
    for (const file of torrents) {
      try {
        const buf = await file.arrayBuffer()
        let bin = ''
        const bytes = new Uint8Array(buf)
        for (const byte of bytes) bin += String.fromCodePoint(byte)
        const torrentB64 = btoa(bin)
        await streamImport({ torrentB64, folderId: viewMode === ALL_VIEW ? null : viewMode })
        ok++
      } catch (e: unknown) {
        fails.push(`${file.name}: ${e instanceof Error ? e.message : String(e)}`)
      }
    }
    setImporting(false)
    let suffix = ''
    if (skipped > 0) {
      const plural = skipped === 1 ? '' : 's'
      suffix = ` (${skipped} ignorado${plural} — não .torrent)`
    }
    let msg: { kind: 'ok' | 'err'; text: string }
    if (fails.length === 0) {
      const plural = ok === 1 ? '' : 's'
      msg = { kind: 'ok', text: `${ok} torrent${plural} importado${plural}${suffix}` }
    } else {
      msg = { kind: 'err', text: `${ok} ok, ${fails.length} falha(s): ${fails[0]}${suffix}` }
    }
    setImportMsg(msg)
    await load()
  }

  const handleRemove = async (name: string) => {
    if (!confirm(`Remover "${name}" dos favoritos?`)) return
    await favoriteRemove(name)
    setFavs(favs.filter(f => f.name !== name))
  }

  // Filter favorites by current view: ALL_VIEW shows everything; null = root
  // (favorites without folder); a positive id = that folder only.
  const filteredFavs = useMemo(() => {
    if (viewMode === ALL_VIEW) return favs
    if (viewMode === null) return favs.filter(f => f.folderId === null)
    return favs.filter(f => f.folderId === viewMode)
  }, [favs, viewMode])

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
    if (!confirm(`Excluir pasta "${target?.name}"? Favoritos dentro voltam pra raiz.`)) return
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
    <div className="min-h-screen bg-gray-900 flex flex-col">
      <PullToRefreshIndicator pull={ptr.pull} progress={ptr.progress} refreshing={ptr.refreshing} />
      <NavHeader />

      <main className="flex-1 max-w-7xl 2xl:max-w-[min(95vw,1600px)] mx-auto w-full px-4 py-6 flex flex-col md:flex-row gap-4">
        {/* Sidebar — folder tree (oculta no mobile pra não comprimir o conteúdo) */}
        <aside className="w-64 flex-shrink-0 hidden md:block">
          <div className="flex items-center justify-between mb-2">
            <h2 className="text-xs uppercase tracking-wider text-gray-500">Pastas</h2>
            <button
              onClick={() => setCreatingRoot(true)}
              title="Nova pasta"
              className="p-1 text-gray-500 hover:text-pink-400"
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
                  viewMode === ALL_VIEW ? 'bg-pink-500/15 text-pink-200 border border-pink-500/30' : 'text-gray-300 hover:bg-gray-800 border border-transparent'
                }`}
              >
                <Heart className="w-3.5 h-3.5 fill-current" />
                Todos
                <span className="ml-auto text-[10px] text-gray-500">{favs.length}</span>
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
                <span className="ml-auto text-[10px] text-gray-500">{favs.filter(f => f.folderId == null).length}</span>
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
                className="w-full bg-gray-900 border border-gray-700 rounded px-2 py-1 text-xs text-gray-100 focus:outline-none focus:border-pink-500"
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
            onDropOnFolder={(fid, name) => handleDropOnFolder(fid, name)}
          />
        </aside>

        {/* Main — favorites grid */}
        <section className="flex-1 min-w-0">
          <div className="flex items-center justify-between flex-wrap gap-3 mb-4">
            <div className="flex items-center gap-3">
              <Heart className="w-5 h-5 text-pink-400 fill-current" />
              <h1 className="text-lg font-semibold text-gray-100">{pageTitle(viewMode, ALL_VIEW, folders)}</h1>
              {!loading && (
                <span className="text-xs text-gray-500 bg-gray-800 border border-gray-700 px-2 py-0.5 rounded-full">
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
              <button
                onClick={() => { setShowImport(true); setImportMsg(null) }}
                className="flex items-center gap-1.5 text-xs bg-pink-500/15 hover:bg-pink-500/25 text-pink-200 border border-pink-500/30 px-3 py-1.5 rounded-lg transition-colors"
              >
                <Download className="w-3.5 h-3.5" />
                Importar torrent
              </button>
              <p className="text-xs text-gray-500 hidden lg:block">Arraste favoritos pra pastas na lateral pra organizar.</p>
            </div>
          </div>

          {(() => {
            const fallback = renderFavsContent(loading, error, filteredFavs, viewMode, ALL_VIEW, folders)
            if (fallback) return fallback
            return <div className="grid grid-cols-1 md:grid-cols-2 xl:grid-cols-3 gap-4">
              {filteredFavs.map(fav => (
                <div
                  key={fav.name}
                  draggable
                  role="button"
                  tabIndex={0}
                  onDragStart={e => handleFavDragStart(e, fav.name)}
                  className={`card flex flex-col gap-2 group cursor-grab active:cursor-grabbing relative ${
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
                    <h3 className="text-sm font-medium text-gray-100 line-clamp-2 flex-1" title={fav.name}>
                      <FileVideo className="w-3.5 h-3.5 inline mr-1.5 text-gray-500" />
                      {fav.name}
                    </h3>
                    <button
                      onClick={() => handleRemove(fav.name)}
                      title="Remover dos favoritos"
                      className="text-gray-600 hover:text-red-400 transition-colors max-sm:opacity-100 opacity-0 group-hover:opacity-100 flex-shrink-0"
                    >
                      <Trash2 className="w-4 h-4" />
                    </button>
                  </div>

                  <div className="flex items-center gap-3 text-xs text-gray-500 flex-wrap">
                    <span className="flex items-center gap-1">
                      <Clock className="w-3 h-3" />
                      {formatDate(fav.favoritedAt)}
                    </span>
                    <SeedBadge infoHash={fav.infoHash} magnet={fav.magnet} />
                    <span className={`text-[10px] px-1.5 py-0.5 rounded ${
                      fav.reason === 'auto-5min'
                        ? 'bg-blue-500/20 text-blue-300 border border-blue-500/30'
                        : 'bg-pink-500/20 text-pink-300 border border-pink-500/30'
                    }`}>
                      {fav.reason === 'auto-5min' ? 'Auto (5min)' : 'Manual'}
                    </span>
                    {fav.folderId != null && (
                      <span className="text-[10px] px-1.5 py-0.5 rounded bg-gray-800 text-gray-400 border border-gray-700 flex items-center gap-1">
                        <Folder className="w-2.5 h-2.5" />
                        {folders.find(f => f.id === fav.folderId)?.name || '?'}
                      </span>
                    )}
                  </div>

                  <div className="flex gap-1.5 mt-auto pt-2 border-t border-gray-700">
                    <button
                      onClick={() => playFavorite(fav)}
                      disabled={!fav.magnet}
                      title={fav.magnet ? 'Play (usa magnet salvo)' : 'Magnet não salvo — refavorite'}
                      className={`flex items-center gap-1 text-xs px-2.5 py-1.5 rounded-lg flex-1 justify-center transition-colors ${
                        fav.magnet
                          ? 'bg-green-500/20 hover:bg-green-500/30 text-green-300 border border-green-500/30'
                          : 'bg-gray-700/30 text-gray-500 cursor-not-allowed'
                      }`}
                    >
                      <Play className="w-3.5 h-3.5" />
                      Play
                    </button>
                    {/* Details/contents — view files + torrent details without
                        committing to play (consistent with search/history). */}
                    <button
                      onClick={() => openContents(fav)}
                      disabled={!fav.magnet}
                      title="Ver conteúdo e detalhes"
                      className={`flex items-center justify-center text-xs px-2.5 py-1.5 rounded-lg transition-colors ${
                        fav.magnet
                          ? 'bg-gray-700/40 hover:bg-gray-700/70 text-gray-300 border border-gray-700'
                          : 'bg-gray-700/30 text-gray-500 cursor-not-allowed'
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
                        onChange={e => handleDropOnFolder(e.target.value === '' ? null : Number(e.target.value), fav.name)}
                        title="Mover para pasta"
                        className="text-xs px-2 py-1.5 rounded-lg bg-gray-700/40 text-gray-300 border border-gray-700 focus:outline-none focus:border-green-500 cursor-pointer max-w-[45%]"
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
          })()}
        </section>
      </main>

      {/* Import modal — paste magnet(s) or drop a .torrent file */}
      {showImport && (
        <dialog
          className="fixed inset-0 bg-black/70 flex items-center justify-center z-50 p-4 open:flex"
          onClick={() => !importing && setShowImport(false)}
          onKeyDown={e => e.key === 'Escape' && !importing && setShowImport(false)}
          onFocus={() => {}}
          onClose={() => !importing && setShowImport(false)}
          open
        >
          <div
            className="bg-gray-800 border border-gray-700 rounded-2xl w-full max-w-lg p-5 flex flex-col gap-4"
          >
            <div className="flex items-center justify-between">
              <h2 className="text-base font-semibold text-gray-100 flex items-center gap-2">
                <Download className="w-4 h-4 text-pink-400" />
                Importar torrent
                {viewMode !== ALL_VIEW && (
                  <span className="text-[10px] text-gray-400 font-normal">
                    → {folders.find(f => f.id === viewMode)?.name || 'pasta'}
                  </span>
                )}
              </h2>
              <button onClick={() => !importing && setShowImport(false)} className="text-gray-500 hover:text-gray-300">
                <X className="w-5 h-5" />
              </button>
            </div>

            {/* Magnet textarea — one per line for batch */}
            <div>
              <label htmlFor="import-magnet" className="text-xs text-gray-400 mb-1 block">Magnet link (um por linha pra importar vários)</label>
              <textarea
                id="import-magnet"
                value={magnetInput}
                onChange={e => setMagnetInput(e.target.value)}
                placeholder="magnet:?xt=urn:btih:..."
                rows={3}
                className="w-full bg-gray-900 border border-gray-700 rounded-lg px-3 py-2 text-sm text-gray-200 font-mono resize-y focus:border-pink-500 focus:outline-none"
              />
              <button
                onClick={importMagnets}
                disabled={importing || !magnetInput.trim()}
                className="mt-2 w-full flex items-center justify-center gap-2 text-sm bg-pink-500/20 hover:bg-pink-500/30 text-pink-200 border border-pink-500/30 px-3 py-2 rounded-lg transition-colors disabled:opacity-40"
              >
                {importing ? <Loader2 className="w-4 h-4 animate-spin" /> : <Download className="w-4 h-4" />}
                Importar magnet{magnetInput.split('\n').filter(l => l.trim()).length > 1 ? 's' : ''}
              </button>
            </div>

            <div className="flex items-center gap-2 text-[10px] text-gray-600 uppercase tracking-wider">
              <div className="flex-1 h-px bg-gray-700" /> ou <div className="flex-1 h-px bg-gray-700" />
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
              className={`flex flex-col items-center justify-center gap-2 border-2 border-dashed rounded-xl py-6 cursor-pointer transition-colors ${
                dragOverDrop ? 'border-pink-500 bg-pink-500/10' : 'border-gray-700 hover:border-gray-600'
              }`}
            >
              <UploadCloud className="w-7 h-7 text-gray-500" />
              <span className="text-sm text-gray-400">Arraste arquivos .torrent ou clique pra escolher (vários)</span>
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
          </dialog>
        )}

        {/* Multi-select action bar — appears when ≥1 favorite is checked. */}
      {selected.size > 0 && (
        <div className="fixed bottom-4 left-1/2 -translate-x-1/2 z-40 flex items-center gap-3 bg-gray-800 border border-gray-700 rounded-full shadow-2xl px-4 py-2 safe-bottom">
          <span className="text-sm text-gray-200 whitespace-nowrap">{selected.size} selecionado{selected.size === 1 ? '' : 's'}</span>
          <select
            defaultValue=""
            onChange={e => { moveSelectedToFolder(e.target.value === '' ? null : Number(e.target.value)); e.target.value = '' }}
            className="bg-gray-900 border border-gray-700 rounded-lg text-sm text-gray-200 px-2 py-1 focus:outline-none focus:border-green-500"
          >
            <option value="" disabled>Mover para…</option>
            <option value="">Raiz (sem pasta)</option>
            {folders.map(f => (
              <option key={f.id} value={f.id}>{f.name}</option>
            ))}
          </select>
          <button onClick={clearSelection} title="Limpar seleção" className="text-gray-400 hover:text-gray-100">
            <X className="w-4 h-4" />
          </button>
        </div>
      )}

      {/* Contents/details modal — files + torrent details without playing. */}
      <TorrentContentsModal
        result={contentsTarget}
        onClose={() => setContentsTarget(null)}
        onPlayFile={(r, fileIdx) => { setContentsTarget(null); playSingle(r, fileIdx) }}
      />
    </div>
  )
}
