import { useState, useEffect } from 'react'
import { Link, useNavigate } from 'react-router-dom'
import { useTranslation } from 'react-i18next'
import { ListMusic, Plus, Loader2, Trash2, Clock } from 'lucide-react'
import { playlistsList, playlistsCreate, playlistsDelete, Playlist } from '../api/client'
import NavHeader from '../components/NavHeader'
import PullToRefreshIndicator from '../components/PullToRefreshIndicator'
import { usePullToRefresh } from '../lib/usePullToRefresh'
import { useConfirm } from '../components/ConfirmDialog'
import { formatDate } from '../lib/format'

export default function PlaylistsPage() {
  const { t } = useTranslation()
  const nav = useNavigate()
  const confirm = useConfirm()
  const [lists, setLists] = useState<Playlist[]>([])
  const [loading, setLoading] = useState(true)
  const [creating, setCreating] = useState(false)
  const [newName, setNewName] = useState('')
  const [error, setError] = useState('')

  const load = async () => {
    setLoading(true)
    setError('')
    try { setLists(await playlistsList()) }
    catch (e: any) { setError(e?.response?.data?.error || e.message) }
    finally { setLoading(false) }
  }

  useEffect(() => { load() }, [])

  const ptr = usePullToRefresh({ onRefresh: load, disabled: loading })

  const submit = async (e: React.FormEvent) => {
    e.preventDefault()
    if (!newName.trim()) return
    const p = await playlistsCreate(newName.trim())
    setNewName('')
    setCreating(false)
    nav(`/playlists/${p.id}`)
  }

  const remove = async (p: Playlist) => {
    const ok = await confirm({ title: t('playlists.delete_title'), message: t('playlists.delete_message', { name: p.name }), confirmLabel: t('playlists.delete_confirm'), destructive: true })
    if (!ok) return
    await playlistsDelete(p.id)
    setLists(lists.filter(x => x.id !== p.id))
  }

  return (
    <div className="min-h-screen bg-surface flex flex-col">
      <PullToRefreshIndicator pull={ptr.pull} progress={ptr.progress} refreshing={ptr.refreshing} />
      <NavHeader />

      <main className="flex-1 max-w-7xl 2xl:max-w-[min(95vw,1600px)] mx-auto w-full px-4 py-6 flex flex-col gap-4">
        <div className="flex items-center justify-between flex-wrap gap-3">
          <div className="flex items-center gap-3">
            <ListMusic className="w-5 h-5 text-blue-400" />
            <h1 className="text-lg font-semibold text-text-primary">{t('playlists.title')}</h1>
            {!loading && (
              <span className="text-xs text-text-muted bg-surface-secondary border border-default px-2 py-0.5 rounded-full">
                {t('playlists.count', { count: lists.length })}
              </span>
            )}
          </div>
          <button
            onClick={() => setCreating(c => !c)}
            className="flex items-center gap-1.5 text-xs bg-green-500/20 hover:bg-green-500/30 text-green-400 border border-green-500/30 px-3 py-1.5 rounded-lg transition-colors"
          >
            <Plus className="w-3.5 h-3.5" /> {t('playlists.new')}
          </button>
        </div>

        {creating && (
          <form onSubmit={submit} className="card flex gap-2">
            <input
              autoFocus
              type="text"
              value={newName}
              onChange={e => setNewName(e.target.value)}
              placeholder={t('playlists.name_placeholder')}
              className="input-field flex-1"
            />
            <button type="submit" disabled={!newName.trim()} className="btn-primary disabled:opacity-50">
              {t('playlists.create')}
            </button>
            <button type="button" onClick={() => { setCreating(false); setNewName('') }} className="btn-secondary">
              {t('playlists.cancel')}
            </button>
          </form>
        )}

        {error && <div className="card text-red-400 text-sm">{error}</div>}

{(() => {
          if (loading) return <div className="flex justify-center py-20"><Loader2 className="w-8 h-8 animate-spin text-text-muted" /></div>
          if (lists.length === 0) return <div className="flex flex-col items-center justify-center py-20 text-text-muted"><ListMusic className="w-16 h-16 mb-4 opacity-30" /><p className="text-xl font-medium">{t('playlists.empty_title')}</p><p className="text-sm mt-2">{t('playlists.empty_hint')}</p></div>
          return <div className="grid grid-cols-1 md:grid-cols-2 lg:grid-cols-3 gap-4">{lists.map(p => (
            <Link to={`/playlists/${p.id}`} key={p.id} className="card flex flex-col gap-2 hover:border-blue-500/30 group">
              <div className="flex items-start justify-between gap-2">
                <h3 className="text-base font-medium text-text-primary line-clamp-1 flex-1">{p.name}</h3>
                <button onClick={(e) => { e.preventDefault(); remove(p) }} className="text-text-muted hover:text-red-400 max-sm:opacity-100 opacity-0 group-hover:opacity-100 transition-all"><Trash2 className="w-4 h-4" /></button>
              </div>
              {p.description && <p className="text-xs text-text-muted line-clamp-2">{p.description}</p>}
              <div className="flex items-center gap-3 text-xs text-text-muted mt-auto pt-2 border-t border-default">
                <span className="flex items-center gap-1"><ListMusic className="w-3 h-3" />{t('playlists.items', { count: p.itemCount ?? 0 })}</span>
                <span className="flex items-center gap-1"><Clock className="w-3 h-3" />{formatDate(p.updatedAt)}</span>
              </div>
            </Link>
          ))}</div>
        })()}
      </main>
    </div>
  )
}
