import { useEffect, useRef, useState } from 'react'
import { HardDriveDownload, Loader2, Check, AlertCircle } from 'lucide-react'
import { parseLocalHash, localCacheStart, localCacheStatus, LocalCacheStatus } from '../../api/client'

// LocalCacheButton is the "cache mark" + trigger for a local/rclone file in the
// player. For LOCAL files the old "Baixar no Servidor" (torrent) button makes no
// sense — there's no magnet — so this replaces it: it pre-fetches the whole file
// to local disk (queued, with progress) for instant, seekable, EIO-proof
// playback. Idle → "Cachear"; copying → "Cacheando 42%"; ready → "Cacheado".
export function LocalCacheButton({ hash }: { readonly hash: string }) {
  const loc = parseLocalHash(hash)
  const [st, setSt] = useState<LocalCacheStatus | null>(null)
  const pollRef = useRef<ReturnType<typeof globalThis.setInterval> | null>(null)

  const stopPoll = () => {
    if (pollRef.current) {
      globalThis.clearInterval(pollRef.current)
      pollRef.current = null
    }
  }

  const refresh = async () => {
    if (!loc) return
    try {
      const s = await localCacheStatus(loc.mount, loc.path)
      setSt(s)
      // Stop polling once it settles (ready/error/none).
      if (s.status !== 'queued' && s.status !== 'copying') stopPoll()
    } catch { /* transient — keep polling */ }
  }

  // Fetch the initial mark; poll only while a copy is in flight.
  useEffect(() => {
    if (!loc) return
    refresh()
    return stopPoll
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [hash])

  const startPoll = () => {
    stopPoll()
    // Skip the poll fetch while the tab is backgrounded (resumes on return).
    pollRef.current = globalThis.setInterval(() => { if (!document.hidden) refresh() }, 2000)
  }

  const onClick = async () => {
    if (!loc) return
    const status = st?.status
    if (status === 'queued' || status === 'copying' || status === 'ready') return
    try {
      setSt(await localCacheStart(loc.mount, loc.path))
      startPoll()
    } catch { /* surfaced on next poll */ }
  }

  // Hide entirely for files that don't need caching: a bad hash, or a file
  // already on local disk (cacheable=false — only rclone/NFS/CIFS mounts get
  // the button). We wait for the first status fetch before showing anything,
  // so a local file never flashes a pointless "Cachear" button.
  if (!loc || !st?.cacheable) return null

  const status = st?.status ?? 'none'
  const busy = status === 'queued' || status === 'copying'
  const ready = status === 'ready'
  const errored = status === 'error'

  let label = 'Cachear localmente'
  if (status === 'queued') label = 'Na fila…'
  else if (status === 'copying') label = `Cacheando ${st?.percent ?? 0}%`
  else if (ready) label = 'Cacheado'
  else if (errored) label = 'Erro — tentar de novo'

  let icon = <HardDriveDownload className="w-3.5 h-3.5" />
  if (busy) icon = <Loader2 className="w-3.5 h-3.5 animate-spin" />
  else if (ready) icon = <Check className="w-3.5 h-3.5" />
  else if (errored) icon = <AlertCircle className="w-3.5 h-3.5" />

  let cls = 'bg-green-500/20 hover:bg-green-500/30 text-green-700 dark:text-green-300 border-green-500/30'
  if (ready) cls = 'bg-emerald-500/20 text-emerald-400 border-emerald-500/30'
  else if (errored) cls = 'bg-red-500/15 hover:bg-red-500/25 text-red-400 border-red-500/30'
  else if (busy) cls = 'bg-surface-tertiary text-text-secondary border-strong'

  return (
    <button
      onClick={onClick}
      disabled={busy || ready}
      title="Baixar o arquivo inteiro pro disco local (rclone/Drive) — playback instantâneo, com seek e imune a falhas do mount"
      className={`flex items-center gap-1.5 text-xs px-3 py-1.5 rounded-lg transition-colors border ${cls}`}
    >
      {icon}
      <span>{label}</span>
    </button>
  )
}
