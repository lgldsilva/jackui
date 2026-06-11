import { useTranslation } from 'react-i18next'
import { Download } from 'lucide-react'

type PdfViewerProps = {
  readonly url: string
  readonly fileName: string
}

// PdfViewer — native browser PDF rendering via <iframe> (Chromium/Firefox/
// desktop Safari ship viewers; no pdf.js bundle needed). Known caveat: iOS
// Safari renders only the first page inside an iframe, so a download/open
// fallback is always offered below the frame.
export default function PdfViewer({ url, fileName }: PdfViewerProps) {
  const { t } = useTranslation()
  return (
    <div className="flex flex-col h-full min-h-[60vh]">
      <iframe src={url} title={fileName} className="w-full flex-1 min-h-[55vh] bg-white" />
      <div className="flex items-center justify-center gap-2 py-2 border-t border-default text-xs text-text-muted">
        <span>{t('viewer.pdf_fallback')}</span>
        <a href={url} download={fileName} className="inline-flex items-center gap-1 text-blue-400 hover:text-blue-300">
          <Download className="w-3.5 h-3.5" /> {t('viewer.download')}
        </a>
      </div>
    </div>
  )
}
