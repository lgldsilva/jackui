import { useTranslation } from 'react-i18next'
import { Heart, FolderPlus, Inbox, Eye } from 'lucide-react'
import { StreamFavorite } from '../../api/client'
import { type FolderNode } from '../../lib/favoritesTree'
import { FolderTree } from './FolderTree'
import { rootFolderClass } from './favoritesHelpers'

type FolderSidebarProps = {
  readonly revealHidden: boolean
  readonly viewMode: number | null
  readonly ALL_VIEW: number
  readonly favs: StreamFavorite[]
  readonly tree: FolderNode[]
  readonly dropOnRoot: boolean
  readonly creatingRoot: boolean
  readonly newFolderInput: React.RefObject<HTMLInputElement>
  readonly selectedFolderId: number | null
  readonly expanded: Set<number>
  readonly editingId: number | null
  readonly setCreatingRoot: (v: boolean) => void
  readonly setViewMode: (v: number | null) => void
  readonly setSelectedFolderId: (v: number | null) => void
  readonly setDropOnRoot: (v: boolean) => void
  readonly setExpanded: React.Dispatch<React.SetStateAction<Set<number>>>
  readonly setEditingId: (v: number | null) => void
  readonly onCreateRoot: () => void
  readonly onRename: (id: number, name: string) => void
  readonly onDeleteFolder: (id: number) => void
  readonly onCreateSub: (parentId: number) => void
  readonly onToggleHidden: (id: number, hidden: boolean) => void
  readonly onDropOnFolder: (folderId: number | null, favoriteName: string) => void
}

export default function FolderSidebar(p: FolderSidebarProps) {
  const { t } = useTranslation()
  const { revealHidden, viewMode, ALL_VIEW, favs, tree } = p
  return (
    <aside className="w-64 flex-shrink-0 hidden md:block">
      <div className="flex items-center justify-between mb-2">
        <h2 className="text-xs uppercase tracking-wider text-text-muted cursor-default select-none" title={revealHidden ? t('favorites.hiddenFoldersVisible') : undefined}>
          {t('favorites.folders')}{revealHidden && <Eye className="inline w-3 h-3 ml-1 text-amber-400" aria-label={t('favorites.hiddenVisibleAria')} />}
        </h2>
        <button
          onClick={() => p.setCreatingRoot(true)}
          title={t('favorites.newFolder')}
          className="p-1 text-text-muted hover:text-pink-400"
        >
          <FolderPlus className="w-4 h-4" />
        </button>
      </div>

      {/* Special views */}
      <ul className="flex flex-col gap-0.5 mb-2">
        <li>
          <button
            onClick={() => { p.setViewMode(ALL_VIEW); p.setSelectedFolderId(null) }}
            onKeyDown={e => { if (e.key === 'Enter' || e.key === ' ') { e.preventDefault(); p.setViewMode(ALL_VIEW); p.setSelectedFolderId(null) } }}
            className={`w-full flex items-center gap-2 px-2 py-1 rounded-md text-sm transition-colors ${
              viewMode === ALL_VIEW ? 'bg-pink-500/15 text-pink-700 dark:text-pink-200 border border-pink-500/30' : 'text-text-primary hover:bg-surface-secondary border border-transparent'
            }`}
          >
            <Heart className="w-3.5 h-3.5 fill-current" />
            {t('favorites.all')}
            <span className="ml-auto text-[10px] text-text-muted">{favs.length}</span>
          </button>
        </li>
        <li>
          <button
            onDragOver={e => { e.preventDefault(); p.setDropOnRoot(true) }}
            onDragLeave={() => p.setDropOnRoot(false)}
            onDrop={e => {
              e.preventDefault()
              const name = e.dataTransfer.getData('text/x-favorite-name')
              if (name) p.onDropOnFolder(null, name)
            }}
            onClick={() => { p.setViewMode(null); p.setSelectedFolderId(null) }}
            onKeyDown={e => { if (e.key === 'Enter' || e.key === ' ') { e.preventDefault(); p.setViewMode(null); p.setSelectedFolderId(null) } }}
            className={`w-full flex items-center gap-2 px-2 py-1 rounded-md text-sm transition-colors ${rootFolderClass(viewMode, p.dropOnRoot)}`}
          >
            <Inbox className="w-3.5 h-3.5" />
            {t('favorites.noFolder')}
            <span className="ml-auto text-[10px] text-text-muted">{favs.filter(f => f.folderId == null).length}</span>
          </button>
        </li>
      </ul>

      {/* New root folder input */}
      {p.creatingRoot && (
        <div className="mb-2">
          <input
            ref={p.newFolderInput}
            autoFocus
            placeholder={t('favorites.folderNamePlaceholder')}
            onBlur={p.onCreateRoot}
            onKeyDown={e => {
              if (e.key === 'Enter') p.onCreateRoot()
              if (e.key === 'Escape') p.setCreatingRoot(false)
            }}
            className="w-full bg-surface border border-default rounded px-2 py-1 text-xs text-text-primary focus:outline-none focus:border-pink-500"
          />
        </div>
      )}

      {/* Folder tree */}
      <FolderTree
        nodes={tree}
        depth={0}
        selectedId={p.selectedFolderId}
        expanded={p.expanded}
        editingId={p.editingId}
        onSelect={id => { p.setSelectedFolderId(id); p.setViewMode(id) }}
        onToggle={id => p.setExpanded(prev => {
          const next = new Set(prev)
          if (next.has(id)) next.delete(id); else next.add(id)
          return next
        })}
        onStartEdit={p.setEditingId}
        onCommitEdit={p.onRename}
        onCancelEdit={() => p.setEditingId(null)}
        onDelete={p.onDeleteFolder}
        onCreateSub={p.onCreateSub}
        onToggleHidden={p.onToggleHidden}
        onDropOnFolder={(fid, name) => p.onDropOnFolder(fid, name)}
      />
    </aside>
  )
}
