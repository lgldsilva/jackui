import { useState, useEffect } from 'react'
import { Loader2, Trash2, Zap, Server, ShieldAlert } from 'lucide-react'
import { fetchActiveTranscodes, killTranscodeSession, HLSSessionSnapshot, GPUInfo } from '../api/client'

export default function ActiveTranscodesCard() {
  const [sessions, setSessions] = useState<HLSSessionSnapshot[]>([])
  const [gpu, setGpu] = useState<GPUInfo | null>(null)
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState('')
  const [killingKey, setKillingKey] = useState<string | null>(null)

  const loadData = async (silent = false) => {
    if (!silent) setLoading(true)
    setError('')
    try {
      const data = await fetchActiveTranscodes()
      setSessions(data.sessions || [])
      setGpu(data.gpu || null)
    } catch (e: unknown) {
      setError(e instanceof Error ? e.message : String(e))
    } finally {
      setLoading(false)
    }
  }

  useEffect(() => {
    loadData(false)
    const interval = setInterval(() => {
      loadData(true)
    }, 5000)
    return () => clearInterval(interval)
  }, [])

  const handleKill = async (key: string) => {
    if (!confirm('Deseja realmente derrubar e encerrar esta sessão de transcodificação? O player do usuário irá parar.')) {
      return
    }
    setKillingKey(key)
    try {
      await killTranscodeSession(key)
      await loadData(true)
    } catch (e: any) {
      alert('Erro ao encerrar sessão: ' + (e?.response?.data?.error || e.message))
    } finally {
      setKillingKey(null)
    }
  }

  if (loading && !gpu) {
    return (
      <div className="card flex items-center gap-3 text-gray-400">
        <Loader2 className="w-4 h-4 animate-spin text-cyan-400" />
        Carregando monitoramento de transcode e GPU...
      </div>
    )
  }

  const vramPercent = gpu && gpu.vramTotal && gpu.vramUsed
    ? (gpu.vramUsed / gpu.vramTotal) * 100
    : 0

  return (
    <div className="card flex flex-col gap-4">
      {/* Header */}
      <div className="flex items-center justify-between border-b border-gray-700/60 pb-3">
        <div className="flex items-center gap-2">
          <Server className="w-5 h-5 text-cyan-400" />
          <h2 className="text-lg font-semibold text-gray-100">Transcode & Uso de GPU</h2>
        </div>
        {sessions.length > 0 && (
          <span className="flex items-center gap-1 text-[10px] font-bold px-2 py-0.5 rounded bg-amber-500/20 text-amber-300 border border-amber-500/30 animate-pulse">
            <Zap className="w-3 h-3 text-amber-400" />
            {sessions.length} {sessions.length === 1 ? 'sessão ativa' : 'sessões ativas'}
          </span>
        )}
      </div>

      {/* GPU Load Dashboard */}
      {gpu && gpu.type !== 'none' && (
        <div className="grid grid-cols-1 md:grid-cols-2 gap-4 bg-gray-900/60 border border-gray-800 rounded-xl p-4">
          <div className="space-y-1.5">
            <div className="flex justify-between text-xs text-gray-400">
              <span>Uso da GPU ({gpu.type === 'nvidia' ? 'NVIDIA' : 'Intel/AMD VAAPI'}):</span>
              <span className="font-bold text-gray-200">{gpu.gpu}%</span>
            </div>
            <div className="w-full bg-gray-800 rounded-full h-2 overflow-hidden">
              <div
                className="bg-cyan-500 h-full rounded-full transition-all duration-500"
                style={{ width: `${gpu.gpu}%` }}
              />
            </div>
          </div>

          {gpu.type === 'nvidia' && gpu.vramTotal !== undefined && gpu.vramUsed !== undefined && (
            <div className="space-y-1.5">
              <div className="flex justify-between text-xs text-gray-400">
                <span>Uso de Memória de Vídeo (VRAM):</span>
                <span className="font-bold text-gray-200">
                  {gpu.vramUsed} MB / {gpu.vramTotal} MB ({vramPercent.toFixed(0)}%)
                </span>
              </div>
              <div className="w-full bg-gray-800 rounded-full h-2 overflow-hidden">
                <div
                  className="bg-emerald-500 h-full rounded-full transition-all duration-500"
                  style={{ width: `${vramPercent}%` }}
                />
              </div>
            </div>
          )}
        </div>
      )}

      {/* Sessions list */}
      <div className="flex flex-col gap-2">
        <h3 className="text-xs font-semibold text-gray-400 uppercase tracking-wider">
          Sessões de Transcode HLS
        </h3>
        
        {sessions.length === 0 ? (
          <p className="text-sm text-gray-500 py-4 text-center bg-gray-900/20 rounded-xl border border-dashed border-gray-800/40">
            Nenhuma sessão de transcodificação ativa no momento.
          </p>
        ) : (
          <div className="overflow-x-auto">
            <table className="w-full text-sm text-left border-collapse">
              <thead>
                <tr className="border-b border-gray-800 text-xs text-gray-500 font-medium">
                  <th className="py-2 px-3">Sessão / Arquivo</th>
                  <th className="py-2 px-3">Codificador</th>
                  <th className="py-2 px-3">Buffer (.ts)</th>
                  <th className="py-2 px-3">Iniciado</th>
                  <th className="py-2 px-3 text-right">Ação</th>
                </tr>
              </thead>
              <tbody className="divide-y divide-gray-800/40">
                {sessions.map(s => {
                  const parts = s.key.split('-')
                  const displayKey = parts.length > 0 ? parts[0].slice(0, 8) + '...' : s.key
                  const fileIndex = parts.length > 1 ? ` (Arq: ${parts[1]})` : ''

                  let codecClass: string
                  if (s.codec === 'nvidia') {
                    codecClass = 'bg-green-500/10 text-green-300 border-green-500/20'
                  } else if (s.codec === 'vaapi') {
                    codecClass = 'bg-red-500/10 text-red-300 border-red-500/20'
                  } else {
                    codecClass = 'bg-yellow-500/10 text-yellow-300 border-yellow-500/20'
                  }

                  let codecLabel: string
                  if (s.codec === 'nvidia') {
                    codecLabel = 'NVIDIA HW'
                  } else if (s.codec === 'vaapi') {
                    codecLabel = 'VAAPI HW'
                  } else {
                    codecLabel = 'CPU SW'
                  }

                  return (
                    <tr key={s.key} className="hover:bg-gray-800/30 transition-colors group">
                      <td className="py-3 px-3 font-mono text-xs text-gray-300">
                        <div className="font-semibold text-gray-200">
                          {displayKey}
                        </div>
                        <div className="text-[10px] text-gray-500">
                          Hash {s.key.slice(0, 32)}...{fileIndex}
                        </div>
                      </td>
                      <td className="py-3 px-3">
                        <span className={`px-2 py-0.5 rounded text-[10px] font-bold border ${codecClass}`}>
                          {codecLabel}
                        </span>
                      </td>
                      <td className="py-3 px-3 font-mono text-xs text-gray-400">
                        {s.segmentsReady} segs
                      </td>
                      <td className="py-3 px-3 text-xs text-gray-400">
                        {new Date(s.startedAt).toLocaleTimeString('pt-BR')}
                      </td>
                      <td className="py-3 px-3 text-right">
                        <button
                          onClick={() => handleKill(s.key)}
                          disabled={killingKey === s.key}
                          title="Derrubar Sessão (Kill)"
                          className="p-1.5 rounded-lg border border-red-500/30 bg-red-500/10 text-red-400 hover:bg-red-500 hover:text-white transition-all disabled:opacity-50"
                        >
                          {killingKey === s.key ? (
                            <Loader2 className="w-3.5 h-3.5 animate-spin" />
                          ) : (
                            <Trash2 className="w-3.5 h-3.5" />
                          )}
                        </button>
                      </td>
                    </tr>
                  )
                })}
              </tbody>
            </table>
          </div>
        )}
      </div>
      
      {error && (
        <div className="flex items-center gap-2 p-3 text-xs rounded-xl bg-red-500/10 border border-red-500/20 text-red-400">
          <ShieldAlert className="w-4 h-4 text-red-400 flex-shrink-0" />
          <span>Erro ao consultar transcodes: {error}</span>
        </div>
      )}
    </div>
  )
}
