import { useState, useEffect } from 'react'
import { HardDrive, Loader2, Save, Plus, Trash2, Lock, Users, X, Search } from 'lucide-react'
import {
  getMounts, updateMounts, adminListUsers,
  ExternalMount, AdminUser,
} from '../api/client'

// UserAccessPicker — shows the users with access as removable chips and a search
// box to add others (typeahead over the users not yet selected). Replaces the
// flat pill toggles, which gave no search and no clear "who has access" view.
function UserAccessPicker({ allUsers, selected, onToggle }: {
  readonly allUsers: AdminUser[]
  readonly selected: string[]
  readonly onToggle: (username: string) => void
}) {
  const [query, setQuery] = useState('')
  const [open, setOpen] = useState(false)
  const q = query.trim().toLowerCase()
  const available = allUsers.filter(u => !selected.includes(u.username))
  const matches = q ? available.filter(u => u.username.toLowerCase().includes(q)) : available

  return (
    <div className="flex flex-col gap-2">
      <div className="flex flex-wrap items-center gap-1.5">
        <span className="text-[11px] text-text-muted">Com acesso:</span>
        {selected.length === 0 && <span className="text-[11px] text-amber-400/80">ninguém ainda — adicione abaixo</span>}
        {selected.map(u => (
          <span key={u} className="inline-flex items-center gap-1 text-[11px] pl-2 pr-1 py-0.5 rounded-md bg-blue-500/20 text-blue-300 border border-blue-500/40">
            {u}
            <button onClick={() => onToggle(u)} title={`Remover ${u}`} className="hover:text-white p-0.5"><X className="w-3 h-3" /></button>
          </span>
        ))}
      </div>
      {available.length > 0 && (
        <div className="relative">
          <Search className="absolute left-2.5 top-1/2 -translate-y-1/2 w-3.5 h-3.5 text-text-muted pointer-events-none" />
          <input
            value={query}
            onChange={e => { setQuery(e.target.value); setOpen(true) }}
            onFocus={() => setOpen(true)}
            onBlur={() => setTimeout(() => setOpen(false), 120)}
            placeholder="buscar usuário para adicionar…"
            className="w-full bg-surface-secondary border border-default rounded-lg pl-8 pr-3 py-1.5 text-xs text-text-primary focus:outline-none focus:border-blue-500/50"
          />
          {open && matches.length > 0 && (
            <ul className="absolute z-10 mt-1 w-full max-h-40 overflow-y-auto bg-surface border border-default rounded-lg shadow-2xl">
              {matches.map(u => (
                <li key={u.id}>
                  {/* onMouseDown fires before the input's onBlur, so the pick registers. */}
                  <button
                    onMouseDown={() => { onToggle(u.username); setQuery(''); setOpen(false) }}
                    className="w-full text-left px-3 py-1.5 text-xs text-text-primary hover:bg-surface-secondary"
                  >
                    {u.username}
                  </button>
                </li>
              ))}
            </ul>
          )}
        </div>
      )}
    </div>
  )
}

// Visibility model for a mount: shared with everyone, restricted to some users,
// or isolated so each user only sees their own subdir.
type Visibility = 'all' | 'restricted' | 'perUser'

// MountRow carries a stable _key for React lists (the mount has no id, and its
// name changes while editing, so an index/name key would be unstable).
type MountRow = ExternalMount & { _key: number }

let rowKeySeq = 0
function withKeys(ms: ExternalMount[]): MountRow[] {
  return ms.map((m) => ({ ...m, _key: rowKeySeq++ }))
}

function visibilityOf(m: ExternalMount): Visibility {
  if (m.userSubpath) return 'perUser'
  if ((m.allowedUsers?.length ?? 0) > 0) return 'restricted'
  return 'all'
}

// Pure list helpers — kept top-level so the state updaters stay shallow (avoids
// deeply-nested callbacks: sonarjs S2004).
function toggleInList(list: string[], item: string): string[] {
  return list.includes(item) ? list.filter((u) => u !== item) : [...list, item]
}
function replaceAt<T>(arr: T[], i: number, next: T): T[] {
  return arr.map((x, idx) => (idx === i ? next : x))
}

