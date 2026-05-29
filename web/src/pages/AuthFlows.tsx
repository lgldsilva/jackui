import { useEffect, useState } from 'react'
import { useNavigate, useSearchParams } from 'react-router-dom'
import { Loader2, AlertCircle, CheckCircle2, UserPlus, KeyRound, Mail } from 'lucide-react'
import { registerAccount, verifyEmail, forgotPassword, resetPassword } from '../api/client'

// Shared shell for the unauthenticated auth pages (register / verify / forgot /
// reset) — same centered card as the login screen.
function Shell({ title, children }: { readonly title: string; readonly children: React.ReactNode }) {
  return (
    <div className="min-h-screen bg-gray-900 flex items-center justify-center px-4 safe-top safe-bottom">
      <div className="w-full max-w-sm">
        <div className="flex justify-center mb-6">
          <span className="text-3xl font-bold text-green-500">Jack</span>
          <span className="text-3xl font-bold text-gray-100">UI</span>
        </div>
        <div className="bg-gray-800 border border-gray-700 rounded-2xl p-6 flex flex-col gap-4 shadow-2xl">
          <h1 className="text-lg font-semibold text-gray-100">{title}</h1>
          {children}
        </div>
      </div>
    </div>
  )
}

function Err({ text }: { readonly text: string }) {
  return <div className="bg-red-500/10 border border-red-500/30 text-red-400 text-sm rounded-lg p-3 flex items-center gap-2"><AlertCircle className="w-4 h-4 flex-shrink-0" />{text}</div>
}
function Ok({ text }: { readonly text: string }) {
  return <div className="bg-green-500/10 border border-green-500/30 text-green-300 text-sm rounded-lg p-3 flex items-center gap-2"><CheckCircle2 className="w-4 h-4 flex-shrink-0" />{text}</div>
}

export function RegisterPage() {
  const nav = useNavigate()
  const [params] = useSearchParams()
  const invite = params.get('invite') || ''
  const [username, setUsername] = useState('')
  const [email, setEmail] = useState('')
  const [password, setPassword] = useState('')
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState('')
  const [done, setDone] = useState('')

  const handleSubmit = async (e: React.FormEvent) => {
    e.preventDefault(); setBusy(true); setError('')
    try {
      const r = await registerAccount(username, email, password, invite)
      setDone(r.message)
    } catch (err: any) {
      setError(err?.response?.data?.error || 'Falha no cadastro')
    } finally { setBusy(false) }
  }

  if (done) return <Shell title="Cadastro criado"><Ok text={done} /><button onClick={() => nav('/login')} className="btn-primary">Ir para o login</button></Shell>

  return (
    <Shell title={invite ? 'Criar conta (convite)' : 'Criar conta'}>
      <form onSubmit={handleSubmit} className="flex flex-col gap-3">
        <input className="input-field" placeholder="Usuário" autoComplete="username" value={username} onChange={e => setUsername(e.target.value)} required />
        <input className="input-field" placeholder="E-mail" type="email" autoComplete="email" value={email} onChange={e => setEmail(e.target.value)} required />
        <input className="input-field" placeholder="Senha (≥6)" type="password" autoComplete="new-password" value={password} onChange={e => setPassword(e.target.value)} required />
        {!invite && <p className="text-xs text-gray-500">Sem convite, sua conta fica pendente até um admin aprovar.</p>}
        {error && <Err text={error} />}
        <button type="submit" disabled={busy || !username || !email || password.length < 6} className="btn-primary flex items-center justify-center gap-2 disabled:opacity-50">
          {busy ? <Loader2 className="w-4 h-4 animate-spin" /> : <UserPlus className="w-4 h-4" />} Cadastrar
        </button>
        <button type="button" onClick={() => nav('/login')} className="text-xs text-gray-400 hover:text-green-400">Já tenho conta</button>
      </form>
    </Shell>
  )
}

