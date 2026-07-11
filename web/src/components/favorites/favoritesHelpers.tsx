import type { TFunction } from 'i18next'
import { Heart, Loader2 } from 'lucide-react'
import { StreamFavorite, FavoriteFolder } from '../../api/client'

export function rootFolderClass(viewMode: number | null, dropOnRoot: boolean): string {
  if (viewMode === null) return 'bg-pink-500/15 text-pink-700 dark:text-pink-200 border border-pink-500/30'
  if (dropOnRoot) return 'bg-pink-500/20 border border-pink-500/50 text-pink-700 dark:text-pink-100'
  return 'text-text-primary hover:bg-surface-secondary border border-transparent'
}

export function pageTitle(viewMode: number | null, ALL_VIEW: number, folders: FavoriteFolder[], t: TFunction): string {
  if (viewMode === ALL_VIEW) return t('favorites.allFavorites')
  if (viewMode === null) return t('favorites.noFolder')
  return folders.find(f => f.id === viewMode)?.name || t('favorites.fallbackTitle')
}

export function renderFavsContent(loading: boolean, error: string, filteredFavs: StreamFavorite[], viewMode: number | null, ALL_VIEW: number, _folders: FavoriteFolder[], t: TFunction): JSX.Element | null {
  if (loading) {
    return <div className="flex items-center justify-center py-20 text-text-muted">
      <Loader2 className="w-8 h-8 animate-spin" />
    </div>
  }
  if (error) {
    return <div className="card text-red-400 text-sm">{t('favorites.errorLabel', { error })}</div>
  }
  if (filteredFavs.length === 0) {
    const insideFolder = viewMode !== ALL_VIEW
    return <div className="flex flex-col items-center justify-center py-20 text-text-muted">
      <Heart className="w-16 h-16 mb-4 opacity-30" />
      <p className="text-xl font-medium">{insideFolder ? t('favorites.emptyInFolder') : t('favorites.emptyNone')}</p>
      <p className="text-sm mt-2 text-center max-w-md">
        {viewMode === ALL_VIEW
          ? t('favorites.emptyHintAll')
          : t('favorites.emptyHintFolder')}
      </p>
    </div>
  }
  return null
}
