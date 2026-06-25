import { useState } from 'react'
import { ArrowLeftRight, ChevronDown, ChevronUp } from 'lucide-react'
import { useTransfers } from '../lib/transfers'
import type { TransferSnapshot } from '../api/transfers'
import FileProgressBar from './FileProgressBar'

// TransfersDock — the global, fixed panel that shows every active/recent file
// move/copy (post-download move, Local-tab move, promote, AI-rename) with one
// consistent FileProgressBar. Sits bottom-LEFT so it never collides with the
// player dock (bottom-right). Hides itself when there's nothing to show.

const KIND_LABEL: Record<string, string> = {
  'download-move': 'Finalizando download',
  'local-move': 'Movendo',
  'promote': 'Promovendo',
  'ai-rename': 'Organizando (IA)',
}

function kindLabel(kind: string): string {
  return KIND_LABEL[kind] ?? 'Transferência'
}

export default function TransfersDock() {
  const { transfers, cancel } = useTransfers()
  const [collapsed, setCollapsed] = useState(false)

  if (transfers.length === 0) return null

  const running = transfers.filter((t) => t.status === 'running').length
  const queued = transfers.filter((t) => t.status === 'queued').length

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
            Transferências
            {running > 0 && <span className="ml-1 text-text-muted font-normal">· {running} ativa{running > 1 ? 's' : ''}</span>}
            {queued > 0 && <span className="ml-1 text-text-muted font-normal">· {queued} na fila</span>}
          </span>
          {collapsed ? <ChevronUp className="w-3.5 h-3.5" /> : <ChevronDown className="w-3.5 h-3.5" />}
        </button>
        {!collapsed && (
          <div className="max-h-[40vh] overflow-y-auto p-3 flex flex-col gap-3">
            {transfers.map((t: TransferSnapshot) => (
              <FileProgressBar
                key={t.id}
                label={`${kindLabel(t.kind)}: ${t.label}`}
                status={t.status}
                filesDone={t.filesDone}
                filesTotal={t.filesTotal}
                bytesDone={t.bytesDone}
                bytesTotal={t.bytesTotal}
                ratePerSec={t.ratePerSec}
                etaSeconds={t.etaSeconds}
                progress={t.progress}
                error={t.error}
                onCancel={() => cancel(t.id)}
              />
            ))}
          </div>
        )}
      </div>
    </div>
  )
}
