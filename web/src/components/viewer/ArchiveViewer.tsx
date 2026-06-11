import { useEffect, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { ArrowLeft, FileText, FileImage, File as FileIcon, Eye } from 'lucide-react'
import { previewArchiveList, previewArchiveEntryURL, type ArchiveEntry } from '../../api/preview'
import { detectViewerKind, isNfoLike } from './viewerKind'
import { decodeText } from './cp437'
import { ViewerLoading, ViewerError, formatBytes } from './common'

const INNER_TEXT_CAP = 256 * 1024 // display cap; the server already caps decompressed bytes

type ArchiveViewerProps = {
  readonly infoHash: string
  readonly fileIdx: number
}

type InnerPreview = { name: string; kind: 'text' | 'image' }

// innerKind: what can we preview from INSIDE an archive? text + images only —
// matching the backend allowlist (no nested archives, no pdf-from-zip).
function innerKind(name: string): InnerPreview['kind'] | null {
  const kind = detectViewerKind(name)
  if (kind === 'text') return 'text'
  if (kind === 'image') return 'image'
  return null
}

function entryIcon(name: string) {
  const kind = innerKind(name)
  if (kind === 'text') return <FileText className="w-4 h-4 text-blue-400 flex-shrink-0" />
  if (kind === 'image') return <FileImage className="w-4 h-4 text-purple-400 flex-shrink-0" />
  return <FileIcon className="w-4 h-4 text-text-muted flex-shrink-0" />
}

// ArchiveViewer — lists zip/tar/tar.gz/rar contents (name + size) and previews
// one inner text/image at a time. Listing of a torrent-hosted archive works
// before the download finishes (zip central directory reads only what it needs).
export default function ArchiveViewer({ infoHash, fileIdx }: ArchiveViewerProps) {
  const { t } = useTranslation()
  const [entries, setEntries] = useState<ArchiveEntry[] | null>(null)
  const [truncated, setTruncated] = useState(false)
  const [error, setError] = useState('')
  const [inner, setInner] = useState<InnerPreview | null>(null)

  useEffect(() => {
    let cancelled = false
    setEntries(null)
    setInner(null)
    setError('')
    previewArchiveList(infoHash, fileIdx)
      .then(l => {
        if (cancelled) return
        setEntries(l.entries)
        setTruncated(l.truncated)
      })
      .catch(e => { if (!cancelled) setError(e?.response?.data?.error || e?.message || t('viewer.load_failed')) })
    return () => { cancelled = true }
  }, [infoHash, fileIdx, t])

  if (error) return <ViewerError message={error} />
  if (!entries) return <ViewerLoading hint={t('viewer.archive_loading')} />

  if (inner) {
    return (
      <div className="flex flex-col h-full min-h-[50vh]">
        <button
          onClick={() => setInner(null)}
          className="flex items-center gap-2 px-4 py-2 text-xs text-text-secondary hover:text-text-primary border-b border-default text-left"
        >
          <ArrowLeft className="w-3.5 h-3.5" />
          <span className="truncate">{inner.name}</span>
        </button>
        <InnerEntryView infoHash={infoHash} fileIdx={fileIdx} entry={inner} />
      </div>
    )
  }

  return (
    <div className="h-full overflow-auto min-h-[50vh]">
      {entries.length === 0 && (
        <p className="p-6 text-sm text-text-muted text-center">{t('viewer.archive_empty')}</p>
      )}
      <ul className="divide-y divide-default/50">
        {entries.map(e => {
          const kind = innerKind(e.name)
          return (
            <li key={e.name}>
              <button
                onClick={() => kind && setInner({ name: e.name, kind })}
                disabled={!kind}
                title={kind ? t('viewer.preview_inline') : undefined}
                className={`w-full flex items-center gap-2 px-4 py-2 text-left text-xs ${
                  kind ? 'hover:bg-surface-tertiary/50 text-text-primary cursor-pointer' : 'text-text-muted cursor-default'
                }`}
              >
                {entryIcon(e.name)}
                <span className="truncate flex-1 min-w-0">{e.name}</span>
                {kind && <Eye className="w-3.5 h-3.5 text-blue-400 flex-shrink-0" />}
                <span className="text-text-muted tabular-nums flex-shrink-0">{formatBytes(e.size)}</span>
              </button>
            </li>
          )
        })}
      </ul>
      {truncated && (
        <p className="px-4 py-3 text-xs text-yellow-400 italic">{t('viewer.archive_truncated')}</p>
      )}
    </div>
  )
}

// InnerEntryView fetches and renders one entry from inside the archive.
function InnerEntryView({ infoHash, fileIdx, entry }: {
  readonly infoHash: string
  readonly fileIdx: number
  readonly entry: InnerPreview
}) {
  const { t } = useTranslation()
  const [text, setText] = useState<string | null>(null)
  const [error, setError] = useState('')
  const [loading, setLoading] = useState(entry.kind === 'text')
  const url = previewArchiveEntryURL(infoHash, fileIdx, entry.name)

  useEffect(() => {
    if (entry.kind !== 'text') return
    let cancelled = false
    setLoading(true)
    setText(null)
    setError('')
    fetch(url)
      .then(async r => {
        if (!r.ok) {
          const body = await r.json().catch(() => null)
          throw new Error(body?.error || `HTTP ${r.status}`)
        }
        const buf = await r.arrayBuffer()
        if (cancelled) return
        const full = decodeText(buf, isNfoLike(entry.name))
        setText(full.length > INNER_TEXT_CAP ? full.slice(0, INNER_TEXT_CAP) : full)
      })
      .catch(e => { if (!cancelled) setError(e?.message || t('viewer.load_failed')) })
      .finally(() => { if (!cancelled) setLoading(false) })
    return () => { cancelled = true }
  }, [url, entry, t])

  if (entry.kind === 'image') {
    return (
      <div className="flex-1 flex items-center justify-center p-4 overflow-auto">
        <img src={url} alt={entry.name} className="max-w-full max-h-[65vh] object-contain" onError={() => setError(t('viewer.load_failed'))} />
        {error && <ViewerError message={error} />}
      </div>
    )
  }
  return (
    <div className="flex-1 overflow-auto">
      {loading && <ViewerLoading />}
      {error && <ViewerError message={error} />}
      {text !== null && (
        <pre className={`text-xs text-text-primary font-mono p-4 break-words ${isNfoLike(entry.name) ? 'whitespace-pre leading-[1.15]' : 'whitespace-pre-wrap'}`}>{text}</pre>
      )}
    </div>
  )
}
