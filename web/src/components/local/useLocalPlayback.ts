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

// Click on an entry: folder → navigate; non-playable but viewable file
// → universal viewer; playable file → player (with an implicit playlist of the
// same-kind siblings in the folder).
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
    // Non-playable but viewable (NFO/image/PDF/CBZ/zip/EPUB) → opens the
    // universal viewer instead of being a dead click.
    if (!e.isPlayable) {
      if (isViewable(e.name)) setPreviewEntry(e)
      return
    }
    // Routes the file through the main PlayerProvider/PlayerModal via a
    // synthetic SearchResult with pseudo-hash `local-...` (mount+path encoded).
    // Result: the full player opens — embedded subtitles, sidecar .srt/.vtt,
    // OpenSubtitles auto, persisted choice, everything. The client functions (streamProbe,
    // streamSidecars, subtitlesAuto, etc.) detect the prefix and route to
    // /api/local/* without changing PlayerModal.
    //
    // The playable siblings of the SAME kind (video↔video, audio↔audio), in the
    // displayed order (`visible`), become an implicit playlist — so ⏮⏭ navigate between
    // the folder's episodes/tracks. Each local file keeps its own
    // pseudo-hash (which the player already plays by itself); without this the player got only 1
    // file and the next/previous buttons stayed inert.
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
      // expand=true: local files open the player MAXIMIZED (not the minimized audio
      // dock) — the user clicked to see/hear the full experience.
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
