import { useState, useEffect } from 'react'
import { X, ListMusic, Plus, Check, Loader2 } from 'lucide-react'
import {
  playlistsList, playlistsCreate, playlistsAddItem, pickTorrentSource,
  Playlist, SearchResult,
} from '../api/client'
import { useScrollLock } from '../lib/useScrollLock'

type Props = {
  readonly result: SearchResult | null
  readonly onClose: () => void
  readonly fileIndex?: number
  readonly fileTitle?: string
}

/**
 * "Add to playlist" picker — shown when user clicks the playlist icon on a ResultCard.
 * Lists existing playlists + lets create a new one inline.
 */
export default function PlaylistPickerModal({ result, onClose, fileIndex, fileTitle }: Props) {
  useScrollLock(!!result)
  const [lists, setLists] = useState<Playlist[]>([])
  const [loading, setLoading] = useState(false)
  const [creating, setCreating] = useState(false)
  const [newName, setNewName] = useState('')
  const [busy, setBusy] = useState(false)
  const [added, setAdded] = useState<number | null>(null)
  const [error, setError] = useState('')

  useEffect(() => {
    if (!result) return
    setLoading(true)
    setError('')
    setAdded(null)
    setCreating(false)
    setNewName('')
    playlistsList()
      .then(setLists)
      .catch(e => setError(e?.response?.data?.error || e.message))
      .finally(() => setLoading(false))
  }, [result])

  const handleAddPlaylist = async (p: Playlist) => {
    if (!result) return
    setBusy(true)
    try {
      await playlistsAddItem(p.id, {
        title: fileTitle || result.title,
        magnet: pickTorrentSource(result),
        infoHash: result.infoHash,
        fileIndex: fileIndex,
      })
      setAdded(p.id)
      setTimeout(onClose, 800)
    } catch (e: any) {
      setError(e?.response?.data?.error || e.message)
    } finally {
      setBusy(false)
    }
  }

  const createAndAdd = async (e: React.FormEvent) => {
    e.preventDefault()
    if (!newName.trim() || !result) return
    setBusy(true)
    try {
      const p = await playlistsCreate(newName.trim())
      await playlistsAddItem(p.id, {
        title: fileTitle || result.title,
        magnet: pickTorrentSource(result),
        infoHash: result.infoHash,
        fileIndex: fileIndex,
      })
      setAdded(p.id)
      setTimeout(onClose, 800)
    } catch (e: any) {
      setError(e?.response?.data?.error || e.message)
    } finally {
      setBusy(false)
    }
  }

  if (!result) return null

  return (
    <dialog
      className="fixed inset-0 bg-black/60 backdrop-blur-sm flex items-center justify-center z-50 p-4 open:flex"
      onClick={e => e.target === e.currentTarget && onClose()}
      onKeyDown={e => e.key === 'Escape' && onClose()}
      onClose={onClose}
      onFocus={() => {}}
      open
    >
      <div className="bg-gray-800 rounded-2xl border border-gray-700 w-full max-w-md shadow-2xl">
        <div className="flex items-center justify-between p-5 border-b border-gray-700">
          <h2 className="text-lg font-semibold text-gray-100 flex items-center gap-2">
            <ListMusic className="w-5 h-5 text-blue-400" />
            Adicionar à playlist
          </h2>
          <button onClick={onClose} className="text-gray-400 hover:text-gray-200">
            <X className="w-5 h-5" />
          </button>
        </div>

        <div className="p-5 flex flex-col gap-3">
          <p className="text-xs text-gray-400 line-clamp-2 bg-gray-900 rounded p-2">
            {result.title}
          </p>

          {error && <p className="text-sm text-red-400">{error}</p>}

          {loading ? (
            <div className="flex justify-center py-4"><Loader2 className="w-5 h-5 animate-spin" /></div>
          ) : (
            <div className="flex flex-col gap-1 max-h-60 overflow-y-auto">
              {lists.length === 0 && !creating && (
                <p className="text-sm text-gray-500 italic text-center py-4">
                  Você ainda não tem playlists. Crie uma abaixo.
                </p>
              )}
              {lists.map(p => (
                <button
                  key={p.id}
                  onClick={() => handleAddPlaylist(p)}
                  disabled={busy}
                  className={`flex items-center justify-between gap-2 px-3 py-2 rounded-lg text-sm text-left transition-colors ${
                    added === p.id
                      ? 'bg-green-500/20 text-green-300 border border-green-500/30'
                      : 'bg-gray-900/50 hover:bg-gray-900 text-gray-200 border border-transparent'
                  }`}
                >
                  <div className="min-w-0">
                    <p className="truncate">{p.name}</p>
                    <p className="text-[10px] text-gray-500">{p.itemCount ?? 0} itens</p>
                  </div>
                  {added === p.id && <Check className="w-4 h-4 flex-shrink-0" />}
                </button>
              ))}
            </div>
          )}

          {!creating && (
                <button
                  onClick={() => setCreating(true)}
              className="flex items-center gap-1.5 text-sm text-blue-400 hover:text-blue-300 transition-colors mt-2"
            >
              <Plus className="w-4 h-4" /> Nova playlist
            </button>
          )}

          {creating && (
            <form onSubmit={createAndAdd} className="flex gap-2 mt-2">
              <input
                autoFocus
                type="text"
                value={newName}
                onChange={e => setNewName(e.target.value)}
                placeholder="Nome da nova playlist"
                className="input-field flex-1"
                disabled={busy}
              />
              <button
                type="submit"
                disabled={busy || !newName.trim()}
                className="btn-primary disabled:opacity-50"
              >
                {busy ? <Loader2 className="w-4 h-4 animate-spin" /> : 'Criar'}
              </button>
            </form>
          )}
        </div>
      </div>
    </dialog>
  )
}
