import { useEffect, useMemo, useState } from 'react'
import {
  ChevronRight,
  Folder,
  FileVideo,
  FileAudio,
  File as FileIcon,
  HardDrive,
  Home,
  X,
  ArrowDown,
  ArrowUp,
} from 'lucide-react'
import NavHeader from '../components/NavHeader'
import { usePersistedState } from '../lib/storage'
import {
  LocalEntry,
  LocalMount,
  localFileURL,
  localList,
  localMounts,
} from '../api/client'

type SortKey = 'name' | 'size' | 'date'
type KindFilter = 'all' | 'video' | 'audio' | 'other'

const VIDEO_EXTS = ['.mp4', '.m4v', '.mkv', '.avi', '.mov', '.wmv', '.webm', '.flv', '.mpeg', '.mpg', '.ts', '.m2ts']
const AUDIO_EXTS = ['.mp3', '.m4a', '.aac', '.flac', '.ogg', '.wav', '.opus']

function extOf(name: string): string {
  const i = name.lastIndexOf('.')
  return i === -1 ? '' : name.slice(i).toLowerCase()
}

function isVideo(name: string): boolean {
  return VIDEO_EXTS.includes(extOf(name))
}

function isAudio(name: string): boolean {
  return AUDIO_EXTS.includes(extOf(name))
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

function EntryIcon({ entry }: { entry: LocalEntry }) {
  if (entry.isDir) return <Folder className="w-5 h-5 text-blue-400" />
  if (isVideo(entry.name)) return <FileVideo className="w-5 h-5 text-purple-400" />
  if (isAudio(entry.name)) return <FileAudio className="w-5 h-5 text-pink-400" />
  return <FileIcon className="w-5 h-5 text-gray-400" />
}

function Breadcrumbs({
  mountName,
  path,
  onNavigate,
}: {
  mountName: string
  path: string
  onNavigate: (p: string) => void
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

function PlayerModal({
  mount,
  path,
  onClose,
}: {
  mount: string
  path: string
  onClose: () => void
}) {
  const src = localFileURL(mount, path)
  const audio = isAudio(path)

  return (
    <div
      className="fixed inset-0 z-50 bg-black/80 flex items-center justify-center p-4"
      onClick={onClose}
    >
      <div
        className="bg-gray-900 rounded-2xl max-w-5xl w-full overflow-hidden border border-gray-700"
        onClick={(e) => e.stopPropagation()}
      >
        <div className="flex items-center justify-between px-4 py-3 border-b border-gray-700">
          <div className="text-gray-100 font-medium truncate">{path}</div>
          <button
            onClick={onClose}
            className="text-gray-400 hover:text-gray-100"
            aria-label="close"
          >
            <X className="w-5 h-5" />
          </button>
        </div>
        <div className="bg-black flex items-center justify-center">
          {audio ? (
            <audio src={src} controls autoPlay className="w-full p-6" />
          ) : (
            <video
              src={src}
              controls
              autoPlay
              className="w-full max-h-[75vh]"
              playsInline
            />
          )}
        </div>
      </div>
    </div>
  )
}

export default function LocalPage() {
  const [mounts, setMounts] = useState<LocalMount[]>([])
  const [activeMount, setActiveMount] = useState<string>('')
  const [path, setPath] = useState<string>('')
  const [entries, setEntries] = useState<LocalEntry[]>([])
  const [loading, setLoading] = useState(false)
  const [error, setError] = useState('')
  const [playing, setPlaying] = useState<LocalEntry | null>(null)
  const [kind, setKind] = usePersistedState<KindFilter>('local.kind', 'all')
  const [sortKey, setSortKey] = usePersistedState<SortKey>('local.sortKey', 'name')
  const [sortDir, setSortDir] = usePersistedState<'asc' | 'desc'>('local.sortDir', 'asc')

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
        if (ms.length > 0 && !activeMount) {
          setActiveMount(ms[0].name)
        }
      })
      .catch((e: unknown) => {
        const msg = e instanceof Error ? e.message : 'Erro ao carregar mounts'
        setError(msg)
      })
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [])

  useEffect(() => {
    if (!activeMount) return
    setLoading(true)
    setError('')
    localList(activeMount, path)
      .then(setEntries)
      .catch((e: unknown) => {
        const msg = e instanceof Error ? e.message : 'Erro ao listar diretorio'
        setError(msg)
        setEntries([])
      })
      .finally(() => setLoading(false))
  }, [activeMount, path])

  const handleSelectMount = (name: string) => {
    setActiveMount(name)
    setPath('')
  }

  const handleEntryClick = (e: LocalEntry) => {
    if (e.isDir) {
      setPath(e.path)
      return
    }
    if (e.isPlayable) {
      setPlaying(e)
    }
  }

  return (
    <div className="h-screen bg-gray-900 flex flex-col overflow-hidden">
      <NavHeader />
      <main className="flex-1 min-h-0 max-w-7xl 2xl:max-w-[min(95vw,1600px)] mx-auto w-full px-4 py-6 flex gap-6">
        {/* Sidebar */}
        <aside className="w-56 flex-shrink-0 overflow-y-auto">
          <h2 className="text-xs uppercase tracking-wider text-gray-500 mb-3">
            Mounts
          </h2>
          {mounts.length === 0 ? (
            <p className="text-sm text-gray-500">
              Nenhum mount configurado. Adicione em <code>config.yaml</code>:
              <code className="block mt-2 p-2 bg-gray-800 rounded text-xs">
                external:{'\n'}  mounts:{'\n'}    - name: HD Externo{'\n'}      path: /mnt/external
              </code>
            </p>
          ) : (
            <ul className="space-y-1">
              {mounts.map((m) => {
                const active = m.name === activeMount
                return (
                  <li key={m.name}>
                    <button
                      onClick={() => handleSelectMount(m.name)}
                      className={`w-full flex items-center gap-2 px-3 py-2 rounded-lg text-sm transition-colors ${
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
        </aside>

        {/* Content */}
        <section className="flex-1 min-w-0 min-h-0 flex flex-col gap-4">
          {activeMount && (
            <Breadcrumbs mountName={activeMount} path={path} onNavigate={setPath} />
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
                  <li key={e.path}>
                    <button
                      onClick={() => handleEntryClick(e)}
                      disabled={!clickable}
                      className={`w-full flex items-center gap-3 px-4 py-2.5 text-left transition-colors ${
                        clickable
                          ? 'hover:bg-gray-700/50 cursor-pointer'
                          : 'cursor-default opacity-70'
                      }`}
                    >
                      <EntryIcon entry={e} />
                      <span className="flex-1 truncate text-gray-100">
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
                  </li>
                )
              })}
            </ul>
          )}
        </section>
      </main>

      {playing && activeMount && (
        <PlayerModal
          mount={activeMount}
          path={playing.path}
          onClose={() => setPlaying(null)}
        />
      )}
    </div>
  )
}
