import { useState, useEffect } from 'react'
import { useTranslation } from 'react-i18next'
import { Cpu, RefreshCw, Loader2, Check, X, Zap } from 'lucide-react'
import { transcodeCapabilities, TranscodeCapabilities, TranscodeEncoder } from '../api/client'
import { errMessage } from '../lib/errMessage'

const BACKEND_LABELS: Record<string, string> = {
  'nvidia':    'NVIDIA NVENC',
  'amd-vaapi': 'AMD/Intel VAAPI',
  'amd-amf':   'AMD AMF',
  'intel-qsv': 'Intel QuickSync',
  'apple-vt':  'Apple VideoToolbox',
  'cpu':       'CPU (libx264/x265)',
}

const BACKEND_COLORS: Record<string, string> = {
  'nvidia':    'text-green-400 border-green-500/30 bg-green-500/10',
  'amd-vaapi': 'text-red-400 border-red-500/30 bg-red-500/10',
  'amd-amf':   'text-red-400 border-red-500/30 bg-red-500/10',
  'intel-qsv': 'text-blue-400 border-blue-500/30 bg-blue-500/10',
  'apple-vt':  'text-text-primary border-strong/30 bg-gray-500/10',
  'cpu':       'text-yellow-400 border-yellow-500/30 bg-yellow-500/10',
}

