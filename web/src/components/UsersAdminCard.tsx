import { useEffect, useState } from 'react'
import { Loader2, Users, Check, Ban, Trash2, UserPlus, RotateCcw } from 'lucide-react'
import { adminListUsers, adminCreateUser, adminDeleteUser, adminSetUserStatus, AdminUser } from '../api/client'
import { useAuth } from '../auth/AuthContext'

// UsersAdminCard — admin-only user management: see everyone (with status/email),
// approve pending accounts, disable/re-enable, delete, and create users.
export default function UsersAdminCard() {
  const { isAdmin, user } = useAuth()
  const [users, setUsers] = useState<AdminUser[] | null>(null)
  const [err, setErr] = useState('')
  const [creating, setCreating] = useState({ username: '', password: '', role: 'user' as 'user' | 'admin' })

  const load = () => {
    adminListUsers().then(setUsers).catch(e => setErr(e?.response?.data?.error || 'Falha ao listar'))
  }
  useEffect(() => { if (isAdmin) load() }, [isAdmin])

  if (!isAdmin) return null

  const act = async (fn: () => Promise<void>) => {
    setErr('')
    try { await fn(); load() } catch (e: any) { setErr(e?.response?.data?.error || 'Falha na operação') }
  }

  const statusChip = (s: AdminUser['status']) => {
    const map = { active: 'bg-green-500/15 text-green-400', pending: 'bg-amber-500/15 text-amber-400', disabled: 'bg-gray-700 text-gray-400' }
    const label = { active: 'Ativo', pending: 'Pendente', disabled: 'Desabilitado' }
    return <span className={`text-[10px] px-1.5 py-0.5 rounded ${map[s]}`}>{label[s]}</span>
  }

  return (
    <section className="card flex flex-col gap-3">
      <h2 className="text-lg font-semibold text-gray-100 flex items-center gap-2"><Users className="w-5 h-5" /> Usuários</h2>
      {err && <p className="text-xs text-red-400">{err}</p>}

      {users === null ? (
        <div className="flex items-center gap-2 text-gray-400 text-sm"><Loader2 className="w-4 h-4 animate-spin" /> Carregando…</div>
      ) : (
        <div className="flex flex-col divide-y divide-gray-700/60">
          {users.map(u => (
            <div key={u.id} className="flex items-center gap-2 py-2 text-sm flex-wrap">
              <div className="flex-1 min-w-0">
                <p className="text-gray-200 truncate">
                  {u.username}
                  <span className="text-gray-500"> · {u.role === 'admin' ? 'admin' : 'user'}</span>
                  {u.email && <span className="text-gray-500"> · {u.email}{u.emailVerified ? '' : ' (não confirmado)'}</span>}
                </p>
              </div>
              {statusChip(u.status)}
              <div className="flex items-center gap-1 flex-shrink-0">
                {u.status === 'pending' && (
                  <button onClick={() => act(() => adminSetUserStatus(u.id, 'active'))} title="Aprovar"
                    className="p-1.5 rounded text-green-400 hover:bg-green-500/15"><Check className="w-4 h-4" /></button>
                )}
                {u.status === 'active' && u.id !== user?.id && (
                  <button onClick={() => act(() => adminSetUserStatus(u.id, 'disabled'))} title="Desabilitar"
                    className="p-1.5 rounded text-amber-400 hover:bg-amber-500/15"><Ban className="w-4 h-4" /></button>
                )}
                {u.status === 'disabled' && (
                  <button onClick={() => act(() => adminSetUserStatus(u.id, 'active'))} title="Reativar"
                    className="p-1.5 rounded text-green-400 hover:bg-green-500/15"><RotateCcw className="w-4 h-4" /></button>
                )}
                {u.id !== user?.id && (
                  <button onClick={() => { if (confirm(`Excluir ${u.username}?`)) act(() => adminDeleteUser(u.id)) }} title="Excluir"
                    className="p-1.5 rounded text-gray-500 hover:text-red-400 hover:bg-red-500/10"><Trash2 className="w-4 h-4" /></button>
                )}
              </div>
            </div>
          ))}
        </div>
      )}

      {/* Create user inline */}
      <div className="flex flex-wrap items-center gap-2 pt-2 border-t border-gray-700/60">
        <input value={creating.username} onChange={e => setCreating(c => ({ ...c, username: e.target.value }))} placeholder="usuário"
          className="bg-gray-900 border border-gray-700 rounded-lg px-2 py-1 text-sm text-gray-200 w-32" />
        <input type="password" value={creating.password} onChange={e => setCreating(c => ({ ...c, password: e.target.value }))} placeholder="senha"
          className="bg-gray-900 border border-gray-700 rounded-lg px-2 py-1 text-sm text-gray-200 w-32" />
        <select value={creating.role} onChange={e => setCreating(c => ({ ...c, role: e.target.value as 'user' | 'admin' }))}
          className="bg-gray-900 border border-gray-700 rounded-lg px-2 py-1 text-sm text-gray-200">
          <option value="user">user</option>
          <option value="admin">admin</option>
        </select>
        <button
          onClick={() => act(async () => { await adminCreateUser(creating.username, creating.password, creating.role); setCreating({ username: '', password: '', role: 'user' }) })}
          disabled={!creating.username || !creating.password}
          className="flex items-center gap-1.5 text-sm bg-gray-700 hover:bg-gray-600 disabled:opacity-50 text-gray-100 rounded-lg px-3 py-1">
          <UserPlus className="w-4 h-4" /> Criar
        </button>
      </div>
    </section>
  )
}
