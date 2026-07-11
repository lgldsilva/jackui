import { useTranslation } from 'react-i18next'
import { Trash2, Folder, FolderOpen, ChevronRight, ChevronDown, Pencil, FolderPlus, Eye, EyeOff } from 'lucide-react'
import { type FolderNode } from '../../lib/favoritesTree'

export type TreeProps = {
  readonly nodes: FolderNode[]
  readonly depth: number
  readonly selectedId: number | null
  readonly expanded: Set<number>
  readonly editingId: number | null
  readonly onSelect: (id: number | null) => void
  readonly onToggle: (id: number) => void
  readonly onStartEdit: (id: number) => void
  readonly onCommitEdit: (id: number, name: string) => void
  readonly onCancelEdit: () => void
  readonly onDelete: (id: number) => void
  readonly onCreateSub: (parentId: number) => void
  readonly onToggleHidden: (id: number, hidden: boolean) => void
  readonly onDropOnFolder: (folderId: number, favoriteName: string) => void
}

export function FolderTree(p: TreeProps) {
  const { t } = useTranslation()
  return (
    <ul className="flex flex-col gap-0.5">
      {p.nodes.map(node => {
        const isOpen = p.expanded.has(node.folder.id)
        const isSelected = p.selectedId === node.folder.id
        const isEditing = p.editingId === node.folder.id
        return (
            <li key={node.folder.id}>
            <button
              type="button"
              className={`group flex items-center gap-1 px-2 py-1 rounded-md text-sm transition-colors w-full text-left ${
                isSelected ? 'bg-pink-500/15 text-pink-700 dark:text-pink-200 border border-pink-500/30' : 'text-text-primary hover:bg-surface-secondary border border-transparent'
              }`}
              style={{ paddingLeft: `${depthIndent(p.depth)}px` }}
              onDragOver={e => { e.preventDefault(); e.dataTransfer.dropEffect = 'move' }}
              onDrop={e => {
                e.preventDefault()
                const name = e.dataTransfer.getData('text/x-favorite-name')
                if (name) p.onDropOnFolder(node.folder.id, name)
              }}
              onClick={() => p.onSelect(node.folder.id)}
            >
              {node.children.length > 0 ? (
                <button onClick={() => p.onToggle(node.folder.id)} className="text-text-muted hover:text-text-primary">
                  {isOpen ? <ChevronDown className="w-3 h-3" /> : <ChevronRight className="w-3 h-3" />}
                </button>
              ) : (
                <span className="w-3" />
              )}
              {isOpen ? <FolderOpen className="w-3.5 h-3.5 text-pink-400" /> : <Folder className="w-3.5 h-3.5 text-text-muted" />}
              {node.folder.hidden && <EyeOff className="w-3 h-3 text-amber-400 flex-shrink-0" aria-label={t('favorites.folderHiddenAria')} />}
              {isEditing ? (
                <input
                  autoFocus
                  defaultValue={node.folder.name}
                  onBlur={e => p.onCommitEdit(node.folder.id, e.currentTarget.value)}
                  onKeyDown={e => {
                    if (e.key === 'Enter') p.onCommitEdit(node.folder.id, e.currentTarget.value)
                    if (e.key === 'Escape') p.onCancelEdit()
                  }}
                  className="flex-1 bg-surface border border-default rounded px-1 text-xs text-text-primary focus:outline-none focus:border-pink-500"
                />
              ) : (
                <button
                  onClick={() => p.onSelect(node.folder.id)}
                  onDoubleClick={() => p.onStartEdit(node.folder.id)}
                  className="flex-1 min-w-0 text-left truncate"
                  title={node.folder.name}
                >
                  {node.folder.name}
                </button>
              )}
              <div className="max-sm:opacity-100 opacity-0 group-hover:opacity-100 flex items-center gap-0.5 transition-opacity">
                <button onClick={() => p.onCreateSub(node.folder.id)} title={t('favorites.subfolder')} className="p-0.5 text-text-muted hover:text-text-primary">
                  <FolderPlus className="w-3 h-3" />
                </button>
                <button onClick={() => p.onStartEdit(node.folder.id)} title={t('favorites.rename')} className="p-0.5 text-text-muted hover:text-text-primary">
                  <Pencil className="w-3 h-3" />
                </button>
                <button onClick={() => p.onToggleHidden(node.folder.id, !node.folder.hidden)} title={node.folder.hidden ? t('favorites.showFolder') : t('favorites.hideFolder')} className="p-0.5 text-text-muted hover:text-amber-400">
                  {node.folder.hidden ? <Eye className="w-3 h-3" /> : <EyeOff className="w-3 h-3" />}
                </button>
                <button onClick={() => p.onDelete(node.folder.id)} title={t('favorites.delete')} className="p-0.5 text-text-muted hover:text-red-400">
                  <Trash2 className="w-3 h-3" />
                </button>
              </div>
            </button>
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