export default function ExternalMountsCard() {
  const [mounts, setMounts] = useState<MountRow[] | null>(null)
  const [users, setUsers] = useState<AdminUser[]>([])
  const [loading, setLoading] = useState(true)
  const [saving, setSaving] = useState(false)
  const [error, setError] = useState('')
  const [notice, setNotice] = useState('')

  useEffect(() => {
    Promise.all([getMounts(), adminListUsers().catch(() => [] as AdminUser[])])
      .then(([m, u]) => { setMounts(withKeys(m)); setUsers(u) })
      .catch((e: unknown) => setError(e instanceof Error ? e.message : String(e)))
      .finally(() => setLoading(false))
  }, [])

  const update = (i: number, patch: Partial<ExternalMount>) =>
    setMounts((ms) => ms ? replaceAt(ms, i, { ...ms[i], ...patch }) : ms)

  const setVisibility = (i: number, v: Visibility) => {
    if (v === 'perUser') update(i, { userSubpath: true, allowedUsers: [] })
    else if (v === 'all') update(i, { userSubpath: false, allowedUsers: [] })
    else update(i, { userSubpath: false })
  }

  const toggleUser = (i: number, username: string) =>
    setMounts((ms) => {
      if (!ms) return ms
      const next = toggleInList(ms[i].allowedUsers ?? [], username)
      return replaceAt(ms, i, { ...ms[i], allowedUsers: next })
    })

  const addMount = () => setMounts((ms) => [...(ms ?? []), { name: '', path: '', userSubpath: false, allowedUsers: [], _key: rowKeySeq++ }])
  const removeMount = (i: number) => setMounts((ms) => ms ? ms.filter((_, idx) => idx !== i) : ms)

  const handleSave = async () => {
    if (!mounts) return
    setSaving(true); setError(''); setNotice('')
    try {
      // Strip the UI-only _key before persisting.
      await updateMounts(mounts.map(({ _key: _drop, ...m }) => m))
      setNotice('Salvo e aplicado ao vivo.')
    } catch (e: unknown) {
      setError(e instanceof Error ? e.message : String(e))
    } finally {
      setSaving(false)
    }
  }

  if (loading) {
    return (
      <div className="card flex items-center gap-3 text-text-secondary">
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
        <h2 className="text-lg font-semibold text-text-primary">Pastas externas (mounts)</h2>
      </div>
      <p className="text-xs text-text-muted -mt-2">
        Pastas montadas no servidor que aparecem na aba Local. Defina quem vê cada uma.
      </p>

      {mounts.length === 0 && <p className="text-sm text-text-muted text-center py-4">Nenhum mount configurado.</p>}

      <div className="flex flex-col gap-4">
        {mounts.map((m, i) => {
          const vis = visibilityOf(m)
          return (
            <div key={m._key} className="bg-surface/60 rounded-lg p-3 flex flex-col gap-3 border border-default">
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
                <span className="text-xs text-text-secondary">Visibilidade</span>
                <div className="flex flex-wrap gap-2">
                  <VisButton active={vis === 'all'} onClick={() => setVisibility(i, 'all')} icon={<Users className="w-3.5 h-3.5" />} label="Todos" />
                  <VisButton active={vis === 'restricted'} onClick={() => setVisibility(i, 'restricted')} icon={<Users className="w-3.5 h-3.5" />} label="Usuários específicos" />
                  <VisButton active={vis === 'perUser'} onClick={() => setVisibility(i, 'perUser')} icon={<Lock className="w-3.5 h-3.5" />} label="Separado por usuário" />
                </div>
              </div>
              {vis === 'restricted' && (
                users.length === 0
                  ? <span className="text-[11px] text-text-muted">Sem usuários para listar.</span>
                  : <UserAccessPicker allUsers={users} selected={m.allowedUsers ?? []} onToggle={(u) => toggleUser(i, u)} />
              )}
              {vis === 'perUser' && (
                <p className="text-[11px] text-text-muted">Cada usuário vê e grava só no seu próprio subdiretório dentro desta pasta.</p>
              )}
            </div>
          )
        })}
      </div>

      <button onClick={addMount} className="flex items-center gap-1.5 text-xs text-blue-400 hover:text-blue-300 self-start">
        <Plus className="w-4 h-4" /> Adicionar mount
      </button>

      <div className="flex items-center justify-between gap-3 border-t border-default pt-4">
        <div className="text-xs">
          {error && <span className="text-red-400">{error}</span>}
          {notice && <span className="text-green-400">{notice}</span>}
        </div>
        <button
          onClick={handleSave}
          disabled={saving}
          className="flex items-center gap-2 bg-blue-500 hover:bg-blue-600 disabled:opacity-50 text-white font-semibold px-4 py-2 rounded-lg text-sm transition-colors min-h-[44px]"
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
        : 'bg-surface-secondary text-text-secondary border-default hover:border-strong'}`}
    >
      {icon}{label}
    </button>
  )
}
