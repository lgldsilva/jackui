import {
  Check, FileVideo, FileAudio, FileText, Loader2, Download,
} from 'lucide-react'
import { DownloadEntry, StreamFile, TorrentInfo } from '../../api/client'
import { formatBytes } from '../../lib/format'

// Minimal translate-fn signature so the module-level render helpers can receive
// the component's `t` without pulling the full i18next type surface.
type TFn = (key: string, opts?: Record<string, unknown>) => string

// Distingue o file que o download representa (highlight verde) dos outros
// arquivos do torrent (listados em cinza). Em torrents single-file os dois
// coincidem; em multi-file isso ajuda o user a ver o que tinha junto.
export function fileIcon(f: StreamFile, primary: boolean) {
  const color = primary ? 'text-green-400' : 'text-text-muted'
  if (f.isVideo) return <FileVideo className={`w-4 h-4 ${color} flex-shrink-0`} />
  if (/\.(mp3|flac|ogg|wav|m4a|aac|opus)$/i.test(f.path)) {
    return <FileAudio className={`w-4 h-4 ${color} flex-shrink-0`} />
  }
  return <FileText className={`w-4 h-4 ${color} flex-shrink-0`} />
}

export function renderFilesTab(
  torrent: TorrentInfo | null | undefined,
  syntheticFile: StreamFile | null,
  filePath: string,
  fileIndex: number,
  fileIcon: (f: StreamFile, primary: boolean) => React.ReactNode,
  siblings: readonly DownloadEntry[],
  adopting: number | null,
  onAdopt: (f: StreamFile) => void,
  t: TFn,
): React.ReactNode {
  if (!torrent && !syntheticFile) {
    return (
      <p className="text-xs text-text-muted italic py-2">
        {t('downloads.inspect.filesInactive')}
      </p>
    )
  }
  if (!torrent && syntheticFile) {
    return (
      <ul className="bg-surface border border-default rounded-lg divide-y divide-default overflow-hidden">
        <li className="px-3 py-2 flex items-center gap-2.5 bg-green-500/5">
          {fileIcon(syntheticFile, true)}
          <div className="flex-1 min-w-0">
            <p className="text-sm truncate text-green-700 dark:text-green-300 font-medium" title={syntheticFile.path}>{syntheticFile.path}</p>
            {filePath && <p className="text-[10px] text-text-muted font-mono truncate mt-0.5" title={filePath}>{filePath}</p>}
          </div>
          <div className="text-right flex-shrink-0">
            <p className="text-xs text-text-secondary">{formatBytes(syntheticFile.size)}</p>
            <p className="text-[10px] text-green-400 uppercase tracking-wide">{t('downloads.inspect.thisDownload')}</p>
          </div>
        </li>
      </ul>
    )
  }
  if (!torrent || torrent.files.length === 0) {
    return <p className="text-xs text-text-muted italic">{t('downloads.inspect.noFiles')}</p>
  }
  const hasRow = (idx: number) => siblings.some(s => s.fileIndex === idx)
  const missing = torrent.files.filter(f => !hasRow(f.index))
  return (
    <div className="flex flex-col gap-2">
      {missing.length > 1 && (
        <button
          onClick={() => { for (const f of missing) onAdopt(f) }}
          disabled={adopting !== null}
          className="self-start flex items-center gap-1.5 text-xs bg-cyan-500/15 hover:bg-cyan-500/25 disabled:opacity-50 text-cyan-700 dark:text-cyan-300 border border-cyan-500/30 px-3 py-1.5 rounded-lg transition-colors"
          title={t('downloads.inspect.downloadMissingTitle')}
        >
          <Download className="w-3.5 h-3.5" /> {t('downloads.inspect.downloadMissing', { count: missing.length })}
        </button>
      )}
      <ul className="bg-surface border border-default rounded-lg divide-y divide-default overflow-hidden">
        {torrent.files.map(f => {
          const isPrimary = f.index === fileIndex
          // Marca claramente o que falta baixar: completo (>=99.9%) vs incompleto
          // (mostra o % em âmbar + barra, mesmo a 0%) — assim dá pra ver qual
          // arquivo do torrent ficou pra trás.
          const hasProgress = typeof f.progress === 'number'
          const done = hasProgress && f.progress >= 0.999
          const pct = hasProgress ? Math.round(f.progress * 100) : null
          const tracked = hasRow(f.index)
          return (
            <li key={f.index} className={`px-3 py-2 flex items-center gap-2.5 ${isPrimary ? 'bg-green-500/5' : ''}`}>
              {fileIcon(f, isPrimary)}
              <div className="flex-1 min-w-0">
                <p className={`text-sm truncate ${isPrimary ? 'text-green-700 dark:text-green-300 font-medium' : 'text-text-primary'}`} title={f.path}>{f.path}</p>
                {hasProgress && !done && (
                  <div className="mt-1 h-1 bg-surface-tertiary rounded overflow-hidden">
                    <div className="h-full bg-amber-500" style={{ width: `${Math.max(2, pct ?? 0)}%` }} />
                  </div>
                )}
              </div>
              {/* Arquivo sem registro de download = está só em streaming (cache).
                  Botão adota como download: reusa o cache e move ao concluir. */}
              {!tracked ? (
                <button
                  onClick={() => onAdopt(f)}
                  disabled={adopting !== null}
                  title={t('downloads.inspect.adoptFileTitle')}
                  className="flex-shrink-0 inline-flex items-center gap-1 text-[10px] bg-cyan-500/15 hover:bg-cyan-500/25 disabled:opacity-50 text-cyan-700 dark:text-cyan-300 border border-cyan-500/30 px-2 py-1 rounded-md transition-colors"
                >
                  {adopting === f.index ? <Loader2 className="w-3 h-3 animate-spin" /> : <Download className="w-3 h-3" />}
                  {t('downloads.inspect.download')}
                </button>
              ) : pct !== null && (
                done
                  ? <span className="text-[10px] text-emerald-400 flex-shrink-0 inline-flex items-center gap-0.5" title={t('downloads.inspect.fileComplete')}><Check className="w-3 h-3" />{t('downloads.inspect.ok')}</span>
                  : <span className="text-[10px] text-amber-400 tabular-nums flex-shrink-0" title={t('downloads.inspect.fileIncomplete')}>{pct}%</span>
              )}
              {f.size > 0 && <span className="text-xs text-text-muted tabular-nums flex-shrink-0">{formatBytes(f.size)}</span>}
            </li>
          )
        })}
      </ul>
    </div>
  )
}
