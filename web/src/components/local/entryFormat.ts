import type { TFunction } from 'i18next'

const VIDEO_EXTS = new Set(['.mp4', '.m4v', '.mkv', '.avi', '.mov', '.wmv', '.webm', '.flv', '.mpeg', '.mpg', '.ts', '.m2ts'])
const AUDIO_EXTS = new Set(['.mp3', '.m4a', '.aac', '.flac', '.ogg', '.wav', '.opus'])

function extOf(name: string): string {
  const i = name.lastIndexOf('.')
  return i === -1 ? '' : name.slice(i).toLowerCase()
}

export function isVideo(name: string): boolean {
  return VIDEO_EXTS.has(extOf(name))
}

export function isAudio(name: string): boolean {
  return AUDIO_EXTS.has(extOf(name))
}

// formatCount renders a directory's child count ("12 itens" / "1 item").
export function formatCount(n: number, t: TFunction): string {
  return t(n === 1 ? 'local.browser.countItem' : 'local.browser.countItems', { count: n })
}
