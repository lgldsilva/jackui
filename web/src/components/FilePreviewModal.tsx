import { useEffect, useState } from 'react'
import { X, FileText, FileImage, FileType2, Download, Loader2, AlertCircle } from 'lucide-react'
import { streamFileURL, withToken } from '../api/client'
import { Sheet } from './Sheet'

/**
 * FilePreviewModal — inline read-only viewer for non-playable files that
 * commonly ship inside torrents: README/NFO/info/log/srt/vtt, PDFs, images.
 *
 * Why this exists: scene releases of movies/series almost always bundle a
 * `.nfo` (release notes), a sample `.txt`, sometimes `.log` from the ripper,
 * and the subtitle `.srt` files. Before this modal the user had two options
 * to read them: download the file locally, or open it as raw bytes in a new
 * tab (browser default behaviour for unknown content-type → "untitled").
 * Both broke the flow of looking at a torrent. Now a click previews inline.
 *
 * Trade-offs:
 *   - Text payload capped at 256 KiB. Log files can be huge (build logs,
 *     debug dumps); rendering 50 MB of text would freeze the page. Beyond
 *     the cap we truncate and show a "Download full" link.
 *   - PDF uses native browser `<iframe>` — Chromium/Safari ship PDF viewers,
 *     this works without bundling pdf.js. Fallback: download.
 *   - Images use `<img>` with `object-contain` so portrait/landscape both fit.
 */

type FilePreviewModalProps = {
  readonly infoHash: string
  readonly fileIdx: number
  readonly filePath: string
  readonly fileSize: number
  readonly onClose: () => void
}

type Kind = 'text' | 'pdf' | 'image' | 'unknown'

// Extension → preview kind. Listed exhaustively rather than via regex so we
// can be confident no playable file ever gets sent to the wrong handler.
const TEXT_EXTS = new Set(['txt', 'srt', 'vtt', 'ass', 'ssa', 'log', 'info', 'nfo', 'md', 'json', 'xml', 'csv', 'cue', 'sfv', 'm3u', 'yaml', 'yml', 'ini', 'conf', 'cfg', 'readme', 'license'])
const IMAGE_EXTS = new Set(['jpg', 'jpeg', 'png', 'gif', 'webp', 'bmp', 'svg', 'ico'])
const PDF_EXTS = new Set(['pdf'])

export function detectPreviewKind(path: string): Kind {
  const lastDot = path.lastIndexOf('.')
  if (lastDot < 0) return 'unknown'
  const ext = path.slice(lastDot + 1).toLowerCase()
  if (TEXT_EXTS.has(ext)) return 'text'
  if (PDF_EXTS.has(ext)) return 'pdf'
  if (IMAGE_EXTS.has(ext)) return 'image'
  // Files named "readme" without extension are common — handle by name too.
  const base = path.slice(path.lastIndexOf('/') + 1).toLowerCase()
  if (base === 'readme' || base === 'license' || base === 'changelog') return 'text'
  return 'unknown'
}

const TEXT_CAP_BYTES = 256 * 1024 // 256 KiB

