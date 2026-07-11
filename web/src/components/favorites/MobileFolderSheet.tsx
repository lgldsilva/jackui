import { useTranslation } from 'react-i18next'
import { Heart, Trash2, Folder, FolderPlus, Pencil, Inbox, Eye, EyeOff } from 'lucide-react'
import { StreamFavorite, FavoriteFolder } from '../../api/client'
import { Sheet } from '../Sheet'
import { flattenTree, type FolderNode } from '../../lib/favoritesTree'

type MobileFolderSheetProps = {
  readonly open: boolean
  readonly onClose: () => void
  readonly revealHidden: boolean
  readonly viewMode: number | null
  readonly ALL_VIEW: number
  readonly favs: StreamFavorite[]
  readonly tree: FolderNode[]
  readonly setViewMode: (v: number | null) => void
  readonly setSelectedFolderId: (v: number | null) => void
  readonly onCreateRoot: () => void
  readonly onToggleHidden: (id: number, hidden: boolean) => void
  readonly onCreateSub: (parentId: number) => void
  readonly onRename: (folder: FavoriteFolder) => void
  readonly onDeleteFolder: (id: number) => void
}

export default function MobileFolderSheet(p: MobileFolderSheetProps) {
  const { t } = useTranslation()
  const { revealHidden, viewMode, ALL_VIEW, favs, tree } = p
  return (
    <Sheet
      open={p.open}
      onClose={p.onClose}
      title={<>{t('favorites.folders')}{revealHidden && <Eye className="inline w-3.5 h-3.5 ml-1 text-amber-400" aria-label={t('favorites.hiddenVisibleAria')} />}</>}
      icon={<Folder className="w-4 h-4 text-pink-400 flex-shrink-0" />}
      size="sm"
    >
      {/* Criar/editar/excluir categorias direto no mobile (a sidebar com isso
          é hidden md:block). */}
      <button
        onClick={p.onCreateRoot}
        className="w-full flex items-center justify-center gap-2 mb-2 px-3 min-h-[44px] rounded-lg text-sm bg-pink-500/15 text-pink-700 dark:text-pink-200 border border-pink-500/30 hover:bg-pink-500/25 transition-colors"
      >
        <FolderPlus className="w-4 h-4 flex-shrink-0" />
        {t('favorites.newFolder')}
      </button>
      <ul className="flex flex-col gap-1">
        <li>
          <button
            onClick={() => { p.setViewMode(ALL_VIEW); p.setSelectedFolderId(null); p.onClose() }}
            className={`w-full flex items-center gap-2 px-3 min-h-[44px] rounded-lg text-sm transition-colors ${
              viewMode === ALL_VIEW ? 'bg-pink-500/15 text-pink-700 dark:text-pink-200 border border-pink-500/30' : 'text-text-primary hover:bg-surface-tertiary border border-transparent'
            }`}
          >
            <Heart className="w-4 h-4 fill-current flex-shrink-0" />
            <span className="flex-1 text-left">{t('favorites.all')}</span>
            <span className="text-[10px] text-text-muted">{favs.length}</span>
          </button>
        </li>
        <li>
          <button
            onClick={() => { p.setViewMode(null); p.setSelectedFolderId(null); p.onClose() }}
            className={`w-full flex items-center gap-2 px-3 min-h-[44px] rounded-lg text-sm transition-colors ${
              viewMode === null ? 'bg-pink-500/15 text-pink-700 dark:text-pink-200 border border-pink-500/30' : 'text-text-primary hover:bg-surface-tertiary border border-transparent'
            }`}
          >
            <Inbox className="w-4 h-4 flex-shrink-0" />
            <span className="flex-1 text-left">{t('favorites.noFolder')}</span>
            <span className="text-[10px] text-text-muted">{favs.filter(f => f.folderId == null).length}</span>
          </button>
        </li>
        {flattenTree(tree).map(({ folder, depth }) => (
          <li key={folder.id} className={`flex items-center rounded-lg transition-colors ${
            viewMode === folder.id ? 'bg-pink-500/15 border border-pink-500/30' : 'border border-transparent hover:bg-surface-tertiary'
          }`}>
            <button
              onClick={() => { p.setViewMode(folder.id); p.setSelectedFolderId(folder.id); p.onClose() }}
              className={`flex-1 min-w-0 flex items-center gap-2 min-h-[44px] text-sm text-left ${
                viewMode === folder.id ? 'text-pink-700 dark:text-pink-200' : 'text-text-primary'
              }`}
              style={{ paddingLeft: `${12 + depth * 16}px` }}
            >
              <Folder className="w-4 h-4 text-text-muted flex-shrink-0" />
              <span className="flex-1 text-left truncate">{folder.name}</span>
              {folder.hidden && <EyeOff className="w-3.5 h-3.5 text-amber-400 flex-shrink-0" aria-label={t('favorites.folderHiddenAria')} />}
              <span className="text-[10px] text-text-muted">{favs.filter(f => f.folderId === folder.id).length}</span>
            </button>
            {/* Ações da categoria — ocultar / subpasta / renomear / excluir.
                Pastas ocultas só aparecem aqui com o modo revelado ativo. */}
            <button onClick={() => p.onToggleHidden(folder.id, !folder.hidden)} title={folder.hidden ? t('favorites.showFolder') : t('favorites.hideFolder')} className="p-2 text-text-muted hover:text-amber-400 flex-shrink-0">
              {folder.hidden ? <Eye className="w-4 h-4" /> : <EyeOff className="w-4 h-4" />}
            </button>
            <button onClick={() => p.onCreateSub(folder.id)} title={t('favorites.newSubfolder')} className="p-2 text-text-muted hover:text-pink-400 flex-shrink-0">
              <FolderPlus className="w-4 h-4" />
            </button>
            <button onClick={() => p.onRename(folder)} title={t('favorites.rename')} className="p-2 text-text-muted hover:text-text-primary flex-shrink-0">
              <Pencil className="w-4 h-4" />
            </button>
            <button onClick={() => p.onDeleteFolder(folder.id)} title={t('favorites.delete')} className="p-2 pr-3 text-text-muted hover:text-red-400 flex-shrink-0">
              <Trash2 className="w-4 h-4" />
            </button>
          </li>
        ))}
      </ul>
    </Sheet>
  )
}
