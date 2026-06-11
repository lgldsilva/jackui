import { useState, useEffect } from 'react'
import { Link } from 'react-router-dom'
import { useTranslation } from 'react-i18next'
import {
  ArrowLeft, Plus, Trash2, Save, Loader2, CheckCircle, XCircle,
  Settings, Download, Server, BrainCircuit, Shield,
} from 'lucide-react'
import {
  AppConfig,
  DownloadClientFull,
  getConfig,
  saveConfig,
  testJackettConnection,
} from '../api/client'
import StreamCacheCard from '../components/StreamCacheCard'
import StreamSettingsCard from '../components/StreamSettingsCard'
import DownloadsQueueCard from '../components/DownloadsQueueCard'
import ExternalMountsCard from '../components/ExternalMountsCard'
import TranscodeCapabilitiesCard from '../components/TranscodeCapabilitiesCard'
import ActiveTranscodesCard from '../components/ActiveTranscodesCard'
import AIBenchmarkCard from '../components/AIBenchmarkCard'
import ErrorBoundary from '../components/ErrorBoundary'
import { Sheet } from '../components/Sheet'
import AccountTab from './settings/AccountTab'
import GeneralTab from './settings/GeneralTab'
import { useAuth } from '../auth/AuthContext'

const DEFAULT_CLIENT: DownloadClientFull = {
  id: '', name: '', type: 'qbittorrent', url: '', username: '', password: '', default: false,
}

let clientCounter = 0
function generateId(type: string) {
  return `${type}-${++clientCounter}-${Date.now()}`
}

type TabId = 'geral' | 'downloads' | 'stream' | 'conta' | 'ia'

const TABS: { id: TabId; labelKey: string; icon: React.ReactNode }[] = [
  { id: 'geral', labelKey: 'nav.general_tab', icon: <Settings className="w-4 h-4" /> },
  { id: 'downloads', labelKey: 'nav.downloads_tab', icon: <Download className="w-4 h-4" /> },
  { id: 'stream', labelKey: 'nav.stream_tab', icon: <Server className="w-4 h-4" /> },
  { id: 'conta', labelKey: 'nav.account_tab', icon: <Shield className="w-4 h-4" /> },
  { id: 'ia', labelKey: 'nav.ia_tab', icon: <BrainCircuit className="w-4 h-4" /> },
]

// TabButton — fora do componente pai (evita recriar a cada render; S6478).
function TabButton({ tab, active, onClick }: {
  readonly tab: typeof TABS[number]
  readonly active: boolean
  readonly onClick: () => void
}) {
  const { t } = useTranslation()
  return (
    <button
      onClick={onClick}
      className={`flex items-center gap-1.5 px-3 py-2 text-sm rounded-lg transition-colors whitespace-nowrap ${
        active
          ? 'bg-pink-500/15 text-pink-700 dark:text-pink-200 border border-pink-500/30'
          : 'text-text-secondary hover:text-text-primary hover:bg-surface-secondary border border-transparent'
      }`}
    >
      {tab.icon}
      {t(tab.labelKey)}
    </button>
  )
}

// Tabs a non-admin can use: their own account + general (language/about only).
const NON_ADMIN_TAB_IDS: TabId[] = ['conta', 'geral']

