import { AlertCircle } from 'lucide-react'
import { useTranslation } from 'react-i18next'
import { TorrentInfo } from '../../api/client'
import { buildErrorInfo } from './playerEffects'

// Frozen diagnostic snapshot type mirrors videoDiagnostic()'s return.
type Diag = Record<string, unknown>

// Diagnostic chip shown under the video-error message. Renders the frozen
// error-time snapshot (falls back to a live probe) — MediaError code, ready/net
// state, transcode status — so a single user report is enough to debug.
function renderDiagnosticChip(lastErrorDiag: Diag | null, videoDiagnostic: () => Diag, t: ReturnType<typeof useTranslation>['t']) {
  const diag = (lastErrorDiag ?? videoDiagnostic()) as Record<string, any>
  const codeNames: Record<number, string> = { 1: 'ABORTED', 2: 'NETWORK', 3: 'DECODE', 4: 'SRC_NOT_SUPPORTED' }
  const codeName = diag.errorCode ? codeNames[diag.errorCode] || `code ${diag.errorCode}` : '—'
  return (
    <div className="mt-3 text-[10px] text-text-muted font-mono space-y-0.5">
      <div>MediaError: <span className="text-yellow-400">{codeName}</span> {diag.errorMsg ? `· ${diag.errorMsg}` : ''}</div>
      <div>ready={diag.readyState ?? '—'} net={diag.networkState ?? '—'} {diag.isTranscoded ? '· transcode ON' : '· direct play'}{diag.transcodeFallbackAttempted ? ' · fallback tried' : ''}</div>
      <div className="text-text-muted">{t('player.modal.fullLogHint')}</div>
    </div>
  )
}

export function VideoErrorOverlay(props: {
  info: TorrentInfo | null
  selectedFile: number
  lastErrorDiag: Diag | null
  videoDiagnostic: () => Diag
  onRetry: () => void
}) {
  const { info, selectedFile, lastErrorDiag, videoDiagnostic, onRetry } = props
  const { t } = useTranslation()
  const cf = info?.files?.[selectedFile]
  const peers = info?.peers ?? 0
  const fileDownloaded = cf?.downloaded ?? 0
  const starving = fileDownloaded < 30 * 1024 * 1024
  const kind: 'swarm' | 'codec' = (peers === 0 || starving) ? 'swarm' : 'codec'
  const errorData = buildErrorInfo(peers, starving, info)
  return (
    <div className="absolute inset-0 flex flex-col items-center justify-center text-text-primary p-6 text-center">
      <AlertCircle className={`w-12 h-12 mb-3 ${kind === 'swarm' ? 'text-orange-400' : 'text-yellow-400'}`} />
      <p className="font-medium">{errorData.title}</p>
      <p className="text-sm text-text-muted mt-2 max-w-md">{errorData.detail}</p>
      {renderDiagnosticChip(lastErrorDiag, videoDiagnostic, t)}
      <button
        onClick={onRetry}
        className="mt-4 text-xs text-green-400 hover:underline"
      >
        {t('player.modal.tryAgain')}
      </button>
    </div>
  )
}
