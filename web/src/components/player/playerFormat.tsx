import { Loader2, Check, Download } from 'lucide-react'
import { MediaTrack } from '../../api/client'

export function formatSize(bytes: number): string {
  if (bytes === 0 || !bytes) return '0 B'
  const k = 1024
  const sizes = ['B', 'KB', 'MB', 'GB', 'TB']
  const i = Math.floor(Math.log(bytes) / Math.log(k))
  return `${Number.parseFloat((bytes / Math.pow(k, i)).toFixed(2))} ${sizes[i]}`
}

export type FileType = 'all' | 'video' | 'audio' | 'other'
export const PLAYER_AUDIO_RE = /\.(mp3|flac|m4a|aac|ogg|wav|opus|alac|wma)$/i
export const PLAYER_VIDEO_RE = /\.(mp4|mkv|avi|mov|webm|m4v|wmv|flv|ts|m2ts|vob)$/i
// Variable playback speed for audiobooks / lectures.
export const SPEED_OPTIONS = [0.75, 1, 1.25, 1.5, 1.75, 2, 2.5, 3] as const

// canPlayNativeHls: o browser toca HLS (.m3u8) nativo? True no Safari e em
// qualquer browser iOS (WebKit); false no Chrome/Firefox/Edge desktop → precisam do
// hls.js. Cacheado porque não muda durante a sessão.
let _nativeHlsSupport: boolean | null = null
export function canPlayNativeHls(): boolean {
  if (_nativeHlsSupport === null) {
    if (globalThis.window === undefined || globalThis.document === undefined) {
      return false
    }
    try {
      _nativeHlsSupport = globalThis.document.createElement('video').canPlayType('application/vnd.apple.mpegurl') !== ''
    } catch {
      _nativeHlsSupport = false
    }
  }
  return _nativeHlsSupport
}

// shouldUseHlsJs decides whether to play an HLS (.m3u8) stream via hls.js (MSE)
// instead of the browser's native HLS. Desktop (Chrome/FF/Edge) has no native
// HLS → ALWAYS hls.js. WebKit (Safari/iOS) plays HLS natively, EXCEPT in audio
// mode where we force hls.js (when MSE is available) so the decoded audio flows
// through the Web Audio graph → EQ/visualizer work on iOS too. Video on WebKit
// stays native. With no MSE (older iPhones) it falls back to native HLS — sound
// without EQ, no regression. `hlsSupported` is Hls.isSupported(), passed in to
// keep this module free of the hls.js import.
export function shouldUseHlsJs(opts: { isHls: boolean; audioMode: boolean; hlsSupported: boolean; nativeHls?: boolean }): boolean {
  if (!opts.isHls || !opts.hlsSupported) return false
  const native = opts.nativeHls ?? canPlayNativeHls() // override only for tests
  if (!native) return true             // desktop: hls.js is the only HLS path
  return opts.audioMode                // WebKit: force hls.js only in audio mode
}

// fileType buckets a file for the sidebar type filter: video (backend flag or
// extension) → audio (extension) → everything else.
export function fileType(f: { isVideo?: boolean; path: string }): Exclude<FileType, 'all'> {
  if (f.isVideo || PLAYER_VIDEO_RE.test(f.path)) return 'video'
  if (PLAYER_AUDIO_RE.test(f.path)) return 'audio'
  return 'other'
}

export const FILE_EXTRA_RE = (() => {
  const SPACE_OR_DASH = String.raw`[\s-]?`
  return new RegExp(String.raw`\b(featurettes?|extras?|bonus|behind${SPACE_OR_DASH}the${SPACE_OR_DASH}scenes|deleted${SPACE_OR_DASH}scenes|making${SPACE_OR_DASH}of|samples?|trailers?|interviews?|gag${SPACE_OR_DASH}reel|outtakes?)\b`, 'i')
})()
export const FILE_AUDIO_RE = /\.(mp3|flac|m4a|aac|ogg|wav|opus|alac|wma)$/i

// parseEpisodeTag extracts a normalised SxxEyy label from a filename, or null.
// Single source for the player + sidebar so both agree on what an "episode" is.
export function parseEpisodeTag(path: string): string | null {
  const m = /[Ss](\d{1,2})[ ._-]?[Ee](\d{1,3})/.exec(path)
  if (m) return `S${m[1].padStart(2, '0')}E${m[2].padStart(2, '0')}`
  return null
}

