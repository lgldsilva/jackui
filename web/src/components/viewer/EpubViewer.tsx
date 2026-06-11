import { useEffect, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { ChevronLeft, ChevronRight } from 'lucide-react'
import { previewEpubManifest, previewEpubChapterURL } from '../../api/preview'
import { ViewerLoading, ViewerError } from './common'

type EpubViewerProps = {
  readonly infoHash: string
  readonly fileIdx: number
}

// EpubViewer — chapter-by-chapter EPUB reading. The server parses the OPF
// spine, sanitizes each XHTML chapter and serves it under CSP sandbox; here it
// renders inside <iframe sandbox> (NO allow-scripts / allow-same-origin), so
// book content can never touch our origin or the JWT in localStorage.
export default function EpubViewer({ infoHash, fileIdx }: EpubViewerProps) {
  const { t } = useTranslation()
  const [title, setTitle] = useState('')
  const [chapters, setChapters] = useState<string[] | null>(null)
  const [chapter, setChapter] = useState(0)
  const [error, setError] = useState('')

  useEffect(() => {
    let cancelled = false
    setChapters(null)
    setChapter(0)
    setError('')
    previewEpubManifest(infoHash, fileIdx)
      .then(m => {
        if (cancelled) return
        setTitle(m.title)
        setChapters(m.chapters)
        if (m.chapters.length === 0) setError(t('viewer.epub_empty'))
      })
      .catch(e => { if (!cancelled) setError(e?.response?.data?.error || e?.message || t('viewer.load_failed')) })
    return () => { cancelled = true }
  }, [infoHash, fileIdx, t])

  if (error) return <ViewerError message={error} />
  if (!chapters) return <ViewerLoading hint={t('viewer.epub_loading')} />

  const total = chapters.length
  const navigate = (delta: number) => setChapter(c => Math.min(Math.max(c + delta, 0), total - 1))

  return (
    <div className="flex flex-col h-full min-h-[60vh]">
      <iframe
        // sandbox sem allow-scripts/allow-same-origin: capítulo roda em origem
        // opaca, sem JS — o conteúdo do livro é hostil por padrão.
        sandbox=""
        src={previewEpubChapterURL(infoHash, fileIdx, chapters[chapter])}
        title={title || t('viewer.epub_chapter', { n: chapter + 1 })}
        className="w-full flex-1 min-h-[55vh] bg-white"
      />
      <div className="flex items-center justify-center gap-3 py-2 border-t border-default">
        <button onClick={() => navigate(-1)} disabled={chapter === 0} title={t('viewer.prev')} className="p-1.5 rounded hover:bg-surface-tertiary text-text-secondary disabled:opacity-30">
          <ChevronLeft className="w-5 h-5" />
        </button>
        <select
          value={chapter}
          onChange={e => setChapter(Number(e.target.value))}
          aria-label={t('viewer.epub_chapter_select')}
          className="bg-surface border border-default rounded px-2 py-1 text-xs text-text-primary max-w-[50%]"
        >
          {chapters.map((name, i) => (
            <option key={name} value={i}>
              {t('viewer.epub_chapter', { n: i + 1 })} — {name.split('/').at(-1)}
            </option>
          ))}
        </select>
        <span className="text-xs text-text-muted tabular-nums">{chapter + 1} / {total}</span>
        <button onClick={() => navigate(1)} disabled={chapter >= total - 1} title={t('viewer.next')} className="p-1.5 rounded hover:bg-surface-tertiary text-text-secondary disabled:opacity-30">
          <ChevronRight className="w-5 h-5" />
        </button>
      </div>
    </div>
  )
}
