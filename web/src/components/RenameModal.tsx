import { useEffect, useRef, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { Pencil, Loader2, AlertCircle } from 'lucide-react'
import { LocalEntry, localRename } from '../api/client'
import { Sheet } from './Sheet'

type Props = {
  readonly mount: string
  readonly entry: LocalEntry | null
  readonly onClose: () => void
  readonly onRenamed: () => void
}

// Renomeia um arquivo/pasta in-place (mesma pasta-pai). O backend valida que o
// nome é "puro" (sem barras nem ".."), recusa colisão e mantém o vínculo do
// torrent. O input pré-seleciona o "stem" (nome sem extensão) pra editar o
// título sem mexer no ".mkv".
export default function RenameModal({ mount, entry, onClose, onRenamed }: Props) {
  const { t } = useTranslation()
  const [name, setName] = useState('')
  const [submitting, setSubmitting] = useState(false)
  const [error, setError] = useState('')
  const inputRef = useRef<HTMLInputElement>(null)

  useEffect(() => {
    if (!entry) return
    setName(entry.name)
    setError('')
    setSubmitting(false)
    // Foca e seleciona só o stem (até a última extensão) num arquivo; numa pasta
    // seleciona tudo. Timeout: o Sheet monta o DOM no próximo tick.
    const t = setTimeout(() => {
      const el = inputRef.current
      if (!el) return
      el.focus()
      const dot = entry.isDir ? -1 : entry.name.lastIndexOf('.')
      if (dot > 0) el.setSelectionRange(0, dot)
      else el.select()
    }, 50)
    return () => clearTimeout(t)
  }, [entry])

  if (!entry) return null

  const trimmed = name.trim()
  const invalid = trimmed === '' || trimmed === '.' || trimmed === '..' || /[/\\]/.test(trimmed)
  const unchanged = trimmed === entry.name
  const canSubmit = !submitting && !invalid && !unchanged

  const handleRename = async () => {
    if (!canSubmit) return
    setSubmitting(true)
    setError('')
    try {
      await localRename(mount, entry.path, trimmed)
      onRenamed()
      onClose()
    } catch (e: any) {
      setError(e?.response?.data?.error || e.message || t('downloads.rename.errorGeneric'))
    } finally {
      setSubmitting(false)
    }
  }

  return (
    <Sheet
      open
      onClose={onClose}
      size="sm"
      title={entry.isDir ? t('downloads.rename.renameFolder') : t('downloads.rename.renameFile')}
      icon={<Pencil className="w-4 h-4 text-amber-400 flex-shrink-0" />}
    >
      <div className="flex flex-col gap-3 pb-1">
        <input
          ref={inputRef}
          type="text"
          value={name}
          onChange={e => setName(e.target.value)}
          onKeyDown={e => { if (e.key === 'Enter') handleRename() }}
          placeholder={t('downloads.rename.placeholder')}
          className="w-full bg-surface-tertiary border border-strong rounded px-3 py-2 text-sm text-text-primary focus:outline-none focus:border-amber-500"
        />

        {invalid && trimmed !== '' && (
          <p className="text-xs text-amber-400">{t('downloads.rename.invalidName')}</p>
        )}

        {error && (
          <p className="text-xs text-red-400 bg-red-500/10 border border-red-500/20 rounded px-2 py-1.5 flex items-center gap-1">
            <AlertCircle className="w-3.5 h-3.5 flex-shrink-0" />{error}
          </p>
        )}

        <div className="flex items-center gap-2 justify-end">
          <button onClick={onClose} disabled={submitting} className="text-sm text-text-secondary hover:text-text-primary px-3 py-1.5 rounded">
            {t('downloads.rename.cancel')}
          </button>
          <button
            onClick={handleRename}
            disabled={!canSubmit}
            className="flex items-center gap-2 text-sm bg-amber-500/20 hover:bg-amber-500/30 disabled:opacity-50 text-amber-700 dark:text-amber-300 border border-amber-500/30 px-4 py-1.5 rounded transition-colors"
          >
            {submitting ? <Loader2 className="w-3.5 h-3.5 animate-spin" /> : <Pencil className="w-3.5 h-3.5" />}
            {t('downloads.rename.submit')}
          </button>
        </div>
      </div>
    </Sheet>
  )
}
