import { useMemo } from 'react'
import { withToken } from '../../api/http'
import { isLocalHash, parseLocalHash, streamFileURL, type TorrentInfo } from '../../api/client'

// useAudioDirectUrl resolve a URL DIRECT para reprodução de áudio,
// independentemente da origem ser local (rclone/disco) ou torrent.
// NUNCA retorna HLS/transcode — é sempre o endpoint raw que serve bytes com
// Range, igual ao que o audiotest.html usa.
export function useAudioDirectUrl(
  info: TorrentInfo | null,
  selectedFile: number,
  mediaToken: string,
): string {
  return useMemo(() => {
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
  }, [info, selectedFile, mediaToken])
}
