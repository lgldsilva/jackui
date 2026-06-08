import { useEffect, useMemo, useRef, useState } from 'react'
import { useSearchParams } from 'react-router-dom'
import {
  ChevronRight,
  ChevronDown,
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
  FolderX,
  FolderInput,
  Upload,
  Search,
  Check,
  X,
  Lock,
  Users,
  MoreVertical,
} from 'lucide-react'
import NavHeader from '../components/NavHeader'
import { usePersistedState } from '../lib/storage'
import { formatBytes } from '../lib/format'
import { usePlayer } from '../components/PlayerProvider'
import { useAuth } from '../auth/AuthContext'
import { useConfirm } from '../components/ConfirmDialog'
import { useLongPress } from '../lib/useLongPress'
import { useIsMobile } from '../lib/useMediaQuery'
import { Sheet } from '../components/Sheet'
import { BatchActionBar } from '../components/BatchActionBar'
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
  localCleanEmptyDirs,
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

// Barra de espaço livre/total do filesystem do mount (discos físicos, rclone).
// Some quando o backend não conseguiu medir (mount quebrado → totalBytes 0).
// MountBadge flags a mount's visibility: 🔒 per-user (private subdir) or
// 👥 restricted (visible only to specific users). Shared mounts get no badge.
function MountBadge({ m }: { readonly m: LocalMount }) {
  if (m.userSubpath) {
    return <Lock className="w-3 h-3 text-amber-400 flex-shrink-0" aria-label="privado por usuário" />
  }
  if (m.restricted) {
    return <Users className="w-3 h-3 text-blue-400 flex-shrink-0" aria-label="restrito a usuários específicos" />
  }
  return null
}

