import { useEffect, useMemo, useRef, useState } from 'react'
import { useSearchParams } from 'react-router-dom'
import {
  ChevronRight,
  Folder,
  FileVideo,
  FileAudio,
  File as FileIcon,
  HardDrive,
  Home,
  ArrowDown,
  ArrowUp,
  Trash2,
  ArrowUpCircle,
  FolderSync,
  FolderInput,
  Upload,
  X,
} from 'lucide-react'
import NavHeader from '../components/NavHeader'
import { usePersistedState } from '../lib/storage'
import { usePlayer } from '../components/PlayerProvider'
import { useAuth } from '../auth/AuthContext'
import LocalPromoteModal from '../components/LocalPromoteModal'
import ReclassifyFolderModal from '../components/ReclassifyFolderModal'
import MoveFolderModal from '../components/MoveFolderModal'
import {
  LocalEntry,
  LocalMount,
  SearchResult,
  AdminUser,
  buildLocalHash,
  localThumbURL,
  localList,
  localMounts,
  localDelete,
  localUpload,
  adminListUsers,
  setLocalViewAsUser,
} from '../api/client'

type SortKey = 'name' | 'size' | 'date'
type KindFilter = 'all' | 'video' | 'audio' | 'other'

const VIDEO_EXTS = new Set(['.mp4', '.m4v', '.mkv', '.avi', '.mov', '.wmv', '.webm', '.flv', '.mpeg', '.mpg', '.ts', '.m2ts'])
const AUDIO_EXTS = new Set(['.mp3', '.m4a', '.aac', '.flac', '.ogg', '.wav', '.opus'])

function extOf(name: string): string {
  const i = name.lastIndexOf('.')
  return i === -1 ? '' : name.slice(i).toLowerCase()
}

function isVideo(name: string): boolean {
  return VIDEO_EXTS.has(extOf(name))
}

function isAudio(name: string): boolean {
  return AUDIO_EXTS.has(extOf(name))
}

function formatSize(bytes: number): string {
  if (bytes < 1024) return `${bytes} B`
  const units = ['KB', 'MB', 'GB', 'TB']
  let value = bytes / 1024
  let i = 0
  while (value >= 1024 && i < units.length - 1) {
    value /= 1024
    i++
  }
  return `${value.toFixed(value >= 10 ? 0 : 1)} ${units[i]}`
}

function formatDate(iso: string): string {
  try {
    return new Date(iso).toLocaleDateString()
  } catch {
    return ''
  }
}

function EntryIcon({ entry, mount }: { readonly entry: LocalEntry; readonly mount: string }) {
  const [thumbFailed, setThumbFailed] = useState(false)
  if (entry.isDir) return <Folder className="w-5 h-5 text-blue-400 flex-shrink-0" />
  if (isVideo(entry.name)) {
    // Early-frame preview (lazy); falls back to the icon if the server can't
    // decode one (204/error). Fixed 16:9 box keeps rows aligned.
    if (thumbFailed) return <FileVideo className="w-5 h-5 text-purple-400 flex-shrink-0" />
    return (
      <img
        src={localThumbURL(mount, entry.path)}
        alt=""
        loading="lazy"
        onError={() => setThumbFailed(true)}
        className="w-14 h-8 object-cover rounded bg-gray-900 border border-gray-700 flex-shrink-0"
      />
    )
  }
  if (isAudio(entry.name)) return <FileAudio className="w-5 h-5 text-pink-400 flex-shrink-0" />
  return <FileIcon className="w-5 h-5 text-gray-400 flex-shrink-0" />
}

