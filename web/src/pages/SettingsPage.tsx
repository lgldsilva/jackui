import { useState, useEffect } from 'react'
import { Link } from 'react-router-dom'
import { ArrowLeft, Plus, Trash2, Save, Wifi, Loader2, CheckCircle, XCircle } from 'lucide-react'
import {
  AppConfig,
  DownloadClientFull,
  getConfig,
  saveConfig,
  testJackettConnection,
} from '../api/client'
import StreamCacheCard from '../components/StreamCacheCard'
import TranscodeCapabilitiesCard from '../components/TranscodeCapabilitiesCard'
import AIBenchmarkCard from '../components/AIBenchmarkCard'
import AccountCard from '../components/AccountCard'
import UsersAdminCard from '../components/UsersAdminCard'
import ErrorBoundary from '../components/ErrorBoundary'

const DEFAULT_CLIENT: DownloadClientFull = {
  id: '',
  name: '',
  type: 'qbittorrent',
  url: '',
  username: '',
  password: '',
  default: false,
}

let clientCounter = 0
function generateId(type: string) {
  return `${type}-${++clientCounter}-${Date.now()}`
}

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

  useEffect(() => {
    getConfig()
      .then(setConfig)
      .catch(() => setError('Falha ao carregar configuracoes'))
      .finally(() => setLoading(false))
  }, [])

  const handleTestJackett = async () => {
    setTesting(true)
    setTestResult(null)
    try {
      const result = await testJackettConnection()
      setTestResult(result.success ? 'success' : 'error')
    } catch {
      setTestResult('error')
    } finally {
      setTesting(false)
    }
  }

  const handleSave = async () => {
    if (!config) return
    setSaving(true)
    setSaveResult(null)
    try {
      await saveConfig(config)
      setSaveResult('success')
    } catch {
      setSaveResult('error')
    } finally {
      setSaving(false)
    }
  }

  const handleAddClient = () => {
    setEditingClient({ ...DEFAULT_CLIENT })
    setEditingIndex(null)
  }

  const handleEditClient = (client: DownloadClientFull, index: number) => {
    setEditingClient({ ...client })
    setEditingIndex(index)
  }

  const handleSaveClient = () => {
    if (!config || !editingClient) return

    const client = { ...editingClient }
    if (!client.id) {
      client.id = generateId(client.type)
    }

    const newClients = [...config.downloadClients]
    if (editingIndex !== null) {
      newClients[editingIndex] = client
    } else {
      newClients.push(client)
    }

    setConfig({ ...config, downloadClients: newClients })
    setEditingClient(null)
    setEditingIndex(null)
  }

  const handleDeleteClient = (index: number) => {
    if (!config) return
    const newClients = config.downloadClients.filter((_, i) => i !== index)
    setConfig({ ...config, downloadClients: newClients })
  }

  const handleSetDefault = async (index: number) => {
    if (!config) return
    const newClients = config.downloadClients.map((c, i) => ({
      ...c,
      default: i === index,
    }))
    const updated = { ...config, downloadClients: newClients }
    // Optimistic UI: reflect immediately, then persist. If save fails we surface via banner.
    setConfig(updated)
    setSaving(true)
    setSaveResult(null)
    try {
      await saveConfig(updated)
      setSaveResult('success')
    } catch {
      setSaveResult('error')
    } finally {
      setSaving(false)
    }
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

  return (
    <div className="min-h-screen bg-gray-900">
      {/* Header */}
      <header className="bg-gray-800 border-b border-gray-700 px-4 py-3 safe-top">
        <div className="max-w-5xl xl:max-w-6xl 2xl:max-w-[min(95vw,1600px)] mx-auto flex items-center justify-between">
          <div className="flex items-center gap-3">
            <Link
              to="/"
              className="text-gray-400 hover:text-gray-100 transition-colors"
            >
              <ArrowLeft className="w-5 h-5" />
            </Link>
            <h1 className="text-xl font-bold text-gray-100">Configuracoes</h1>
          </div>
          <button
            onClick={handleSave}
            disabled={saving}
            className="btn-primary flex items-center gap-2 disabled:opacity-50"
          >
            {saving ? (
              <Loader2 className="w-4 h-4 animate-spin" />
            ) : (
              <Save className="w-4 h-4" />
            )}
            Salvar
          </button>
        </div>
      </header>

      <main className="max-w-5xl xl:max-w-6xl 2xl:max-w-[min(95vw,1600px)] mx-auto px-4 py-6 flex flex-col gap-6">
        {/* Save result */}
        {saveResult && (
          <div
            className={`flex items-center gap-2 rounded-xl p-3 text-sm ${
              saveResult === 'success'
                ? 'bg-green-500/10 border border-green-500/30 text-green-400'
                : 'bg-red-500/10 border border-red-500/30 text-red-400'
            }`}
          >
            {saveResult === 'success' ? (
              <CheckCircle className="w-4 h-4" />
            ) : (
              <XCircle className="w-4 h-4" />
            )}
            {saveResult === 'success'
              ? 'Configuracoes salvas com sucesso!'
              : 'Erro ao salvar configuracoes'}
          </div>
        )}

        {/* Jackett config */}
        <section className="card flex flex-col gap-4">
          <h2 className="text-lg font-semibold text-gray-100">Jackett</h2>

          <div>
            <label className="block text-sm font-medium text-gray-300 mb-1.5">URL</label>
            <input
              type="url"
              value={config.jackett.url}
              onChange={(e) =>
                setConfig({ ...config, jackett: { ...config.jackett, url: e.target.value } })
              }
              placeholder="http://localhost:9117"
              className="input-field"
            />
          </div>

          <div>
            <label className="block text-sm font-medium text-gray-300 mb-1.5">API Key</label>
            <input
              type="text"
              value={config.jackett.apiKey}
              onChange={(e) =>
                setConfig({ ...config, jackett: { ...config.jackett, apiKey: e.target.value } })
              }
              placeholder={config.jackett.apiKeySet ? 'API key configurada — deixe vazio para manter' : 'Sua API key do Jackett'}
              className="input-field font-mono"
            />
          </div>

          <div className="flex items-center gap-3">
            <button
              onClick={handleTestJackett}
              disabled={testing}
              className="btn-secondary flex items-center gap-2 disabled:opacity-50"
            >
              {testing ? (
                <Loader2 className="w-4 h-4 animate-spin" />
              ) : (
                <Wifi className="w-4 h-4" />
              )}
              Testar conexao
            </button>

            {testResult && (
              <div
                className={`flex items-center gap-1.5 text-sm ${
                  testResult === 'success' ? 'text-green-400' : 'text-red-400'
                }`}
              >
                {testResult === 'success' ? (
                  <CheckCircle className="w-4 h-4" />
                ) : (
                  <XCircle className="w-4 h-4" />
                )}
                {testResult === 'success' ? 'Conexao OK' : 'Falha na conexao'}
              </div>
            )}
          </div>
        </section>

        {/* Download clients */}
        <section className="card flex flex-col gap-4">
          <div className="flex items-center justify-between">
            <h2 className="text-lg font-semibold text-gray-100">Clientes de Download</h2>
            <button
              onClick={handleAddClient}
              className="btn-primary flex items-center gap-2 text-sm py-1.5"
            >
              <Plus className="w-4 h-4" />
              Adicionar
            </button>
          </div>

          {config.downloadClients.length === 0 && (
            <p className="text-gray-500 text-sm text-center py-4">
              Nenhum cliente configurado. Adicione um para comecar.
            </p>
          )}

          <div className="flex flex-col gap-2">
            {config.downloadClients.map((client, i) => (
              <div
                key={client.id || i}
                className="flex items-center justify-between bg-gray-900 rounded-lg p-3 gap-3"
              >
                <div className="flex-1 min-w-0">
                  <div className="flex items-center gap-2">
                    <span className="font-medium text-gray-100 truncate">{client.name}</span>
                    {client.default && (
                      <span className="text-xs bg-green-500/20 text-green-400 px-2 py-0.5 rounded-full border border-green-500/30 flex-shrink-0">
                        padrao
                      </span>
                    )}
                  </div>
                  <div className="text-xs text-gray-400 truncate mt-0.5">
                    {client.type} — {client.url}
                  </div>
                </div>
                <div className="flex items-center gap-1.5 flex-shrink-0">
                  {!client.default && (
                    <button
                      onClick={() => handleSetDefault(i)}
                      className="text-xs text-gray-400 hover:text-green-400 px-2 py-1 rounded transition-colors"
                    >
                      Padrao
                    </button>
                  )}
                  <button
                    onClick={() => handleEditClient(client, i)}
                    className="text-xs btn-secondary py-1 px-2"
                  >
                    Editar
                  </button>
                  <button
                    onClick={() => handleDeleteClient(i)}
                    className="text-red-400 hover:text-red-300 p-1 rounded transition-colors"
                  >
                    <Trash2 className="w-4 h-4" />
                  </button>
                </div>
              </div>
            ))}
          </div>
        </section>

        {/* Port */}
        <section className="card flex flex-col gap-4">
          <h2 className="text-lg font-semibold text-gray-100">Servidor</h2>
          <div>
            <label className="block text-sm font-medium text-gray-300 mb-1.5">Porta</label>
            <input
              type="number"
              value={config.port}
              onChange={(e) => setConfig({ ...config, port: parseInt(e.target.value) || 8989 })}
              className="input-field w-32"
              min={1}
              max={65535}
            />
          </div>
        </section>

        {/* Hardware transcoding capabilities */}
        <TranscodeCapabilitiesCard />

        {/* Account (change password) + admin user management. Wrapped so a crash
            in one card can't blank the whole Settings page (and surfaces the error). */}
        <ErrorBoundary title="Erro no card Conta"><AccountCard /></ErrorBoundary>
        <ErrorBoundary title="Erro no card Usuários"><UsersAdminCard /></ErrorBoundary>

        {/* AI title identification + benchmark */}
        <AIBenchmarkCard />

        {/* Streaming cache */}
        <StreamCacheCard />
      </main>

      {/* Client edit modal */}
      {editingClient && (
        <div
          className="fixed inset-0 bg-black/60 backdrop-blur-sm flex items-center justify-center z-50 p-4"
          onClick={(e) => e.target === e.currentTarget && setEditingClient(null)}
        >
          <div className="bg-gray-800 rounded-2xl border border-gray-700 w-full max-w-md shadow-2xl">
            <div className="flex items-center justify-between p-5 border-b border-gray-700">
              <h3 className="text-lg font-semibold text-gray-100">
                {editingIndex !== null ? 'Editar Cliente' : 'Novo Cliente'}
              </h3>
            </div>

            <div className="p-5 flex flex-col gap-4">
              <div>
                <label className="block text-sm font-medium text-gray-300 mb-1.5">Nome</label>
                <input
                  type="text"
                  value={editingClient.name}
                  onChange={(e) => setEditingClient({ ...editingClient, name: e.target.value })}
                  placeholder="qBittorrent Local"
                  className="input-field"
                />
              </div>

              <div>
                <label className="block text-sm font-medium text-gray-300 mb-1.5">Tipo</label>
                <select
                  value={editingClient.type}
                  onChange={(e) => setEditingClient({ ...editingClient, type: e.target.value })}
                  className="input-field"
                >
                  <option value="qbittorrent">qBittorrent</option>
                  <option value="transmission">Transmission</option>
                </select>
              </div>

              <div>
                <label className="block text-sm font-medium text-gray-300 mb-1.5">URL</label>
                <input
                  type="url"
                  value={editingClient.url}
                  onChange={(e) => setEditingClient({ ...editingClient, url: e.target.value })}
                  placeholder="http://localhost:8080"
                  className="input-field"
                />
              </div>

              <div className="grid grid-cols-2 gap-3">
                <div>
                  <label className="block text-sm font-medium text-gray-300 mb-1.5">
                    Usuario
                  </label>
                  <input
                    type="text"
                    value={editingClient.username}
                    onChange={(e) =>
                      setEditingClient({ ...editingClient, username: e.target.value })
                    }
                    placeholder="admin"
                    className="input-field"
                  />
                </div>
                <div>
                  <label className="block text-sm font-medium text-gray-300 mb-1.5">
                    Senha
                  </label>
                  <input
                    type="password"
                    value={editingClient.password}
                    onChange={(e) =>
                      setEditingClient({ ...editingClient, password: e.target.value })
                    }
                    placeholder="••••••••"
                    className="input-field"
                  />
                </div>
              </div>

              <label className="flex items-center gap-3 cursor-pointer">
                <input
                  type="checkbox"
                  checked={editingClient.default}
                  onChange={(e) =>
                    setEditingClient({ ...editingClient, default: e.target.checked })
                  }
                  className="w-4 h-4 accent-green-500"
                />
                <span className="text-sm text-gray-300">Cliente padrao</span>
              </label>
            </div>

            <div className="flex gap-3 p-5 border-t border-gray-700">
              <button onClick={() => setEditingClient(null)} className="btn-secondary flex-1">
                Cancelar
              </button>
              <button
                onClick={handleSaveClient}
                disabled={!editingClient.name || !editingClient.url}
                className="btn-primary flex-1 disabled:opacity-50"
              >
                Salvar
              </button>
            </div>
          </div>
        </div>
      )}
    </div>
  )
}
