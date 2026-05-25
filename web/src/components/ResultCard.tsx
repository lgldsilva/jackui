import { Download, Magnet, Users, TrendingDown, Clock, HardDrive, Tag } from 'lucide-react'
import { SearchResult } from '../api/client'

interface ResultCardProps {
  result: SearchResult
  onDownload: (result: SearchResult) => void
}

function formatSize(bytes: number): string {
  if (bytes === 0) return '0 B'
  const k = 1024
  const sizes = ['B', 'KB', 'MB', 'GB', 'TB']
  const i = Math.floor(Math.log(bytes) / Math.log(k))
  return `${parseFloat((bytes / Math.pow(k, i)).toFixed(2))} ${sizes[i]}`
}

async function copyToClipboard(text: string) {
  try {
    await navigator.clipboard.writeText(text)
  } catch {
    // Fallback
    const el = document.createElement('textarea')
    el.value = text
    document.body.appendChild(el)
    el.select()
    document.execCommand('copy')
    document.body.removeChild(el)
  }
}

export default function ResultCard({ result, onDownload }: ResultCardProps) {
  const handleCopyMagnet = async () => {
    if (result.magnetUri) {
      await copyToClipboard(result.magnetUri)
    }
  }

  const hasMagnet = Boolean(result.magnetUri)
  const hasTorrent = Boolean(result.link)
  const canDownload = hasMagnet || hasTorrent

  return (
    <div className="card flex flex-col gap-3">
      {/* Title */}
      <div className="flex items-start justify-between gap-2">
        <h3
          className="text-sm font-medium text-gray-100 line-clamp-2 flex-1"
          title={result.title}
        >
          {result.title}
        </h3>
        <span className="text-xs bg-green-500/20 text-green-400 border border-green-500/30 px-2 py-0.5 rounded-full whitespace-nowrap flex-shrink-0">
          {result.tracker}
        </span>
      </div>

      {/* Category */}
      {result.category && (
        <div className="flex items-center gap-1 text-xs text-gray-400">
          <Tag className="w-3 h-3" />
          <span>{result.category}</span>
        </div>
      )}

      {/* Stats */}
      <div className="grid grid-cols-2 gap-2 text-xs">
        <div className="flex items-center gap-1 text-gray-400">
          <HardDrive className="w-3.5 h-3.5" />
          <span>{formatSize(result.size)}</span>
        </div>
        <div className="flex items-center gap-1 text-gray-400">
          <Clock className="w-3.5 h-3.5" />
          <span>{result.age}</span>
        </div>
        <div className="flex items-center gap-1 text-green-400">
          <Users className="w-3.5 h-3.5" />
          <span>{result.seeders} seed</span>
        </div>
        <div className="flex items-center gap-1 text-red-400">
          <TrendingDown className="w-3.5 h-3.5" />
          <span>{result.leechers} leech</span>
        </div>
      </div>

      {/* Actions */}
      <div className="flex gap-2 mt-auto pt-1 border-t border-gray-700">
        {hasMagnet && (
          <button
            onClick={handleCopyMagnet}
            title="Copiar link magnet"
            className="flex items-center gap-1.5 text-xs bg-gray-700 hover:bg-gray-600 text-gray-300 px-2.5 py-1.5 rounded-lg transition-colors"
          >
            <Magnet className="w-3.5 h-3.5" />
            Magnet
          </button>
        )}
        {canDownload && (
          <button
            onClick={() => onDownload(result)}
            className="flex items-center gap-1.5 text-xs btn-primary py-1.5 px-2.5 flex-1 justify-center"
          >
            <Download className="w-3.5 h-3.5" />
            Download
          </button>
        )}
      </div>
    </div>
  )
}
