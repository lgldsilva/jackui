import { memo, useState } from 'react'
import { useTranslation } from 'react-i18next'
import {
  Trash2,
  ArrowUpCircle,
  FolderSync,
  FolderInput,
  Check,
  Lock,
  Unlock,
  MoreVertical,
  Eye,
  EyeOff,
  Pencil,
  HardDriveDownload,
} from 'lucide-react'
import { LocalEntry, buildLocalHash } from '../../api/client'
import { formatBytes, formatDateTime } from '../../lib/format'
import { useLongPress } from '../../lib/useLongPress'
import { Sheet } from '../Sheet'
import { newTabProps, openInNewTab, playHref } from '../../lib/cardNav'
import { isViewable } from '../viewer/viewerKind'
import { EntryIcon } from './EntryIcon'
import { formatCount } from './entryFormat'

export type EntryRowProps = {
  readonly entry: LocalEntry
  readonly mount: string
  readonly selectMode: boolean
  readonly selected: boolean
  readonly canManipulate: boolean
  readonly isAdmin: boolean
  readonly onOpen: (e: LocalEntry) => void
  readonly onEnterSelect: (e: LocalEntry) => void
  readonly onToggleSelect: (e: LocalEntry) => void
  readonly onRename: (e: LocalEntry) => void
  readonly onPromote: (e: LocalEntry) => void
  readonly onReclassify: (e: LocalEntry) => void
  readonly onMove: (e: LocalEntry) => void
  readonly onLock: (e: LocalEntry) => void
  readonly onDelete: (e: LocalEntry) => void
  readonly hidden: boolean
  readonly onToggleHidden: (e: LocalEntry) => void
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

function EntryActions({ entry: e, isAdmin, canAct, hidden, onRename, onPromote, onReclassify, onMove, onLock, onDelete, onToggleHidden }: {
  readonly entry: LocalEntry
  readonly isAdmin: boolean
  readonly canAct: boolean
  readonly hidden: boolean
  readonly onRename: (e: LocalEntry) => void
  readonly onPromote: (e: LocalEntry) => void
  readonly onReclassify: (e: LocalEntry) => void
  readonly onMove: (e: LocalEntry) => void
  readonly onLock: (e: LocalEntry) => void
  readonly onDelete: (e: LocalEntry) => void
  readonly onToggleHidden: (e: LocalEntry) => void
}) {
  const { t } = useTranslation()
  const [menuOpen, setMenuOpen] = useState(false)
  const actions: EntryAction[] = [
    canAct && { key: 'rename', icon: Pencil, label: e.isDir ? t('local.actions.renameFolder') : t('local.actions.renameFile'), color: 'amber', run: () => onRename(e) },
    canAct && !e.isDir && { key: 'promote', icon: ArrowUpCircle, label: t('local.actions.promote'), color: 'cyan', run: () => onPromote(e) },
    isAdmin && { key: 'reclassify', icon: FolderSync, label: e.isDir ? t('local.actions.reclassifyFolder') : t('local.actions.classifyMove'), color: 'purple', run: () => onReclassify(e) },
    isAdmin && { key: 'move', icon: FolderInput, label: t('local.actions.moveMount'), color: 'amber', run: () => onMove(e) },
    // Lock/unlock só faz sentido em pasta: fixa-a (.keep) contra o "limpar vazias".
    canAct && e.isDir && { key: 'lock', icon: e.locked ? Unlock : Lock, label: e.locked ? t('local.actions.unkeep') : t('local.actions.keep'), color: 'amber', run: () => onLock(e) },
    // Hide/unhide is per-user and harmless on any mount, so it's always offered.
    { key: 'hide', icon: hidden ? Eye : EyeOff, label: hidden ? t('local.actions.unhide') : t('local.actions.hide'), color: 'amber', run: () => onToggleHidden(e) },
    canAct && { key: 'delete', icon: Trash2, label: e.isDir ? t('local.actions.deleteFolder') : t('local.actions.deleteFile'), color: 'red', run: () => onDelete(e) },
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
        title={t('local.actions.menu')}
        aria-label={t('local.actions.menu')}
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

// Deep-link "tela toda" de uma row: pasta → o browser daquela pasta; arquivo
// reproduzível → o player via ?play=local-hash. Viewables não têm rota → ''.
function localEntryHref(e: LocalEntry, mount: string): string {
  if (e.isDir) return `/local?mount=${encodeURIComponent(mount)}&path=${encodeURIComponent(e.path)}`
  if (e.isPlayable) return playHref(buildLocalHash(mount, e.path))
  return ''
}

// Handlers de clique/contexto da row. Com href: middle/ctrl/cmd-click e o
// right-click puro abrem nova aba (clique normal roda onActivate); o
// onContextMenu ignora ctrl/cmd pra não abrir DUAS abas no macOS (lá o Ctrl+Click
// dispara contextmenu E click, e o ctrl já cai no newTabProps.onClick). Sem href
// (viewable/seleção): clique normal só.
function localRowNavProps(href: string, onActivate: () => void) {
  if (!href) return { onClick: onActivate }
  return {
    ...newTabProps(href, onActivate),
    onContextMenu: (ev: React.MouseEvent) => {
      if (ev.ctrlKey || ev.metaKey) return
      ev.preventDefault()
      openInNewTab(href)
    },
  }
}

// Uma linha da lista. Extraída pra poder usar useLongPress por item (hooks não
// podem ser chamados dentro de um .map). Long-press entra no modo seleção.
// React.memo evita re-render de todas as linhas quando só muda estado não-relacionado
// da página (upload em andamento, notice, seleção de OUTRA row) — os handlers do pai
// são estáveis (useCallback), então a comparação shallow padrão basta.
function EntryRowInner(props: EntryRowProps) {
  const { t } = useTranslation()
  const { entry: e, mount, selectMode, selected, canManipulate, isAdmin } = props
  // Viewable = não-reproduzível mas com viewer universal (NFO/imagem/PDF/
  // quadrinhos/zip/EPUB). A linha deixa de ser "morta": clique abre o preview.
  const viewable = !e.isDir && !e.isPlayable && isViewable(e.name)
  const clickable = e.isDir || e.isPlayable || viewable
  const canAct = canManipulate || isAdmin
  // contextMenu:false: right-click here opens a new tab (handled below), so the
  // hook must NOT map onContextMenu to "enter select mode" — otherwise the
  // {...pressHandlers} spread would shadow the new-tab handler. Touch long-press
  // (onTouchStart) still enters select; desktop has the toolbar "Selecionar".
  const lp = useLongPress(() => props.onEnterSelect(e), { enabled: !selectMode && canAct, contextMenu: false })
  const pressHandlers = selectMode || !canAct ? {} : lp

  // Modo seleção não navega; senão deriva o deep-link + handlers (ver helpers).
  const newTabHref = selectMode ? '' : localEntryHref(e, mount)
  const onActivate = () => (selectMode ? props.onToggleSelect(e) : props.onOpen(e))
  const navProps = localRowNavProps(newTabHref, onActivate)

  return (
    <li className={`flex items-center justify-between group ${selected ? 'bg-green-500/10' : 'hover:bg-surface-tertiary/20'}`}>
      <button
        {...navProps}
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
          <span className="text-text-primary font-medium line-clamp-2 [overflow-wrap:anywhere] flex items-center gap-1.5">
            {props.hidden && <EyeOff className="w-3.5 h-3.5 text-amber-400 flex-shrink-0" aria-label={t('local.row.hiddenAria')} />}
            {e.locked && <Lock className="w-3.5 h-3.5 text-amber-400 flex-shrink-0" aria-label={t('local.row.keptAria')} />}
            {viewable && <Eye className="w-3.5 h-3.5 text-blue-400 flex-shrink-0" aria-label={t('local.row.viewableAria')} />}
            {e.incomplete && (
              <span
                className="flex-shrink-0 inline-flex items-center gap-1 px-1.5 py-0.5 rounded text-[10px] font-medium bg-amber-500/15 text-amber-700 dark:text-amber-300 border border-amber-500/30"
                title={t('local.row.downloadingTitle')}
              >
                <HardDriveDownload className="w-3 h-3" />{t('local.row.downloading')}
              </span>
            )}
            {e.name}
          </span>
          {/* Metadados compactos só no mobile — no desktop ficam nas colunas à
              direita (hidden sm:block). Sem isso a row no celular mostrava só
              ícone + nome. */}
          <span className="sm:hidden text-[11px] text-text-muted flex items-center gap-1.5">
            {e.isDir
              ? <>{formatCount(e.childCount ?? 0, t)}<span className="text-text-muted">·</span></>
              : <>{formatBytes(e.size)}<span className="text-text-muted">·</span></>}
            {formatDateTime(e.modTime)}
          </span>
        </span>
        {/* Tamanho (arquivo) ou quantidade de itens (pasta). */}
        <span className="text-xs text-text-muted text-right flex-shrink-0 hidden sm:block w-24">
          {e.isDir ? formatCount(e.childCount ?? 0, t) : formatBytes(e.size)}
        </span>
        <span className="text-xs text-text-muted w-32 text-right hidden sm:block flex-shrink-0">{formatDateTime(e.modTime)}</span>
      </button>

      {/* Ações por-item: desktop = botões no hover; mobile = ⋮ → Sheet. */}
      {!selectMode && (
        <EntryActions
          entry={e}
          isAdmin={isAdmin}
          canAct={canAct}
          hidden={props.hidden}
          onRename={props.onRename}
          onPromote={props.onPromote}
          onReclassify={props.onReclassify}
          onMove={props.onMove}
          onLock={props.onLock}
          onDelete={props.onDelete}
          onToggleHidden={props.onToggleHidden}
        />
      )}
    </li>
  )
}

export const EntryRow = memo(EntryRowInner)
