import { useState, useEffect, useCallback } from 'react'
import { useTranslation } from 'react-i18next'
import { useVisiblePolling } from '../../lib/useVisiblePolling'
import { Loader2, ArrowDown, ArrowUp, Users, Lock } from 'lucide-react'
import { PeerInfo, downloadPeers } from '../../api/downloads'
import { formatRate } from '../../lib/format'

type Props = { readonly downloadId: number }

// Ordena pelos peers mais "interessantes" no topo: quem estamos enviando primeiro
// (importa pra ratio), depois quem nos envia, depois maior disponibilidade.
function sortPeers(peers: PeerInfo[]): PeerInfo[] {
  return [...peers].sort((a, b) =>
    b.upRate - a.upRate || b.downRate - a.downRate || b.availability - a.availability,
  )
}

// PeersTab lista os peers conectados do torrent, com polling de 2s enquanto
// montado (o pai só monta quando a aba está aberta). A lib anacrolix não expõe
// choke/interest, então "enviando"/"recebendo" são INFERIDOS das taxas ao vivo.
export default function PeersTab({ downloadId }: Props) {
  const { t } = useTranslation()
  const [peers, setPeers] = useState<PeerInfo[]>([])
  const [active, setActive] = useState(true)
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState('')

  const load = useCallback(async () => {
    try {
      const d = await downloadPeers(downloadId)
      setPeers(d.peers)
      setActive(d.active)
      setError('')
    } catch (e: unknown) {
      setError((e as Error)?.message || t('downloads.inspect.peers.errorLoading'))
    } finally {
      setLoading(false)
    }
  }, [downloadId, t])

  useEffect(() => { load() }, [load])
  useVisiblePolling(load, 2000)

  if (loading && peers.length === 0) {
    return (
      <div className="flex items-center gap-2 text-text-secondary py-8 justify-center">
        <Loader2 className="w-4 h-4 animate-spin" />
        {t('downloads.inspect.peers.loading')}
      </div>
    )
  }
  if (error) {
    return <p className="text-sm text-red-400 py-6 text-center">{error}</p>
  }
  if (!active) {
    return (
      <p className="text-sm text-text-muted py-6 text-center">
        {t('downloads.inspect.peers.inactive')}
      </p>
    )
  }
  if (peers.length === 0) {
    return (
      <div className="flex flex-col items-center gap-2 text-text-muted py-8">
        <Users className="w-6 h-6 opacity-50" />
        <p className="text-sm">{t('downloads.inspect.peers.noPeers')}</p>
      </div>
    )
  }

  const sorted = sortPeers(peers)
  const sending = sorted.filter((p) => p.sending).length
  const seeders = sorted.filter((p) => p.isSeeder).length

  return (
    <div className="space-y-3">
      <div className="flex items-center gap-3 text-xs text-text-muted">
        <span>{t('downloads.inspect.peers.peersCount', { count: peers.length })}</span>
        <span className="text-emerald-600 dark:text-emerald-400">{t('downloads.inspect.peers.summarySending', { count: sending })}</span>
        <span>{t('downloads.inspect.peers.seedersCount', { count: seeders })}</span>
      </div>
      <div className="overflow-x-auto">
        <table className="w-full text-xs">
          <thead>
            <tr className="text-left text-text-muted border-b border-default">
              <th className="py-1.5 pr-2 font-medium">{t('downloads.inspect.peers.headerPeer')}</th>
              <th className="py-1.5 px-2 font-medium">{t('downloads.inspect.peers.headerAvailable')}</th>
              <th className="py-1.5 px-2 font-medium text-right whitespace-nowrap"><ArrowDown className="w-3 h-3 inline" /></th>
              <th className="py-1.5 px-2 font-medium text-right whitespace-nowrap"><ArrowUp className="w-3 h-3 inline" /></th>
              <th className="py-1.5 pl-2 font-medium">{t('downloads.inspect.peers.headerState')}</th>
            </tr>
          </thead>
          <tbody>
            {sorted.map((p) => (
              <tr key={`${p.addr}-${p.network ?? ''}`} className="border-b border-default/50">
                <td className="py-1.5 pr-2 align-top">
                  <code className="font-mono text-text-primary break-all">{p.addr || '—'}</code>
                  <div className="text-[10px] text-text-muted flex items-center gap-1">
                    {p.client && <span className="truncate max-w-[140px]" title={p.client}>{p.client}</span>}
                    {p.network && <span className="uppercase">{p.network}</span>}
                    {p.encrypted && <Lock className="w-2.5 h-2.5" aria-label={t('downloads.inspect.peers.encrypted')} />}
                  </div>
                </td>
                <td className="py-1.5 px-2 align-top min-w-[90px]">
                  <div className="flex items-center gap-1.5">
                    <div className="flex-1 h-1.5 rounded bg-surface overflow-hidden min-w-[40px]">
                      <div
                        className={p.isSeeder ? 'h-full bg-emerald-500' : 'h-full bg-cyan-500'}
                        style={{ width: `${Math.round(p.availability * 100)}%` }}
                      />
                    </div>
                    <span className="text-text-secondary tabular-nums w-9 text-right">{Math.round(p.availability * 100)}%</span>
                  </div>
                </td>
                <td className="py-1.5 px-2 text-right align-top tabular-nums whitespace-nowrap text-text-secondary">
                  {p.downRate > 0 ? formatRate(p.downRate) : '—'}
                </td>
                <td className="py-1.5 px-2 text-right align-top tabular-nums whitespace-nowrap text-text-secondary">
                  {p.upRate > 0 ? <span className="text-emerald-600 dark:text-emerald-400">{formatRate(p.upRate)}</span> : '—'}
                </td>
                <td className="py-1.5 pl-2 align-top">
                  <div className="flex flex-wrap gap-1">
                    {p.isSeeder && <Badge cls="bg-emerald-500/15 text-emerald-700 dark:text-emerald-300 border-emerald-500/30">{t('downloads.inspect.peers.badgeSeeder')}</Badge>}
                    {p.sending && <Badge cls="bg-cyan-500/15 text-cyan-700 dark:text-cyan-300 border-cyan-500/30">{t('downloads.inspect.peers.badgeSending')}</Badge>}
                    {p.receiving && <Badge cls="bg-blue-500/15 text-blue-700 dark:text-blue-300 border-blue-500/30">{t('downloads.inspect.peers.badgeReceiving')}</Badge>}
                  </div>
                </td>
              </tr>
            ))}
          </tbody>
        </table>
      </div>
      <p className="text-[10px] text-text-muted">
        {t('downloads.inspect.peers.footerNote')}
      </p>
    </div>
  )
}

function Badge({ children, cls }: { readonly children: React.ReactNode; readonly cls: string }) {
  return <span className={`text-[10px] px-1.5 py-0.5 rounded-md border font-medium whitespace-nowrap ${cls}`}>{children}</span>
}
