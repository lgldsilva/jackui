import { useState, useEffect } from 'react'
import { Link, useParams, useNavigate } from 'react-router-dom'
import { ArrowLeft, Loader2, Play, Trash2, GripVertical, Save, ListMusic, Shuffle } from 'lucide-react'
import {
  playlistsGet, playlistsUpdate, playlistsRemoveItem, playlistsReorderItem,
  Playlist, PlaylistItem,
} from '../api/client'
import NavHeader from '../components/NavHeader'
import Thumbnail from '../components/Thumbnail'
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
  const { playPlaylist, toggleShuffle, shuffle } = usePlayer()

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

  const move = async (it: PlaylistItem, dir: -1 | 1) => {
    if (!playlist) return
    const newPos = it.position + dir
    if (newPos < 0 || newPos >= items.length) return
    await playlistsReorderItem(playlist.id, it.id, newPos)
    await load()
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
        {loading ? (
          <div className="flex justify-center py-20"><Loader2 className="w-8 h-8 animate-spin text-gray-500" /></div>
        ) : !playlist ? (
          <div className="text-center py-20 text-gray-500">
            <p>Playlist não encontrada</p>
            <Link to="/playlists" className="text-green-400 mt-2 inline-block">Voltar</Link>
          </div>
        ) : (
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
                      <Save className="w-4 h-4" /> Salvar
                    </button>
                    <button onClick={() => { setEditing(false); setName(playlist.name); setDescription(playlist.description) }} className="btn-secondary">
                      Cancelar
                    </button>
                  </div>
                </>
              ) : (
                <>
                  <div className="flex items-start justify-between gap-3">
                    <div>
                      <h1 className="text-xl font-semibold text-gray-100 flex items-center gap-2">
                        <ListMusic className="w-5 h-5 text-blue-400" /> {playlist.name}
                      </h1>
                      {playlist.description && (
                        <p className="text-sm text-gray-400 mt-1">{playlist.description}</p>
                      )}
                    </div>
                    <button onClick={() => setEditing(true)} className="text-xs text-gray-400 hover:text-gray-200">
                      Editar
                    </button>
                  </div>
                  <div className="flex items-center justify-between flex-wrap gap-2">
                    <p className="text-xs text-gray-500">
                      {items.length} {items.length === 1 ? 'item' : 'itens'}
                    </p>
                    {items.length > 0 && (
                      <div className="flex items-center gap-2">
                        <button
                          onClick={() => startAt(0)}
                          className="btn-primary flex items-center gap-1.5 text-sm"
                          title="Tocar todos a partir do primeiro"
                        >
                          <Play className="w-4 h-4" /> Tocar todos
                        </button>
                        <button
                          onClick={() => {
                            if (!shuffle) toggleShuffle()
                            startAt(0)
                          }}
                          className="btn-secondary flex items-center gap-1.5 text-sm"
                          title="Embaralhar e tocar"
                        >
                          <Shuffle className="w-4 h-4" /> Embaralhar
                        </button>
                      </div>
                    )}
                  </div>
                </>
              )}
            </div>

            {items.length === 0 ? (
              <div className="flex flex-col items-center justify-center py-20 text-gray-500">
                <ListMusic className="w-16 h-16 mb-4 opacity-30" />
                <p>Nenhum item ainda</p>
                <p className="text-xs mt-2">Use "Adicionar à playlist" nos cards de busca pra adicionar torrents aqui.</p>
              </div>
            ) : (
              <div className="card flex flex-col gap-1">
                {items.map((it, idx) => (
                  <div key={it.id} className="flex items-center gap-2 p-2 hover:bg-gray-900/50 rounded-lg group">
                    <span className="text-xs text-gray-600 font-mono w-6 text-right">{idx + 1}.</span>
                    <div className="flex flex-col gap-0.5">
                      <button
                        onClick={() => move(it, -1)}
                        disabled={idx === 0}
                        className="text-gray-600 hover:text-gray-300 disabled:opacity-30"
                      >
                        <GripVertical className="w-3 h-3 rotate-180" />
                      </button>
                      <button
                        onClick={() => move(it, 1)}
                        disabled={idx === items.length - 1}
                        className="text-gray-600 hover:text-gray-300 disabled:opacity-30"
                      >
                        <GripVertical className="w-3 h-3" />
                      </button>
                    </div>
                    {/* Lazy TMDB poster — falls back to Film/Music icon. */}
                    <Thumbnail title={it.title} size="sm" infoHash={it.infoHash} />
                    <div className="flex-1 min-w-0">
                      <p className="text-sm text-gray-200 truncate" title={it.title}>{it.title}</p>
                      {it.infoHash && (
                        <p className="text-[10px] text-gray-600 font-mono truncate">
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
        )}
      </main>

    </div>
  )
}
