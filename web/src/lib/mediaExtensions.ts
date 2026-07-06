/** Shared media extension sets for player, local browser and torrent contents. */

export const VIDEO_EXTENSIONS = new Set([
  'mp4', 'mkv', 'avi', 'mov', 'webm', 'm4v', 'wmv', 'flv', 'ts', 'm2ts', 'vob',
])

export const AUDIO_EXTENSIONS = new Set([
  'mp3', 'flac', 'ogg', 'wav', 'm4a', 'aac', 'opus', 'alac', 'wma',
])

export const VIDEO_EXT_RE = new RegExp(
  String.raw`\.(${[...VIDEO_EXTENSIONS].join('|')})$`,
  'i',
)

export const AUDIO_EXT_RE = new RegExp(
  String.raw`\.(${[...AUDIO_EXTENSIONS].join('|')})$`,
  'i',
)

export function isVideoPath(path: string): boolean {
  const ext = path.split('.').pop()?.toLowerCase() ?? ''
  return VIDEO_EXTENSIONS.has(ext)
}

export function isAudioPath(path: string): boolean {
  const ext = path.split('.').pop()?.toLowerCase() ?? ''
  return AUDIO_EXTENSIONS.has(ext)
}
