import { useEffect, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { decodeText } from './cp437'
import { isNfoLike } from './viewerKind'
import { ViewerLoading, ViewerError } from './common'

export const TEXT_CAP_BYTES = 256 * 1024 // 256 KiB — beyond this, truncate + download link

type TextViewerProps = {
  readonly url: string
  readonly filePath: string
  readonly fileSize: number
}

// TextViewer — plain-text/code/NFO renderer. Fetches at most TEXT_CAP_BYTES
// via Range (a 50 MB log must not freeze the page), decodes UTF-8 with CP437
// fallback for scene NFOs (DOS box-drawing art), renders in <pre>: content is
// never interpreted, so the extension allowlist can be generous.
export default function TextViewer({ url, filePath, fileSize }: TextViewerProps) {
  const { t } = useTranslation()
  const [text, setText] = useState<string | null>(null)
  const [loading, setLoading] = useState(false)
  const [error, setError] = useState('')

  useEffect(() => {
    let cancelled = false
    setLoading(true)
    setError('')
    setText(null)
    fetch(url, { headers: { Range: `bytes=0-${TEXT_CAP_BYTES - 1}` } })
      .then(async r => {
        if (!r.ok && r.status !== 206) throw new Error(`HTTP ${r.status}`)
        const buf = await r.arrayBuffer()
        if (cancelled) return
        setText(decodeText(buf, isNfoLike(filePath)))
      })
      .catch(e => { if (!cancelled) setError(e?.message || t('viewer.load_failed')) })
      .finally(() => { if (!cancelled) setLoading(false) })
    return () => { cancelled = true }
  }, [url, filePath, t])

  const truncated = fileSize > TEXT_CAP_BYTES
  const nfo = isNfoLike(filePath)
  return (
    <div className="h-full overflow-auto">
      {loading && <ViewerLoading />}
      {error && <ViewerError message={error} />}
      {text !== null && (
        <>
          <pre className={`text-xs text-text-primary font-mono p-4 break-words ${nfo ? 'whitespace-pre leading-[1.15]' : 'whitespace-pre-wrap'}`}>
            {text}
          </pre>
          {truncated && (
            <div className="px-4 pb-4 text-xs text-yellow-400 italic">
              {t('viewer.text_truncated', { kb: (TEXT_CAP_BYTES / 1024).toFixed(0) })}
            </div>
          )}
        </>
      )}
    </div>
  )
}
