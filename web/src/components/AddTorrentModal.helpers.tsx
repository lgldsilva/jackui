import { Loader2, AlertCircle, CheckCircle2 } from 'lucide-react'
import {
  downloadCreate, downloadBatchCreate, buildBatchFiles, isWholeTorrentSelection, WHOLE_TORRENT_FILE_INDEX,
  downloadTorrent,
  SearchResult, StreamFile
} from '../api/client'
import { formatBytes } from '../lib/format'

export type TorrentItem = {
  readonly id: string
  readonly name: string
  readonly file?: File
  readonly magnet?: string
  readonly infoHash?: string
  readonly loading: boolean
  readonly error?: string
  readonly totalSize?: number
  readonly files?: StreamFile[]
  readonly selectedFiles: Set<number>
  readonly expanded?: boolean
}

export const KEY_CLIENT = 'lastClientId'
export const KEY_PATH = 'lastSavePath'
export const KEY_RECENT_PATHS = 'recentSavePaths'
export const INTERNAL_ID = '__internal__'

// Minimal translate-fn signature so the module-level status renderer can format
// with the component's `t`.
export type TFn = (key: string, opts?: Record<string, unknown>) => string

export function renderItemStatus(item: TorrentItem, t: TFn) {
  if (item.loading) {
    return <span className="text-xs text-text-muted flex items-center gap-1.5">
      <Loader2 className="w-3 h-3 animate-spin text-cyan-400" />
      {t('downloads.addTorrent.fetchingMetadata')}
    </span>
  }
  if (item.error) {
    return <span className="text-xs text-red-400 flex items-center gap-1.5">
      <AlertCircle className="w-3.5 h-3.5" />
      {item.error}
    </span>
  }
  return <span className="text-xs text-text-secondary flex items-center gap-1.5">
    <CheckCircle2 className="w-3.5 h-3.5 text-emerald-400" />
    {formatBytes(item.totalSize || 0)}
    {item.files && <>
      <span className="text-text-muted">•</span>
      <span>{t('downloads.addTorrent.filesSelected', { count: item.files.length, selected: item.selectedFiles.size })}</span>
    </>}
  </span>
}

export async function confirmDownloads( // NOSONAR: complexidade cognitiva rastreada no refactor de god-components (auditoria #417)
  readyItems: TorrentItem[],
  selectedClientId: string,
  savePath: string,
): Promise<number> {
  let successCount = 0
  for (const item of readyItems) {
    const infoHash = item.infoHash
    const magnet = item.magnet || (infoHash ? `magnet:?xt=urn:btih:${infoHash}` : '')
    if (!infoHash || !magnet) continue
    if (selectedClientId === INTERNAL_ID) {
      if ((item.files?.length ?? 0) > 0) {
        const all = item.files ?? []
        const picks = all.filter(f => item.selectedFiles.has(f.index))
        if (isWholeTorrentSelection(all, item.selectedFiles)) {
          // Todos marcados → 1 linha "torrent inteiro" (-2), não N por-arquivo.
          await downloadCreate({ infoHash, fileIndex: WHOLE_TORRENT_FILE_INDEX, magnet, name: item.name, filePath: '', fileSize: all.reduce((s, f) => s + (f.size || 0), 0) })
        } else {
          // Subconjunto → batch numa request (substitui o Promise.all de N POSTs).
          await downloadBatchCreate({ infoHash, magnet, name: item.name, files: buildBatchFiles(picks) })
        }
      } else {
        await downloadCreate({ infoHash, fileIndex: 0, magnet, name: item.name, filePath: '', fileSize: 0 })
      }
    } else {
      await downloadTorrent(selectedClientId, magnet, '', savePath || undefined)
    }
    successCount++
  }
  return successCount
}

export function notifyAdded(readyItems: TorrentItem[], onAdded: (r: SearchResult) => void, onClose: () => void) {
  if (readyItems.length === 1) {
    const first = readyItems[0]
    onAdded({ title: first.name, tracker: '', categoryId: 0, category: '', size: first.totalSize || 0, seeders: 0, leechers: 0, age: '', magnetUri: first.magnet || `magnet:?xt=urn:btih:${first.infoHash}`, link: '', infoHash: first.infoHash || '', publishDate: '' })
  } else {
    setTimeout(() => onClose(), 1200)
  }
}
