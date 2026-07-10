import { useTranslation } from 'react-i18next'
import {
  ChevronDown,
  HardDrive,
  HardDriveDownload,
  FolderSync,
  CopyCheck,
  Upload,
  RefreshCw,
} from 'lucide-react'
import { LocalEntry, LocalMount } from '../../api/client'
import CleanEmptyButton from './CleanEmptyButton'
import { Breadcrumbs } from './Breadcrumbs'

type Props = {
  readonly activeMount: string
  readonly path: string
  readonly onNavigate: (path: string) => void
  readonly onOpenMountSheet: () => void
  readonly onRefresh: () => void
  readonly loading: boolean
  readonly activeMountObj: LocalMount | undefined
  readonly onCacheFolder: () => void
  readonly canManipulate: boolean
  readonly isAdmin: boolean
  readonly fileInputRef: React.MutableRefObject<HTMLInputElement | null>
  readonly onUploadPick: (e: React.ChangeEvent<HTMLInputElement>) => void
  readonly uploadInFlight: boolean
  readonly onCleanEmptyDirs: (scope: 'here' | 'root') => void
  readonly onShowDuplicates: () => void
  readonly onReclassify: (entry: LocalEntry) => void
}

// Barra do breadcrumb + botões de ação (recarregar, cachear pasta, upload,
// limpar vazias, duplicados, reclassificar).
export function LocalActionBar({
  activeMount, path, onNavigate, onOpenMountSheet, onRefresh, loading, activeMountObj,
  onCacheFolder, canManipulate, isAdmin, fileInputRef, onUploadPick, uploadInFlight,
  onCleanEmptyDirs, onShowDuplicates, onReclassify,
}: Props) {
  const { t } = useTranslation()
  return (
    <div className="flex-shrink-0 flex flex-wrap items-center gap-2">
      <div className="flex items-center gap-2 min-w-0 flex-1 max-md:basis-full">
        {/* Dropdown de mount — só no mobile (a sidebar some em <md) */}
        <button
          onClick={onOpenMountSheet}
          className="md:hidden flex-shrink-0 flex items-center gap-1.5 px-2.5 min-h-[40px] rounded-lg bg-surface-secondary border border-default text-sm text-text-primary max-w-[45vw]"
        >
          <HardDrive className="w-4 h-4 text-green-400 flex-shrink-0" />
          <span className="truncate">{activeMount}</span>
          <ChevronDown className="w-4 h-4 text-text-muted flex-shrink-0" />
        </button>
        <Breadcrumbs mountName={activeMount} path={path} onNavigate={onNavigate} />
      </div>
      {/* Botões de ação agrupados: no mobile quebram juntos para a linha
          de baixo (antes encavalavam no breadcrumb); inline no desktop. */}
      <div className="flex items-center gap-2 flex-shrink-0">
      <button
        onClick={onRefresh}
        disabled={loading}
        title={t('local.reloadTitle')}
        className="flex-shrink-0 inline-flex items-center gap-1.5 text-sm bg-surface-tertiary/60 hover:bg-surface-tertiary disabled:opacity-50 text-text-primary border border-strong px-3 py-1.5 rounded-lg transition-colors font-medium"
      >
        <RefreshCw className={`w-4 h-4 ${loading ? 'animate-spin' : ''}`} />
        <span className="hidden sm:inline">{t('local.reload')}</span>
      </button>
      {activeMountObj?.cacheable && (
        <button
          onClick={onCacheFolder}
          title={t('local.cacheFolderTitle')}
          className="flex-shrink-0 inline-flex items-center gap-1.5 text-sm bg-green-500/15 hover:bg-green-500/25 text-green-400 border border-green-500/30 px-3 py-1.5 rounded-lg transition-colors font-medium"
        >
          <HardDriveDownload className="w-4 h-4" />
          <span className="hidden sm:inline">{t('local.cacheFolder')}</span>
        </button>
      )}
      {(canManipulate || isAdmin) && (
        <>
          <input
            ref={fileInputRef}
            type="file"
            accept=".mkv,.mp4,.m4v,.avi,.mov,.webm,.ts,.m2ts,.mpg,.mpeg,.wmv,.flv,.ogv,.3gp,.srt,.vtt,.ass,.ssa,.sub"
            onChange={onUploadPick}
            className="hidden"
          />
          <button
            onClick={() => fileInputRef.current?.click()}
            disabled={uploadInFlight}
            title={t('local.uploadTitle')}
            className="flex-shrink-0 inline-flex items-center gap-1.5 text-sm bg-green-500/15 hover:bg-green-500/25 disabled:opacity-50 text-green-400 border border-green-500/30 px-3 py-1.5 rounded-lg transition-colors font-medium"
          >
            <Upload className="w-4 h-4" />
            <span className="hidden sm:inline">{t('local.uploadButton')}</span>
          </button>
          <CleanEmptyButton atRoot={!path} onClean={onCleanEmptyDirs} />
          <button
            onClick={onShowDuplicates}
            title={t('local.duplicatesTitle')}
            className="flex-shrink-0 inline-flex items-center gap-1.5 text-sm bg-surface-tertiary/60 hover:bg-surface-tertiary text-text-primary border border-strong px-3 py-1.5 rounded-lg transition-colors font-medium"
          >
            <CopyCheck className="w-4 h-4" />
            <span className="hidden sm:inline">{t('local.duplicatesButton')}</span>
          </button>
        </>
      )}
      {isAdmin && (
        <button
          onClick={() => onReclassify({ name: path ? path.split('/').pop() || path : activeMount, path, isDir: true, size: 0, modTime: '', isPlayable: false })}
          title={t('local.reclassifyTitle')}
          className="flex-shrink-0 inline-flex items-center gap-1.5 text-sm bg-purple-500/15 hover:bg-purple-500/25 text-purple-400 border border-purple-500/30 px-3 py-1.5 rounded-lg transition-colors font-medium"
        >
          <FolderSync className="w-4 h-4" />
          <span className="hidden sm:inline">{t('local.reclassifyButton')}</span>
        </button>
      )}
      </div>
    </div>
  )
}