export default function TranscodeCapabilitiesCard() {
  const { t } = useTranslation()
  const [caps, setCaps] = useState<TranscodeCapabilities | null>(null)
  const [loading, setLoading] = useState(true)
  const [refreshing, setRefreshing] = useState(false)
  const [error, setError] = useState('')

  const load = async (force = false) => {
    if (force) setRefreshing(true)
    else setLoading(true)
    setError('')
    try {
      const data = await transcodeCapabilities(force)
      setCaps(data)
    } catch (e: unknown) {
      setError(errMessage(e))
    } finally {
      setLoading(false)
      setRefreshing(false)
    }
  }

  useEffect(() => { load(false) }, [])

  if (loading && !caps) {
    return (
      <div className="card flex items-center gap-3 text-text-secondary">
        <Loader2 className="w-4 h-4 animate-spin" />
        Probing transcoder capabilities...
      </div>
    )
  }

  if (error) {
    return <div className="card text-red-400 text-sm">{t('transcode.caps_error', { error })}</div>
  }

  if (!caps) return null

  // Group encoders by backend for readable layout
  const byBackend: Record<string, TranscodeEncoder[]> = {}
  caps.encoders.forEach(e => {
    if (!byBackend[e.backend]) byBackend[e.backend] = []
    byBackend[e.backend].push(e)
  })

  // Order backends: functional first, then alphabetic
  const sortedBackends = Object.keys(byBackend).sort((a, b) => {
    const aFunc = byBackend[a].some(e => e.functional) ? 0 : 1
    const bFunc = byBackend[b].some(e => e.functional) ? 0 : 1
    if (aFunc !== bFunc) return aFunc - bFunc
    return a.localeCompare(b)
  })

  return (
    <div className="card flex flex-col gap-4">
      {/* Header */}
      <div className="flex items-center justify-between">
        <div className="flex items-center gap-2">
          <Cpu className="w-5 h-5 text-green-500" />
          <h2 className="text-lg font-semibold text-text-primary">Hardware Transcoding</h2>
        </div>
        <button
          onClick={() => load(true)}
          disabled={refreshing}
          title={t('transcode.reprobe_title')}
          className="text-text-secondary hover:text-text-primary disabled:opacity-50 transition-colors"
        >
          <RefreshCw className={`w-4 h-4 ${refreshing ? 'animate-spin' : ''}`} />
        </button>
      </div>

      {/* Preferred summary */}
      <div className="bg-surface rounded-lg p-3 flex flex-col gap-1.5 text-sm">
        <div className="flex justify-between items-baseline">
          <span className="text-text-muted text-xs">{t('transcode.active_encoder_h264')}</span>
          <span className="text-green-400 font-mono">{caps.preferred || t('transcode.none')}</span>
        </div>
        <div className="flex justify-between items-baseline">
          <span className="text-text-muted text-xs">{t('transcode.active_encoder_hevc')}</span>
          <span className="text-green-400 font-mono">{caps.preferredHevc || t('transcode.none')}</span>
        </div>
        <div className="flex justify-between items-baseline">
          <span className="text-text-muted text-xs">FFmpeg:</span>
          <span className="text-text-secondary text-xs font-mono truncate ml-2" title={caps.ffmpegVersion}>
            {caps.ffmpegVersion.split(' ').slice(0, 3).join(' ')}
          </span>
        </div>
        <div className="flex gap-2 mt-1 flex-wrap">
          {caps.hasNvidia && <span className="text-[10px] bg-green-500/20 text-green-700 dark:text-green-300 border border-green-500/30 px-1.5 py-0.5 rounded">NVIDIA</span>}
          {caps.hasVaapi && <span className="text-[10px] bg-red-500/20 text-red-700 dark:text-red-300 border border-red-500/30 px-1.5 py-0.5 rounded">VAAPI</span>}
          {caps.hasQsv && <span className="text-[10px] bg-blue-500/20 text-blue-700 dark:text-blue-300 border border-blue-500/30 px-1.5 py-0.5 rounded">QSV</span>}
          {!caps.hasNvidia && !caps.hasVaapi && !caps.hasQsv && (
            <span className="text-[10px] bg-yellow-500/20 text-yellow-700 dark:text-yellow-300 border border-yellow-500/30 px-1.5 py-0.5 rounded">CPU-only</span>
          )}
        </div>
      </div>

      {/* Encoders grouped by backend */}
      <div className="flex flex-col gap-2">
        {sortedBackends.map(backend => {
          const encs = byBackend[backend]
          const anyFunctional = encs.some(e => e.functional)
          return (
            <div
              key={backend}
              className={`rounded-lg border px-3 py-2 ${anyFunctional ? BACKEND_COLORS[backend] || 'border-default bg-surface/50' : 'border-default bg-surface/30 opacity-60'}`}
            >
              <p className="text-xs font-medium mb-1.5">{BACKEND_LABELS[backend] || backend}</p>
              <div className="flex flex-col gap-1">
                {encs.map(e => {
                  let statusIcon: React.ReactNode
                  if (e.functional) {
                    statusIcon = <Check className="w-3 h-3 text-green-400 flex-shrink-0" />
                  } else if (e.available) {
                    statusIcon = <X className="w-3 h-3 text-red-400 flex-shrink-0" />
                  } else {
                    statusIcon = <span className="w-3 h-3 inline-block flex-shrink-0 text-text-muted">·</span>
                  }
                  return (
                  <div key={e.id} className="flex items-center justify-between gap-2 text-xs">
                    <div className="flex items-center gap-1.5 min-w-0">
                      {statusIcon}
                      <code className="text-text-primary truncate">{e.id}</code>
                    </div>
                    <div className="flex items-center gap-2 flex-shrink-0">
                      {e.benchFps && (
                        <span className="flex items-center gap-0.5 text-text-muted text-[10px]">
                          <Zap className="w-2.5 h-2.5" />
                          {e.benchFps.toFixed(0)} fps
                        </span>
                      )}
                      {e.error && (
                        <span className="text-[10px] text-red-400/70 truncate max-w-[140px]" title={e.error}>
                          {e.error.split('\n')[0].slice(0, 30)}
                        </span>
                      )}
                    </div>
                  </div>
                )})}
              </div>
            </div>
          )
        })}
      </div>

      <p className="text-xs text-text-muted">
        {t('transcode.probed_note', { date: new Date(caps.probedAt).toLocaleString('pt-BR') })}
      </p>
    </div>
  )
}
