import { useTranslation } from 'react-i18next'
import { X, FileText, FileImage, FileType2, BookOpen, Archive, BookText, Download } from 'lucide-react'
import { Sheet } from './Sheet'
import { detectViewerKind, type ViewerKind } from './viewer/viewerKind'
import { previewRawURL } from '../api/preview'
import TextViewer from './viewer/TextViewer'
import ImageViewer, { type ImageItem } from './viewer/ImageViewer'
import PdfViewer from './viewer/PdfViewer'
import ComicViewer from './viewer/ComicViewer'
import ArchiveViewer from './viewer/ArchiveViewer'
import EpubViewer from './viewer/EpubViewer'

/**
 * FilePreviewModal — universal read-only viewer for non-playable files, from
 * torrents (infoHash + fileIdx) OR local mounts (the `local-...` pseudo-hash
 * encodes mount+path, same mechanism the player uses).
 *
 * This is a thin shell: header bar (name, size, download, close) + a switch
 * that delegates to the kind-specific viewer in ./viewer/*. Each viewer owns
 * its data fetching — keeps this file from regrowing into a god-component.
 */

type FilePreviewModalProps = {
  readonly infoHash: string
  readonly fileIdx: number
  readonly filePath: string
  readonly fileSize: number
  // Sibling image candidates for prev/next navigation. When absent, the
  // viewer shows only the clicked image.
  readonly imageItems?: ReadonlyArray<ImageItem>
  readonly imageStart?: number
  readonly onClose: () => void
}

const KIND_ICON: Record<ViewerKind, typeof FileText> = {
  text: FileText,
  image: FileImage,
  pdf: FileType2,
  comic: BookOpen,
  archive: Archive,
  epub: BookText,
  unknown: FileText,
}

export default function FilePreviewModal({ infoHash, fileIdx, filePath, fileSize, imageItems, imageStart, onClose }: FilePreviewModalProps) {
  const { t } = useTranslation()
  const kind = detectViewerKind(filePath)
  const rawURL = previewRawURL(infoHash, fileIdx)
  const fileName = filePath.split('/').at(-1) ?? filePath
  const Icon = KIND_ICON[kind]

  const images: ReadonlyArray<ImageItem> = imageItems && imageItems.length > 0
    ? imageItems
    : [{ label: filePath, url: rawURL }]

  return (
    // hideHeader: o preview é edge-to-edge com barra própria (download +
    // fechar). zClass z-[60] preserva a sobreposição sobre o player.
    <Sheet open onClose={onClose} size="4xl" zClass="z-[60]" hideHeader>
      <>
        {/* Barra do preview — cola no topo do corpo (compensa o p-4 do Sheet) */}
        <div className="-mx-4 -mt-4 mb-4 flex items-center justify-between p-3 border-b border-default">
          <h3 className="text-sm font-semibold text-text-primary flex items-center gap-2 min-w-0">
            <Icon className="w-4 h-4 text-blue-400 flex-shrink-0" />
            <span className="truncate" title={filePath}>{fileName}</span>
            <span className="text-xs text-text-muted flex-shrink-0">
              {fileSize > 0 ? `${(fileSize / 1024).toFixed(1)} KB` : ''}
            </span>
          </h3>
          <div className="flex items-center gap-1 flex-shrink-0">
            <a
              href={rawURL}
              download={fileName}
              title={t('viewer.download_full')}
              className="p-1.5 rounded hover:bg-surface-tertiary text-text-secondary hover:text-text-primary"
            >
              <Download className="w-4 h-4" />
            </a>
            <button
              onClick={onClose}
              className="p-1.5 rounded hover:bg-surface-tertiary text-text-secondary hover:text-text-primary"
              title={t('viewer.close')}
            >
              <X className="w-4 h-4" />
            </button>
          </div>
        </div>

        <div className="-mx-4 -mb-4 min-h-[50vh] bg-surface rounded-b-2xl overflow-hidden flex flex-col">
          {kind === 'text' && <TextViewer url={rawURL} filePath={filePath} fileSize={fileSize} />}
          {kind === 'image' && <ImageViewer items={images} start={imageStart ?? 0} />}
          {kind === 'pdf' && <PdfViewer url={rawURL} fileName={fileName} />}
          {kind === 'comic' && <ComicViewer infoHash={infoHash} fileIdx={fileIdx} />}
          {kind === 'archive' && <ArchiveViewer infoHash={infoHash} fileIdx={fileIdx} />}
          {kind === 'epub' && <EpubViewer infoHash={infoHash} fileIdx={fileIdx} />}
          {kind === 'unknown' && (
            <div className="p-8 text-center text-text-muted text-sm">
              {t('viewer.no_preview')}
            </div>
          )}
        </div>
      </>
    </Sheet>
  )
}
