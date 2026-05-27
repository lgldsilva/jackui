import { useState } from 'react'
import { Loader2, KeyRound, User, ShieldCheck, ShieldOff, Copy, Check } from 'lucide-react'
import { changePassword, mfaEnroll, mfaVerify, mfaDisable } from '../api/client'
import { useAuth } from '../auth/AuthContext'

// AccountCard — self-service: shows who you are and lets you change your own
// password (verifying the current one). Visible to every logged-in user.
export default function AccountCard() {
  const { user, refresh } = useAuth()
  const [current, setCurrent] = useState('')
  const [next, setNext] = useState('')
  const [confirm, setConfirm] = useState('')
  const [busy, setBusy] = useState(false)
  const [msg, setMsg] = useState<{ ok: boolean; text: string } | null>(null)

  // MFA enrollment state
  const [enroll, setEnroll] = useState<{ secret: string; uri: string } | null>(null)
  const [mfaCode, setMfaCode] = useState('')
  const [mfaPw, setMfaPw] = useState('')
  const [mfaMsg, setMfaMsg] = useState('')
  const [copied, setCopied] = useState(false)

  if (!user) return null

  const startEnroll = async () => {
    setMfaMsg('')
    try { setEnroll(await mfaEnroll()) } catch (e: any) { setMfaMsg(e?.response?.data?.error || 'Falha ao iniciar') }
  }
  const confirmEnroll = async () => {
    setMfaMsg('')
    try { await mfaVerify(mfaCode); setEnroll(null); setMfaCode(''); await refresh(); setMfaMsg('MFA ativado.') }
    catch (e: any) { setMfaMsg(e?.response?.data?.error || 'Código inválido') }
  }
  const disableMfa = async () => {
    setMfaMsg('')
    try { await mfaDisable(mfaPw); setMfaPw(''); await refresh(); setMfaMsg('MFA desativado.') }
    catch (e: any) { setMfaMsg(e?.response?.data?.error || 'Senha incorreta') }
  }

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

      {/* MFA (TOTP) — opt-in */}
      <div className="flex flex-col gap-2 pt-3 border-t border-gray-700/60 max-w-sm">
        <span className="text-xs text-gray-400 flex items-center gap-1.5">
          {user.mfaEnabled ? <ShieldCheck className="w-3.5 h-3.5 text-green-400" /> : <ShieldOff className="w-3.5 h-3.5" />}
          Verificação em duas etapas (TOTP) {user.mfaEnabled ? '— ativa' : '— inativa'}
        </span>

        {user.mfaEnabled ? (
          <div className="flex items-center gap-2">
            <input type="password" value={mfaPw} onChange={e => setMfaPw(e.target.value)} placeholder="senha p/ desativar" autoComplete="current-password"
              className="bg-gray-900 border border-gray-700 rounded-lg px-3 py-1.5 text-sm text-gray-200 flex-1" />
            <button onClick={disableMfa} disabled={!mfaPw} className="text-sm bg-gray-700 hover:bg-gray-600 disabled:opacity-50 text-gray-100 rounded-lg px-3 py-1.5">Desativar</button>
          </div>
        ) : enroll ? (
          <div className="flex flex-col gap-2">
            <p className="text-xs text-gray-400">Adicione no app autenticador (escaneie ou digite o segredo), depois informe o código:</p>
            <div className="flex items-center gap-2 bg-gray-900 border border-gray-700 rounded-lg px-2 py-1">
              <code className="text-xs text-gray-200 font-mono truncate flex-1">{enroll.secret}</code>
              <button onClick={() => { navigator.clipboard?.writeText(enroll.secret); setCopied(true) }} title="Copiar segredo"
                className="text-gray-400 hover:text-gray-100 flex-shrink-0">{copied ? <Check className="w-4 h-4 text-green-400" /> : <Copy className="w-4 h-4" />}</button>
            </div>
            <a href={enroll.uri} className="text-[11px] text-blue-400 hover:underline truncate" title={enroll.uri}>abrir no app (otpauth://)</a>
            <div className="flex items-center gap-2">
              <input value={mfaCode} onChange={e => setMfaCode(e.target.value.replace(/\D/g, '').slice(0, 6))} placeholder="000000" inputMode="numeric"
                className="bg-gray-900 border border-gray-700 rounded-lg px-3 py-1.5 text-sm text-gray-200 font-mono tracking-widest w-28 text-center" />
              <button onClick={confirmEnroll} disabled={mfaCode.length !== 6} className="text-sm bg-green-600 hover:bg-green-500 disabled:opacity-50 text-white rounded-lg px-3 py-1.5">Confirmar</button>
              <button onClick={() => { setEnroll(null); setMfaCode('') }} className="text-xs text-gray-500 hover:text-gray-300">cancelar</button>
            </div>
          </div>
        ) : (
          <button onClick={startEnroll} className="self-start flex items-center gap-1.5 text-sm bg-gray-700 hover:bg-gray-600 text-gray-100 rounded-lg px-3 py-1.5">
            <ShieldCheck className="w-4 h-4" /> Ativar MFA
          </button>
        )}
        {mfaMsg && <span className="text-xs text-gray-400">{mfaMsg}</span>}
      </div>
    </section>
  )
}