export default function SettingsPage() {
  const { t } = useTranslation()
  const { isAdmin } = useAuth()
  const [config, setConfig] = useState<AppConfig | null>(null)
  const [loading, setLoading] = useState(true)
  const [saving, setSaving] = useState(false)
  const [testing, setTesting] = useState(false)
  const [testResult, setTestResult] = useState<'success' | 'error' | null>(null)
  const [testMsg, setTestMsg] = useState('')
  const [saveResult, setSaveResult] = useState<'success' | 'error' | null>(null)
  const [error, setError] = useState('')
  const [editingClient, setEditingClient] = useState<DownloadClientFull | null>(null)
  const [editingIndex, setEditingIndex] = useState<number | null>(null)
  const [activeTab, setActiveTab] = useState<TabId>(isAdmin ? 'geral' : 'conta')

  const visibleTabs = isAdmin ? TABS : NON_ADMIN_TAB_IDS.flatMap(id => TABS.filter(tb => tb.id === id))

  // If the role resolves after mount (or flips), never leave an inaccessible tab active.
  useEffect(() => {
    if (!isAdmin && !NON_ADMIN_TAB_IDS.includes(activeTab)) setActiveTab('conta')
  }, [isAdmin, activeTab])

  useEffect(() => {
    // /api/config is AdminOnly — calling it as a regular user 403'd and killed
    // the whole page. Non-admins only get config-free tabs.
    if (!isAdmin) { setLoading(false); return }
    setLoading(true)
    getConfig()
      .then(setConfig)
      .catch(() => setError('Falha ao carregar configuracoes'))
      .finally(() => setLoading(false))
  }, [isAdmin])

  const handleTestJackett = async () => {
    if (!config) return
    setTesting(true); setTestResult(null); setTestMsg('')
    try {
      // Test the URL/key currently in the form (not yet saved); an empty apiKey
      // tells the server to reuse the stored one.
      const r = await testJackettConnection({ url: config.jackett.url, apiKey: config.jackett.apiKey })
      setTestResult(r.success ? 'success' : 'error')
      if (!r.success) setTestMsg(r.error || r.message || '')
    } catch (e) {
      setTestResult('error')
      setTestMsg(e instanceof Error ? e.message : '')
    } finally { setTesting(false) }
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

  const connectionMsg = testResult === 'success'
    ? 'Conexão OK'
    : 'Falha na conexão' + (testMsg ? `: ${testMsg}` : '')

  if (loading) {
    return (
      <div className="min-h-screen bg-surface flex items-center justify-center">
        <Loader2 className="w-8 h-8 animate-spin text-green-500" />
      </div>
    )
  }

  // Only admins depend on config — for everyone else the page renders without it.
  if (isAdmin && !config) {
    return (
      <div className="min-h-screen bg-surface flex items-center justify-center">
        <p className="text-red-400">{error || 'Erro ao carregar configuracoes'}</p>
      </div>
    )
  }

  return (
    <div className="min-h-screen bg-surface">
      {/* Header */}
      <header className="bg-surface-secondary border-b border-default px-4 py-3 safe-top">
        <div className="max-w-5xl xl:max-w-6xl 2xl:max-w-[min(95vw,1600px)] mx-auto flex items-center justify-between">
          <div className="flex items-center gap-3">
            <Link to="/" className="text-text-secondary hover:text-text-primary transition-colors">
              <ArrowLeft className="w-5 h-5" />
            </Link>
            <h1 className="text-xl font-bold text-text-primary">{t('settings.title')}</h1>
          </div>
          {isAdmin && (
            <button onClick={handleSave} disabled={saving} className="btn-primary flex items-center gap-2 disabled:opacity-50">
              {saving ? <Loader2 className="w-4 h-4 animate-spin" /> : <Save className="w-4 h-4" />}
              {t('settings.save')}
            </button>
          )}
        </div>
      </header>

      {/* Tab bar — scroll horizontal no mobile */}
      <div className="border-b border-default overflow-x-auto">
        <div className="max-w-5xl xl:max-w-6xl 2xl:max-w-[min(95vw,1600px)] mx-auto px-4 py-2 flex gap-2">
          {visibleTabs.map(tab => <TabButton key={tab.id} tab={tab} active={activeTab === tab.id} onClick={() => setActiveTab(tab.id)} />)}
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
            {saveResult === 'success' ? t('settings.success') : 'Erro ao salvar configuracoes'}
          </div>
        )}

        {/* ════════ GUIA GERAL ════════ */}
        {activeTab === 'geral' && (
          <GeneralTab
            config={config}
            setConfig={setConfig}
            isAdmin={isAdmin}
            testing={testing}
            testResult={testResult}
            connectionMsg={connectionMsg}
            onTestJackett={handleTestJackett}
          />
        )}

        {/* ════════ GUIA DOWNLOADS ════════ */}
        {activeTab === 'downloads' && config && (
          <>
          <section className="card flex flex-col gap-4">
            <div className="flex items-center justify-between">
              <h2 className="text-lg font-semibold text-text-primary">Clientes de Download</h2>
              <button onClick={handleAddClient} className="btn-primary flex items-center gap-2 text-sm py-1.5">
                <Plus className="w-4 h-4" /> Adicionar
              </button>
            </div>
            {config.downloadClients.length === 0 && (
              <p className="text-text-muted text-sm text-center py-4">Nenhum cliente configurado.</p>
            )}
            <div className="flex flex-col gap-2">
              {config.downloadClients.map((client, i) => (
                <div key={client.id || i} className="flex items-center justify-between bg-surface rounded-lg p-3 gap-3">
                  <div className="flex-1 min-w-0">
                    <div className="flex items-center gap-2">
                      <span className="font-medium text-text-primary truncate">{client.name}</span>
                      {client.default && (
                        <span className="text-xs bg-green-500/20 text-green-400 px-2 py-0.5 rounded-full border border-green-500/30">padrao</span>
                      )}
                    </div>
                    <div className="text-xs text-text-secondary truncate mt-0.5">{client.type} — {client.url}</div>
                  </div>
                  <div className="flex items-center gap-1.5 flex-shrink-0">
                    {!client.default && (
                      <button onClick={() => handleSetDefault(i)} className="text-xs text-text-secondary hover:text-green-400 px-2 py-1 rounded">Padrao</button>
                    )}
                    <button onClick={() => handleEditClient(client, i)} className="text-xs btn-secondary py-1 px-2">Editar</button>
                    <button onClick={() => handleDeleteClient(i)} className="text-red-400 hover:text-red-500 dark:hover:text-red-300 p-1 rounded"><Trash2 className="w-4 h-4" /></button>
                  </div>
                </div>
              ))}
            </div>
          </section>
          <DownloadsQueueCard />
          <ExternalMountsCard />
          </>
        )}

        {/* ════════ GUIA STREAM ════════ */}
        {activeTab === 'stream' && config && (
          <>
            <section className="card flex flex-col gap-4">
              <h2 className="text-lg font-semibold text-text-primary">Servidor</h2>
              <div>
                <label htmlFor="server-port-stream" className="block text-sm font-medium text-text-primary mb-1.5">Porta</label>
                <input id="server-port-stream" type="number" value={config.port}
                  onChange={e => setConfig({ ...config, port: Number.parseInt(e.target.value) || 8989 })}
                  className="input-field w-32" min={1} max={65535} />
              </div>
            </section>
            {/* Performance do streamer (banda/memória/storage/peers) — feature #43. */}
            <ErrorBoundary title="Erro no card de performance"><StreamSettingsCard /></ErrorBoundary>
            <TranscodeCapabilitiesCard />
            <ErrorBoundary title="Erro no monitor de transcode"><ActiveTranscodesCard /></ErrorBoundary>
            <StreamCacheCard />
          </>
        )}

        {/* ════════ GUIA CONTA ════════ */}
        {activeTab === 'conta' && <AccountTab />}

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
              <label htmlFor="dc-name" className="block text-sm font-medium text-text-primary mb-1.5">Nome</label>
              <input id="dc-name" type="text" value={editingClient.name}
                onChange={e => setEditingClient({ ...editingClient, name: e.target.value })}
                placeholder="qBittorrent Local" className="input-field" />
            </div>
            <div>
              <label htmlFor="dc-type" className="block text-sm font-medium text-text-primary mb-1.5">Tipo</label>
              <select id="dc-type" value={editingClient.type}
                onChange={e => setEditingClient({ ...editingClient, type: e.target.value })}
                className="input-field">
                <option value="qbittorrent">qBittorrent</option>
                <option value="transmission">Transmission</option>
              </select>
            </div>
            <div>
              <label htmlFor="dc-url" className="block text-sm font-medium text-text-primary mb-1.5">URL</label>
              <input id="dc-url" type="url" value={editingClient.url}
                onChange={e => setEditingClient({ ...editingClient, url: e.target.value })}
                placeholder="http://localhost:8080" className="input-field" />
            </div>
            <div className="grid grid-cols-1 sm:grid-cols-2 gap-3">
              <div>
                <label htmlFor="dc-user" className="block text-sm font-medium text-text-primary mb-1.5">Usuario</label>
                <input id="dc-user" type="text" value={editingClient.username}
                  onChange={e => setEditingClient({ ...editingClient, username: e.target.value })}
                  placeholder="admin" className="input-field" />
              </div>
              <div>
                <label htmlFor="dc-pass" className="block text-sm font-medium text-text-primary mb-1.5">Senha</label>
                <input id="dc-pass" type="password" value={editingClient.password}
                  onChange={e => setEditingClient({ ...editingClient, password: e.target.value })}
                  placeholder="••••••••" className="input-field" />
              </div>
            </div>
            <label className="flex items-center gap-3 cursor-pointer">
              <input type="checkbox" checked={editingClient.default}
                onChange={e => setEditingClient({ ...editingClient, default: e.target.checked })}
                className="w-4 h-4 accent-green-500" />
              <span className="text-sm text-text-primary">Cliente padrao</span>
            </label>
          </div>
        </Sheet>
      )}

    </div>
  )
}
