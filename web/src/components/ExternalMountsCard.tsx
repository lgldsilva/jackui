import { useState, useEffect } from 'react'
import { HardDrive, Loader2, Save, Plus, Trash2, Lock, Users } from 'lucide-react'
import {
  getMounts, updateMounts, adminListUsers,
  ExternalMount, AdminUser,
} from '../api/client'

// Visibility model for a mount: shared with everyone, restricted to some users,
// or isolated so each user only sees their own subdir.
type Visibility = 'all' | 'restricted' | 'perUser'

function visibilityOf(m: ExternalMount): Visibility {
  if (m.userSubpath) return 'perUser'
  if ((m.allowedUsers?.length ?? 0) > 0) return 'restricted'
  return 'all'
}

export default function ExternalMountsCard() {
  const [mounts, setMounts] = useState<ExternalMount[] | null>(null)
  const [users, setUsers] = useState<AdminUser[]>([])
  const [loading, setLoading] = useState(true)
  const [saving, setSaving] = useState(false)
  const [error, setError] = useState('')
  const [notice, setNotice] = useState('')

  useEffect(() => {
    Promise.all([getMounts(), adminListUsers().catch(() => [] as AdminUser[])])
      .then(([m, u]) => { setMounts(m); setUsers(u) })
      .catch((e: unknown) => setError(e instanceof Error ? e.message : String(e)))
      .finally(() => setLoading(false))
  }, [])

  const update = (i: number, patch: Partial<ExternalMount>) =>
    setMounts((ms) => ms ? ms.map((m, idx) => idx === i ? { ...m, ...patch } : m) : ms)

  const setVisibility = (i: number, v: Visibility) => {
    if (v === 'perUser') update(i, { userSubpath: true, allowedUsers: [] })
    else if (v === 'all') update(i, { userSubpath: false, allowedUsers: [] })
    else update(i, { userSubpath: false })
  }

  const toggleUser = (i: number, username: string) =>
    setMounts((ms) => ms ? ms.map((m, idx) => {
      if (idx !== i) return m
      const cur = m.allowedUsers ?? []
      return { ...m, allowedUsers: cur.includes(username) ? cur.filter(u => u !== username) : [...cur, username] }
    }) : ms)

  const addMount = () => setMounts((ms) => [...(ms ?? []), { name: '', path: '', userSubpath: false, allowedUsers: [] }])
  const removeMount = (i: number) => setMounts((ms) => ms ? ms.filter((_, idx) => idx !== i) : ms)

  const handleSave = async () => {
    if (!mounts) return
    setSaving(true); setError(''); setNotice('')
    try {
      await updateMounts(mounts)
      setNotice('Salvo e aplicado ao vivo.')
    } catch (e: unknown) {
      setError(e instanceof Error ? e.message : String(e))
    } finally {
      setSaving(false)
    }
  }

  if (loading) {
    return (
      <div className="card flex items-center gap-3 text-gray-400">
        <Loader2 className="w-4 h-4 animate-spin" /> Carregando mounts...
      </div>
    )
  }
  if (error && !mounts) {
    return <div className="card text-red-400 text-sm">Mounts indisponíveis: {error}</div>
  }
  if (!mounts) return null

  return (
    <div className="card flex flex-col gap-5">
      <div className="flex items-center gap-2">
        <HardDrive className="w-5 h-5 text-blue-400" />
        <h2 className="text-lg font-semibold text-gray-100">Pastas externas (mounts)</h2>
      </div>
      <p className="text-xs text-gray-500 -mt-2">
        Pastas montadas no servidor que aparecem na aba Local. Defina quem vê cada uma.
      </p>

      {mounts.length === 0 && <p className="text-sm text-gray-500 text-center py-4">Nenhum mount configurado.</p>}

      <div className="flex flex-col gap-4">
        {mounts.map((m, i) => {
          const vis = visibilityOf(m)
          return (
            <div key={i} className="bg-gray-900/60 rounded-lg p-3 flex flex-col gap-3 border border-gray-800">
              <div className="flex items-center gap-2">
                <input
                  value={m.name}
                  onChange={(e) => update(i, { name: e.target.value })}
                  placeholder="Nome (ex: GDrive Sensitivo)"
                  className="input-field min-h-[40px] flex-1"
                />
                <button onClick={() => removeMount(i)} title="Remover mount" className="p-2 text-red-400 hover:bg-red-500/10 rounded-lg">
                  <Trash2 className="w-4 h-4" />
                </button>
              </div>
              <input
                value={m.path}
                onChange={(e) => update(i, { path: e.target.value })}
                placeholder="Caminho no container (ex: /mnt/gdrive/sensitive)"
                className="input-field min-h-[40px] font-mono text-xs"
              />
              <div className="flex flex-col gap-2">
                <span className="text-xs text-gray-400">Visibilidade</span>
                <div className="flex flex-wrap gap-2">
                  <VisButton active={vis === 'all'} onClick={() => setVisibility(i, 'all')} icon={<Users className="w-3.5 h-3.5" />} label="Todos" />
                  <VisButton active={vis === 'restricted'} onClick={() => setVisibility(i, 'restricted')} icon={<Users className="w-3.5 h-3.5" />} label="Usuários específicos" />
                  <VisButton active={vis === 'perUser'} onClick={() => setVisibility(i, 'perUser')} icon={<Lock className="w-3.5 h-3.5" />} label="Separado por usuário" />
                </div>
              </div>
              {vis === 'restricted' && (
                <div className="flex flex-wrap gap-1.5">
                  {users.length === 0 && <span className="text-[11px] text-gray-600">Sem usuários para listar.</span>}
                  {users.map((u) => {
                    const on = (m.allowedUsers ?? []).includes(u.username)
                    return (
                      <button
                        key={u.id}
                        onClick={() => toggleUser(i, u.username)}
                        className={`text-[11px] px-2 py-1 rounded-md border transition-colors ${on
                          ? 'bg-blue-500/20 text-blue-300 border-blue-500/40'
                          : 'bg-gray-800 text-gray-400 border-gray-700 hover:border-gray-600'}`}
                      >
                        {u.username}
                      </button>
                    )
                  })}
                </div>
              )}
              {vis === 'perUser' && (
                <p className="text-[11px] text-gray-600">Cada usuário vê e grava só no seu próprio subdiretório dentro desta pasta.</p>
              )}
            </div>
          )
        })}
      </div>

      <button onClick={addMount} className="flex items-center gap-1.5 text-xs text-blue-400 hover:text-blue-300 self-start">
        <Plus className="w-4 h-4" /> Adicionar mount
      </button>

      <div className="flex items-center justify-between gap-3 border-t border-gray-800 pt-4">
        <div className="text-xs">
          {error && <span className="text-red-400">{error}</span>}
          {notice && <span className="text-green-400">{notice}</span>}
        </div>
        <button
          onClick={handleSave}
          disabled={saving}
          className="flex items-center gap-2 bg-blue-500 hover:bg-blue-600 disabled:opacity-50 text-gray-900 font-semibold px-4 py-2 rounded-lg text-sm transition-colors min-h-[44px]"
        >
          {saving ? <Loader2 className="w-4 h-4 animate-spin" /> : <Save className="w-4 h-4" />} Salvar
        </button>
      </div>
    </div>
  )
}

function VisButton({ active, onClick, icon, label }: Readonly<{ active: boolean; onClick: () => void; icon: React.ReactNode; label: string }>) {
  return (
    <button
      onClick={onClick}
      className={`flex items-center gap-1.5 text-xs px-3 py-1.5 rounded-lg border transition-colors ${active
        ? 'bg-blue-500/20 text-blue-300 border-blue-500/40'
        : 'bg-gray-800 text-gray-400 border-gray-700 hover:border-gray-600'}`}
    >
      {icon}{label}
    </button>
  )
}
