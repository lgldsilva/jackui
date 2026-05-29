import { useState, useEffect } from 'react'
import { Link, useParams, useNavigate } from 'react-router-dom'
import { ArrowLeft, Loader2, Play, Trash2, ListMusic, Check, Pencil, GripVertical } from 'lucide-react'
import {
  playlistsGet, playlistsUpdate, playlistsRemoveItem, playlistsReorderItem,
  Playlist, PlaylistItem,
} from '../api/client'
import NavHeader from '../components/NavHeader'
import { usePlayer } from '../components/PlayerProvider'

export default function PlaylistDetailPage() {
  const { id } = useParams<{ id: string }>()
  const nav = useNavigate()
  const playlistID = parseInt(id || '0', 10)

  const [playlist, setPlaylist] = useState<Playlist | null>(null)
  const [items, setItems] = useState<PlaylistItem[]>([])
  const [loading, setLoading] = useState(true)
  const [editing, setEditing] = useState(false)
  const [name, setName] = useState('')
  const [description, setDescription] = useState('')
  const { playPlaylist } = usePlayer()

  const startAt = (idx: number) => {
    if (!playlist || items.length === 0) return
    playPlaylist(playlist.name, items, idx)
  }

  const load = async () => {
    if (!playlistID) return
    setLoading(true)
    try {
      const { playlist: p, items: its } = await playlistsGet(playlistID)
      setPlaylist(p)
      setItems(its || [])
      setName(p.name)
      setDescription(p.description || '')
    } finally {
      setLoading(false)
    }
  }

  useEffect(() => { load() }, [playlistID])

  const saveMeta = async () => {
    if (!playlist) return
    await playlistsUpdate(playlist.id, name, description)
    setPlaylist({ ...playlist, name, description })
    setEditing(false)
  }

  const removeItem = async (it: PlaylistItem) => {
    if (!playlist) return
    await playlistsRemoveItem(playlist.id, it.id)
    setItems(items.filter(x => x.id !== it.id).map((x, i) => ({ ...x, position: i })))
  }

  const [dragIdx, setDragIdx] = useState<number | null>(null)

  // Drag-to-reorder: reorder optimistically, persist via PATCH, roll back from
  // the server on failure. `to` is the destination index — the backend's
  // Reorder takes the same 0-based position.
  const handleReorderDrop = async (to: number) => {
    const from = dragIdx
    setDragIdx(null)
    if (from === null || from === to || !playlist) return
    const reordered = [...items]
    const [moved] = reordered.splice(from, 1)
    reordered.splice(to, 0, moved)
    setItems(reordered.map((x, i) => ({ ...x, position: i })))
    try {
      await playlistsReorderItem(playlist.id, moved.id, to)
    } catch {
      load()
    }
  }


  let mainContent: React.ReactNode
  if (loading) {
    mainContent = <div className="flex justify-center py-20"><Loader2 className="w-8 h-8 animate-spin text-gray-500" /></div>
  } else if (playlist) {
    mainContent = (
      <>
        <div className="card flex flex-col gap-3">
          {editing ? (
            <>
              <input
                type="text"
                value={name}
                onChange={e => setName(e.target.value)}
                className="input-field text-lg font-semibold"
              />
              <textarea
                value={description}
                onChange={e => setDescription(e.target.value)}
                placeholder="Descrição (opcional)"
                rows={2}
                className="input-field text-sm"
              />
              <div className="flex gap-2">
                <button onClick={saveMeta} className="btn-primary flex items-center gap-1.5">
                  <Check className="w-4 h-4" /> Salvar
                </button>
                <button onClick={() => { setEditing(false); setName(playlist.name); setDescription(playlist.description) }} className="btn-secondary">
                  Cancelar
                </button>
              </div>
            </>
          ) : (
            <div className="flex items-start justify-between gap-4">
              <div className="flex items-start gap-2 min-w-0">
                <ListMusic className="w-5 h-5 text-blue-400 mt-0.5 flex-shrink-0" />
                <div className="min-w-0">
                  <h2 className="text-lg font-semibold text-gray-100">{playlist.name}</h2>
                  {playlist.description && (
                    <p className="text-sm text-gray-400 mt-1">{playlist.description}</p>
                  )}
                </div>
              </div>
              <button onClick={() => setEditing(true)} className="btn-secondary flex items-center gap-1.5 flex-shrink-0">
                <Pencil className="w-4 h-4" /> Editar
              </button>
            </div>
          )}
        </div>

        {items.length === 0 ? (
          <div className="card flex flex-col items-center justify-center gap-3 py-16 text-gray-500">
            <ListMusic className="w-12 h-12 text-gray-600" />
            <p className="text-base font-medium text-gray-400">Playlist vazia</p>
            <p className="text-xs mt-2">Use &quot;Adicionar à playlist&quot; nos cards de busca pra adicionar torrents aqui.</p>
          </div>
        ) : (
          <div className="flex flex-col gap-1.5">
            {items.map((it, idx) => (
                <div
                  key={it.id}
                  draggable
                  onDragStart={() => setDragIdx(idx)}
                  onDragOver={(e) => e.preventDefault()}
                  onDrop={() => handleReorderDrop(idx)}
                  onDragEnd={() => setDragIdx(null)}
                  role="button" tabIndex={0}
                  onKeyDown={e => { if (e.key === 'Enter' || e.key === ' ') { e.preventDefault(); startAt(idx) } }}
                  className={`card flex items-center gap-3 py-2.5 px-3 hover:bg-gray-800/60 transition-colors group ${dragIdx === idx ? 'opacity-50' : ''}`}
                >
                <GripVertical className="w-4 h-4 text-gray-600 flex-shrink-0 cursor-grab active:cursor-grabbing" />
                <div className="flex-1 min-w-0">
                  <button
                    onClick={() => startAt(idx)}
                    className="text-sm text-gray-100 hover:text-green-400 transition-colors text-left font-medium truncate block w-full"
                    title={it.title}
                  >
                    {idx + 1}. {it.title}
                  </button>
                  {it.infoHash && (
                    <p className="text-[10px] text-gray-600 mt-0.5 font-mono">
                      {it.infoHash.slice(0, 16)}...
                    </p>
                  )}
                </div>
                <button
                  onClick={() => startAt(idx)}
                  title="Reproduzir a partir deste item"
                  className="text-green-400 hover:text-green-300 p-1"
                >
                  <Play className="w-4 h-4" />
                </button>
                <button
                  onClick={() => removeItem(it)}
                  title="Remover da playlist"
                  className="text-gray-600 hover:text-red-400 p-1 max-sm:opacity-100 opacity-0 group-hover:opacity-100 transition-opacity"
                >
                  <Trash2 className="w-4 h-4" />
                </button>
              </div>
            ))}
          </div>
        )}
      </>
    )
  } else {
    mainContent = (
      <div className="text-center py-20 text-gray-500">
        <p>Playlist não encontrada</p>
        <Link to="/playlists" className="text-green-400 mt-2 inline-block">Voltar</Link>
      </div>
    )
  }

  return (
    <div className="min-h-screen bg-gray-900 flex flex-col">
      <NavHeader
        rightExtra={
          <button
            onClick={() => nav('/playlists')}
            className="header-link"
            title="Voltar para playlists"
          >
            <ArrowLeft className="w-4 h-4" />
            <span className="hidden md:inline">Voltar</span>
          </button>
        }
      />

      <main className="flex-1 max-w-7xl 2xl:max-w-[min(95vw,1600px)] mx-auto w-full px-4 py-6 flex flex-col gap-4">
        {mainContent}
      </main>

    </div>
  )
}