function MountSpaceLabel({ m }: { readonly m: LocalMount }) {
  if (!m.totalBytes || m.totalBytes <= 0) return null
  const free = m.freeBytes ?? 0
  const pctUsed = Math.min(100, Math.max(0, Math.round(((m.totalBytes - free) / m.totalBytes) * 100)))
  let barColor = 'bg-green-500'
  if (pctUsed > 90) barColor = 'bg-red-500'
  else if (pctUsed > 75) barColor = 'bg-amber-500'
  return (
    <div className="px-3 pb-1 -mt-0.5">
      <div className="h-1 rounded-full bg-surface-tertiary/80 overflow-hidden">
        <div className={`h-full rounded-full ${barColor}`} style={{ width: `${pctUsed}%` }} />
      </div>
      <p className="text-[10px] text-text-muted mt-0.5">{formatBytes(free)} livres de {formatBytes(m.totalBytes)}</p>
    </div>
  )
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
        className="w-14 h-8 object-cover rounded bg-surface border border-default flex-shrink-0"
      />
    )
  }
  if (isAudio(entry.name)) return <FileAudio className="w-5 h-5 text-pink-400 flex-shrink-0" />
  return <FileIcon className="w-5 h-5 text-text-secondary flex-shrink-0" />
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
  const isMobile = useIsMobile()
  // No mobile, paths profundos poluem a barra. Colapsa pra Home › … › atual
  // (o … sobe um nível). No desktop mostra o caminho inteiro.
  const collapsed = isMobile && segments.length > 2
  const shown = collapsed ? segments.slice(-1) : segments

  return (
    <nav className="flex items-center gap-1 text-sm text-text-primary flex-wrap min-w-0">
      <button
        onClick={() => onNavigate('')}
        className="flex items-center gap-1 hover:text-green-400 transition-colors flex-shrink-0 min-w-0"
      >
        <Home className="w-4 h-4 flex-shrink-0" />
        <span className="truncate max-w-[40vw] sm:max-w-none">{mountName}</span>
      </button>
      {collapsed && (
        <span className="flex items-center gap-1 flex-shrink-0">
          <ChevronRight className="w-4 h-4 text-text-muted" />
          <button
            onClick={() => onNavigate(segments.slice(0, -1).join('/'))}
            title="Subir um nível"
            className="px-1 hover:text-green-400 transition-colors"
          >
            …
          </button>
        </span>
      )}
      {shown.map((seg, i) => {
        const idx = collapsed ? segments.length - 1 : i
        const target = segments.slice(0, idx + 1).join('/')
        const isLast = idx === segments.length - 1
        return (
          <span key={target} className="flex items-center gap-1 min-w-0">
            <ChevronRight className="w-4 h-4 text-text-muted flex-shrink-0" />
            <button
              onClick={() => onNavigate(target)}
              className={`hover:text-green-400 transition-colors truncate max-w-[55vw] sm:max-w-none ${
                isLast ? 'text-text-primary font-medium' : ''
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

type EntryRowProps = {
  readonly entry: LocalEntry
  readonly mount: string
  readonly selectMode: boolean
  readonly selected: boolean
  readonly canManipulate: boolean
  readonly isAdmin: boolean
  readonly onOpen: (e: LocalEntry) => void
  readonly onEnterSelect: (e: LocalEntry) => void
  readonly onToggleSelect: (e: LocalEntry) => void
  readonly onPromote: (e: LocalEntry) => void
  readonly onReclassify: (e: LocalEntry) => void
  readonly onMove: (e: LocalEntry) => void
  readonly onDelete: (e: LocalEntry) => void
}

// Ações por-item (promover/reclassificar/mover/apagar). No desktop aparecem no
// hover; no mobile viram um único alvo ⋮ (>=44px) que abre um Sheet — botões
// opacity-0, mesmo invisíveis, capturavam o toque na faixa direita da row e o
// play não disparava (sensação de "tocar duas vezes"). Lista via map pra manter
// a complexidade baixa e não repetir desktop/mobile.
const ACTION_COLOR: Record<string, string> = {
  cyan: 'text-cyan-400 hover:bg-cyan-500/10',
  purple: 'text-purple-400 hover:bg-purple-500/10',
  amber: 'text-amber-400 hover:bg-amber-500/10',
  red: 'text-red-400 hover:bg-red-500/10',
}
type EntryAction = { key: string; icon: typeof Trash2; label: string; color: keyof typeof ACTION_COLOR; run: () => void }

function EntryActions({ entry: e, isAdmin, canAct, onPromote, onReclassify, onMove, onDelete }: {
  readonly entry: LocalEntry
  readonly isAdmin: boolean
  readonly canAct: boolean
  readonly onPromote: (e: LocalEntry) => void
  readonly onReclassify: (e: LocalEntry) => void
  readonly onMove: (e: LocalEntry) => void
  readonly onDelete: (e: LocalEntry) => void
}) {
  const [menuOpen, setMenuOpen] = useState(false)
  const actions: EntryAction[] = [
    canAct && !e.isDir && { key: 'promote', icon: ArrowUpCircle, label: 'Promover / Organizar via IA', color: 'cyan', run: () => onPromote(e) },
    isAdmin && { key: 'reclassify', icon: FolderSync, label: e.isDir ? 'Reclassificar pasta via IA (Plex)' : 'Classificar e mover via IA', color: 'purple', run: () => onReclassify(e) },
    isAdmin && { key: 'move', icon: FolderInput, label: 'Mover para outro mount', color: 'amber', run: () => onMove(e) },
    canAct && { key: 'delete', icon: Trash2, label: e.isDir ? 'Apagar pasta permanentemente' : 'Apagar permanentemente', color: 'red', run: () => onDelete(e) },
  ].filter(Boolean) as EntryAction[]
  if (actions.length === 0) return null

  return (
    <>
      <div className="hidden sm:flex items-center gap-1.5 px-4 opacity-0 group-hover:opacity-100 focus-within:opacity-100 transition-opacity">
        {actions.map(a => {
          const Icon = a.icon
          return (
            <button
              key={a.key}
              onClick={(evt) => { evt.stopPropagation(); a.run() }}
              title={a.label}
              className={`p-1.5 rounded-lg border border-transparent transition-all ${ACTION_COLOR[a.color]}`}
            >
              <Icon className="w-5 h-5" />
            </button>
          )
        })}
      </div>
      <button
        onClick={(evt) => { evt.stopPropagation(); setMenuOpen(true) }}
        title="Ações"
        aria-label="Ações"
        className="sm:hidden flex-shrink-0 flex items-center justify-center min-w-[44px] min-h-[44px] text-text-secondary hover:text-text-primary"
      >
        <MoreVertical className="w-5 h-5" />
      </button>
      {menuOpen && (
        <Sheet open onClose={() => setMenuOpen(false)} size="sm" title={e.name}>
          <div className="flex flex-col gap-1 pb-2">
            {actions.map(a => {
              const Icon = a.icon
              return (
                <button
                  key={a.key}
                  onClick={() => { setMenuOpen(false); a.run() }}
                  className={`flex items-center gap-3 px-3 min-h-[48px] rounded-lg hover:bg-surface-tertiary/40 text-left ${ACTION_COLOR[a.color].split(' ')[0]}`}
                >
                  <Icon className="w-5 h-5 flex-shrink-0" />
                  <span className="text-sm">{a.label}</span>
                </button>
              )
            })}
          </div>
        </Sheet>
      )}
    </>
  )
}

// Uma linha da lista. Extraída pra poder usar useLongPress por item (hooks não
// podem ser chamados dentro de um .map). Long-press entra no modo seleção.
function EntryRow(props: EntryRowProps) {
  const { entry: e, mount, selectMode, selected, canManipulate, isAdmin } = props
  const clickable = e.isDir || e.isPlayable
  const canAct = canManipulate || isAdmin
  const lp = useLongPress(() => props.onEnterSelect(e), { enabled: !selectMode && canAct })
  const pressHandlers = selectMode || !canAct ? {} : lp

  return (
    <li className={`flex items-center justify-between group ${selected ? 'bg-green-500/10' : 'hover:bg-surface-tertiary/20'}`}>
      <button
        onClick={() => (selectMode ? props.onToggleSelect(e) : props.onOpen(e))}
        disabled={!selectMode && !clickable}
        {...pressHandlers}
        className={`flex-1 min-w-0 flex items-center gap-3 px-4 py-2.5 text-left transition-colors ${
          selectMode || clickable ? 'cursor-pointer' : 'cursor-default opacity-70'
        }`}
      >
        {selectMode && (
          <span className={`flex-shrink-0 w-5 h-5 rounded border flex items-center justify-center transition-colors ${
            selected ? 'bg-green-500 border-green-500' : 'border-strong'
          }`}>
            {selected && <Check className="w-3.5 h-3.5 text-white" />}
          </span>
        )}
        <EntryIcon entry={e} mount={mount} />
        <span className="flex-1 min-w-0 flex flex-col gap-0.5">
          <span className="text-text-primary font-medium line-clamp-2 [overflow-wrap:anywhere]">{e.name}</span>
          {/* Metadados compactos só no mobile — no desktop ficam nas colunas à
              direita (hidden sm:block). Sem isso a row no celular mostrava só
              ícone + nome. */}
          <span className="sm:hidden text-[11px] text-text-muted flex items-center gap-1.5">
            {!e.isDir && <>{formatSize(e.size)}<span className="text-text-muted">·</span></>}
            {formatDate(e.modTime)}
          </span>
        </span>
        {!e.isDir && (
          <span className="text-xs text-text-muted text-right flex-shrink-0 hidden sm:block w-20">{formatSize(e.size)}</span>
        )}
        <span className="text-xs text-text-muted w-24 text-right hidden sm:block flex-shrink-0">{formatDate(e.modTime)}</span>
      </button>

      {/* Ações por-item: desktop = botões no hover; mobile = ⋮ → Sheet. */}
      {!selectMode && (
        <EntryActions
          entry={e}
          isAdmin={isAdmin}
          canAct={canAct}
          onPromote={props.onPromote}
          onReclassify={props.onReclassify}
          onMove={props.onMove}
          onDelete={props.onDelete}
        />
      )}
    </li>
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
  const [notice, setNotice] = useState('')
  const { playSingle } = usePlayer()
  const [kind, setKind] = usePersistedState<KindFilter>('local.kind', 'all')
  const [sortKey, setSortKey] = usePersistedState<SortKey>('local.sortKey', 'name')
  const [sortDir, setSortDir] = usePersistedState<'asc' | 'desc'>('local.sortDir', 'asc')

  // Files queued for the promote modal: 1 (single, via the row action) or many
  // (batch selection). The modal applies one destination + AI choice to all in
  // a single call — no more one-modal-per-file walk.
  const [promoteEntries, setPromoteEntries] = useState<LocalEntry[]>([])
  const [reclassifyItem, setReclassifyItem] = useState<LocalEntry | null>(null)
  const [moveItem, setMoveItem] = useState<LocalEntry | null>(null)
  const confirm = useConfirm()

  // Busca textual por nome (filtra a lista visível) + seleção múltipla / lote.
  const [search, setSearch] = useState('')
  const [mountSheetOpen, setMountSheetOpen] = useState(false)
  const [selectMode, setSelectMode] = useState(false)
  const [selected, setSelected] = useState<Set<string>>(new Set())
  const [batchRunning, setBatchRunning] = useState(false)
  const [batchMoveOpen, setBatchMoveOpen] = useState(false)

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
    const q = search.trim().toLowerCase()
    const matchesSearch = (e: LocalEntry) => !q || e.name.toLowerCase().includes(q)
    const dirs = entries.filter((e) => e.isDir && matchesSearch(e))
    let files = entries.filter((e) => !e.isDir && matchesSearch(e))
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
  }, [entries, kind, sortKey, sortDir, search])

  const toggleSort = (key: SortKey) => {
    if (sortKey === key) setSortDir((d) => (d === 'asc' ? 'desc' : 'asc'))
    else { setSortKey(key); setSortDir(key === 'name' ? 'asc' : 'desc') }
  }

  // Sair do modo seleção ao trocar de mount/pasta — evita lote acidental
  // cross-folder (as APIs são scoped a mount+path relativo).
  useEffect(() => {
    setSelectMode(false)
    setSelected(new Set())
  }, [activeMount, path])

  const selectedEntries = useMemo(
    () => entries.filter((e) => selected.has(e.path)),
    [entries, selected],
  )

  const clearSelection = () => { setSelectMode(false); setSelected(new Set()) }
  const toggleSelect = (e: LocalEntry) => setSelected((prev) => {
    const next = new Set(prev)
    if (next.has(e.path)) next.delete(e.path)
    else next.add(e.path)
    return next
  })
  const enterSelect = (e: LocalEntry) => { setSelectMode(true); setSelected(new Set([e.path])) }
  // "Selecionar tudo" age sobre a lista visível (respeita filtro/busca atuais).
  const selectAllVisible = () => setSelected(new Set(visible.map((e) => e.path)))

  const runBatchDelete = async () => {
    if (selectedEntries.length === 0) return
    const ok = await confirm({
      title: 'Apagar permanentemente?',
      message: `Tem certeza que deseja apagar ${selectedEntries.length} ${selectedEntries.length === 1 ? 'item' : 'itens'}? Esta ação é irreversível. O torrent vinculado (se houver) também é removido: download, pieces no cache e favorito.`,
      confirmLabel: 'Apagar',
      destructive: true,
    })
    if (!ok) return
    setBatchRunning(true)
    setError('')
    const results = await Promise.allSettled(selectedEntries.map((e) => localDelete(activeMount, e.path)))
    const failed = results.filter((r) => r.status === 'rejected').length
    if (failed > 0) setError(`${failed} de ${selectedEntries.length} itens não puderam ser apagados.`)
    setBatchRunning(false)
    clearSelection()
    refresh()
  }

  // Promover em lote = um único modal para TODOS os arquivos selecionados
  // (destino + renomeação IA escolhidos uma vez, uma chamada só).
  const runBatchPromote = () => {
    const files = selectedEntries.filter((e) => !e.isDir)
    if (files.length === 0) return
    setPromoteEntries(files)
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
    setNotice('') // stale "N folders removed" shouldn't linger across navigation
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

  const requestDelete = async (item: LocalEntry) => {
    if (!activeMount) return
    const ok = await confirm({
      title: 'Apagar permanentemente?',
      message: (
        <>
          Tem certeza que deseja apagar <span className="text-red-400 font-medium">"{item.name}"</span>? Esta ação é
          irreversível e excluirá o {item.isDir ? 'diretório' : 'arquivo'} de forma permanente no servidor.
          <span className="block mt-2 text-xs text-amber-400/80">
            O torrent vinculado (se houver) também será removido: registro do download, pieces no cache e marcação de favorito.
          </span>
        </>
      ),
      confirmLabel: 'Apagar',
      destructive: true,
    })
    if (!ok) return
    setError('')
    try {
      await localDelete(activeMount, item.path)
      refresh()
    } catch (e: any) {
      setError(e?.response?.data?.error || e.message || 'Erro ao apagar arquivo')
    }
  }

  // Remove empty subfolders left behind after promoting/moving files. Low risk
  // (only deletes truly-empty dirs), so a light confirm is enough.
  const requestCleanEmptyDirs = async () => {
    if (!activeMount) return
    const ok = await confirm({
      title: 'Limpar pastas vazias?',
      message: <>Remover todas as subpastas vazias a partir de <span className="text-text-primary font-medium">"{path || activeMount}"</span>? Arquivos não são afetados.</>,
      confirmLabel: 'Limpar',
    })
    if (!ok) return
    setError('')
    setNotice('')
    try {
      const { cleaned } = await localCleanEmptyDirs(activeMount, path)
      setNotice(cleaned > 0 ? `${cleaned} pasta${cleaned === 1 ? '' : 's'} vazia${cleaned === 1 ? '' : 's'} removida${cleaned === 1 ? '' : 's'}.` : 'Nenhuma pasta vazia encontrada.')
      refresh()
    } catch (e: any) {
      setError(e?.response?.data?.error || e.message || 'Erro ao limpar pastas vazias')
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
    <div className="h-screen bg-surface flex flex-col overflow-hidden">
      <NavHeader />
      <main className="flex-1 min-h-0 max-w-7xl 2xl:max-w-[min(95vw,1600px)] mx-auto w-full px-4 py-6 flex flex-col md:flex-row gap-4 md:gap-6">
        {/* Sidebar — desktop é coluna fixa à esquerda. No mobile some por completo
            (hidden) e dá lugar a um dropdown de mount na barra do breadcrumb, que
            não rouba altura nem força scroll horizontal de chips. */}
        <aside className="hidden md:block md:w-56 flex-shrink-0 md:overflow-y-auto">
          <h2 className="text-xs uppercase tracking-wider text-text-muted mb-2 md:mb-3">
            Mounts
          </h2>
          {mounts.length === 0 ? (
            <><p className="text-sm text-text-muted">
              Nenhum mount configurado. Adicione em <code>config.yaml</code>:
            </p>
            <code className="block mt-2 p-2 bg-surface-secondary rounded text-xs">
                external:{'\n'}  mounts:{'\n'}    - name: HD Externo{'\n'}      path: /mnt/external
              </code></>
          ) : (
            <ul className="flex flex-col gap-1 space-y-1">
              {mounts.map((m) => {
                const active = m.name === activeMount
                return (
                  <li key={m.name} className="flex-shrink-0">
                    <button
                      onClick={() => handleSelectMount(m.name)}
                      className={`w-full flex items-center gap-2 px-3 py-2 rounded-lg text-sm transition-colors whitespace-nowrap ${
                        active
                          ? 'bg-green-500/10 text-green-400 border border-green-500/30'
                          : 'text-text-primary hover:bg-surface-secondary border border-transparent'
                      }`}
                    >
                      <HardDrive className="w-4 h-4 flex-shrink-0" />
                      <span className="truncate">{m.name}</span>
                      <MountBadge m={m} />
                    </button>
                    <MountSpaceLabel m={m} />
                  </li>
                )
              })}
            </ul>
          )}

          {canViewAsUser && (
            <div className="mt-5 md:mt-6">
              <h2 className="text-xs uppercase tracking-wider text-text-muted mb-2">Ver como</h2>
              <select
                value={viewAsUser}
                onChange={(e) => handleViewAsUser(e.target.value)}
                className="w-full px-3 py-2 rounded-lg text-sm bg-surface-secondary border border-default text-text-primary focus:border-green-500/50 focus:outline-none"
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
              <div className="flex items-center gap-2 min-w-0">
                {/* Dropdown de mount — só no mobile (a sidebar some em <md) */}
                <button
                  onClick={() => setMountSheetOpen(true)}
                  className="md:hidden flex-shrink-0 flex items-center gap-1.5 px-2.5 min-h-[40px] rounded-lg bg-surface-secondary border border-default text-sm text-text-primary max-w-[45vw]"
                >
                  <HardDrive className="w-4 h-4 text-green-400 flex-shrink-0" />
                  <span className="truncate">{activeMount}</span>
                  <ChevronDown className="w-4 h-4 text-text-muted flex-shrink-0" />
                </button>
                <Breadcrumbs mountName={activeMount} path={path} onNavigate={(p) => updateNavigation(activeMount, p)} />
              </div>
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
                  <button
                    onClick={requestCleanEmptyDirs}
                    title="Remover subpastas vazias desta pasta"
                    className="flex-shrink-0 inline-flex items-center gap-1.5 text-sm bg-surface-tertiary/60 hover:bg-surface-tertiary text-text-primary border border-strong px-3 py-1.5 rounded-lg transition-colors font-medium"
                  >
                    <FolderX className="w-4 h-4" />
                    <span className="hidden sm:inline">Limpar vazias</span>
                  </button>
                </>
              )}
              {isAdmin && (
                <button
                  onClick={() => setReclassifyItem({ name: path ? path.split('/').pop() || path : activeMount, path, isDir: true, size: 0, modTime: '', isPlayable: false })}
                  title="Reclassificar e organizar esta pasta via IA (estilo Plex), mantendo o vínculo com o torrent"
                  className="flex-shrink-0 inline-flex items-center gap-1.5 text-sm bg-purple-500/15 hover:bg-purple-500/25 text-purple-400 border border-purple-500/30 px-3 py-1.5 rounded-lg transition-colors font-medium"
                >
                  <FolderSync className="w-4 h-4" />
                  <span className="hidden sm:inline">Reclassificar pasta</span>
                </button>
              )}
            </div>
          )}

          {/* Banner de progresso do upload (streaming direto pro disco no backend) */}
          {upload && (
            <div className="flex-shrink-0 bg-surface-secondary border border-green-500/30 rounded-xl p-3 flex flex-col gap-2">
              <div className="flex items-center gap-2 text-sm text-text-primary">
                <Upload className="w-4 h-4 text-green-400 flex-shrink-0 animate-pulse" />
                <span className="truncate flex-1">{upload.name}</span>
                <span className="text-xs text-text-secondary tabular-nums">
                  {formatSize(upload.loaded)} / {formatSize(upload.total)}
                  {upload.total > 0 && ` (${Math.round((upload.loaded / upload.total) * 100)}%)`}
                </span>
                <button
                  onClick={() => uploadAbortRef.current?.abort()}
                  title="Cancelar upload"
                  className="p-1 rounded text-text-secondary hover:text-red-400 transition-colors"
                >
                  <X className="w-4 h-4" />
                </button>
              </div>
              <div className="h-1.5 bg-surface-tertiary rounded-full overflow-hidden">
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

          {/* Toolbar: busca + selecionar; chips de tipo + ordenação (flex-shrink-0
              pra ficar fixa enquanto a lista abaixo rola). */}
          {activeMount && entries.length > 0 && (
            <div className="flex-shrink-0 flex flex-col gap-2">
              <div className="flex items-center gap-2">
                <div className="relative flex-1 min-w-0">
                  <Search className="absolute left-3 top-1/2 -translate-y-1/2 w-4 h-4 text-text-muted pointer-events-none" />
                  <input
                    type="text"
                    value={search}
                    onChange={(e) => setSearch(e.target.value)}
                    placeholder="Buscar arquivo..."
                    className="w-full bg-surface-secondary border border-default rounded-lg pl-9 pr-8 py-2 text-base sm:text-sm text-text-primary placeholder-gray-500 focus:outline-none focus:border-green-500/50"
                  />
                  {search && (
                    <button
                      onClick={() => setSearch('')}
                      aria-label="Limpar busca"
                      className="absolute right-2 top-1/2 -translate-y-1/2 text-text-muted hover:text-text-primary p-1"
                    >
                      <X className="w-3.5 h-3.5" />
                    </button>
                  )}
                </div>
                {(canManipulate || isAdmin) && !selectMode && (
                  <button
                    onClick={() => setSelectMode(true)}
                    className="flex-shrink-0 text-sm px-3 min-h-[44px] sm:min-h-0 sm:py-1.5 rounded-lg border border-default text-text-primary hover:bg-surface-tertiary transition-colors"
                  >
                    Selecionar
                  </button>
                )}
              </div>
              {/* Dois grupos rotulados (Tipo / Ordenar). No mobile empilham
                  (flex-col) com rótulo visível em cada um — antes os chips dos
                  dois grupos se misturavam numa mesma linha-que-quebra, sem
                  rótulo, e ficava confuso. No desktop voltam pra uma linha. */}
              <div className="flex flex-col sm:flex-row sm:flex-wrap sm:items-center gap-2 text-xs">
                <div className="flex flex-wrap items-center gap-2">
                  <span className="text-text-muted sm:hidden mr-0.5">Tipo:</span>
                  {(['all', 'video', 'audio', 'other'] as KindFilter[]).map((k) => (
                    <button
                      key={k}
                      onClick={() => setKind(k)}
                      className={`px-2.5 py-1 rounded-full border transition-colors ${
                        kind === k
                          ? 'bg-green-500/15 text-green-400 border-green-500/40'
                          : 'text-text-secondary border-default hover:border-strong'
                      }`}
                    >
                      {{ all: 'Todos', video: 'Vídeo', audio: 'Áudio', other: 'Outros' }[k]}
                    </button>
                  ))}
                </div>
                <span className="mx-1 h-4 w-px bg-surface-tertiary hidden sm:block" />
                <div className="flex flex-wrap items-center gap-2">
                  <span className="text-text-muted mr-0.5">Ordenar:</span>
                  {(['name', 'size', 'date'] as SortKey[]).map((k) => (
                    <button
                      key={k}
                      onClick={() => toggleSort(k)}
                      className={`flex items-center gap-1 px-2.5 py-1 rounded-full border transition-colors ${
                        sortKey === k
                          ? 'bg-surface-tertiary text-text-primary border-strong'
                          : 'text-text-secondary border-default hover:border-strong'
                      }`}
                    >
                      {{ name: 'Nome', size: 'Tamanho', date: 'Data' }[k]}
                      {sortKey === k && (sortDir === 'asc' ? <ArrowUp className="w-3 h-3" /> : <ArrowDown className="w-3 h-3" />)}
                    </button>
                  ))}
                </div>
              </div>
            </div>
          )}

          {error && (
            <div className="bg-red-500/10 border border-red-500/30 text-red-400 rounded-xl p-4 text-sm">
              {error}
            </div>
          )}

          {notice && (
            <div className="bg-emerald-500/10 border border-emerald-500/30 text-emerald-300 rounded-xl px-4 py-2.5 text-sm flex items-center justify-between gap-3">
              <span>{notice}</span>
              <button onClick={() => setNotice('')} className="text-emerald-400/70 hover:text-emerald-300 text-xs">Fechar</button>
            </div>
          )}

          {loading && (
            <div className="text-text-muted text-sm">Carregando...</div>
          )}

          {!loading && !error && activeMount && visible.length === 0 && (
            <div className="text-text-muted text-sm">
              {entries.length === 0 ? 'Pasta vazia' : 'Nenhum arquivo com esse filtro'}
            </div>
          )}

          {!loading && visible.length > 0 && (
            <ul className={`flex-1 min-h-0 overflow-y-auto divide-y divide-default bg-surface-secondary/50 rounded-xl border border-default ${selectMode ? 'pb-20' : ''}`}>
              {visible.map((e) => (
                <EntryRow
                  key={e.path}
                  entry={e}
                  mount={activeMount}
                  selectMode={selectMode}
                  selected={selected.has(e.path)}
                  canManipulate={canManipulate}
                  isAdmin={isAdmin}
                  onOpen={handleEntryClick}
                  onEnterSelect={enterSelect}
                  onToggleSelect={toggleSelect}
                  onPromote={(entry) => setPromoteEntries([entry])}
                  onReclassify={setReclassifyItem}
                  onMove={setMoveItem}
                  onDelete={requestDelete}
                />
              ))}
            </ul>
          )}

          {/* Modal de Promoção — individual (1) ou lote (N) num único fluxo */}
          <LocalPromoteModal
            mount={activeMount}
            entries={promoteEntries}
            onClose={() => setPromoteEntries([])}
            onPromoted={() => { refresh(); clearSelection() }}
          />

          {/* Modal de Reclassificação em lote via IA */}
          <ReclassifyFolderModal
            mount={activeMount}
            entry={reclassifyItem}
            onClose={() => setReclassifyItem(null)}
            onDone={() => { setReclassifyItem(null); refresh() }}
          />

          {/* Modal de Mover entre mounts — individual (moveItem) ou lote (selectedEntries) */}
          <MoveFolderModal
            mount={activeMount}
            entry={moveItem}
            entries={batchMoveOpen ? selectedEntries : undefined}
            onClose={() => {
              setMoveItem(null)
              if (batchMoveOpen) { setBatchMoveOpen(false); clearSelection() }
            }}
            onMoved={refresh}
          />
        </section>
      </main>

      {/* Barra de ações em lote (modo seleção) */}
      {selectMode && (
        <BatchActionBar
          count={selected.size}
          onCancel={clearSelection}
          allSelected={visible.length > 0 && selected.size === visible.length}
          onSelectAll={selected.size === visible.length ? () => setSelected(new Set()) : selectAllVisible}
          canMove={isAdmin}
          canPromote={canManipulate || isAdmin}
          onDelete={runBatchDelete}
          onMove={() => setBatchMoveOpen(true)}
          onPromote={runBatchPromote}
          running={batchRunning}
        />
      )}

      {/* Dropdown de mounts (mobile) */}
      <Sheet
        open={mountSheetOpen}
        onClose={() => setMountSheetOpen(false)}
        title="Mounts"
        icon={<HardDrive className="w-4 h-4 text-green-400 flex-shrink-0" />}
        size="sm"
      >
        <ul className="space-y-1">
          {mounts.map((m) => (
            <li key={m.name}>
              <button
                onClick={() => { handleSelectMount(m.name); setMountSheetOpen(false) }}
                className={`w-full flex items-center gap-2 px-3 min-h-[44px] rounded-lg text-sm transition-colors ${
                  m.name === activeMount
                    ? 'bg-green-500/10 text-green-400 border border-green-500/30'
                    : 'text-text-primary hover:bg-surface-tertiary border border-transparent'
                }`}
              >
                <HardDrive className="w-4 h-4 flex-shrink-0" />
                <span className="truncate">{m.name}</span>
                <MountBadge m={m} />
              </button>
              <MountSpaceLabel m={m} />
            </li>
          ))}
        </ul>
        {canViewAsUser && (
          <div className="mt-4 pt-4 border-t border-default">
            <h3 className="text-xs uppercase tracking-wider text-text-muted mb-2">Ver como</h3>
            <select
              value={viewAsUser}
              onChange={(e) => { handleViewAsUser(e.target.value); setMountSheetOpen(false) }}
              className="w-full px-3 py-2 rounded-lg text-base bg-surface-secondary border border-default text-text-primary focus:border-green-500/50 focus:outline-none"
            >
              <option value="">Meu espaço (admin)</option>
              {adminUsers.map((u) => (
                <option key={u.id} value={u.username}>{u.username}</option>
              ))}
            </select>
          </div>
        )}
      </Sheet>
    </div>
  )
}
