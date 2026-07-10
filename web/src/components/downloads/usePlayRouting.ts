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

  // Roteia play: se file_path está dentro de algum mount navegável → player
  // local (sem tocar no anacrolix); senão → player do torrent (cache em
  // /data/streams ou ainda baixando). Mantém a UX consistente com os outros
  // pontos do app onde clicar em Play "simplesmente toca".
  const onPlay = (d: DownloadEntry) => {
    const fp = d.filePath
    if (!fp) return
    // Item de torrent INTEIRO: file_path é a PASTA do torrent (não um arquivo)
    // e fileIndex é o sentinel — abre o player sem índice pra ele resolver o
    // arquivo principal e listar os demais.
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
      // Mounts user_subpath isolam o download fisicamente em /{username}/ E o
      // backend re-escopa pelo subdir do usuário ao resolver. Removemos o
      // prefixo do username aqui pra não duplicar (espelha StripUserScope).
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
    // Não está num mount navegável → assume cache (anacrolix). Toca via hash
    // do torrent + fileIndex. Funciona pra downloads em curso E pra completos
    // que ainda não foram movidos pra fora do cache.
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
  // rest — the same "ver arquivos + tocar" the whole-torrent download case gets.
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
  // mount (e.g. a cache-only completion) — so the "Abrir no local" button never
  // shows a dead action. Maps file_path → mount + relpath, stripping the per-user
  // subdir like the player does.
  const openLocalFor = (d: DownloadEntry): (() => void) | undefined => {
    const href = localBrowseHref(d.filePath, mounts, user?.username, d.fileIndex === WHOLE_TORRENT_FILE_INDEX)
    return href ? () => navigate(href) : undefined
  }

  return { onPlay, onTorrentPlay, openLocalFor }
}
