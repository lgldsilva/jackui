import { useState, useEffect } from 'react'
import { Link } from 'react-router-dom'
import {
  ArrowLeft, Plus, Trash2, Save, Wifi, Loader2, CheckCircle, XCircle,
  Settings, Download, Server, BrainCircuit, Shield, Monitor,
  LogOut,
} from 'lucide-react'
import {
  AppConfig,
  DownloadClientFull,
  getConfig,
  saveConfig,
  testJackettConnection,
  listSessions,
  revokeSession,
  revokeOtherSessions,
  SessionInfo,
} from '../api/client'
import StreamCacheCard from '../components/StreamCacheCard'
import TranscodeCapabilitiesCard from '../components/TranscodeCapabilitiesCard'
import ActiveTranscodesCard from '../components/ActiveTranscodesCard'
import AIBenchmarkCard from '../components/AIBenchmarkCard'
import AccountCard from '../components/AccountCard'
import UsersAdminCard from '../components/UsersAdminCard'
import ErrorBoundary from '../components/ErrorBoundary'
import { Sheet } from '../components/Sheet'
import { getRefreshToken } from '../auth/AuthContext'

const DEFAULT_CLIENT: DownloadClientFull = {
  id: '', name: '', type: 'qbittorrent', url: '', username: '', password: '', default: false,
}

let clientCounter = 0
function generateId(type: string) {
  return `${type}-${++clientCounter}-${Date.now()}`
}

function EnvBadge({ envVar }: { readonly envVar: string }) {
  return (
    <span
      className="ml-2 text-[10px] bg-amber-500/15 text-amber-400 border border-amber-500/30 px-1.5 py-0.5 rounded inline-flex items-center gap-1"
      title="Gerenciado por variável de ambiente"
    >
      <span className="opacity-70">ENV</span>
      <code className="font-mono">{envVar}</code>
    </span>
  )
}

type TabId = 'geral' | 'downloads' | 'stream' | 'conta' | 'ia'

const TABS: { id: TabId; label: string; icon: React.ReactNode }[] = [
  { id: 'geral', label: 'Geral', icon: <Settings className="w-4 h-4" /> },
  { id: 'downloads', label: 'Downloads', icon: <Download className="w-4 h-4" /> },
  { id: 'stream', label: 'Stream', icon: <Server className="w-4 h-4" /> },
  { id: 'conta', label: 'Conta', icon: <Shield className="w-4 h-4" /> },
  { id: 'ia', label: 'IA', icon: <BrainCircuit className="w-4 h-4" /> },
]

