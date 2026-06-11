import { useState, useEffect, useCallback } from 'react'
import { useTranslation } from 'react-i18next'
import { Loader2, KeyRound, User, ShieldCheck, ShieldOff, Copy, Check, Fingerprint, Trash2, LifeBuoy, RefreshCw, Mail } from 'lucide-react'
import { changePassword, changeEmail, mfaEnroll, mfaVerify, mfaDisable, mfaBackupCodesRemaining, mfaRegenerateBackupCodes, isPasskeySupported, passkeyList, passkeyRegister, passkeyDelete, PasskeyInfo } from '../api/client'
import { useAuth, getRefreshToken } from '../auth/AuthContext'

// AccountCard — self-service: shows who you are and lets you change your own
// password (verifying the current one). Visible to every logged-in user.
export default function AccountCard() {
  const { t } = useTranslation()
  const { user, refresh } = useAuth()
  const [current, setCurrent] = useState('')
  const [next, setNext] = useState('')
  const [confirm, setConfirm] = useState('')
  const [busy, setBusy] = useState(false)
  const [msg, setMsg] = useState<{ ok: boolean; text: string } | null>(null)

  // Email change state
  const [newEmail, setNewEmail] = useState('')
  const [emailPw, setEmailPw] = useState('')
  const [emailBusy, setEmailBusy] = useState(false)
  const [emailMsg, setEmailMsg] = useState<{ ok: boolean; text: string } | null>(null)
  // Optimistic: after a successful change the context user is stale until the
  // next /auth/me, so mirror the new (unverified) address locally.
  const [emailOverride, setEmailOverride] = useState<string | null>(null)

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

  const loadBackupRemaining = useCallback(async () => {
    if (!user?.mfaEnabled) { setBackupRemaining(null); return }
    try { setBackupRemaining(await mfaBackupCodesRemaining()) } catch { /* ignore */ }
  }, [user?.mfaEnabled])

  useEffect(() => { loadBackupRemaining() }, [loadBackupRemaining])

  if (!user) return null

  const displayEmail = emailOverride ?? user.email
  // After a change the backend resets verification, so the override is always unverified.
  const emailUnverified = emailOverride !== null || user.emailVerified === false

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
      // Sending our refresh token revokes every OTHER session server-side.
      const revoked = await changePassword(current, next, getRefreshToken())
      setMsg({ ok: true, text: revoked > 0 ? t('account.password_changed_revoked', { count: revoked }) : t('account.password_changed') })
      setCurrent(''); setNext(''); setConfirm('')
    } catch (e: any) {
      setMsg({ ok: false, text: e?.response?.data?.error || 'Falha ao alterar a senha.' })
    } finally { setBusy(false) }
  }

  const submitEmail = async () => {
    setEmailMsg(null)
    setEmailBusy(true)
    try {
      await changeEmail(emailPw, newEmail)
      setEmailOverride(newEmail.trim().toLowerCase())
      setEmailMsg({ ok: true, text: t('account.email_updated') })
      setNewEmail(''); setEmailPw('')
    } catch (e: any) {
      setEmailMsg({ ok: false, text: e?.response?.data?.error || t('account.email_update_failed') })
    } finally { setEmailBusy(false) }
  }

  return (
    <section className="card flex flex-col gap-3">
      <h2 className="text-lg font-semibold text-text-primary flex items-center gap-2"><User className="w-5 h-5" /> Conta</h2>
      <p className="text-sm text-text-secondary flex items-center gap-1.5 flex-wrap">
        <span>{user.username}{displayEmail ? ` · ${displayEmail}` : ''} · {user.role === 'admin' ? 'Admin' : 'Usuário'}</span>
        {displayEmail && emailUnverified && (
          <span className="text-[10px] bg-amber-500/15 text-amber-500 border border-amber-500/30 px-1.5 py-0.5 rounded">
            {t('account.email_unverified')}
          </span>
        )}
      </p>

      {/* Trocar e-mail */}
      <div className="flex flex-col gap-2 max-w-sm">
        <label className="text-xs text-text-secondary flex items-center gap-1.5"><Mail className="w-3.5 h-3.5" /> {t('account.change_email')}</label>
        <input type="email" value={newEmail} onChange={e => setNewEmail(e.target.value)} placeholder={t('account.new_email_placeholder')} autoComplete="email"
          className="bg-surface border border-default rounded-lg px-3 py-2 text-sm text-text-primary focus:outline-none focus:border-green-500" />
        <input type="password" value={emailPw} onChange={e => setEmailPw(e.target.value)} placeholder={t('account.confirm_password_placeholder')} autoComplete="current-password"
          className="bg-surface border border-default rounded-lg px-3 py-2 text-sm text-text-primary focus:outline-none focus:border-green-500" />
        <div className="flex items-center gap-3">
          <button onClick={submitEmail} disabled={emailBusy || !newEmail || !emailPw}
            className="flex items-center gap-1.5 text-sm bg-green-600 hover:bg-green-500 disabled:opacity-50 text-white rounded-lg px-3 py-1.5">
            {emailBusy ? <Loader2 className="w-4 h-4 animate-spin" /> : <Mail className="w-4 h-4" />} {t('account.save')}
          </button>
          {emailMsg && <span className={`text-xs ${emailMsg.ok ? 'text-green-400' : 'text-red-400'}`}>{emailMsg.text}</span>}
        </div>
      </div>

      <div className="flex flex-col gap-2 max-w-sm pt-3 border-t border-default/60">
        <label className="text-xs text-text-secondary flex items-center gap-1.5"><KeyRound className="w-3.5 h-3.5" /> Trocar senha</label>
        <input type="password" value={current} onChange={e => setCurrent(e.target.value)} placeholder="Senha atual" autoComplete="current-password"
          className="bg-surface border border-default rounded-lg px-3 py-2 text-sm text-text-primary focus:outline-none focus:border-green-500" />
        <input type="password" value={next} onChange={e => setNext(e.target.value)} placeholder="Nova senha" autoComplete="new-password"
          className="bg-surface border border-default rounded-lg px-3 py-2 text-sm text-text-primary focus:outline-none focus:border-green-500" />
        <input type="password" value={confirm} onChange={e => setConfirm(e.target.value)} placeholder="Confirmar nova senha" autoComplete="new-password"
          className="bg-surface border border-default rounded-lg px-3 py-2 text-sm text-text-primary focus:outline-none focus:border-green-500" />
        <div className="flex items-center gap-3">
          <button onClick={submit} disabled={busy || !current || !next}
            className="flex items-center gap-1.5 text-sm bg-green-600 hover:bg-green-500 disabled:opacity-50 text-white rounded-lg px-3 py-1.5">
            {busy ? <Loader2 className="w-4 h-4 animate-spin" /> : <KeyRound className="w-4 h-4" />} Salvar
          </button>
          {msg && <span className={`text-xs ${msg.ok ? 'text-green-400' : 'text-red-400'}`}>{msg.text}</span>}
        </div>
      </div>

      {/* MFA (TOTP) — opt-in */}
      <div className="flex flex-col gap-2 pt-3 border-t border-default/60 max-w-sm">
        <span className="text-xs text-text-secondary flex items-center gap-1.5">
          {user.mfaEnabled ? <ShieldCheck className="w-3.5 h-3.5 text-green-400" /> : <ShieldOff className="w-3.5 h-3.5" />}
          Verificação em duas etapas (TOTP) {user.mfaEnabled ? '— ativa' : '— inativa'}
        </span>

{(() => {
          if (user.mfaEnabled) return <div className="flex flex-col gap-2">
            <div className="flex items-center gap-2">
              <input type="password" value={mfaPw} onChange={e => setMfaPw(e.target.value)} placeholder="senha p/ desativar" autoComplete="current-password"
                className="bg-surface border border-default rounded-lg px-3 py-1.5 text-sm text-text-primary flex-1" />
              <button onClick={disableMfa} disabled={!mfaPw} className="text-sm bg-surface-tertiary hover:bg-surface-tertiary disabled:opacity-50 text-text-primary rounded-lg px-3 py-1.5">Desativar</button>
            </div>
            <div className="flex items-center gap-2 flex-wrap text-xs text-text-secondary">
              <LifeBuoy className="w-3.5 h-3.5" />
              <span>Códigos de recuperação: {backupRemaining ?? '—'} restantes</span>
              {backupRemaining !== null && backupRemaining <= 2 && (
                <span className="text-amber-400">— poucos! gere novos</span>
              )}
              {showRegen ? (
                <span className="inline-flex items-center gap-1">
                  <input type="password" value={regenPw} onChange={e => setRegenPw(e.target.value)} placeholder="senha" autoComplete="current-password"
                    className="bg-surface border border-default rounded px-2 py-1 text-text-primary w-28" />
                  <button onClick={regenerateBackup} disabled={!regenPw} className="text-green-400 hover:underline disabled:opacity-50">confirmar</button>
                  <button onClick={() => { setShowRegen(false); setRegenPw('') }} className="text-text-muted hover:text-text-primary">cancelar</button>
                </span>
              ) : (
                <button onClick={() => setShowRegen(true)} className="text-blue-400 hover:underline inline-flex items-center gap-1">
                  <RefreshCw className="w-3 h-3" /> gerar novos
                </button>
              )}
            </div>
          </div>
          if (enroll) return <div className="flex flex-col gap-2">
            <p className="text-xs text-text-secondary">Adicione no app autenticador (escaneie ou digite o segredo), depois informe o código:</p>
            <div className="flex items-center gap-2 bg-surface border border-default rounded-lg px-2 py-1">
              <code className="text-xs text-text-primary font-mono truncate flex-1 min-w-0">{enroll.secret}</code>
              <button onClick={() => { navigator.clipboard?.writeText(enroll.secret); setCopied(true) }} title="Copiar segredo"
                className="text-text-secondary hover:text-text-primary flex-shrink-0">{copied ? <Check className="w-4 h-4 text-green-400" /> : <Copy className="w-4 h-4" />}</button>
            </div>
            <a href={enroll.uri} className="text-[11px] text-blue-400 hover:underline truncate" title={enroll.uri}>abrir no app (otpauth://)</a>
            <div className="flex items-center gap-2">
              <input value={mfaCode} onChange={e => setMfaCode(e.target.value.replaceAll(/\D/g, '').slice(0, 6))} placeholder="000000" inputMode="numeric"
                className="bg-surface border border-default rounded-lg px-3 py-1.5 text-sm text-text-primary font-mono tracking-widest w-28 text-center" />
              <button onClick={confirmEnroll} disabled={mfaCode.length !== 6} className="text-sm bg-green-600 hover:bg-green-500 disabled:opacity-50 text-white rounded-lg px-3 py-1.5">Confirmar</button>
              <button onClick={() => { setEnroll(null); setMfaCode('') }} className="text-xs text-text-muted hover:text-text-primary">cancelar</button>
            </div>
          </div>
          return <button onClick={startEnroll} className="self-start flex items-center gap-1.5 text-sm bg-surface-tertiary hover:bg-surface-tertiary text-text-primary rounded-lg px-3 py-1.5">
            <ShieldCheck className="w-4 h-4" /> Ativar MFA
          </button>
        })()}
        {mfaMsg && <span className="text-xs text-text-secondary">{mfaMsg}</span>}

        {/* One-time display of freshly generated backup codes */}
        {backupCodes && backupCodes.length > 0 && (
          <div className="flex flex-col gap-2 bg-amber-500/10 border border-amber-500/30 rounded-lg p-3">
            <p className="text-xs text-amber-700 dark:text-amber-300 flex items-center gap-1.5">
              <LifeBuoy className="w-3.5 h-3.5" /> Guarde estes códigos agora — cada um serve uma vez e não serão mostrados de novo.
            </p>
            <div className="grid grid-cols-1 min-[380px]:grid-cols-2 gap-1 font-mono text-sm text-text-primary">
              {backupCodes.map(code => <span key={code} className="bg-surface rounded px-2 py-1 text-center tracking-wider">{code}</span>)}
            </div>
            <div className="flex items-center gap-3">
              <button
                onClick={() => { navigator.clipboard?.writeText(backupCodes.join('\n')); setCopied(true) }}
                className="text-xs text-text-secondary hover:text-text-primary inline-flex items-center gap-1">
                {copied ? <Check className="w-3.5 h-3.5 text-green-400" /> : <Copy className="w-3.5 h-3.5" />} Copiar todos
              </button>
              <button
                onClick={() => {
                  const blob = new Blob([backupCodes.join('\n') + '\n'], { type: 'text/plain' })
                  const url = URL.createObjectURL(blob)
                  const a = document.createElement('a'); a.href = url; a.download = 'jackui-backup-codes.txt'; a.click()
                  URL.revokeObjectURL(url)
                }}
                className="text-xs text-text-secondary hover:text-text-primary">Baixar .txt</button>
              <button onClick={() => setBackupCodes(null)} className="text-xs text-text-muted hover:text-text-primary ml-auto">já guardei</button>
            </div>
          </div>
        )}
      </div>

      {/* Passkeys (WebAuthn) — biometria / security key */}
      {passkeysSupported && (
        <div className="flex flex-col gap-2 pt-3 border-t border-default/60 max-w-sm">
          <span className="text-xs text-text-secondary flex items-center gap-1.5">
            <Fingerprint className="w-3.5 h-3.5 text-green-400" />
            Passkeys (biometria / chave de segurança) {passkeys.length > 0 ? `— ${passkeys.length}` : '— nenhuma'}
          </span>
          {passkeys.length > 0 && (
            <ul className="flex flex-col gap-1">
              {passkeys.map(pk => (
                <li key={pk.id} className="flex items-center gap-2 bg-surface border border-default rounded-lg px-2 py-1">
                  <code className="text-[11px] text-text-primary font-mono truncate flex-1 min-w-0" title={pk.id}>{pk.id.slice(0, 24)}…</code>
                  <button onClick={() => removePasskey(pk.id)} title="Remover" className="text-text-muted hover:text-red-400 flex-shrink-0">
                    <Trash2 className="w-3.5 h-3.5" />
                  </button>
                </li>
              ))}
            </ul>
          )}
          <button onClick={addPasskey} disabled={pkBusy}
            className="self-start flex items-center gap-1.5 text-sm bg-surface-tertiary hover:bg-surface-tertiary disabled:opacity-50 text-text-primary rounded-lg px-3 py-1.5">
            {pkBusy ? <Loader2 className="w-4 h-4 animate-spin" /> : <Fingerprint className="w-4 h-4" />} Adicionar passkey
          </button>
          {pkMsg && <span className="text-xs text-text-secondary">{pkMsg}</span>}
        </div>
      )}

    </section>
  )
}
