import { useState, useEffect, useRef } from 'react'
import { X, Download, Loader2, Clock, Server } from 'lucide-react'
import { SearchResult, DownloadClient, getClients, downloadTorrent, downloadCreate } from '../api/client'
import { useScrollLock } from '../lib/useScrollLock'
import { load, save, pushMRU } from '../lib/storage'

// Sentinel client id for "download inside JackUI itself" (anacrolix → /data),
// as opposed to handing the torrent to an external qBittorrent/Transmission.
const INTERNAL_ID = '__internal__'

// Pull the 40-hex btih out of a magnet URI. The internal download queue keys
// on info hash; search results sometimes only carry the magnet.
function hashFromMagnet(magnet: string): string {
  const m = magnet.match(/btih:([a-fA-F0-9]{40})/i)
  return m ? m[1].toLowerCase() : ''
}

interface DownloadModalProps {
  result: SearchResult | null
  onClose: () => void
}

const KEY_CLIENT = 'lastClientId'
const KEY_PATH = 'lastSavePath'
const KEY_RECENT_PATHS = 'recentSavePaths'

export default function DownloadModal({ result, onClose }: DownloadModalProps) {
  useScrollLock(!!result)
  const [clients, setClients] = useState<DownloadClient[]>([])
  const [selectedClientId, setSelectedClientId] = useState('')
  const [savePath, setSavePath] = useState('')
  const [recentPaths, setRecentPaths] = useState<string[]>([])
  const [loading, setLoading] = useState(false)
  const [error, setError] = useState('')
  const [success, setSuccess] = useState(false)
  const [showRecent, setShowRecent] = useState(false)
  const pathInputRef = useRef<HTMLInputElement>(null)

  useEffect(() => {
    if (!result) return

    setError('')
    setSuccess(false)
    setRecentPaths(load<string[]>(KEY_RECENT_PATHS, []))
    setSavePath(load<string>(KEY_PATH, ''))

    getClients()
      .then((data) => {
        setClients(data)
        // Priority: last-used > default external > internal (always available)
        const lastId = load<string>(KEY_CLIENT, '')
        const lastValid = lastId === INTERNAL_ID || data.some((c) => c.id === lastId)
        const fallback = data.find((c) => c.default)?.id || INTERNAL_ID
        setSelectedClientId(lastValid ? lastId : fallback)
      })
      .catch(() => {
        // Even with no external clients, internal download is always possible.
        setClients([])
        setSelectedClientId(INTERNAL_ID)
      })
  }, [result])

  const handleDownload = async () => {
    if (!result) return

    setLoading(true)
    setError('')

    try {
      if (selectedClientId === INTERNAL_ID) {
        // Download inside JackUI: enqueue on the background worker. It resolves
        // file path/size from metadata; we just need a hash + magnet.
        const magnet = result.magnetUri || (result.infoHash ? `magnet:?xt=urn:btih:${result.infoHash}` : '')
        const infoHash = result.infoHash || hashFromMagnet(magnet)
        if (!infoHash || !magnet) {
          throw new Error('Sem magnet/infoHash — não dá pra baixar internamente')
        }
        await downloadCreate({ infoHash, fileIndex: 0, magnet, name: result.title, filePath: '', fileSize: 0 })
      } else {
        await downloadTorrent(
          selectedClientId,
          result.magnetUri || '',
          result.link || '',
          savePath || undefined,
        )
      }
      // Persist what worked for next time
      save(KEY_CLIENT, selectedClientId)
      if (savePath.trim()) {
        save(KEY_PATH, savePath.trim())
        pushMRU(KEY_RECENT_PATHS, savePath.trim())
      }
      setSuccess(true)
      setTimeout(onClose, 1200)
    } catch (err: unknown) {
      const errorMessage = err instanceof Error ? err.message : 'Erro ao enviar para o cliente'
      setError(errorMessage)
    } finally {
      setLoading(false)
    }
  }

  const pickRecentPath = (p: string) => {
    setSavePath(p)
    setShowRecent(false)
    pathInputRef.current?.focus()
  }

  if (!result) return null

  return (
    <div
      className="fixed inset-0 bg-black/60 backdrop-blur-sm flex items-center justify-center z-50 p-4"
      onClick={(e) => e.target === e.currentTarget && onClose()}
    >
      <div className="bg-gray-800 rounded-2xl border border-gray-700 w-full max-w-md shadow-2xl">
        <div className="flex items-center justify-between p-5 border-b border-gray-700">
          <h2 className="text-lg font-semibold text-gray-100 flex items-center gap-2">
            <Download className="w-5 h-5 text-green-500" />
            Enviar para Download
          </h2>
          <button onClick={onClose} className="text-gray-400 hover:text-gray-200 transition-colors">
            <X className="w-5 h-5" />
          </button>
        </div>

        <div className="p-5 flex flex-col gap-4">
          <div className="bg-gray-900 rounded-lg p-3">
            <p className="text-sm text-gray-300 line-clamp-2">{result.title}</p>
            <p className="text-xs text-gray-500 mt-1">{result.tracker}</p>
          </div>

          <div>
            <label className="block text-sm font-medium text-gray-300 mb-1.5">
              Destino do download
            </label>
            <select
              value={selectedClientId}
              onChange={(e) => setSelectedClientId(e.target.value)}
              className="input-field"
            >
              <option value={INTERNAL_ID}>JackUI (servidor — assistir aqui)</option>
              {clients.map((c) => (
                <option key={c.id} value={c.id}>
                  {c.name} ({c.type})
                </option>
              ))}
            </select>
            {selectedClientId === INTERNAL_ID && (
              <p className="text-[11px] text-gray-500 mt-1 flex items-center gap-1">
                <Server className="w-3 h-3" />
                Baixa no servidor e aparece em Downloads — pronto pra assistir sem re-baixar.
              </p>
            )}
          </div>

          <div className={`relative ${selectedClientId === INTERNAL_ID ? 'hidden' : ''}`}>
            <label className="block text-sm font-medium text-gray-300 mb-1.5">
              Pasta de Destino{' '}
              <span className="text-gray-500 font-normal">(opcional)</span>
            </label>
            <div className="relative">
              <input
                ref={pathInputRef}
                type="text"
                value={savePath}
                onChange={(e) => setSavePath(e.target.value)}
                onFocus={() => setShowRecent(recentPaths.length > 0)}
                onBlur={() => setTimeout(() => setShowRecent(false), 150)}
                placeholder="/downloads/filmes"
                className="input-field pr-10"
              />
              {recentPaths.length > 0 && (
                <button
                  type="button"
                  onMouseDown={(e) => { e.preventDefault(); setShowRecent(s => !s) }}
                  className="absolute right-3 top-1/2 -translate-y-1/2 text-gray-500 hover:text-gray-300"
                  title="Pastas recentes"
                >
                  <Clock className="w-4 h-4" />
                </button>
              )}
            </div>

            {showRecent && recentPaths.length > 0 && (
              <div className="absolute z-10 left-0 right-0 mt-1 bg-gray-900 border border-gray-700 rounded-lg shadow-xl max-h-48 overflow-y-auto">
                {recentPaths.map((p) => (
                  <button
                    key={p}
                    type="button"
                    onMouseDown={(e) => { e.preventDefault(); pickRecentPath(p) }}
                    className="w-full text-left px-3 py-2 text-sm text-gray-300 hover:bg-gray-800 transition-colors truncate"
                    title={p}
                  >
                    {p}
                  </button>
                ))}
              </div>
            )}
          </div>

          {error && (
            <div className="bg-red-500/10 border border-red-500/30 text-red-400 text-sm rounded-lg p-3">
              {error}
            </div>
          )}
          {success && (
            <div className="bg-green-500/10 border border-green-500/30 text-green-400 text-sm rounded-lg p-3">
              Torrent enviado com sucesso!
            </div>
          )}
        </div>

        <div className="flex gap-3 p-5 border-t border-gray-700">
          <button onClick={onClose} className="btn-secondary flex-1">
            Cancelar
          </button>
          <button
            onClick={handleDownload}
            disabled={loading || !selectedClientId || success}
            className="btn-primary flex-1 flex items-center justify-center gap-2 disabled:opacity-50"
          >
            {loading ? <Loader2 className="w-4 h-4 animate-spin" /> : <Download className="w-4 h-4" />}
            Confirmar
          </button>
        </div>
      </div>
    </div>
  )
}
