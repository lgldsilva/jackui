import { useState, useEffect, useCallback } from 'react'
import { Loader2, KeyRound, User, ShieldCheck, ShieldOff, Copy, Check, Fingerprint, Trash2, LifeBuoy, RefreshCw, Monitor, LogOut } from 'lucide-react'
import { changePassword, mfaEnroll, mfaVerify, mfaDisable, mfaBackupCodesRemaining, mfaRegenerateBackupCodes, isPasskeySupported, passkeyList, passkeyRegister, passkeyDelete, PasskeyInfo, listSessions, revokeSession, revokeOtherSessions, SessionInfo } from '../api/client'
import { useAuth, getRefreshToken } from '../auth/AuthContext'

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

  // MFA backup (recovery) codes
  const [backupCodes, setBackupCodes] = useState<string[] | null>(null) // shown once
  const [backupRemaining, setBackupRemaining] = useState<number | null>(null)
  const [regenPw, setRegenPw] = useState('')
  const [showRegen, setShowRegen] = useState(false)

  // Passkey (WebAuthn) state
  const passkeysSupported = isPasskeySupported()
  const [passkeys, setPasskeys] = useState<PasskeyInfo[]>([])
  const [pkBusy, setPkBusy] = useState(false)
  const [pkMsg, setPkMsg] = useState('')

  const loadPasskeys = useCallback(async () => {
    if (!passkeysSupported) return
    try { setPasskeys(await passkeyList()) } catch { /* ignore */ }
  }, [passkeysSupported])

  useEffect(() => { loadPasskeys() }, [loadPasskeys])

  // Active sessions
  const [sessions, setSessions] = useState<SessionInfo[]>([])
  const loadSessions = useCallback(async () => {
    try { setSessions(await listSessions(getRefreshToken())) } catch { /* ignore */ }
  }, [])
  useEffect(() => { loadSessions() }, [loadSessions])

  const killSession = async (id: string) => {
    try { await revokeSession(id); await loadSessions() } catch { /* ignore */ }
  }
  const killOthers = async () => {
    try { await revokeOtherSessions(getRefreshToken()); await loadSessions() } catch { /* ignore */ }
  }

  const addPasskey = async () => {
    setPkBusy(true); setPkMsg('')
    try {
      await passkeyRegister()
      await loadPasskeys()
      setPkMsg('Passkey adicionada.')
    } catch (e: any) {
      if (e?.name === 'NotAllowedError' || e?.name === 'AbortError') setPkMsg('Cancelado.')
      else setPkMsg(e?.response?.data?.error || e.message || 'Falha ao adicionar passkey.')
    } finally { setPkBusy(false) }
  }
  const removePasskey = async (id: string) => {
    setPkMsg('')
    try { await passkeyDelete(id); await loadPasskeys() }
    catch (e: any) { setPkMsg(e?.response?.data?.error || 'Falha ao remover.') }
  }

  if (!user) return null

  const loadBackupRemaining = useCallback(async () => {
    if (!user?.mfaEnabled) { setBackupRemaining(null); return }
    try { setBackupRemaining(await mfaBackupCodesRemaining()) } catch { /* ignore */ }
  }, [user?.mfaEnabled])

  useEffect(() => { loadBackupRemaining() }, [loadBackupRemaining])

  const startEnroll = async () => {
    setMfaMsg('')
    try { setEnroll(await mfaEnroll()) } catch (e: any) { setMfaMsg(e?.response?.data?.error || 'Falha ao iniciar') }
  }
  const confirmEnroll = async () => {
    setMfaMsg('')
    try {
      const codes = await mfaVerify(mfaCode)
      setEnroll(null); setMfaCode(''); await refresh()
      setBackupCodes(codes); await loadBackupRemaining()
      setMfaMsg('MFA ativado. Guarde os códigos de recuperação abaixo.')
    } catch (e: any) { setMfaMsg(e?.response?.data?.error || 'Código inválido') }
  }
  const disableMfa = async () => {
    setMfaMsg('')
    try { await mfaDisable(mfaPw); setMfaPw(''); setBackupCodes(null); setBackupRemaining(null); await refresh(); setMfaMsg('MFA desativado.') }
    catch (e: any) { setMfaMsg(e?.response?.data?.error || 'Senha incorreta') }
  }
  const regenerateBackup = async () => {
    setMfaMsg('')
    try {
      const codes = await mfaRegenerateBackupCodes(regenPw)
      setRegenPw(''); setShowRegen(false); setBackupCodes(codes); await loadBackupRemaining()
    } catch (e: any) { setMfaMsg(e?.response?.data?.error || 'Senha incorreta') }
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
          <div className="flex flex-col gap-2">
            <div className="flex items-center gap-2">
              <input type="password" value={mfaPw} onChange={e => setMfaPw(e.target.value)} placeholder="senha p/ desativar" autoComplete="current-password"
                className="bg-gray-900 border border-gray-700 rounded-lg px-3 py-1.5 text-sm text-gray-200 flex-1" />
              <button onClick={disableMfa} disabled={!mfaPw} className="text-sm bg-gray-700 hover:bg-gray-600 disabled:opacity-50 text-gray-100 rounded-lg px-3 py-1.5">Desativar</button>
            </div>
            {/* Backup (recovery) codes status + regenerate */}
            <div className="flex items-center gap-2 flex-wrap text-xs text-gray-400">
              <LifeBuoy className="w-3.5 h-3.5" />
              <span>Códigos de recuperação: {backupRemaining ?? '—'} restantes</span>
              {backupRemaining !== null && backupRemaining <= 2 && (
                <span className="text-amber-400">— poucos! gere novos</span>
              )}
              {!showRegen ? (
                <button onClick={() => setShowRegen(true)} className="text-blue-400 hover:underline inline-flex items-center gap-1">
                  <RefreshCw className="w-3 h-3" /> gerar novos
                </button>
              ) : (
                <span className="inline-flex items-center gap-1">
                  <input type="password" value={regenPw} onChange={e => setRegenPw(e.target.value)} placeholder="senha" autoComplete="current-password"
                    className="bg-gray-900 border border-gray-700 rounded px-2 py-1 text-gray-200 w-28" />
                  <button onClick={regenerateBackup} disabled={!regenPw} className="text-green-400 hover:underline disabled:opacity-50">confirmar</button>
                  <button onClick={() => { setShowRegen(false); setRegenPw('') }} className="text-gray-500 hover:text-gray-300">cancelar</button>
                </span>
              )}
            </div>
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

        {/* One-time display of freshly generated backup codes */}
        {backupCodes && backupCodes.length > 0 && (
          <div className="flex flex-col gap-2 bg-amber-500/10 border border-amber-500/30 rounded-lg p-3">
            <p className="text-xs text-amber-300 flex items-center gap-1.5">
              <LifeBuoy className="w-3.5 h-3.5" /> Guarde estes códigos agora — cada um serve uma vez e não serão mostrados de novo.
            </p>
            <div className="grid grid-cols-2 gap-1 font-mono text-sm text-gray-100">
              {backupCodes.map(code => <span key={code} className="bg-gray-900 rounded px-2 py-1 text-center tracking-wider">{code}</span>)}
            </div>
            <div className="flex items-center gap-3">
              <button
                onClick={() => { navigator.clipboard?.writeText(backupCodes.join('\n')); setCopied(true) }}
                className="text-xs text-gray-300 hover:text-white inline-flex items-center gap-1">
                {copied ? <Check className="w-3.5 h-3.5 text-green-400" /> : <Copy className="w-3.5 h-3.5" />} Copiar todos
              </button>
              <button
                onClick={() => {
                  const blob = new Blob([backupCodes.join('\n') + '\n'], { type: 'text/plain' })
                  const url = URL.createObjectURL(blob)
                  const a = document.createElement('a'); a.href = url; a.download = 'jackui-backup-codes.txt'; a.click()
                  URL.revokeObjectURL(url)
                }}
                className="text-xs text-gray-300 hover:text-white">Baixar .txt</button>
              <button onClick={() => setBackupCodes(null)} className="text-xs text-gray-500 hover:text-gray-300 ml-auto">já guardei</button>
            </div>
          </div>
        )}
      </div>

      {/* Passkeys (WebAuthn) — biometria / security key */}
      {passkeysSupported && (
        <div className="flex flex-col gap-2 pt-3 border-t border-gray-700/60 max-w-sm">
          <span className="text-xs text-gray-400 flex items-center gap-1.5">
            <Fingerprint className="w-3.5 h-3.5 text-green-400" />
            Passkeys (biometria / chave de segurança) {passkeys.length > 0 ? `— ${passkeys.length}` : '— nenhuma'}
          </span>
          {passkeys.length > 0 && (
            <ul className="flex flex-col gap-1">
              {passkeys.map(pk => (
                <li key={pk.id} className="flex items-center gap-2 bg-gray-900 border border-gray-700 rounded-lg px-2 py-1">
                  <code className="text-[11px] text-gray-300 font-mono truncate flex-1" title={pk.id}>{pk.id.slice(0, 24)}…</code>
                  <button onClick={() => removePasskey(pk.id)} title="Remover" className="text-gray-500 hover:text-red-400 flex-shrink-0">
                    <Trash2 className="w-3.5 h-3.5" />
                  </button>
                </li>
              ))}
            </ul>
          )}
          <button onClick={addPasskey} disabled={pkBusy}
            className="self-start flex items-center gap-1.5 text-sm bg-gray-700 hover:bg-gray-600 disabled:opacity-50 text-gray-100 rounded-lg px-3 py-1.5">
            {pkBusy ? <Loader2 className="w-4 h-4 animate-spin" /> : <Fingerprint className="w-4 h-4" />} Adicionar passkey
          </button>
          {pkMsg && <span className="text-xs text-gray-400">{pkMsg}</span>}
        </div>
      )}

      {/* Active sessions */}
      <div className="flex flex-col gap-2 pt-3 border-t border-gray-700/60 max-w-sm">
        <span className="text-xs text-gray-400 flex items-center gap-1.5">
          <Monitor className="w-3.5 h-3.5" /> Sessões ativas {sessions.length > 0 ? `— ${sessions.length}` : ''}
        </span>
        {sessions.length === 0 ? (
          <span className="text-xs text-gray-600">Nenhuma sessão registrada.</span>
        ) : (
          <ul className="flex flex-col gap-1">
            {sessions.map(sess => (
              <li key={sess.id} className="flex items-center gap-2 bg-gray-900 border border-gray-700 rounded-lg px-2 py-1.5 text-xs">
                <div className="flex-1 min-w-0">
                  <div className="text-gray-300 flex items-center gap-1.5">
                    {sess.current && <span className="text-green-400">● esta sessão</span>}
                    {!sess.current && <span className="text-gray-500">○</span>}
                    {sess.remember && <span className="text-gray-500">· lembrar 30d</span>}
                  </div>
                  <div className="text-gray-600">
                    criada {new Date(sess.createdAt).toLocaleString()} · expira {new Date(sess.expiresAt).toLocaleDateString()}
                  </div>
                </div>
                {!sess.current && (
                  <button onClick={() => killSession(sess.id)} title="Encerrar" className="text-gray-500 hover:text-red-400 flex-shrink-0">
                    <Trash2 className="w-3.5 h-3.5" />
                  </button>
                )}
              </li>
            ))}
          </ul>
        )}
        {sessions.filter(s => !s.current).length > 0 && (
          <button onClick={killOthers}
            className="self-start flex items-center gap-1.5 text-sm bg-gray-700 hover:bg-gray-600 text-gray-100 rounded-lg px-3 py-1.5">
            <LogOut className="w-4 h-4" /> Encerrar outras sessões
          </button>
        )}
      </div>
    </section>
  )
}