export type SortableFile = { index: number; path: string; size: number; isVideo?: boolean }

export type FileDisplayOpts = {
  filter: string
  typeFilter: FileType
  sortBySize: boolean
  sizeDesc: boolean
}

// filterAndSortFiles is THE display order of the player's file list. The
// sidebar renders it and the prev/next media queue follows it — keeping them
// on the same function is what makes the next button agree with the list the
// user is looking at (torrents rarely store episodes in file order).
export function filterAndSortFiles<T extends SortableFile>(files: readonly T[], opts: FileDisplayOpts): T[] {
  const filterLower = opts.filter.trim().toLowerCase()
  const matches = (f: T) =>
    !filterLower ||
    f.path.toLowerCase().includes(filterLower) ||
    (parseEpisodeTag(f.path) || '').toLowerCase().includes(filterLower)
  return files
    .filter(f => matches(f))
    .filter(f => opts.typeFilter === 'all' || fileType(f) === opts.typeFilter)
    .slice()
    .sort((a, b) => {
      if (opts.sortBySize) {
        if (a.size !== b.size) return opts.sizeDesc ? b.size - a.size : a.size - b.size
        return a.index - b.index
      }
      const ax = FILE_EXTRA_RE.test(a.path), bx = FILE_EXTRA_RE.test(b.path)
      if (ax !== bx) return ax ? 1 : -1
      const ae = parseEpisodeTag(a.path), be = parseEpisodeTag(b.path)
      if (ae && be) return ae.localeCompare(be)
      if (ae) return -1
      if (be) return 1
      return a.index - b.index
    })
}

export function audioTrackTitle(a: MediaTrack): string {
  let t = a.title || a.codec
  if (a.channels) t += ` (${a.channels}ch)`
  return `${t} — clicar transcoda via FFmpeg, perde seek`
}

export function subBtnClass(active: boolean, image: boolean | undefined): string {
  if (active) {
    return image
      ? 'bg-orange-500/20 text-orange-700 dark:text-orange-300 border-orange-500/30'
      : 'bg-emerald-500/20 text-emerald-700 dark:text-emerald-300 border-emerald-500/30'
  }
  return 'bg-surface-tertiary/40 text-text-secondary border-default hover:text-text-primary'
}

// Slim time readout shown below the cover art when an audio track plays in the
// minimized (PiP) card, so the user knows where they are without expanding.
export function subtitleButtonTitle(enabled: boolean, source: string | null): string {
  if (!enabled) return 'Configure OpenSubtitles API key em Settings'
  if (source === 'embedded') return 'Legenda embutida no arquivo (sync perfeito)'
  if (source === 'hash') return 'Legenda casada por hash do arquivo (frame-exato)'
  if (source === 'title') return 'Legenda encontrada pelo título'
  return 'Buscar legendas em português'
}

export function subtitleBtnClass(active: string | null, embedded: number | null, source: string | null, enabled: boolean): string {
  if (active || embedded !== null) {
    if (source === 'embedded' || source === 'hash') return 'bg-emerald-500/20 text-emerald-400 border-emerald-500/30'
    return 'bg-green-500/20 text-green-400 border-green-500/30'
  }
  if (enabled) return 'bg-blue-500/20 hover:bg-blue-500/30 text-blue-700 dark:text-blue-300 border-blue-500/30'
  return 'bg-surface-tertiary/50 text-text-muted border-default cursor-not-allowed opacity-50'
}

export function serverDownloadIcon(loading: boolean, success: boolean): React.ReactNode {
  if (loading) return <Loader2 className="w-3.5 h-3.5 animate-spin" />
  if (success) return <Check className="w-3.5 h-3.5" />
  return <Download className="w-3.5 h-3.5 text-green-400" />
}

export function getSubtitleLabel(embeddedSub: number | null, subActive: string | null, autoSource: string | null, subLoading: boolean): string {
  if (embeddedSub !== null) return 'Legenda embutida'
  if (subActive) return autoSource === 'hash' ? 'Legenda ✓ hash' : 'Legenda ativa'
  if (subLoading) return 'Buscando...'
  return 'Legendas'
}
