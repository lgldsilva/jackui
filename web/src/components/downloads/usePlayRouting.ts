import { useNavigate } from 'react-router-dom'
import { usePlayer } from '../PlayerProvider'
import { useAuth } from '../../auth/AuthContext'
import { localBrowseHref } from '../../lib/localBrowse'
import { buildLocalHash, DownloadEntry, LocalMount, SearchResult, TorrentInfo, WHOLE_TORRENT_FILE_INDEX } from '../../api/client'

// usePlayRouting — routes a Play click to the right player path (local-mount file
// vs torrent cache) and builds the "abrir no local" navigation. Depends only on
// the loaded mounts; everything else comes from the player/auth/router contexts.
export function usePlayRouting(mounts: readonly LocalMount[]) {
  const navigate = useNavigate()
  const { playSingle } = usePlayer()
  const { user } = useAuth()

  // Routes play: if file_path is inside any browsable mount → local
  // player (without touching anacrolix); otherwise → torrent player (cache in
  // /data/streams or still downloading). Keeps the UX consistent with the other
  // spots in the app where clicking Play "just plays".
  const onPlay = (d: DownloadEntry) => {
    const fp = d.filePath
    if (!fp) return
    // WHOLE torrent item: file_path is the torrent's FOLDER (not a file)
    // and fileIndex is the sentinel — opens the player without an index so it resolves the
    // primary file and lists the rest.
    if (d.fileIndex === WHOLE_TORRENT_FILE_INDEX) {
      const synthetic: SearchResult = {
        title: d.name || fp,
        tracker: '', categoryId: 0, category: '', size: d.fileSize,
        seeders: 0, leechers: 0, age: '',
        magnetUri: d.magnet,
        link: '', infoHash: d.infoHash, publishDate: '',
      }
      playSingle(synthetic)
      return
    }
    const m = mounts.find(mt => fp === mt.path || fp.startsWith(mt.path + '/'))
    if (m) {
      let rel = fp.slice(m.path.length).replaceAll(/^\/+/g, '')
      // user_subpath mounts physically isolate the download under /{username}/ and the
      // backend re-scopes by the user subdir when resolving. We remove the
      // username prefix here to avoid duplicating it (mirrors StripUserScope).
      const uname = user?.username
      if (m.userSubpath && uname && (rel === uname || rel.startsWith(uname + '/'))) {
        rel = rel.slice(uname.length).replaceAll(/^\/+/g, '')
      }
      const hash = buildLocalHash(m.name, rel)
      const synthetic: SearchResult = {
        title: d.name || rel.split('/').pop() || rel,
        tracker: '', categoryId: 0, category: '', size: d.fileSize,
        seeders: 0, leechers: 0, age: '',
        magnetUri: `magnet:?xt=urn:btih:${hash}`,
        link: '', infoHash: hash, publishDate: '',
      }
      playSingle(synthetic, 0)
      return
    }
    // Not in a browsable mount → assume cache (anacrolix). Plays via torrent
    // hash + fileIndex. Works for in-progress downloads AND completed ones
    // that haven't been moved out of the cache yet.
    const synthetic: SearchResult = {
      title: d.name || fp.split('/').pop() || fp,
      tracker: '', categoryId: 0, category: '', size: d.fileSize,
      seeders: 0, leechers: 0, age: '',
      magnetUri: d.magnet,
      link: '', infoHash: d.infoHash, publishDate: '',
    }
    playSingle(synthetic, d.fileIndex)
  }

  // Play a STREAMING torrent card (TorrentInfo, no download row). Opens the player
  // by info_hash WITHOUT a file index, so it resolves the main file and lists the
  // rest — the same "view files + play" the whole-torrent download case gets.
  const onTorrentPlay = (t: TorrentInfo) => {
    const synthetic: SearchResult = {
      title: t.name || t.infoHash,
      tracker: '', categoryId: 0, category: '', size: t.totalSize || 0,
      seeders: 0, leechers: 0, age: '',
      magnetUri: `magnet:?xt=urn:btih:${t.infoHash}`,
      link: '', infoHash: t.infoHash, publishDate: '',
    }
    playSingle(synthetic)
  }

  // Returns a handler that opens this download in the local-files browser (at the
  // folder its file lives in), or undefined when the file isn't under a browsable
  // mount (e.g. a cache-only completion) — so the "Open locally" button never
  // shows a dead action. Maps file_path → mount + relpath, stripping the per-user
  // subdir like the player does.
  const openLocalFor = (d: DownloadEntry): (() => void) | undefined => {
    const href = localBrowseHref(d.filePath, mounts, user?.username, d.fileIndex === WHOLE_TORRENT_FILE_INDEX)
    return href ? () => navigate(href) : undefined
  }

  return { onPlay, onTorrentPlay, openLocalFor }
}
