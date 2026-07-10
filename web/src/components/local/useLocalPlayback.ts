import { useCallback } from 'react'
import {
  LocalEntry,
  SearchResult,
  PlaylistItem,
  buildLocalHash,
  localPlayBatch,
} from '../../api/client'
import { usePlayer } from '../PlayerProvider'
import { isVideo, isAudio } from './entryFormat'
import { isViewable } from '../viewer/viewerKind'

// Clique numa entrada: pasta → navega; arquivo não-reproduzível mas visualizável
// → viewer universal; arquivo reproduzível → player (com playlist implícita dos
// irmãos da mesma modalidade na pasta).
export function useLocalPlayback(
  activeMount: string,
  path: string,
  visible: LocalEntry[],
  updateNavigation: (newMount: string, newPath: string, replace?: boolean) => void,
  setPreviewEntry: React.Dispatch<React.SetStateAction<LocalEntry | null>>,
) {
  const { playSingle, playPlaylist } = usePlayer()

  const handleEntryClick = useCallback((e: LocalEntry) => {
    if (e.isDir) {
      updateNavigation(activeMount, e.path)
      return
    }
    if (!activeMount) return
    // Não-reproduzível mas visualizável (NFO/imagem/PDF/CBZ/zip/EPUB) → abre o
    // viewer universal em vez de ser um clique morto.
    if (!e.isPlayable) {
      if (isViewable(e.name)) setPreviewEntry(e)
      return
    }
    // Routes the file through the main PlayerProvider/PlayerModal via a
    // synthetic SearchResult com pseudo-hash `local-...` (mount+path codificados).
    // Resultado: o player completo abre — legendas embedded, sidecar .srt/.vtt,
    // OpenSubtitles auto, escolha persistida, tudo. As funções do client (streamProbe,
    // streamSidecars, subtitlesAuto, etc.) detectam o prefixo e roteiam pra
    // /api/local/* sem mudar PlayerModal.
    //
    // Os irmãos playable da MESMA modalidade (vídeo↔vídeo, áudio↔áudio), na ordem
    // exibida (`visible`), viram uma playlist implícita — assim ⏮⏭ navegam entre
    // os episódios/faixas da pasta. Cada arquivo local mantém seu próprio
    // pseudo-hash (que o player já toca sozinho); sem isso o player recebia só 1
    // arquivo e os botões de próximo/anterior ficavam inertes.
    const clickedIsVideo = isVideo(e.name)
    const siblings = visible.filter(
      (x) => !x.isDir && x.isPlayable && (clickedIsVideo ? isVideo(x.name) : isAudio(x.name)),
    )
    if (siblings.length > 1) {
      const items: PlaylistItem[] = siblings.map((x, pos) => {
        const h = buildLocalHash(activeMount, x.path)
        return {
          id: pos, playlistId: 0, position: pos, title: x.name,
          magnet: `magnet:?xt=urn:btih:${h}`, infoHash: h, fileIndex: 0, addedAt: '',
        }
      })
      const start = Math.max(0, siblings.findIndex((x) => x.path === e.path))
      // Pre-warm the resolution (direct-vs-HLS + URL) of EVERY track in the folder
      // in ONE batch call, instead of one GET /api/local/play (ffprobe) per track
      // when the player navigates/auto-advances. Best-effort (never blocks play).
      void localPlayBatch(activeMount, siblings.map((x) => x.path)).catch(() => {})
      const folderName = path ? path.split('/').pop() || path : activeMount
      // expand=true: arquivos locais abrem o player MAXIMIZADO (não o dock de
      // áudio minimizado) — o usuário clicou pra ver/ouvir a experiência cheia.
      playPlaylist(folderName, items, start, true)
      return
    }
    const hash = buildLocalHash(activeMount, e.path)
    const synthetic: SearchResult = {
      title: e.name,
      tracker: '',
      categoryId: 0,
      category: '',
      size: e.size,
      seeders: 0,
      leechers: 0,
      age: '',
      magnetUri: `magnet:?xt=urn:btih:${hash}`,
      link: '',
      infoHash: hash,
      publishDate: '',
    }
    playSingle(synthetic, 0, undefined, true)
  }, [activeMount, path, visible, playSingle, playPlaylist, updateNavigation, setPreviewEntry])

  return handleEntryClick
}
