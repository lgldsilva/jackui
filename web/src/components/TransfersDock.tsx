import { useState } from 'react'
import { useTranslation } from 'react-i18next'
import { ArrowLeftRight, ChevronDown, ChevronUp } from 'lucide-react'
import i18n from '../lib/i18n'
import { useTransfers } from '../lib/transfers'
import type { TransferSnapshot } from '../api/transfers'
import FileProgressBar from './FileProgressBar'

// TransfersDock — the global, fixed panel that shows every active/recent file
// move/copy (post-download move, Local-tab move, promote, AI-rename) with one
// consistent FileProgressBar. Sits bottom-LEFT so it never collides with the
// player dock (bottom-right). Hides itself when there's nothing to show.

const KIND_KEY: Record<string, string> = {
  'download-move': 'transfers.kind.downloadMove',
  'local-move': 'transfers.kind.localMove',
  'promote': 'transfers.kind.promote',
  'ai-rename': 'transfers.kind.aiRename',
}

function kindLabel(kind: string): string {
  return i18n.t(KIND_KEY[kind] ?? 'transfers.kind.generic')
}

export default function TransfersDock() {
  const { t } = useTranslation()
  const { transfers, cancel } = useTransfers()
  const [collapsed, setCollapsed] = useState(false)

  if (transfers.length === 0) return null

  const running = transfers.filter((x) => x.status === 'running').length
  const queued = transfers.filter((x) => x.status === 'queued').length

  return (
    <div
      className="fixed left-3 z-50 w-[340px] max-w-[calc(100vw-1.5rem)]"
      style={{ bottom: 'calc(0.75rem + env(safe-area-inset-bottom, 0px))' }}
    >
      <div className="rounded-xl border border-strong/60 bg-card dark:bg-gray-900/95 backdrop-blur-md shadow-xl overflow-hidden">
        <button
          onClick={() => setCollapsed((v) => !v)}
          className="w-full flex items-center gap-2 px-3 py-2 text-xs font-semibold text-text-primary border-b border-strong/40 hover:bg-surface-tertiary/40 transition-colors"
        >
          <ArrowLeftRight className="w-3.5 h-3.5 text-amber-400" />
          <span className="flex-1 text-left">
            {t('transfers.title')}
            {running > 0 && <span className="ml-1 text-text-muted font-normal">· {t('transfers.activeCount', { count: running })}</span>}
            {queued > 0 && <span className="ml-1 text-text-muted font-normal">· {t('transfers.queuedCount', { count: queued })}</span>}
          </span>
          {collapsed ? <ChevronUp className="w-3.5 h-3.5" /> : <ChevronDown className="w-3.5 h-3.5" />}
        </button>
        {!collapsed && (
          <div className="max-h-[40vh] overflow-y-auto p-3 flex flex-col gap-3">
            {transfers.map((item: TransferSnapshot) => (
              <FileProgressBar
                key={item.id}
                label={`${kindLabel(item.kind)}: ${item.label}`}
                status={item.status}
                filesDone={item.filesDone}
                filesTotal={item.filesTotal}
                bytesDone={item.bytesDone}
                bytesTotal={item.bytesTotal}
                ratePerSec={item.ratePerSec}
                etaSeconds={item.etaSeconds}
                progress={item.progress}
                error={item.error}
                onCancel={() => cancel(item.id)}
              />
            ))}
          </div>
        )}
      </div>
    </div>
  )
}
