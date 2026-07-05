import { useTranslation } from 'react-i18next'
import { Link2, Loader2, HardDrive, Cloud, ListChecks } from 'lucide-react'
import { Sheet } from './Sheet'
import { formatBytes } from '../lib/format'
import type { DedupMatch } from '../api/client'

type DedupPromptProps = {
  readonly matches: DedupMatch[]
  readonly totalFiles: number
  readonly busy: boolean
  readonly onUseExisting: () => void
  readonly onDownloadAll: () => void
  readonly onCancel: () => void
}

// SourceIcon picks an icon per match source (library/cloud/download).
function SourceIcon({ source }: { readonly source: DedupMatch['source'] }) {
  if (source === 'cloud') return <Cloud className="w-4 h-4 text-cyan-400 flex-shrink-0" />
  if (source === 'download') return <ListChecks className="w-4 h-4 text-text-muted flex-shrink-0" />
  return <HardDrive className="w-4 h-4 text-green-400 flex-shrink-0" />
}

// DedupPrompt asks the user whether to link the files a torrent already has on
// disk (library/cloud/queue) instead of re-downloading them. Presentational: the
// link + reduced-enqueue logic lives in the caller's callbacks.
export default function DedupPrompt({ matches, totalFiles, busy, onUseExisting, onDownloadAll, onCancel }: DedupPromptProps) {
  const { t } = useTranslation()
  return (
    <Sheet
      open
      onClose={onCancel}
      size="md"
      title={t('downloads.dedup.title')}
      icon={<Link2 className="w-4 h-4 text-green-500 flex-shrink-0" />}
      footer={
        <div className="flex flex-col-reverse sm:flex-row gap-3">
          <button onClick={onDownloadAll} disabled={busy} className="btn-secondary flex-1 disabled:opacity-50">
            {t('downloads.dedup.download_all')}
          </button>
          <button
            onClick={onUseExisting}
            disabled={busy}
            className="btn-primary flex-1 flex items-center justify-center gap-2 disabled:opacity-50"
          >
            {busy ? <Loader2 className="w-4 h-4 animate-spin" /> : <Link2 className="w-4 h-4" />}
            {t('downloads.dedup.use_existing')}
          </button>
        </div>
      }
    >
      <div className="flex flex-col gap-3">
        <p className="text-sm text-text-primary">
          {t('downloads.dedup.summary', { count: matches.length, total: totalFiles })}
        </p>
        <p className="text-xs text-text-muted">{t('downloads.dedup.explain')}</p>
        <ul className="bg-surface border border-default rounded-lg max-h-60 overflow-y-auto divide-y divide-default">
          {matches.map((mt) => (
            <li key={mt.fileIndex} className="px-3 py-2 flex items-center gap-2.5">
              <SourceIcon source={mt.source} />
              <span className="flex-1 min-w-0 text-sm text-text-primary truncate" title={mt.name}>
                {mt.name}
              </span>
              <span className="text-[11px] text-text-muted flex-shrink-0">
                {t(`downloads.dedup.source_${mt.source}`)}
                {mt.confidence === 'probable' ? ` · ${t('downloads.dedup.probable')}` : ''}
              </span>
              <span className="text-xs text-text-muted flex-shrink-0">{formatBytes(mt.size)}</span>
            </li>
          ))}
        </ul>
      </div>
    </Sheet>
  )
}
