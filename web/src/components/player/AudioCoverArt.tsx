import { useEffect, useState } from 'react'
import { Volume2 } from 'lucide-react'
import { TorrentInfo, streamArtworkURL, streamArtURL, resolveArt, isLocalHash, parseLocalHash, localAudioCoverURL } from '../../api/client'

// audioCoverURL escolhe a fonte da capa: arquivo LOCAL serve a capa EMBUTIDA (rota
// dedicada, headerless via ?token=); torrent usa a arte extraída por arquivo. Os
// dois respondem 204 quando não há imagem (o <img> onError esconde).
export function audioCoverURL(info: TorrentInfo, selectedFile: number, mediaToken: string): string {
  if (isLocalHash(info.infoHash)) {
    const loc = parseLocalHash(info.infoHash)
    if (loc) return localAudioCoverURL(loc.mount, loc.path, mediaToken || undefined)
  }
  return streamArtworkURL(info.infoHash, selectedFile, mediaToken || undefined)
}

// AudioCoverArt: capa do álbum atrás do player de áudio. Fallback quando o arquivo
// NÃO tem imagem embutida: pra torrents, dispara a cadeia de arte do servidor
// (embedded → TMDB → busca web, music-aware) e mostra o que resolver; arquivos
// locais resolvem o fallback no servidor, então aqui só escondem no miss.
export function AudioCoverArt({ info, selectedFile, mediaToken }: {
  readonly info: TorrentInfo | null
  readonly selectedFile: number
  readonly mediaToken: string
}) {
  const [fallbackSrc, setFallbackSrc] = useState('')
  const [hidden, setHidden] = useState(false)
  useEffect(() => { setFallbackSrc(''); setHidden(false) }, [info?.infoHash, selectedFile])
  if (!info) return null

  const handleError = async () => {
    if (fallbackSrc || isLocalHash(info.infoHash)) { setHidden(true); return }
    const src = await resolveArt(info.infoHash, -1, info.name).catch(() => null)
    if (src) setFallbackSrc(streamArtURL(info.infoHash))
    else setHidden(true)
  }

  const url = fallbackSrc || audioCoverURL(info, selectedFile, mediaToken)
  return (
    <div className="absolute inset-0 flex items-center justify-center bg-gradient-to-br from-gray-800 to-gray-900 pointer-events-none">
      <Volume2 className="absolute w-12 h-12 text-text-muted" />
      {!hidden && (
        <img
          key={url}
          src={url}
          alt=""
          className="relative max-h-full max-w-full object-contain rounded shadow-2xl"
          onError={handleError}
        />
      )}
    </div>
  )
}
