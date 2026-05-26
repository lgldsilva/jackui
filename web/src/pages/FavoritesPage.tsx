import { useState, useEffect, useMemo, useRef } from 'react'
import { Heart, Loader2, Trash2, Play, Clock, FileVideo, FolderPlus, Folder, FolderOpen, ChevronRight, ChevronDown, Pencil, Inbox } from 'lucide-react'
import {
  favoritesList, favoriteRemove, StreamFavorite,
  FavoriteFolder, folderList, folderCreate, folderRename, folderDelete, favoriteSetFolder,
} from '../api/client'
import NavHeader from '../components/NavHeader'
import PullToRefreshIndicator from '../components/PullToRefreshIndicator'
import { useAuth } from '../auth/AuthContext'
import { usePullToRefresh } from '../lib/usePullToRefresh'
import { usePlayer } from '../components/PlayerProvider'

function formatDate(iso: string): string {
  if (!iso) return '—'
  const d = new Date(iso)
  if (isNaN(d.getTime())) return '—'
  const diffH = (Date.now() - d.getTime()) / 3_600_000
  if (diffH < 1) return `${Math.floor(diffH * 60)}m atrás`
  if (diffH < 24) return `${Math.floor(diffH)}h atrás`
  if (diffH < 168) return `${Math.floor(diffH / 24)}d atrás`
  return d.toLocaleDateString('pt-BR', { day: '2-digit', month: 'short' })
}

interface FolderNode {
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

interface TreeProps {
  nodes: FolderNode[]
  depth: number
  selectedId: number | null
  expanded: Set<number>
  editingId: number | null
  onSelect: (id: number | null) => void
  onToggle: (id: number) => void
  onStartEdit: (id: number) => void
  onCommitEdit: (id: number, name: string) => void
  onCancelEdit: () => void
  onDelete: (id: number) => void
  onCreateSub: (parentId: number) => void
  onDropOnFolder: (folderId: number, favoriteName: string) => void
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
              <div className="opacity-0 group-hover:opacity-100 flex items-center gap-0.5 transition-opacity">
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
  const newFolderInput = useRef<HTMLInputElement>(null)
  const [creatingRoot, setCreatingRoot] = useState(false)

  const playFavorite = (f: StreamFavorite) => {
    const looksValid = f.magnet && (f.magnet.startsWith('magnet:') || f.magnet.startsWith('http'))
    if (!looksValid) {
      alert('Magnet inválido nesse favorito. Refavorite via busca para reabilitar Play.')
      return
    }
    playSingle({
      title: f.name, tracker: '', categoryId: 0, category: '', size: 0, seeders: 0, leechers: 0,
      age: '', magnetUri: f.magnet, link: '', infoHash: f.infoHash, publishDate: '',
    })
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

  const handleRemove = async (name: string) => {
    if (!confirm(`Remover "${name}" dos favoritos?`)) return
    await favoriteRemove(name)
    setFavs(favs.filter(f => f.name !== name))
  }

  // Filter favorites by current view: ALL_VIEW shows everything; null = root
  // (favorites without folder); a positive id = that folder only.
  const filteredFavs = useMemo(() => {
    if (viewMode === ALL_VIEW) return favs
    if (viewMode === null) return favs.filter(f => f.folderId == null)
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

  return (
    <div className="min-h-screen bg-gray-900 flex flex-col">
      <PullToRefreshIndicator pull={ptr.pull} progress={ptr.progress} refreshing={ptr.refreshing} />
      <NavHeader />

      <main className="flex-1 max-w-7xl 2xl:max-w-[min(95vw,1600px)] mx-auto w-full px-4 py-6 flex gap-4">
        {/* Sidebar — folder tree */}
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
                className={`w-full flex items-center gap-2 px-2 py-1 rounded-md text-sm transition-colors ${
                  viewMode === null ? 'bg-pink-500/15 text-pink-200 border border-pink-500/30' :
                  dropOnRoot ? 'bg-pink-500/20 border border-pink-500/50 text-pink-100' :
                  'text-gray-300 hover:bg-gray-800 border border-transparent'
                }`}
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
              <h1 className="text-lg font-semibold text-gray-100">
                {viewMode === ALL_VIEW ? 'Todos os favoritos'
                  : viewMode === null ? 'Sem pasta'
                  : folders.find(f => f.id === viewMode)?.name || 'Favoritos'}
              </h1>
              {!loading && (
                <span className="text-xs text-gray-500 bg-gray-800 border border-gray-700 px-2 py-0.5 rounded-full">
                  {filteredFavs.length} item{filteredFavs.length !== 1 ? 's' : ''}
                </span>
              )}
              {isAdmin && (
                <span className="text-[10px] uppercase bg-yellow-500/20 text-yellow-400 border border-yellow-500/30 px-2 py-0.5 rounded">
                  Admin · vê todos
                </span>
              )}
            </div>
            <p className="text-xs text-gray-500 hidden lg:block">Arraste favoritos pra pastas na lateral pra organizar.</p>
          </div>

          {loading ? (
            <div className="flex items-center justify-center py-20 text-gray-500">
              <Loader2 className="w-8 h-8 animate-spin" />
            </div>
          ) : error ? (
            <div className="card text-red-400 text-sm">Erro: {error}</div>
          ) : filteredFavs.length === 0 ? (
            <div className="flex flex-col items-center justify-center py-20 text-gray-500">
              <Heart className="w-16 h-16 mb-4 opacity-30" />
              <p className="text-xl font-medium">Nenhum favorito {viewMode !== ALL_VIEW ? 'nessa pasta' : 'ainda'}</p>
              <p className="text-sm mt-2 text-center max-w-md">
                {viewMode === ALL_VIEW
                  ? 'Abra um torrent no player e clique no ♥ no canto superior.'
                  : 'Arraste favoritos da view "Todos" pra esta pasta.'}
              </p>
            </div>
          ) : (
            <div className="grid grid-cols-1 md:grid-cols-2 xl:grid-cols-3 gap-4">
              {filteredFavs.map(fav => (
                <div
                  key={fav.name}
                  draggable
                  onDragStart={e => { e.dataTransfer.setData('text/x-favorite-name', fav.name); e.dataTransfer.effectAllowed = 'move' }}
                  className="card flex flex-col gap-2 group cursor-grab active:cursor-grabbing"
                >
                  <div className="flex items-start justify-between gap-2">
                    <h3 className="text-sm font-medium text-gray-100 line-clamp-2 flex-1" title={fav.name}>
                      <FileVideo className="w-3.5 h-3.5 inline mr-1.5 text-gray-500" />
                      {fav.name}
                    </h3>
                    <button
                      onClick={() => handleRemove(fav.name)}
                      title="Remover dos favoritos"
                      className="text-gray-600 hover:text-red-400 transition-colors opacity-0 group-hover:opacity-100 flex-shrink-0"
                    >
                      <Trash2 className="w-4 h-4" />
                    </button>
                  </div>

                  <div className="flex items-center gap-3 text-xs text-gray-500 flex-wrap">
                    <span className="flex items-center gap-1">
                      <Clock className="w-3 h-3" />
                      {formatDate(fav.favoritedAt)}
                    </span>
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
                  </div>
                </div>
              ))}
            </div>
          )}
        </section>
      </main>
    </div>
  )
}
