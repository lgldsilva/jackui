import { useMemo } from 'react'
import { withToken } from '../../api/http'
import { isLocalHash, parseLocalHash, streamFileURL, type TorrentInfo } from '../../api/client'

// Pure logic behind the hook, extracted for React-free tests.
export function computeAudioDirectUrl(
  info: TorrentInfo | null,
  selectedFile: number,
  mediaToken: string,
): string {
  if (!info || selectedFile < 0 || !mediaToken) return ''
  const hash = info.infoHash
  if (isLocalHash(hash)) {
    const loc = parseLocalHash(hash)
    if (!loc) return ''
    const params = new URLSearchParams({
      mount: loc.mount,
      path: loc.path,
    })
    return withToken(`/api/local/file?${params.toString()}`, mediaToken)
  }
  return streamFileURL(hash, selectedFile, mediaToken)
}

// useAudioDirectUrl resolves the DIRECT URL for audio playback,
// regardless of the source being local (rclone/disk) or torrent.
// It NEVER returns HLS/transcode — always the raw endpoint serving bytes with
// Range, the same one audiotest.html uses.
export function useAudioDirectUrl(
  info: TorrentInfo | null,
  selectedFile: number,
  mediaToken: string,
): string {
  return useMemo(
    () => computeAudioDirectUrl(info, selectedFile, mediaToken),
    [info, selectedFile, mediaToken],
  )
}
