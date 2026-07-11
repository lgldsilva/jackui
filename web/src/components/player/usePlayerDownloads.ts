import { useState, useCallback, useRef } from 'react'
import {
  SearchResult,
  TorrentInfo,
  streamFileURL,
  downloadLocalFileDirect,
  classifyCategory,
  isLocalHash,
  downloadCreate,
} from '../../api/client'
import { parentDir, filesUnderDir } from '../../lib/treeSelect'

// Groups the player's download entry points (📁↓ folder, "cache on server",
// local Electron download, auto-enqueue-next, AI category classify) and their
// state. Every UI path opens the unified DownloadModal via `playerDownload`.
export function usePlayerDownloads(deps: {
  info: TorrentInfo | null
  result: SearchResult | null
  selectedFile: number
  notifyError: (err: unknown) => void
}) {
  const { info, result, selectedFile, notifyError } = deps

  // Server-side background download state. Loading/success ficam falsos agora
  // que o botão abre o modal unificado (em vez de baixar direto) — mantidos pra
  // não mexer na interface do PlayerControlsPanel.
  const [serverDownloadLoading] = useState(false)
  const [serverDownloadSuccess] = useState(false)
  // Alvo do modal de download aninhado (destino + seleção); indices pré-seleciona.
  const [playerDownload, setPlayerDownload] = useState<{ result: SearchResult; indices?: number[] } | null>(null)
  // Local (Electron) download with automatic categorization
  const [localDownloadLoading, setLocalDownloadLoading] = useState(false)
  const [overrideCategory, setOverrideCategory] = useState<string | null>(null)
  const [classifyingCat, setClassifyingCat] = useState(false)

  // Callback ref para o auto-download do próximo arquivo: a função acessa
  // buildDownloadResult+info (muda a cada render), então espelhamos pra um ref
  // pra o efeito keyeado por info não precisar do callback como dep.
  const enqueueNextDownloadRef = useRef<(fileIndex: number) => void>(() => {})

  // Constrói o SearchResult pro modal a partir do info/result atuais. null pra
  // arquivos locais (sem magnet — o torrent client não os aceita; usam LocalCacheButton).
  const buildDownloadResult = useCallback((): SearchResult | null => {
    if (!info || isLocalHash(info.infoHash)) return null
    const magnet = result?.magnetUri || `magnet:?xt=urn:btih:${info.infoHash}`
    if (result) return { ...result, magnetUri: magnet, infoHash: info.infoHash, title: result.title || info.name }
    return {
      title: info.name, tracker: '', categoryId: 0, category: '', size: 0, seeders: 0,
      leechers: 0, age: '', magnetUri: magnet, link: '', infoHash: info.infoHash, publishDate: '',
    }
  }, [info, result])

  // "Cache no servidor": abre o modal pré-selecionando o arquivo em reprodução.
  const handleServerDownload = () => {
    const r = buildDownloadResult()
    if (!r) return
    setPlayerDownload({ result: r, indices: selectedFile >= 0 ? [selectedFile] : undefined })
  }

  // 📁↓ por arquivo: baixar a pasta inteira (recursiva) daquele arquivo. Abre o
  // modal com todos os arquivos da pasta pré-selecionados.
  const downloadFolderFromPlayer = useCallback((file: TorrentInfo['files'][number]) => {
    const r = buildDownloadResult()
    if (!r || !info) return
    const dir = parentDir(file.path)
    const indices = dir ? filesUnderDir(info.files, dir).map(f => f.index) : [file.index]
    setPlayerDownload({ result: r, indices })
  }, [buildDownloadResult, info])

  // Auto-enqueue callback: when the current streaming file finishes downloading,
  // enqueue the next file in the in-torrent queue as a background download.
  // Reuses buildDownloadResult for magnet/tracker/name synthesis.
  const enqueueNextDownload = useCallback((fileIndex: number) => {
    const r = buildDownloadResult()
    if (!r || !info) return
    const f = info.files.find(x => x.index === fileIndex)
    if (!f) return
    downloadCreate({
      infoHash: info.infoHash,
      fileIndex: f.index,
      magnet: r.magnetUri,
      name: r.title,
      filePath: f.path,
      fileSize: f.size,
      tracker: r.tracker || undefined,
    }).catch(() => {
      // Best-effort: never block playback or pollute the console.
      // downloadCreate is idempotent — safe to retry on next poll.
    })
  }, [buildDownloadResult, info])
  // Espelha o callback num ref pra evitar stale closure no efeito keyeado por info.
  enqueueNextDownloadRef.current = enqueueNextDownload

  // 📁↓ na linha da pasta (árvore): baixa a pasta inteira, recursivamente. O
  // node.path é o caminho real (mesmo em folders single-child colapsados), então
  // filesUnderDir casa tudo sob ele. Abre o modal com esses arquivos pré-marcados.
  const downloadDirFromPlayer = useCallback((dirPath: string) => {
    const r = buildDownloadResult()
    if (!r || !info) return
    const indices = filesUnderDir(info.files, dirPath).map(f => f.index)
    if (indices.length === 0) return
    setPlayerDownload({ result: r, indices })
  }, [buildDownloadResult, info])

  // 'default' = não forçar categoria (deixa o backend categorizar). O <select>
  // tem uma <option value="default">; mapear o estado nela evita o value órfão
  // (a string crua do Jackett, ex. "Movies/HD", não casa com nenhuma option →
  // warning do React + categoria errada no download).
  const effectiveCategory = overrideCategory ?? 'default'

  const handleLocalDownload = async () => {
    if (!info || selectedFile < 0) return
    setLocalDownloadLoading(true)
    try {
      const file = info.files[selectedFile]
      const name = file.path.split('/').pop() || info.name
      const apiPath = streamFileURL(info.infoHash, selectedFile)
      const categoryArg = effectiveCategory === 'default' ? undefined : effectiveCategory
      await downloadLocalFileDirect(apiPath, name, categoryArg)
    } catch (err) {
      notifyError(err)
    } finally {
      setLocalDownloadLoading(false)
    }
  }

  const handleClassifyCategory = async () => {
    if (!info) return
    setClassifyingCat(true)
    try {
      const res = await classifyCategory(info.name, result?.category ? String(result.category) : undefined)
      if (res.category && res.category !== 'other') {
        setOverrideCategory(res.category)
      }
    } catch { /* silent */ }
    setClassifyingCat(false)
  }

  return {
    serverDownloadLoading,
    serverDownloadSuccess,
    playerDownload,
    setPlayerDownload,
    localDownloadLoading,
    overrideCategory,
    setOverrideCategory,
    classifyingCat,
    effectiveCategory,
    enqueueNextDownloadRef,
    buildDownloadResult,
    handleServerDownload,
    downloadFolderFromPlayer,
    downloadDirFromPlayer,
    handleLocalDownload,
    handleClassifyCategory,
  }
}
