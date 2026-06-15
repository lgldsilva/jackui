import { useEffect, useState } from 'react'
import { Server, Loader2, ArrowUp, X } from 'lucide-react'
import { streamTrackers, TrackerScrape } from '../api/client'

type Props = {
  readonly infoHash?: string
  readonly magnet?: string
}

// TrackerStatsList shows the torrent's trackers and the swarm size each one
// reports (BEP 48 scrape). Hosts only — the server masks any passkey. Loads once
// when the info panel opens; kept in its own component so the modal stays lean.
export default function TrackerStatsList({ infoHash, magnet }: Props) {
  const [rows, setRows] = useState<TrackerScrape[] | null>(null)
  const [loading, setLoading] = useState(false)

  useEffect(() => {
    if (!infoHash) return
    let cancelled = false
    setLoading(true)
    setRows(null)
    streamTrackers(infoHash, magnet)
      .then(r => { if (!cancelled) setRows(r) })
      .finally(() => { if (!cancelled) setLoading(false) })
    return () => { cancelled = true }
  }, [infoHash, magnet])

  if (!infoHash) return null

  return (
    <div className="mt-3">
      <div className="text-xs text-text-muted uppercase tracking-wider mb-1">Trackers (seeds reais)</div>
      {loading && (
        <div className="flex items-center gap-2 text-xs text-text-secondary">
          <Loader2 className="w-3.5 h-3.5 animate-spin" /> Consultando trackers…
        </div>
      )}
      {!loading && rows && rows.length === 0 && (
        <div className="text-xs text-text-muted">Nenhum tracker para consultar (magnet/.torrent sem announce).</div>
      )}
      {!loading && rows && rows.length > 0 && (
        <ul className="flex flex-col gap-1">
          {rows.map((t, i) => (
            <li key={`${t.tracker}-${i}`} className="flex items-center gap-2 text-xs">
              <Server className="w-3 h-3 text-text-muted flex-shrink-0" />
              <span className="text-text-secondary truncate flex-1 min-w-0" title={t.tracker}>{t.tracker}</span>
              {t.ok ? (
                <span className="flex items-center gap-2 tabular-nums flex-shrink-0">
                  <span className="flex items-center gap-0.5 text-green-500"><ArrowUp className="w-3 h-3" />{t.seeders}</span>
                  <span className="text-red-400">{t.leechers} leech</span>
                </span>
              ) : (
                <span className="text-text-muted flex items-center gap-1 flex-shrink-0"><X className="w-3 h-3" /> sem resposta</span>
              )}
            </li>
          ))}
        </ul>
      )}
    </div>
  )
}
