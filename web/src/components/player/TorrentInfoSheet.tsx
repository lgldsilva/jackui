import { Loader2, FileVideo, Download, Upload, Users, Activity, Check, Cpu, Info, Hash, Server, Copy } from 'lucide-react'
import { SearchResult, TorrentInfo } from '../../api/client'
import { formatRate } from '../../lib/format'
import { formatSize } from './playerFormat'
import { Sheet } from '../Sheet'
import type { TFn } from './playerTypes'

// Torrent-info overlay opened from the player header. Rendered ABOVE the player
// (z-[60] > the modal's z-50) so it floats over the video. Reads the live `info`
// so swarm stats update while open.
export function renderTorrentInfoModal(props: {
  info: TorrentInfo
  result: SearchResult
  isTranscoded: boolean
  encoderLabel: string
  onClose: () => void
  onCopyHash: () => void
  hashCopied: boolean
  effectiveCategory: string
  setOverrideCategory: (v: string | null) => void
  handleClassifyCategory: () => void
  classifyingCat: boolean
  t: TFn
}) {
  const { info, result, isTranscoded, encoderLabel, onClose, onCopyHash, hashCopied, effectiveCategory, setOverrideCategory, handleClassifyCategory, classifyingCat, t } = props
  const pct = info.progress === undefined ? null : `${(info.progress * 100).toFixed(1)}%`
  const Row = ({ icon, label, children }: { icon?: React.ReactNode; label: string; children: React.ReactNode }) => (
    <div className="flex items-start gap-2 py-1.5 border-b border-default/40 last:border-0">
      <span className="text-text-muted text-xs w-28 flex-shrink-0 flex items-center gap-1.5">{icon}{label}</span>
      <span className="text-text-primary text-sm min-w-0 break-words flex-1">{children}</span>
    </div>
  )
  return (
    <Sheet
      open
      onClose={onClose}
      zClass="z-[60]"
      lockScroll={false}
      size="md"
      title={t('player.modal.torrentInfo')}
      icon={<Info className="w-4 h-4 text-blue-400 flex-shrink-0" />}
    >
      <Row icon={<FileVideo className="w-3.5 h-3.5" />} label={t('player.modal.info.name')}>{info.name || result.title}</Row>
      {info.name && info.name !== result.title && <Row label={t('player.modal.info.release')}>{result.title}</Row>}
      <Row icon={<Download className="w-3.5 h-3.5" />} label={t('player.modal.info.size')}>{formatSize(info.totalSize)} · {info.files.length} {info.files.length === 1 ? t('player.files.file') : t('player.files.files')}</Row>
      <Row icon={<Users className="w-3.5 h-3.5" />} label={t('player.modal.info.seedsPeers')}>{info.seeders ?? 0} / {info.peers ?? 0}</Row>
      {(info.downRate ?? 0) > 0 && <Row icon={<Activity className="w-3.5 h-3.5" />} label={t('player.modal.info.speed')}>{formatRate(info.downRate)}{pct && ` · ${t('player.modal.info.pctDownloaded', { pct })}`}</Row>}
      {(info.bytesDownloaded ?? 0) > 0 && <Row icon={<Download className="w-3.5 h-3.5" />} label={t('player.modal.info.downloaded')}>{formatSize(info.bytesDownloaded ?? 0)}{pct && ` · ${pct}`}</Row>}
      {((info.bytesUploaded ?? 0) > 0 || (info.upRate ?? 0) > 0) && <Row icon={<Upload className="w-3.5 h-3.5" />} label={t('player.modal.info.uploaded')}>{formatSize(info.bytesUploaded ?? 0)}{(info.upRate ?? 0) > 0 ? ` · ${formatRate(info.upRate)}` : ''}</Row>}
      {info.stalled && (info.downRate ?? 0) === 0 && <Row icon={<Loader2 className="w-3.5 h-3.5 animate-spin" />} label={t('player.modal.info.transfer')}>{t('player.modal.info.awaitingData')}</Row>}
      {result.tracker && <Row icon={<Server className="w-3.5 h-3.5" />} label={t('player.modal.info.tracker')}>{result.tracker}</Row>}
      {globalThis.electronAPI ? (
        <Row label={t('player.modal.info.category')}>
          <div className="flex items-center gap-1.5">
            <select
              className="bg-surface-tertiary text-text-primary text-xs rounded px-2 py-1 border border-strong"
              value={effectiveCategory}
              onChange={(e) => setOverrideCategory(e.target.value === 'default' ? null : e.target.value)}
            >
              <option value="default">{result?.category || t('player.modal.category.auto')}</option>
              <option value="movies">{t('player.modal.category.movies')}</option>
              <option value="tv">{t('player.modal.category.tv')}</option>
              <option value="music">{t('player.modal.category.music')}</option>
              <option value="games">{t('player.modal.category.games')}</option>
              <option value="software">{t('player.modal.category.software')}</option>
              <option value="adult">{t('player.modal.category.adult')}</option>
              <option value="books">{t('player.modal.category.books')}</option>
              <option value="other">{t('player.modal.category.other')}</option>
            </select>
            <button
              onClick={handleClassifyCategory}
              disabled={classifyingCat}
              className="text-[10px] bg-indigo-500/20 hover:bg-indigo-500/30 text-indigo-700 dark:text-indigo-300 px-1.5 py-0.5 rounded"
              title={t('player.modal.category.detectAI')}
            >
              {classifyingCat ? '…' : t('player.modal.category.ai')}
            </button>
          </div>
        </Row>
      ) : (
        result.category && <Row label={t('player.modal.info.category')}>{result.category}</Row>
      )}
      {isTranscoded && <Row icon={<Cpu className="w-3.5 h-3.5" />} label={t('player.modal.info.encoder')}>{encoderLabel || 'GPU'}</Row>}
      {info.infoHash && (
        <Row icon={<Hash className="w-3.5 h-3.5" />} label={t('player.modal.info.infoHash')}>
          <span className="flex items-center gap-2 min-w-0">
            <span className="font-mono text-xs truncate min-w-0">{info.infoHash}</span>
            <button onClick={onCopyHash} title={t('player.modal.copy')} className="flex-shrink-0 text-text-muted hover:text-text-primary">
              {hashCopied ? <Check className="w-3.5 h-3.5 text-green-400" /> : <Copy className="w-3.5 h-3.5" />}
            </button>
          </span>
        </Row>
      )}
    </Sheet>
  )
}