export default function SettingsPage() {
  const [config, setConfig] = useState<AppConfig | null>(null)
  const [loading, setLoading] = useState(true)
  const [saving, setSaving] = useState(false)
  const [testing, setTesting] = useState(false)
  const [testResult, setTestResult] = useState<'success' | 'error' | null>(null)
  const [saveResult, setSaveResult] = useState<'success' | 'error' | null>(null)
  const [error, setError] = useState('')
  const [editingClient, setEditingClient] = useState<DownloadClientFull | null>(null)
  const [editingIndex, setEditingIndex] = useState<number | null>(null)
  const [activeTab, setActiveTab] = useState<TabId>('geral')

  // Sessions modal
  const [sessionModalOpen, setSessionModalOpen] = useState(false)
  const [sessions, setSessions] = useState<SessionInfo[]>([])
  const [sessionsLoading, setSessionsLoading] = useState(false)

  const loadSessions = async () => {
    setSessionsLoading(true)
    try { setSessions(await listSessions(getRefreshToken())) } catch { /* ignore */ }
    setSessionsLoading(false)
  }

  useEffect(() => {
    getConfig()
      .then(setConfig)
      .catch(() => setError('Falha ao carregar configuracoes'))
      .finally(() => setLoading(false))
  }, [])

  const handleTestJackett = async () => {
    setTesting(true); setTestResult(null)
    try { const r = await testJackettConnection(); setTestResult(r.success ? 'success' : 'error') }
    catch { setTestResult('error') }
    finally { setTesting(false) }
  }

  const handleSave = async () => {
    if (!config) return
    setSaving(true); setSaveResult(null)
    try { await saveConfig(config); setSaveResult('success') }
    catch { setSaveResult('error') }
    finally { setSaving(false) }
  }

  const handleAddClient = () => { setEditingClient({ ...DEFAULT_CLIENT }); setEditingIndex(null) }
  const handleEditClient = (client: DownloadClientFull, index: number) => { setEditingClient({ ...client }); setEditingIndex(index) }

  const handleSaveClient = () => {
    if (!config || !editingClient) return
    const client = { ...editingClient }
    if (!client.id) client.id = generateId(client.type)
    const newClients = [...config.downloadClients]
    if (editingIndex === null) newClients.push(client)
    else newClients[editingIndex] = client
    setConfig({ ...config, downloadClients: newClients })
    setEditingClient(null); setEditingIndex(null)
  }

  const handleDeleteClient = (index: number) => {
    if (!config) return
    setConfig({ ...config, downloadClients: config.downloadClients.filter((_, i) => i !== index) })
  }

  const handleSetDefault = async (index: number) => {
    if (!config) return
    const updated = {
      ...config,
      downloadClients: config.downloadClients.map((c, i) => ({ ...c, default: i === index })),
    }
    setConfig(updated)
    setSaving(true); setSaveResult(null)
    try { await saveConfig(updated); setSaveResult('success') } catch { setSaveResult('error') }
    finally { setSaving(false) }
  }

  if (loading) {
    return (
      <div className="min-h-screen bg-gray-900 flex items-center justify-center">
        <Loader2 className="w-8 h-8 animate-spin text-green-500" />
      </div>
    )
  }

  if (!config) {
    return (
      <div className="min-h-screen bg-gray-900 flex items-center justify-center">
        <p className="text-red-400">{error || 'Erro ao carregar configuracoes'}</p>
      </div>
    )
  }

  const TabButton = ({ tab }: { tab: typeof TABS[number] }) => (
    <button
      onClick={() => setActiveTab(tab.id)}
      className={`flex items-center gap-1.5 px-3 py-2 text-sm rounded-lg transition-colors whitespace-nowrap ${
        activeTab === tab.id
          ? 'bg-pink-500/15 text-pink-200 border border-pink-500/30'
          : 'text-gray-400 hover:text-gray-200 hover:bg-gray-800 border border-transparent'
      }`}
    >
      {tab.icon}
      {tab.label}
    </button>
  )

  return (
    <div className="min-h-screen bg-gray-900">
      {/* Header */}
      <header className="bg-gray-800 border-b border-gray-700 px-4 py-3 safe-top">
        <div className="max-w-5xl xl:max-w-6xl 2xl:max-w-[min(95vw,1600px)] mx-auto flex items-center justify-between">
          <div className="flex items-center gap-3">
            <Link to="/" className="text-gray-400 hover:text-gray-100 transition-colors">
              <ArrowLeft className="w-5 h-5" />
            </Link>
            <h1 className="text-xl font-bold text-gray-100">Configuracoes</h1>
          </div>
          <button onClick={handleSave} disabled={saving} className="btn-primary flex items-center gap-2 disabled:opacity-50">
            {saving ? <Loader2 className="w-4 h-4 animate-spin" /> : <Save className="w-4 h-4" />}
            Salvar
          </button>
        </div>
      </header>

      {/* Tab bar — scroll horizontal no mobile */}
      <div className="border-b border-gray-700 overflow-x-auto">
        <div className="max-w-5xl xl:max-w-6xl 2xl:max-w-[min(95vw,1600px)] mx-auto px-4 py-2 flex gap-2">
          {TABS.map(tab => <TabButton key={tab.id} tab={tab} />)}
        </div>
      </div>

      <main className="max-w-5xl xl:max-w-6xl 2xl:max-w-[min(95vw,1600px)] mx-auto px-4 py-6 flex flex-col gap-6">
        {saveResult && (
          <div className={`flex items-center gap-2 rounded-xl p-3 text-sm ${
            saveResult === 'success'
              ? 'bg-green-500/10 border border-green-500/30 text-green-400'
              : 'bg-red-500/10 border border-red-500/30 text-red-400'
          }`}>
            {saveResult === 'success' ? <CheckCircle className="w-4 h-4" /> : <XCircle className="w-4 h-4" />}
            {saveResult === 'success' ? 'Configuracoes salvas com sucesso!' : 'Erro ao salvar configuracoes'}
          </div>
        )}

        {/* ════════ GUIA GERAL ════════ */}
        {activeTab === 'geral' && (
          <>
            <section className="card flex flex-col gap-4">
              <h2 className="text-lg font-semibold text-gray-100">Jackett</h2>
              <div>
                <label htmlFor="jackett-url" className="block text-sm font-medium text-gray-300 mb-1.5">
                  URL {config.envOverrides?.JACKETT_URL && <EnvBadge envVar="JACKETT_URL" />}
                </label>
                <input id="jackett-url" type="url" value={config.jackett.url}
                  onChange={e => setConfig({ ...config, jackett: { ...config.jackett, url: e.target.value } })}
                  placeholder="http://localhost:9117" className="input-field" />
              </div>
              <div>
                <label htmlFor="jackett-apikey" className="block text-sm font-medium text-gray-300 mb-1.5">
                  API Key {config.envOverrides?.JACKETT_API_KEY && <EnvBadge envVar="JACKETT_API_KEY" />}
                </label>
                <input id="jackett-apikey" type="text" value={config.jackett.apiKey}
                  onChange={e => setConfig({ ...config, jackett: { ...config.jackett, apiKey: e.target.value } })}
                  placeholder={config.jackett.apiKeySet ? 'API key configurada — deixe vazio para manter' : 'Sua API key do Jackett'}
                  className="input-field font-mono" />
              </div>
              <div className="flex items-center gap-3">
                <button onClick={handleTestJackett} disabled={testing}
                  className="btn-secondary flex items-center gap-2 disabled:opacity-50">
                  {testing ? <Loader2 className="w-4 h-4 animate-spin" /> : <Wifi className="w-4 h-4" />}
                  Testar conexao
                </button>
                {testResult && (
                  <div className={`flex items-center gap-1.5 text-sm ${testResult === 'success' ? 'text-green-400' : 'text-red-400'}`}>
                    {testResult === 'success' ? <CheckCircle className="w-4 h-4" /> : <XCircle className="w-4 h-4" />}
                    {testResult === 'success' ? 'Conexao OK' : 'Falha na conexao'}
                  </div>
                )}
              </div>
            </section>

            <section className="card flex flex-col gap-4">
              <h2 className="text-lg font-semibold text-gray-100">Servidor</h2>
              <div>
                <label htmlFor="server-port" className="block text-sm font-medium text-gray-300 mb-1.5">Porta</label>
                <input id="server-port" type="number" value={config.port}
                  onChange={e => setConfig({ ...config, port: Number.parseInt(e.target.value) || 8989 })}
                  className="input-field w-32" min={1} max={65535} />
              </div>
            </section>

            {/* Env overrides */}
            {config.envOverrides && Object.keys(config.envOverrides).length > 0 && (
              <section className="card flex flex-col gap-3">
                <h2 className="text-base font-semibold text-gray-200">
                  Variáveis de Ambiente
                  <span className="ml-2 text-[10px] text-amber-400 font-normal">(prioridade sobre config.yaml)</span>
                </h2>
                <div className="space-y-1.5">
                  {Object.entries(config.envOverrides).map(([key, value]) => (
                    <div key={key} className="flex items-center gap-2 text-xs">
                      <code className="text-amber-400 font-mono">{key}</code>
                      <span className="text-gray-500">=</span>
                      <code className="text-gray-300 font-mono break-all">{value}</code>
                    </div>
                  ))}
                </div>
                <p className="text-gray-600 text-[11px]">
                  Estas variáveis sobrescrevem o config.yaml ao iniciar.
                </p>
              </section>
            )}

            {/* About */}
            <section className="card flex flex-col gap-3">
              <h2 className="text-base font-semibold text-gray-200">Sobre o JackUI</h2>
              <div className="space-y-1.5 text-xs text-gray-400">
                {typeof window !== 'undefined' && window.electronAPI ? <VersionInfo /> : (
                  <><p>Versão: desenvolvimento</p><p>Executando via navegador</p></>
                )}
              </div>
            </section>
          </>
        )}

        {/* ════════ GUIA DOWNLOADS ════════ */}
        {activeTab === 'downloads' && (
          <section className="card flex flex-col gap-4">
            <div className="flex items-center justify-between">
              <h2 className="text-lg font-semibold text-gray-100">Clientes de Download</h2>
              <button onClick={handleAddClient} className="btn-primary flex items-center gap-2 text-sm py-1.5">
                <Plus className="w-4 h-4" /> Adicionar
              </button>
            </div>
            {config.downloadClients.length === 0 && (
              <p className="text-gray-500 text-sm text-center py-4">Nenhum cliente configurado.</p>
            )}
            <div className="flex flex-col gap-2">
              {config.downloadClients.map((client, i) => (
                <div key={client.id || i} className="flex items-center justify-between bg-gray-900 rounded-lg p-3 gap-3">
                  <div className="flex-1 min-w-0">
                    <div className="flex items-center gap-2">
                      <span className="font-medium text-gray-100 truncate">{client.name}</span>
                      {client.default && (
                        <span className="text-xs bg-green-500/20 text-green-400 px-2 py-0.5 rounded-full border border-green-500/30">padrao</span>
                      )}
                    </div>
                    <div className="text-xs text-gray-400 truncate mt-0.5">{client.type} — {client.url}</div>
                  </div>
                  <div className="flex items-center gap-1.5 flex-shrink-0">
                    {!client.default && (
                      <button onClick={() => handleSetDefault(i)} className="text-xs text-gray-400 hover:text-green-400 px-2 py-1 rounded">Padrao</button>
                    )}
                    <button onClick={() => handleEditClient(client, i)} className="text-xs btn-secondary py-1 px-2">Editar</button>
                    <button onClick={() => handleDeleteClient(i)} className="text-red-400 hover:text-red-300 p-1 rounded"><Trash2 className="w-4 h-4" /></button>
                  </div>
                </div>
              ))}
            </div>
          </section>
        )}

        {/* ════════ GUIA STREAM ════════ */}
        {activeTab === 'stream' && (
          <>
            <section className="card flex flex-col gap-4">
              <h2 className="text-lg font-semibold text-gray-100">Servidor</h2>
              <div>
                <label htmlFor="server-port-stream" className="block text-sm font-medium text-gray-300 mb-1.5">Porta</label>
                <input id="server-port-stream" type="number" value={config.port}
                  onChange={e => setConfig({ ...config, port: Number.parseInt(e.target.value) || 8989 })}
                  className="input-field w-32" min={1} max={65535} />
              </div>
            </section>
            <TranscodeCapabilitiesCard />
            <ErrorBoundary title="Erro no monitor de transcode"><ActiveTranscodesCard /></ErrorBoundary>
            <StreamCacheCard />
          </>
        )}

        {/* ════════ GUIA CONTA ════════ */}
        {activeTab === 'conta' && (
          <>
            <ErrorBoundary title="Erro no card Conta"><AccountCard /></ErrorBoundary>
            <ErrorBoundary title="Erro no card Usuários"><UsersAdminCard /></ErrorBoundary>

            {/* Sessions modal trigger */}
            <section className="card">
              <button
                onClick={() => { loadSessions(); setSessionModalOpen(true) }}
                className="flex items-center gap-2 text-sm text-gray-300 hover:text-gray-100 w-full"
              >
                <Monitor className="w-4 h-4 text-gray-500" />
                Ver sessões ativas
                <span className="text-xs text-gray-500">→</span>
              </button>
            </section>
          </>
        )}

        {/* ════════ GUIA IA ════════ */}
        {activeTab === 'ia' && (
          <ErrorBoundary title="Erro no card IA"><AIBenchmarkCard /></ErrorBoundary>
        )}
      </main>

      {/* Client edit modal */}
      {editingClient && (
        <Sheet open onClose={() => setEditingClient(null)} size="md"
          title={editingIndex === null ? 'Novo Cliente' : 'Editar Cliente'}
          footer={
            <div className="flex gap-3">
              <button onClick={() => setEditingClient(null)} className="btn-secondary flex-1">Cancelar</button>
              <button onClick={handleSaveClient} disabled={!editingClient.name || !editingClient.url} className="btn-primary flex-1 disabled:opacity-50">Salvar</button>
            </div>
          }>
          <div className="flex flex-col gap-4">
            <div>
              <label className="block text-sm font-medium text-gray-300 mb-1.5">Nome</label>
              <input type="text" value={editingClient.name}
                onChange={e => setEditingClient({ ...editingClient, name: e.target.value })}
                placeholder="qBittorrent Local" className="input-field" />
            </div>
            <div>
              <label className="block text-sm font-medium text-gray-300 mb-1.5">Tipo</label>
              <select value={editingClient.type}
                onChange={e => setEditingClient({ ...editingClient, type: e.target.value })}
                className="input-field">
                <option value="qbittorrent">qBittorrent</option>
                <option value="transmission">Transmission</option>
              </select>
            </div>
            <div>
              <label className="block text-sm font-medium text-gray-300 mb-1.5">URL</label>
              <input type="url" value={editingClient.url}
                onChange={e => setEditingClient({ ...editingClient, url: e.target.value })}
                placeholder="http://localhost:8080" className="input-field" />
            </div>
            <div className="grid grid-cols-1 sm:grid-cols-2 gap-3">
              <div>
                <label className="block text-sm font-medium text-gray-300 mb-1.5">Usuario</label>
                <input type="text" value={editingClient.username}
                  onChange={e => setEditingClient({ ...editingClient, username: e.target.value })}
                  placeholder="admin" className="input-field" />
              </div>
              <div>
                <label className="block text-sm font-medium text-gray-300 mb-1.5">Senha</label>
                <input type="password" value={editingClient.password}
                  onChange={e => setEditingClient({ ...editingClient, password: e.target.value })}
                  placeholder="••••••••" className="input-field" />
              </div>
            </div>
            <label className="flex items-center gap-3 cursor-pointer">
              <input type="checkbox" checked={editingClient.default}
                onChange={e => setEditingClient({ ...editingClient, default: e.target.checked })}
                className="w-4 h-4 accent-green-500" />
              <span className="text-sm text-gray-300">Cliente padrao</span>
            </label>
          </div>
        </Sheet>
      )}

      {/* Sessions modal */}
      <Sheet open={sessionModalOpen} onClose={() => setSessionModalOpen(false)} title="Sessões Ativas" size="sm">
        {sessionsLoading ? (
          <div className="flex justify-center py-8"><Loader2 className="w-6 h-6 animate-spin text-gray-500" /></div>
        ) : sessions.length === 0 ? (
          <p className="text-sm text-gray-500 text-center py-4">Nenhuma sessão registrada.</p>
        ) : (
          <div className="flex flex-col gap-1.5">
            {sessions.map(sess => (
              <div key={sess.id} className="flex items-center gap-2 bg-gray-900 border border-gray-700 rounded-lg px-3 py-2 text-sm">
                <div className="flex-1 min-w-0">
                  <div className="text-gray-200 flex items-center gap-1.5">
                    {sess.current && <span className="text-green-400">● esta sessão</span>}
                    {!sess.current && <span className="text-gray-500">○</span>}
                    {sess.remember && <span className="text-gray-500 text-xs">· lembrar 30d</span>}
                  </div>
                  <div className="text-gray-500 text-xs mt-0.5">
                    criada {new Date(sess.createdAt).toLocaleString()} · expira {new Date(sess.expiresAt).toLocaleDateString()}
                  </div>
                </div>
                {!sess.current && (
                  <button onClick={async () => { await revokeSession(sess.id); loadSessions() }}
                    title="Encerrar" className="text-gray-500 hover:text-red-400 flex-shrink-0">
                    <Trash2 className="w-3.5 h-3.5" />
                  </button>
                )}
              </div>
            ))}
            {sessions.some(s => !s.current) && (
              <button onClick={async () => { await revokeOtherSessions(getRefreshToken()); loadSessions() }}
                className="flex items-center gap-1.5 text-sm bg-gray-700 hover:bg-gray-600 text-gray-100 rounded-lg px-3 py-2 self-start mt-2">
                <LogOut className="w-4 h-4" /> Encerrar outras sessões
              </button>
            )}
          </div>
        )}
      </Sheet>
    </div>
  )
}

function VersionInfo() {
  const [ver, setVer] = useState<{ version: string; commit: string; date: string } | null>(null)
  useEffect(() => { window.electronAPI?.getAppVersion().then(setVer).catch(() => {}) }, [])
  if (!ver) return <p className="animate-pulse">Carregando…</p>
  return (
    <>
      <p>Versão: <span className="text-gray-300">{ver.version}</span></p>
      <p>Commit: <span className="text-gray-300 font-mono">{ver.commit}</span></p>
      <p>Build: <span className="text-gray-300">{new Date(ver.date).toLocaleString('pt-BR')}</span></p>
      <p>Plataforma: <span className="text-gray-300">{navigator.platform}</span></p>
    </>
  )
}
