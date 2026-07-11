import { useRef, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { useToast } from '../Toast'
import { SearchResult, streamAdd, streamAddTorrentFile } from '../../api/client'

// useDownloadDragDrop — page-level drag & drop of magnet text / .torrent files.
// Owns the drag-overlay state and returns the handlers to wire onto the section.
// A single dropped torrent/magnet resolves to a SearchResult and opens the
// DownloadModal (via setDownloadTarget); multiple files open AddTorrentModal.
export function useDownloadDragDrop(deps: {
  readonly setLoading: (v: boolean) => void
  readonly setDownloadTarget: (r: SearchResult) => void
  readonly setPreloadFiles: (f: File[]) => void
  readonly setShowAddModal: (v: boolean) => void
}) {
  const { setLoading, setDownloadTarget, setPreloadFiles, setShowAddModal } = deps
  const { notify, notifyError } = useToast()
  const { t } = useTranslation()
  const [isDraggingPage, setIsDraggingPage] = useState(false)
  const dragCounter = useRef(0)

  const handleDragEnter = (e: React.DragEvent) => {
    e.preventDefault()
    e.stopPropagation()
    dragCounter.current++
    if ((e.dataTransfer?.items?.length ?? 0) > 0) {
      setIsDraggingPage(true)
    }
  }

  const handleDragLeave = (e: React.DragEvent) => {
    e.preventDefault()
    e.stopPropagation()
    dragCounter.current--
    if (dragCounter.current === 0) {
      setIsDraggingPage(false)
    }
  }

  const handleDragOver = (e: React.DragEvent) => {
    e.preventDefault()
    e.stopPropagation()
  }

  const handleDrop = async (e: React.DragEvent) => {
    e.preventDefault()
    e.stopPropagation()
    setIsDraggingPage(false)
    dragCounter.current = 0

    // Verifica magnet arrastado como texto
    const textData = e.dataTransfer.getData('text/plain')
    if (textData?.trim().startsWith('magnet:?')) {
      const magnet = textData.trim()
      setLoading(true)
      try {
        const info = await streamAdd(magnet)
        const synthetic: SearchResult = {
          title: info.name,
          tracker: '',
          categoryId: 0,
          category: '',
          size: info.totalSize,
          seeders: info.seeders || 0,
          leechers: info.peers || 0,
          age: '',
          magnetUri: magnet,
          link: '',
          infoHash: info.infoHash,
          publishDate: '',
        }
        setDownloadTarget(synthetic)
      } catch (err: unknown) {
        notifyError(err)
      } finally {
        setLoading(false)
      }
      return
    }

    if ((e.dataTransfer?.files?.length ?? 0) > 0) {
      const files = Array.from(e.dataTransfer.files)
      const torrentFiles = files.filter(f => f.name.endsWith('.torrent'))

      if (torrentFiles.length === 0) {
        notify(t('downloads.page.dropOnlyTorrentOrMagnet'), 'error')
        return
      }

      if (torrentFiles.length === 1) {
        setLoading(true)
        try {
          const info = await streamAddTorrentFile(torrentFiles[0])
          const synthetic: SearchResult = {
            title: info.name,
            tracker: '',
            categoryId: 0,
            category: '',
            size: info.totalSize,
            seeders: info.seeders || 0,
            leechers: info.peers || 0,
            age: '',
            magnetUri: `magnet:?xt=urn:btih:${info.infoHash}`,
            link: '',
            infoHash: info.infoHash,
            publishDate: '',
          }
          setDownloadTarget(synthetic)
        } catch (err: unknown) {
          notifyError(err)
        } finally {
          setLoading(false)
        }
      } else {
        // Múltiplos arquivos
        setPreloadFiles(torrentFiles)
        setShowAddModal(true)
      }
    }
  }

  return { isDraggingPage, handleDragEnter, handleDragLeave, handleDragOver, handleDrop }
}
