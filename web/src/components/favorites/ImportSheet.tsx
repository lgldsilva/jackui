import { useTranslation } from 'react-i18next'
import { Loader2, Download, UploadCloud } from 'lucide-react'
import { FavoriteFolder } from '../../api/client'
import { Sheet } from '../Sheet'

type ImportSheetProps = {
  readonly open: boolean
  readonly onClose: () => void
  readonly importing: boolean
  readonly viewMode: number | null
  readonly ALL_VIEW: number
  readonly folders: FavoriteFolder[]
  readonly magnetInput: string
  readonly setMagnetInput: (v: string) => void
  readonly alsoDownload: boolean
  readonly setAlsoDownload: (v: boolean) => void
  readonly onImportMagnets: () => void
  readonly onImportFiles: (files: File[]) => void
  readonly importMsg: { kind: 'ok' | 'err'; text: string } | null
  readonly dragOverDrop: boolean
  readonly setDragOverDrop: (v: boolean) => void
}

export default function ImportSheet(p: ImportSheetProps) {
  const { t } = useTranslation()
  const { viewMode, ALL_VIEW, folders, magnetInput } = p
  return (
    <Sheet
      open={p.open}
      onClose={p.onClose}
      size="lg"
      icon={<Download className="w-4 h-4 text-pink-400 flex-shrink-0" />}
      title={
        <>
          {t('favorites.importTorrent')}
          {viewMode !== ALL_VIEW && (
            <span className="text-[10px] text-text-secondary font-normal ml-1">
              → {folders.find(f => f.id === viewMode)?.name || t('favorites.folderFallback')}
            </span>
          )}
        </>
      }
    >
      <div className="flex flex-col gap-4">
        {/* Magnet textarea — one per line for batch */}
        <div>
          <label htmlFor="import-magnet" className="text-xs text-text-secondary mb-1 block">{t('favorites.magnetLinkLabel')}</label>
          <textarea
            id="import-magnet"
            value={magnetInput}
            onChange={e => p.setMagnetInput(e.target.value)}
            placeholder="magnet:?xt=urn:btih:..."
            rows={3}
            className="w-full bg-surface border border-default rounded-lg px-3 py-2 text-sm text-text-primary font-mono resize-y focus:border-pink-500 focus:outline-none"
          />
          <label htmlFor="also-download-checkbox" className="flex items-center gap-2 mt-2 text-sm text-text-primary cursor-pointer">
            <input
              id="also-download-checkbox"
              type="checkbox"
              checked={p.alsoDownload}
              onChange={e => p.setAlsoDownload(e.target.checked)}
              aria-describedby="also-download-hint"
              className="accent-pink-500 w-4 h-4"
            />
            <span>{t('favorites.alsoEnqueueDownload')}</span>
          </label>
          <p id="also-download-hint" className="text-[11px] text-text-muted pl-6">{t('favorites.alsoEnqueueDownloadHint')}</p>
          <button
            onClick={p.onImportMagnets}
            disabled={p.importing || !magnetInput.trim()}
            className="mt-2 w-full flex items-center justify-center gap-2 text-sm bg-pink-500/20 hover:bg-pink-500/30 text-pink-700 dark:text-pink-200 border border-pink-500/30 px-3 py-2 rounded-lg transition-colors disabled:opacity-40"
          >
            {p.importing ? <Loader2 className="w-4 h-4 animate-spin" /> : <Download className="w-4 h-4" />}
            {p.alsoDownload ? t('favorites.importAndDownload') : t('favorites.importMagnet')}
          </button>
        </div>

        <div className="flex items-center gap-2 text-[10px] text-text-muted uppercase tracking-wider">
          <div className="flex-1 h-px bg-surface-tertiary" /> {t('favorites.or')} <div className="flex-1 h-px bg-surface-tertiary" />
        </div>

        {/* .torrent dropzone */}
        <label
          onDragOver={e => { e.preventDefault(); p.setDragOverDrop(true) }}
          onDragLeave={() => p.setDragOverDrop(false)}
          onDrop={e => {
            e.preventDefault()
            p.setDragOverDrop(false)
            const fs = Array.from(e.dataTransfer.files || [])
            if (fs.length) p.onImportFiles(fs)
          }}
          className={`flex flex-col items-center justify-center gap-2 border-2 border-dashed rounded-xl py-10 cursor-pointer transition-colors ${
            p.dragOverDrop ? 'border-pink-500 bg-pink-500/10' : 'border-default hover:border-strong'
          }`}
        >
          <UploadCloud className="w-7 h-7 text-text-muted" />
          <span className="text-sm text-text-secondary">{t('favorites.dropzoneHint')}</span>
          <input
            type="file"
            accept=".torrent"
            multiple
            className="hidden"
            onChange={e => { const fs = Array.from(e.target.files || []); if (fs.length) p.onImportFiles(fs) }}
          />
        </label>

        {p.importMsg && (
          <p role="status" aria-live="polite" className={`text-sm ${p.importMsg.kind === 'ok' ? 'text-green-400' : 'text-red-400'}`}>
            {p.importMsg.text}
          </p>
        )}
      </div>
    </Sheet>
  )
}
