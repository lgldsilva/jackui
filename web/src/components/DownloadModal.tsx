import { useState, useEffect } from 'react'
import { X, Download, Loader2 } from 'lucide-react'
import { SearchResult, DownloadClient, getClients, downloadTorrent } from '../api/client'

interface DownloadModalProps {
  result: SearchResult | null
  onClose: () => void
}

export default function DownloadModal({ result, onClose }: DownloadModalProps) {
  const [clients, setClients] = useState<DownloadClient[]>([])
  const [selectedClientId, setSelectedClientId] = useState('')
  const [savePath, setSavePath] = useState('')
  const [loading, setLoading] = useState(false)
  const [error, setError] = useState('')
  const [success, setSuccess] = useState(false)

  useEffect(() => {
    if (result) {
      setError('')
      setSuccess(false)
      getClients()
        .then((data) => {
          setClients(data)
          const defaultClient = data.find((c) => c.default) || data[0]
          if (defaultClient) setSelectedClientId(defaultClient.id)
        })
        .catch(() => setError('Falha ao carregar clientes'))
    }
  }, [result])

  const handleDownload = async () => {
    if (!result) return

    setLoading(true)
    setError('')

    try {
      await downloadTorrent(
        selectedClientId,
        result.magnetUri || '',
        result.link || '',
        savePath || undefined
      )
      setSuccess(true)
      setTimeout(() => {
        onClose()
      }, 1500)
    } catch (err: unknown) {
      const errorMessage = err instanceof Error ? err.message : 'Erro ao enviar para o cliente'
      setError(errorMessage)
    } finally {
      setLoading(false)
    }
  }

  if (!result) return null

  return (
    <div
      className="fixed inset-0 bg-black/60 backdrop-blur-sm flex items-center justify-center z-50 p-4"
      onClick={(e) => e.target === e.currentTarget && onClose()}
    >
      <div className="bg-gray-800 rounded-2xl border border-gray-700 w-full max-w-md shadow-2xl">
        {/* Header */}
        <div className="flex items-center justify-between p-5 border-b border-gray-700">
          <h2 className="text-lg font-semibold text-gray-100 flex items-center gap-2">
            <Download className="w-5 h-5 text-green-500" />
            Enviar para Download
          </h2>
          <button
            onClick={onClose}
            className="text-gray-400 hover:text-gray-200 transition-colors"
          >
            <X className="w-5 h-5" />
          </button>
        </div>

        {/* Content */}
        <div className="p-5 flex flex-col gap-4">
          {/* Torrent title */}
          <div className="bg-gray-900 rounded-lg p-3">
            <p className="text-sm text-gray-300 line-clamp-2">{result.title}</p>
            <p className="text-xs text-gray-500 mt-1">{result.tracker}</p>
          </div>

          {/* Client selector */}
          <div>
            <label className="block text-sm font-medium text-gray-300 mb-1.5">
              Cliente de Download
            </label>
            {clients.length === 0 ? (
              <p className="text-sm text-gray-400">Nenhum cliente configurado</p>
            ) : (
              <select
                value={selectedClientId}
                onChange={(e) => setSelectedClientId(e.target.value)}
                className="input-field"
              >
                {clients.map((c) => (
                  <option key={c.id} value={c.id}>
                    {c.name} ({c.type})
                  </option>
                ))}
              </select>
            )}
          </div>

          {/* Save path */}
          <div>
            <label className="block text-sm font-medium text-gray-300 mb-1.5">
              Pasta de Destino{' '}
              <span className="text-gray-500 font-normal">(opcional)</span>
            </label>
            <input
              type="text"
              value={savePath}
              onChange={(e) => setSavePath(e.target.value)}
              placeholder="/downloads/filmes"
              className="input-field"
            />
          </div>

          {/* Error / Success */}
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

        {/* Footer */}
        <div className="flex gap-3 p-5 border-t border-gray-700">
          <button onClick={onClose} className="btn-secondary flex-1">
            Cancelar
          </button>
          <button
            onClick={handleDownload}
            disabled={loading || !selectedClientId || success}
            className="btn-primary flex-1 flex items-center justify-center gap-2 disabled:opacity-50"
          >
            {loading ? (
              <Loader2 className="w-4 h-4 animate-spin" />
            ) : (
              <Download className="w-4 h-4" />
            )}
            Confirmar
          </button>
        </div>
      </div>
    </div>
  )
}