export default function FilePreviewModal({ infoHash, fileIdx, filePath, fileSize, onClose }: FilePreviewModalProps) {
  const kind = detectPreviewKind(filePath)
  const [text, setText] = useState<string | null>(null)
  const [truncated, setTruncated] = useState(false)
  const [loading, setLoading] = useState(false)
  const [error, setError] = useState('')

  const streamURL = streamFileURL(infoHash, fileIdx)
  // The auth token is in withToken'd URLs already; PDF/image embed needs the
  // tokened URL so the browser can fetch authenticated. streamFileURL returns
  // the tokened version, so reuse directly.

  // Fetch text content (only for text kind). Read up to TEXT_CAP_BYTES via
  // Range header so log files don't blow the heap.
  useEffect(() => {
    if (kind !== 'text') return
    let cancelled = false
    setLoading(true)
    setError('')
    setText(null)
    setTruncated(false)
    fetch(streamURL, { headers: { Range: `bytes=0-${TEXT_CAP_BYTES - 1}` } })
      .then(async r => {
        if (cancelled) return
        if (!r.ok && r.status !== 206) {
          throw new Error(`HTTP ${r.status}`)
        }
        const buf = await r.arrayBuffer()
        // Try UTF-8 first; if the file has BOM or is latin1, fall back.
        const dec = new TextDecoder('utf-8', { fatal: false })
        const content = dec.decode(buf)
        if (cancelled) return
        setText(content)
        setTruncated(fileSize > TEXT_CAP_BYTES)
      })
      .catch(e => { if (!cancelled) setError(e?.message || 'Falha ao carregar') })
      .finally(() => { if (!cancelled) setLoading(false) })
    return () => { cancelled = true }
  // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [infoHash, fileIdx, kind])

  const fileName = filePath.split('/').slice(-1)[0]
  let Icon: typeof FileText
  if (kind === 'pdf') {
    Icon = FileType2
  } else if (kind === 'image') {
    Icon = FileImage
  } else {
    Icon = FileText
  }

  return (
    // hideHeader: o preview é edge-to-edge (texto/PDF/imagem) com uma barra
    // própria que carrega o botão de download + o fechar; o título sozinho não
    // agregava. zClass z-[60] preserva a sobreposição original sobre o player.
    <Sheet
      open
      onClose={onClose}
      size="4xl"
      zClass="z-[60]"
      hideHeader
    >
      <>
        {/* Barra do preview — cola no topo do corpo (compensa o p-4 do Sheet) */}
        <div className="-mx-4 -mt-4 mb-4 flex items-center justify-between p-3 border-b border-gray-700">
          <h3 className="text-sm font-semibold text-gray-100 flex items-center gap-2 min-w-0">
            <Icon className="w-4 h-4 text-blue-400 flex-shrink-0" />
            <span className="truncate" title={filePath}>{fileName}</span>
            <span className="text-xs text-gray-500 flex-shrink-0">
              {fileSize > 0 ? `${(fileSize / 1024).toFixed(1)} KB` : ''}
            </span>
          </h3>
          <div className="flex items-center gap-1 flex-shrink-0">
            <a
              href={withToken(streamURL)}
              download={fileName}
              title="Baixar arquivo completo"
              className="p-1.5 rounded hover:bg-gray-700 text-gray-400 hover:text-gray-200"
            >
              <Download className="w-4 h-4" />
            </a>
            <button
              onClick={onClose}
              className="p-1.5 rounded hover:bg-gray-700 text-gray-400 hover:text-gray-200"
              title="Fechar"
            >
              <X className="w-4 h-4" />
            </button>
          </div>
        </div>

        <div className="-mx-4 -mb-4 min-h-[50vh] bg-gray-900 rounded-b-2xl">
          {kind === 'text' && (
            <div className="h-full overflow-auto">
              {loading && (
                <div className="flex items-center justify-center py-12 text-gray-500">
                  <Loader2 className="w-6 h-6 animate-spin" />
                </div>
              )}
              {error && (
                <div className="p-4 text-sm text-red-400 flex items-center gap-2">
                  <AlertCircle className="w-4 h-4" /> {error}
                </div>
              )}
              {text !== null && (
                <>
                  <pre className="text-xs text-gray-200 font-mono p-4 whitespace-pre-wrap break-words">{text}</pre>
                  {truncated && (
                    <div className="px-4 pb-4 text-xs text-yellow-400 italic">
                      Mostrando os primeiros {(TEXT_CAP_BYTES / 1024).toFixed(0)} KB. Use o botão de download pra arquivo completo.
                    </div>
                  )}
                </>
              )}
            </div>
          )}

          {kind === 'pdf' && (
            // Browsers ship native PDF viewers — let the browser render. iframe is
            // simpler than `<object>` and gets PDF reader chrome (zoom, search,
            // page nav) automatically in Chrome/Edge/Safari.
            <iframe
              src={withToken(streamURL)}
              title={fileName}
              className="w-full h-full min-h-[60vh] bg-white"
            />
          )}

          {kind === 'image' && (
            <div className="flex items-center justify-center p-4 min-h-[50vh]">
              <img
                src={withToken(streamURL)}
                alt={fileName}
                className="max-w-full max-h-[75vh] object-contain"
              />
            </div>
          )}

          {kind === 'unknown' && (
            <div className="p-8 text-center text-gray-500 text-sm">
              Tipo de arquivo sem preview disponível. Use o botão de download.
            </div>
          )}
        </div>
      </>
    </Sheet>
  )
}