function Breadcrumbs({
  mountName,
  path,
  onNavigate,
}: {
  readonly mountName: string
  readonly path: string
  readonly onNavigate: (p: string) => void
}) {
  const segments = useMemo(() => (path === '' ? [] : path.split('/')), [path])

  return (
    <nav className="flex items-center gap-1 text-sm text-gray-300 flex-wrap">
      <button
        onClick={() => onNavigate('')}
        className="flex items-center gap-1 hover:text-green-400 transition-colors"
      >
        <Home className="w-4 h-4" />
        <span>{mountName}</span>
      </button>
      {segments.map((seg, idx) => {
        const target = segments.slice(0, idx + 1).join('/')
        const isLast = idx === segments.length - 1
        return (
          <span key={target} className="flex items-center gap-1">
            <ChevronRight className="w-4 h-4 text-gray-600" />
            <button
              onClick={() => onNavigate(target)}
              className={`hover:text-green-400 transition-colors ${
                isLast ? 'text-gray-100 font-medium' : ''
              }`}
            >
              {seg}
            </button>
          </span>
        )
      })}
    </nav>
  )
}


export default function LocalPage() {
  const [searchParams, setSearchParams] = useSearchParams()
  const [mounts, setMounts] = useState<LocalMount[]>([])
  const activeMount = searchParams.get('mount') || ''
  const path = searchParams.get('path') || ''
  const [entries, setEntries] = useState<LocalEntry[]>([])
  const [loading, setLoading] = useState(false)
  const [error, setError] = useState('')
  const { playSingle } = usePlayer()
  const [kind, setKind] = usePersistedState<KindFilter>('local.kind', 'all')
  const [sortKey, setSortKey] = usePersistedState<SortKey>('local.sortKey', 'name')
  const [sortDir, setSortDir] = usePersistedState<'asc' | 'desc'>('local.sortDir', 'asc')

  const [promoteItem, setPromoteItem] = useState<LocalEntry | null>(null)
  const [deleteConfirmItem, setDeleteConfirmItem] = useState<LocalEntry | null>(null)
  const [deleting, setDeleting] = useState(false)
  const [reclassifyItem, setReclassifyItem] = useState<LocalEntry | null>(null)
  const [moveItem, setMoveItem] = useState<LocalEntry | null>(null)

  // Upload state: tracks the in-flight transfer for the progress banner. The
  // AbortController lets the user cancel mid-stream; the hidden <input> is reset
  // after each pick so re-selecting the same file fires onChange again.
  const [upload, setUpload] = useState<{ name: string; loaded: number; total: number } | null>(null)
  const [uploadError, setUploadError] = useState('')
  const uploadAbortRef = useRef<AbortController | null>(null)
  const fileInputRef = useRef<HTMLInputElement | null>(null)

  const updateNavigation = (newMount: string, newPath: string, replace = false) => {
    const params = new URLSearchParams(globalThis.location.search)
    if (newMount) params.set('mount', newMount)
    else params.delete('mount')
    
    if (newPath) params.set('path', newPath)
    else params.delete('path')
    
    setSearchParams(params, { replace })
  }

  const { isGuest, isAdmin } = useAuth()
  // Admin "view as user": '' = own space. When set, every /api/local/* call
  // carries ?user= (the backend re-checks the admin role before honoring it).
  const [viewAsUser, setViewAsUser] = useState('')
  const [adminUsers, setAdminUsers] = useState<AdminUser[]>([])
  const activeMountObj = useMemo(() => mounts.find((m) => m.name === activeMount), [mounts, activeMount])
  const canViewAsUser = isAdmin && !!activeMountObj?.userSubpath
  const canManipulate = !isGuest && activeMount.toLowerCase() === 'meus downloads'

  // Folders always show (so navigation never gets filtered away); the kind
  // filter + sort apply within each group, folders kept on top.
  const visible = useMemo(() => {
    const dirs = entries.filter((e) => e.isDir)
    let files = entries.filter((e) => !e.isDir)
    if (kind === 'video') files = files.filter((e) => isVideo(e.name))
    else if (kind === 'audio') files = files.filter((e) => isAudio(e.name))
    else if (kind === 'other') files = files.filter((e) => !isVideo(e.name) && !isAudio(e.name))

    const cmp = (a: LocalEntry, b: LocalEntry) => {
      let r = 0
      if (sortKey === 'name') r = a.name.localeCompare(b.name, undefined, { numeric: true })
      else if (sortKey === 'size') r = a.size - b.size
      else r = new Date(a.modTime).getTime() - new Date(b.modTime).getTime()
      return sortDir === 'asc' ? r : -r
    }
    return [...dirs.sort(cmp), ...files.sort(cmp)]
  }, [entries, kind, sortKey, sortDir])

  const toggleSort = (key: SortKey) => {
    if (sortKey === key) setSortDir((d) => (d === 'asc' ? 'desc' : 'asc'))
    else { setSortKey(key); setSortDir(key === 'name' ? 'asc' : 'desc') }
  }

  useEffect(() => {
    localMounts()
      .then((ms) => {
        setMounts(ms)
        const params = new URLSearchParams(globalThis.location.search)
        const mountFromUrl = params.get('mount')
        if (ms.length > 0 && !mountFromUrl) {
          params.set('mount', ms[0].name)
          params.set('path', '')
          setSearchParams(params, { replace: true })
        }
      })
      .catch((e: unknown) => {
        const msg = e instanceof Error ? e.message : 'Erro ao carregar mounts'
        setError(msg)
      })
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [])

  // reqSeq guards against out-of-order responses: when the user navigates
  // quickly (or the initial mount load is still in flight), two localList calls
  // race and the slower one could overwrite the newer result — showing stale or
  // empty content and a flash. Only the latest request is allowed to commit.
  const reqSeq = useRef(0)

  const refresh = () => {
    if (!activeMount) return
    // Sync the client module's view-as state BEFORE any local call so list +
    // subsequent play/thumb/move/delete all hit the selected user's space.
    setLocalViewAsUser(viewAsUser)
    const seq = ++reqSeq.current
    setLoading(true)
    setError('')
    localList(activeMount, path)
      .then((data) => {
        if (seq !== reqSeq.current) return
        setEntries(data)
      })
      .catch((e: unknown) => {
        if (seq !== reqSeq.current) return
        const msg = e instanceof Error ? e.message : 'Erro ao listar diretorio'
        setError(msg)
        setEntries([])
      })
      .finally(() => {
        if (seq === reqSeq.current) setLoading(false)
      })
  }

  useEffect(() => {
    refresh()
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [activeMount, path, viewAsUser])

  // Load the user list once for the admin "view as user" selector.
  useEffect(() => {
    if (!isAdmin) return
    adminListUsers().then(setAdminUsers).catch(() => setAdminUsers([]))
  }, [isAdmin])

  // Reset the view-as override when leaving the page so other views (e.g. the
  // player opening a local "continue watching" item) aren't silently scoped to
  // another user.
  useEffect(() => {
    return () => setLocalViewAsUser('')
  }, [])

  const handleDelete = async () => {
    if (!deleteConfirmItem || !activeMount) return
    setDeleting(true)
    setError('')
    try {
      await localDelete(activeMount, deleteConfirmItem.path)
      setDeleteConfirmItem(null)
      refresh()
    } catch (e: any) {
      setError(e?.response?.data?.error || e.message || 'Erro ao apagar arquivo')
    } finally {
      setDeleting(false)
    }
  }

  const handleUploadPick = async (e: React.ChangeEvent<HTMLInputElement>) => {
    const file = e.target.files?.[0]
    // Reset the input so picking the same file again re-fires onChange.
    e.target.value = ''
    if (!file || !activeMount) return

    const controller = new AbortController()
    uploadAbortRef.current = controller
    setUploadError('')
    setUpload({ name: file.name, loaded: 0, total: file.size })
    setLocalViewAsUser(viewAsUser) // keep admin "view as user" scoping consistent
    try {
      await localUpload(
        activeMount,
        path,
        file,
        (loaded, total) => setUpload({ name: file.name, loaded, total }),
        controller.signal,
      )
      setUpload(null)
      refresh()
    } catch (err: any) {
      if (controller.signal.aborted) {
        setUploadError('Upload cancelado.')
      } else {
        setUploadError(err?.response?.data?.error || err?.message || 'Erro ao enviar arquivo')
      }
      setUpload(null)
    } finally {
      uploadAbortRef.current = null
    }
  }

  const handleSelectMount = (name: string) => {
    updateNavigation(name, '')
    setViewAsUser('') // back to own space when switching mounts
  }

  const handleViewAsUser = (username: string) => {
    setLocalViewAsUser(username) // module state — takes effect on the next call
    setViewAsUser(username)
    updateNavigation(activeMount, '') // jump to the root of the selected user's space
  }

  const handleEntryClick = (e: LocalEntry) => {
    if (e.isDir) {
      updateNavigation(activeMount, e.path)
      return
    }
    if (!e.isPlayable || !activeMount) return
    // Routes the file through the main PlayerProvider/PlayerModal via a
    // synthetic SearchResult com pseudo-hash `local-...` (mount+path codificados).
    // Resultado: o player completo abre — legendas embedded, sidecar .srt/.vtt,
    // OpenSubtitles auto, escolha persistida, tudo. As funções do client (streamProbe,
    // streamSidecars, subtitlesAuto, etc.) detectam o prefixo e roteiam pra
    // /api/local/* sem mudar PlayerModal.
    const hash = buildLocalHash(activeMount, e.path)
    const synthetic: SearchResult = {
      title: e.name,
      tracker: '',
      categoryId: 0,
      category: '',
      size: e.size,
      seeders: 0,
      leechers: 0,
      age: '',
      magnetUri: `magnet:?xt=urn:btih:${hash}`,
      link: '',
      infoHash: hash,
      publishDate: '',
    }
    playSingle(synthetic, 0)
  }

  return (
    <div className="h-screen bg-gray-900 flex flex-col overflow-hidden">
      <NavHeader />
      <main className="flex-1 min-h-0 max-w-7xl 2xl:max-w-[min(95vw,1600px)] mx-auto w-full px-4 py-6 flex flex-col md:flex-row gap-4 md:gap-6">
        {/* Sidebar — desktop é coluna fixa à esquerda; mobile vira faixa horizontal
            no topo (chips de mount) pra não roubar metade da tela do conteúdo. */}
        <aside className="md:w-56 flex-shrink-0 md:overflow-y-auto">
          <h2 className="text-xs uppercase tracking-wider text-gray-500 mb-2 md:mb-3">
            Mounts
          </h2>
          {mounts.length === 0 ? (
            <><p className="text-sm text-gray-500">
              Nenhum mount configurado. Adicione em <code>config.yaml</code>:
            </p>
            <code className="block mt-2 p-2 bg-gray-800 rounded text-xs">
                external:{'\n'}  mounts:{'\n'}    - name: HD Externo{'\n'}      path: /mnt/external
              </code></>
          ) : (
            <ul className="flex md:flex-col gap-2 md:gap-1 overflow-x-auto md:overflow-visible md:space-y-1 -mx-1 px-1 md:mx-0 md:px-0">
              {mounts.map((m) => {
                const active = m.name === activeMount
                return (
                  <li key={m.name} className="flex-shrink-0">
                    <button
                      onClick={() => handleSelectMount(m.name)}
                      className={`w-full flex items-center gap-2 px-3 py-2 rounded-lg text-sm transition-colors whitespace-nowrap ${
                        active
                          ? 'bg-green-500/10 text-green-400 border border-green-500/30'
                          : 'text-gray-300 hover:bg-gray-800 border border-transparent'
                      }`}
                    >
                      <HardDrive className="w-4 h-4 flex-shrink-0" />
                      <span className="truncate">{m.name}</span>
                    </button>
                  </li>
                )
              })}
            </ul>
          )}

          {canViewAsUser && (
            <div className="mt-5 md:mt-6">
              <h2 className="text-xs uppercase tracking-wider text-gray-500 mb-2">Ver como</h2>
              <select
                value={viewAsUser}
                onChange={(e) => handleViewAsUser(e.target.value)}
                className="w-full px-3 py-2 rounded-lg text-sm bg-gray-800 border border-gray-700 text-gray-200 focus:border-green-500/50 focus:outline-none"
              >
                <option value="">Meu espaço (admin)</option>
                {adminUsers.map((u) => (
                  <option key={u.id} value={u.username}>{u.username}</option>
                ))}
              </select>
              {viewAsUser && (
                <p className="mt-1.5 text-[11px] text-amber-400/80">
                  Vendo o espaço de <strong>{viewAsUser}</strong>
                </p>
              )}
            </div>
          )}
        </aside>

        {/* Content */}
        <section className="flex-1 min-w-0 min-h-0 flex flex-col gap-4">
          {activeMount && (
            <div className="flex-shrink-0 flex items-center justify-between gap-3">
              <Breadcrumbs mountName={activeMount} path={path} onNavigate={(p) => updateNavigation(activeMount, p)} />
              {(canManipulate || isAdmin) && (
                <>
                  <input
                    ref={fileInputRef}
                    type="file"
                    accept=".mkv,.mp4,.m4v,.avi,.mov,.webm,.ts,.m2ts,.mpg,.mpeg,.wmv,.flv,.ogv,.3gp,.srt,.vtt,.ass,.ssa,.sub"
                    onChange={handleUploadPick}
                    className="hidden"
                  />
                  <button
                    onClick={() => fileInputRef.current?.click()}
                    disabled={!!upload}
                    title="Enviar arquivo para esta pasta"
                    className="flex-shrink-0 inline-flex items-center gap-1.5 text-sm bg-green-500/15 hover:bg-green-500/25 disabled:opacity-50 text-green-400 border border-green-500/30 px-3 py-1.5 rounded-lg transition-colors font-medium"
                  >
                    <Upload className="w-4 h-4" />
                    <span className="hidden sm:inline">Upload</span>
                  </button>
                </>
              )}
            </div>
          )}

          {/* Banner de progresso do upload (streaming direto pro disco no backend) */}
          {upload && (
            <div className="flex-shrink-0 bg-gray-800 border border-green-500/30 rounded-xl p-3 flex flex-col gap-2">
              <div className="flex items-center gap-2 text-sm text-gray-200">
                <Upload className="w-4 h-4 text-green-400 flex-shrink-0 animate-pulse" />
                <span className="truncate flex-1">{upload.name}</span>
                <span className="text-xs text-gray-400 tabular-nums">
                  {formatSize(upload.loaded)} / {formatSize(upload.total)}
                  {upload.total > 0 && ` (${Math.round((upload.loaded / upload.total) * 100)}%)`}
                </span>
                <button
                  onClick={() => uploadAbortRef.current?.abort()}
                  title="Cancelar upload"
                  className="p-1 rounded text-gray-400 hover:text-red-400 transition-colors"
                >
                  <X className="w-4 h-4" />
                </button>
              </div>
              <div className="h-1.5 bg-gray-700 rounded-full overflow-hidden">
                <div
                  className="h-full bg-green-500 transition-all duration-150"
                  style={{ width: `${upload.total > 0 ? (upload.loaded / upload.total) * 100 : 0}%` }}
                />
              </div>
            </div>
          )}

          {uploadError && (
            <div className="flex-shrink-0 bg-amber-500/10 border border-amber-500/30 text-amber-400 rounded-xl px-4 py-2.5 text-sm flex items-center justify-between gap-2">
              <span>{uploadError}</span>
              <button onClick={() => setUploadError('')} className="text-amber-400/70 hover:text-amber-300">
                <X className="w-4 h-4" />
              </button>
            </div>
          )}

          {/* Toolbar: kind filter chips + sort controls (flex-shrink-0 so it
              stays put while the list below scrolls). */}
          {activeMount && entries.length > 0 && (
            <div className="flex-shrink-0 flex flex-wrap items-center gap-2 text-xs">
              {(['all', 'video', 'audio', 'other'] as KindFilter[]).map((k) => (
                <button
                  key={k}
                  onClick={() => setKind(k)}
                  className={`px-2.5 py-1 rounded-full border transition-colors ${
                    kind === k
                      ? 'bg-green-500/15 text-green-400 border-green-500/40'
                      : 'text-gray-400 border-gray-700 hover:border-gray-600'
                  }`}
                >
                  {{ all: 'Todos', video: 'Vídeo', audio: 'Áudio', other: 'Outros' }[k]}
                </button>
              ))}
              <span className="mx-1 h-4 w-px bg-gray-700" />
              <span className="text-gray-500">Ordenar:</span>
              {(['name', 'size', 'date'] as SortKey[]).map((k) => (
                <button
                  key={k}
                  onClick={() => toggleSort(k)}
                  className={`flex items-center gap-1 px-2.5 py-1 rounded-full border transition-colors ${
                    sortKey === k
                      ? 'bg-gray-700 text-gray-100 border-gray-600'
                      : 'text-gray-400 border-gray-700 hover:border-gray-600'
                  }`}
                >
                  {{ name: 'Nome', size: 'Tamanho', date: 'Data' }[k]}
                  {sortKey === k && (sortDir === 'asc' ? <ArrowUp className="w-3 h-3" /> : <ArrowDown className="w-3 h-3" />)}
                </button>
              ))}
            </div>
          )}

          {error && (
            <div className="bg-red-500/10 border border-red-500/30 text-red-400 rounded-xl p-4 text-sm">
              {error}
            </div>
          )}

          {loading && (
            <div className="text-gray-500 text-sm">Carregando...</div>
          )}

          {!loading && !error && activeMount && visible.length === 0 && (
            <div className="text-gray-500 text-sm">
              {entries.length === 0 ? 'Pasta vazia' : 'Nenhum arquivo com esse filtro'}
            </div>
          )}

          {!loading && visible.length > 0 && (
            <ul className="flex-1 min-h-0 overflow-y-auto divide-y divide-gray-800 bg-gray-800/50 rounded-xl border border-gray-700">
              {visible.map((e) => {
                const clickable = e.isDir || e.isPlayable
                return (
                  <li key={e.path} className="flex items-center justify-between hover:bg-gray-700/20 group">
                    <button
                      onClick={() => handleEntryClick(e)}
                      disabled={!clickable}
                      className={`flex-1 flex items-center gap-3 px-4 py-2.5 text-left transition-colors ${
                        clickable
                          ? 'cursor-pointer'
                          : 'cursor-default opacity-70'
                      }`}
                    >
                      <EntryIcon entry={e} mount={activeMount} />
                      <span className="flex-1 truncate text-gray-100 font-medium">
                        {e.name}
                      </span>
                      {!e.isDir && (
                        <span className="text-xs text-gray-500 w-20 text-right">
                          {formatSize(e.size)}
                        </span>
                      )}
                      <span className="text-xs text-gray-500 w-24 text-right hidden sm:block">
                        {formatDate(e.modTime)}
                      </span>
                    </button>

                    {/* Ações rápidas */}
                    {(canManipulate || isAdmin) && (
                      <div className="flex items-center gap-1.5 px-4 sm:opacity-0 group-hover:opacity-100 focus-within:opacity-100 transition-opacity">
                        {/* Promover: usuário em Meus downloads OU admin em qualquer mount */}
                        {(canManipulate || isAdmin) && !e.isDir && (
                          <button
                            onClick={(evt) => { evt.stopPropagation(); setPromoteItem(e) }}
                            title="Promover / Organizar via IA"
                            className="p-1.5 rounded-lg text-cyan-400 hover:bg-cyan-500/10 border border-transparent hover:border-cyan-500/20 transition-all"
                          >
                            <ArrowUpCircle className="w-4.5 h-4.5" />
                          </button>
                        )}
                        {/* Reclassificar: admin em dirs E arquivos */}
                        {isAdmin && (
                          <button
                            onClick={(evt) => { evt.stopPropagation(); setReclassifyItem(e) }}
                            title={e.isDir ? 'Reclassificar pasta via IA (Plex)' : 'Classificar e mover via IA'}
                            className="p-1.5 rounded-lg text-purple-400 hover:bg-purple-500/10 border border-transparent hover:border-purple-500/20 transition-all"
                          >
                            <FolderSync className="w-4.5 h-4.5" />
                          </button>
                        )}
                        {/* Mover entre mounts: admin */}
                        {isAdmin && (
                          <button
                            onClick={(evt) => { evt.stopPropagation(); setMoveItem(e) }}
                            title="Mover para outro mount"
                            className="p-1.5 rounded-lg text-amber-400 hover:bg-amber-500/10 border border-transparent hover:border-amber-500/20 transition-all"
                          >
                            <FolderInput className="w-4.5 h-4.5" />
                          </button>
                        )}
                        {/* Apagar: usuário em Meus downloads OU admin em qualquer
                            mount (pastas e arquivos). Alinha com o backend
                            canModifyMount, que libera admin em qualquer mount. */}
                        {(canManipulate || isAdmin) && (
                          <button
                            onClick={(evt) => { evt.stopPropagation(); setDeleteConfirmItem(e) }}
                            title={e.isDir ? 'Apagar pasta permanentemente' : 'Apagar permanentemente'}
                            className="p-1.5 rounded-lg text-red-400 hover:bg-red-500/10 border border-transparent hover:border-red-500/20 transition-all"
                          >
                            <Trash2 className="w-4.5 h-4.5" />
                          </button>
                        )}
                      </div>
                    )}
                  </li>
                )
              })}
            </ul>
          )}

          {/* Confirmação de Deleção */}
          {deleteConfirmItem && (
            <div className="fixed inset-0 bg-black/60 backdrop-blur-sm flex items-center justify-center z-50 p-4">
              <div className="bg-gray-800 rounded-2xl border border-gray-700 w-full max-w-md shadow-2xl p-6 flex flex-col gap-4">
                <h3 className="text-base font-semibold text-gray-100 flex items-center gap-2">
                  <Trash2 className="w-5 h-5 text-red-400" />
                  Apagar permanentemente?
                </h3>
                <p className="text-sm text-gray-300">
                  Tem certeza que deseja apagar <span className="text-red-400 font-medium">"{deleteConfirmItem.name}"</span>? Esta ação é irreversível e excluirá o arquivo de forma permanente no servidor.
                </p>
                <p className="text-xs text-amber-400/80">
                  O torrent vinculado (se houver) também será removido: registro do download, pieces no cache e marcação de favorito.
                </p>
                <div className="flex items-center gap-2 justify-end mt-2">
                  <button
                    onClick={() => setDeleteConfirmItem(null)}
                    disabled={deleting}
                    className="text-sm text-gray-400 hover:text-gray-200 px-3 py-1.5 rounded"
                  >
                    Cancelar
                  </button>
                  <button
                    onClick={handleDelete}
                    disabled={deleting}
                    className="text-sm bg-red-500/20 hover:bg-red-500/30 disabled:opacity-50 text-red-300 border border-red-500/30 px-4 py-1.5 rounded transition-colors flex items-center gap-1.5 font-medium"
                  >
                    {deleting ? 'Apagando...' : 'Apagar'}
                  </button>
                </div>
              </div>
            </div>
          )}

          {/* Modal de Promoção */}
          <LocalPromoteModal
            mount={activeMount}
            entry={promoteItem}
            onClose={() => setPromoteItem(null)}
            onPromoted={refresh}
          />

          {/* Modal de Reclassificação em lote via IA */}
          <ReclassifyFolderModal
            mount={activeMount}
            entry={reclassifyItem}
            onClose={() => setReclassifyItem(null)}
            onDone={() => { setReclassifyItem(null); refresh() }}
          />

          {/* Modal de Mover entre mounts */}
          <MoveFolderModal
            mount={activeMount}
            entry={moveItem}
            onClose={() => setMoveItem(null)}
            onMoved={() => { setMoveItem(null); refresh() }}
          />
        </section>
      </main>
    </div>
  )
}
