import { useState } from 'react'
import { Loader2, KeyRound, User } from 'lucide-react'
import { changePassword } from '../api/client'
import { useAuth } from '../auth/AuthContext'

// AccountCard — self-service: shows who you are and lets you change your own
// password (verifying the current one). Visible to every logged-in user.
export default function AccountCard() {
  const { user } = useAuth()
  const [current, setCurrent] = useState('')
  const [next, setNext] = useState('')
  const [confirm, setConfirm] = useState('')
  const [busy, setBusy] = useState(false)
  const [msg, setMsg] = useState<{ ok: boolean; text: string } | null>(null)

  if (!user) return null

  const submit = async () => {
    setMsg(null)
    if (next.length < 6) { setMsg({ ok: false, text: 'A nova senha precisa de ao menos 6 caracteres.' }); return }
    if (next !== confirm) { setMsg({ ok: false, text: 'A confirmação não bate.' }); return }
    setBusy(true)
    try {
      await changePassword(current, next)
      setMsg({ ok: true, text: 'Senha alterada.' })
      setCurrent(''); setNext(''); setConfirm('')
    } catch (e: any) {
      setMsg({ ok: false, text: e?.response?.data?.error || 'Falha ao alterar a senha.' })
    } finally { setBusy(false) }
  }

  return (
    <section className="card flex flex-col gap-3">
      <h2 className="text-lg font-semibold text-gray-100 flex items-center gap-2"><User className="w-5 h-5" /> Conta</h2>
      <p className="text-sm text-gray-400">
        {user.username}{user.email ? ` · ${user.email}` : ''} · {user.role === 'admin' ? 'Admin' : 'Usuário'}
      </p>
      <div className="flex flex-col gap-2 max-w-sm">
        <label className="text-xs text-gray-400 flex items-center gap-1.5"><KeyRound className="w-3.5 h-3.5" /> Trocar senha</label>
        <input type="password" value={current} onChange={e => setCurrent(e.target.value)} placeholder="Senha atual" autoComplete="current-password"
          className="bg-gray-900 border border-gray-700 rounded-lg px-3 py-2 text-sm text-gray-200 focus:outline-none focus:border-green-500" />
        <input type="password" value={next} onChange={e => setNext(e.target.value)} placeholder="Nova senha" autoComplete="new-password"
          className="bg-gray-900 border border-gray-700 rounded-lg px-3 py-2 text-sm text-gray-200 focus:outline-none focus:border-green-500" />
        <input type="password" value={confirm} onChange={e => setConfirm(e.target.value)} placeholder="Confirmar nova senha" autoComplete="new-password"
          className="bg-gray-900 border border-gray-700 rounded-lg px-3 py-2 text-sm text-gray-200 focus:outline-none focus:border-green-500" />
        <div className="flex items-center gap-3">
          <button onClick={submit} disabled={busy || !current || !next}
            className="flex items-center gap-1.5 text-sm bg-green-600 hover:bg-green-500 disabled:opacity-50 text-white rounded-lg px-3 py-1.5">
            {busy ? <Loader2 className="w-4 h-4 animate-spin" /> : <KeyRound className="w-4 h-4" />} Salvar
          </button>
          {msg && <span className={`text-xs ${msg.ok ? 'text-green-400' : 'text-red-400'}`}>{msg.text}</span>}
        </div>
      </div>
    </section>
  )
}