export function VerifyEmailPage() {
  const nav = useNavigate()
  const [params] = useSearchParams()
  const [state, setState] = useState<'busy' | 'ok' | 'err'>('busy')
  const [msg, setMsg] = useState('')
  useEffect(() => {
    const token = params.get('token') || ''
    if (token) {
      verifyEmail(token).then(() => { setState('ok'); setMsg('E-mail confirmado!') })
        .catch((e: any) => { setState('err'); setMsg(e?.response?.data?.error || 'Falha ao confirmar.') })
    } else {
      setState('err'); setMsg('Token ausente.')
    }
  }, [])
  return (
    <Shell title="Confirmar e-mail">
      {state === 'busy' && <div className="flex items-center gap-2 text-gray-400"><Loader2 className="w-4 h-4 animate-spin" /> Confirmando…</div>}
      {state === 'ok' && <Ok text={msg} />}
      {state === 'err' && <Err text={msg} />}
      {state !== 'busy' && <button onClick={() => nav('/login')} className="btn-primary">Ir para o login</button>}
    </Shell>
  )
}

export function ForgotPasswordPage() {
  const nav = useNavigate()
  const [email, setEmail] = useState('')
  const [busy, setBusy] = useState(false)
  const [msg, setMsg] = useState('')
  const handleSubmit = async (e: React.FormEvent) => {
    e.preventDefault(); setBusy(true)
    try { setMsg(await forgotPassword(email)) } catch { setMsg('Se o e-mail estiver cadastrado, enviamos um link.') } finally { setBusy(false) }
  }
  if (msg) return <Shell title="Recuperar senha"><Ok text={msg} /><button onClick={() => nav('/login')} className="btn-primary">Voltar ao login</button></Shell>
  return (
    <Shell title="Recuperar senha">
      <form onSubmit={handleSubmit} className="flex flex-col gap-3">
        <input className="input-field" placeholder="Seu e-mail" type="email" autoComplete="email" value={email} onChange={e => setEmail(e.target.value)} required />
        <button type="submit" disabled={busy || !email} className="btn-primary flex items-center justify-center gap-2 disabled:opacity-50">
          {busy ? <Loader2 className="w-4 h-4 animate-spin" /> : <Mail className="w-4 h-4" />} Enviar link
        </button>
        <button type="button" onClick={() => nav('/login')} className="text-xs text-gray-400 hover:text-green-400">Voltar</button>
      </form>
    </Shell>
  )
}

export function ResetPasswordPage() {
  const nav = useNavigate()
  const [params] = useSearchParams()
  const token = params.get('token') || ''
  const [password, setPassword2] = useState('')
  const [confirm, setConfirm] = useState('')
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState('')
  const [done, setDone] = useState(false)
  const handleSubmit = async (e: React.FormEvent) => {
    e.preventDefault(); setError('')
    if (password.length < 6) { setError('Mínimo 6 caracteres.'); return }
    if (password !== confirm) { setError('As senhas não batem.'); return }
    setBusy(true)
    try { await resetPassword(token, password); setDone(true) }
    catch (err: any) { setError(err?.response?.data?.error || 'Falha ao redefinir.') }
    finally { setBusy(false) }
  }
  if (token) {
    if (done) return <Shell title="Senha redefinida"><Ok text="Pronto! Você já pode entrar." /><button onClick={() => nav('/login')} className="btn-primary">Ir para o login</button></Shell>
  } else {
    return <Shell title="Redefinir senha"><Err text="Token ausente ou inválido." /></Shell>
  }
  return (
    <Shell title="Redefinir senha">
      <form onSubmit={handleSubmit} className="flex flex-col gap-3">
        <input className="input-field" placeholder="Nova senha (≥6)" type="password" autoComplete="new-password" value={password} onChange={e => setPassword2(e.target.value)} required />
        <input className="input-field" placeholder="Confirmar nova senha" type="password" autoComplete="new-password" value={confirm} onChange={e => setConfirm(e.target.value)} required />
        {error && <Err text={error} />}
        <button type="submit" disabled={busy} className="btn-primary flex items-center justify-center gap-2 disabled:opacity-50">
          {busy ? <Loader2 className="w-4 h-4 animate-spin" /> : <KeyRound className="w-4 h-4" />} Redefinir
        </button>
      </form>
    </Shell>
  )
}
