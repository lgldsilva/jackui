import { useEffect, useState } from 'react'
import { Folder, Loader2, Home, ChevronRight, HardDrive } from 'lucide-react'
import { useTranslation } from 'react-i18next'
import { DownloadDestination, downloadDestinations, downloadDestBrowse } from '../api/client'

type Picked = { destBase: string; destSubdir: string }

// DownloadDestinationPicker lets the user choose WHERE a download lands: a
// writable destination (mount / promote dir) plus an optional subfolder browsed
// from the server. Emits { destBase, destSubdir } via onChange. An empty base
// means "use the default download dir" (the server's downloadDir/<user>).
export default function DownloadDestinationPicker({ onChange }: { readonly onChange: (v: Picked) => void }) {
  const { t } = useTranslation()
  const [dests, setDests] = useState<DownloadDestination[]>([])
  const [base, setBase] = useState('') // "" = default
  const [path, setPath] = useState('')
  const [dirs, setDirs] = useState<string[]>([])
  const [newFolder, setNewFolder] = useState('')
  const [loading, setLoading] = useState(false)

  useEffect(() => {
    downloadDestinations().then(setDests).catch(() => {})
  }, [])

  useEffect(() => {
    if (!base) {
      setDirs([])
      return
    }
    setLoading(true)
    downloadDestBrowse(base, path)
      .then((r) => setDirs(r.dirs || []))
      .catch(() => setDirs([]))
      .finally(() => setLoading(false))
  }, [base, path])

  const trimmed = newFolder.trim()
  const finalSub = trimmed ? (path ? `${path}/${trimmed}` : trimmed) : path
  useEffect(() => {
    onChange({ destBase: base, destSubdir: base ? finalSub : '' })
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [base, finalSub])

  const crumbs = path ? path.split('/') : []

  return (
    <div>
      <label htmlFor="dl-dest-base" className="block text-sm font-medium text-text-primary mb-1.5">
        {t('downloads.dest.label')}
      </label>
      <select
        id="dl-dest-base"
        value={base}
        onChange={(e) => { setBase(e.target.value); setPath(''); setNewFolder('') }}
        className="input-field"
      >
        <option value="">{t('downloads.dest.default')}</option>
        {dests.map((d) => (
          <option key={d.path} value={d.path}>{d.name}</option>
        ))}
      </select>

      {base && (
        <div className="mt-2 border border-default rounded-lg bg-surface p-2">
          {/* Breadcrumb */}
          <div className="flex items-center gap-1 text-xs text-text-muted flex-wrap mb-1.5">
            <button type="button" onClick={() => setPath('')} className="flex items-center gap-1 hover:text-text-primary">
              <Home className="w-3.5 h-3.5" />
            </button>
            {crumbs.map((seg, i) => (
              <span key={`${seg}-${i}`} className="flex items-center gap-1">
                <ChevronRight className="w-3 h-3" />
                <button
                  type="button"
                  onClick={() => setPath(crumbs.slice(0, i + 1).join('/'))}
                  className="hover:text-text-primary"
                >
                  {seg}
                </button>
              </span>
            ))}
          </div>

          {/* Subfolder list */}
          {loading ? (
            <div className="flex items-center gap-2 text-xs text-text-muted py-2">
              <Loader2 className="w-3.5 h-3.5 animate-spin" />
              {t('downloads.dest.loading')}
            </div>
          ) : (
            <ul className="max-h-40 overflow-y-auto">
              {dirs.length === 0 && (
                <li className="text-xs text-text-muted px-1 py-1.5 flex items-center gap-1">
                  <HardDrive className="w-3.5 h-3.5" />
                  {t('downloads.dest.empty')}
                </li>
              )}
              {dirs.map((d) => (
                <li key={d}>
                  <button
                    type="button"
                    onClick={() => setPath(path ? `${path}/${d}` : d)}
                    className="w-full flex items-center gap-2 px-1 py-1.5 text-sm text-text-primary hover:bg-surface-secondary rounded truncate"
                  >
                    <Folder className="w-4 h-4 text-amber-400 flex-shrink-0" />
                    <span className="truncate">{d}</span>
                  </button>
                </li>
              ))}
            </ul>
          )}

          {/* New subfolder */}
          <input
            type="text"
            value={newFolder}
            onChange={(e) => setNewFolder(e.target.value)}
            placeholder={t('downloads.dest.newFolder')}
            className="input-field mt-2 text-sm"
          />
          <p className="text-[11px] text-text-muted mt-1.5">
            {t('downloads.dest.target')}: <span className="text-text-secondary">{finalSub || t('downloads.dest.root')}</span>
          </p>
        </div>
      )}
    </div>
  )
}
