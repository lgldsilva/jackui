import { api } from './http'

// ── Auth: account + admin user management ────────────────────────────────────
export type AdminUser = {
  id: number
  username: string
  email: string
  role: 'admin' | 'user' | 'guest'
  status: 'active' | 'pending' | 'disabled'
  emailVerified: boolean
  ntfyTopic: string
  createdAt: string
}

// refresh (optional) is the caller's own refresh token: when sent, the backend
// revokes every OTHER session after the change (keeps this device logged in).
export const changePassword = async (current: string, next: string, refresh?: string): Promise<number> => {
  const { data } = await api.post<{ revoked: number }>('/auth/password', { current, new: next, refresh: refresh || '' })
  return data.revoked ?? 0
}
export const changeEmail = async (password: string, email: string): Promise<void> => {
  await api.post('/auth/email', { password, email })
}
export const mfaEnroll = async (): Promise<{ secret: string; uri: string }> => {
  const { data } = await api.post<{ secret: string; uri: string }>('/auth/mfa/enroll')
  return data
}
export const mfaVerify = async (code: string): Promise<string[]> => {
  const { data } = await api.post<{ backupCodes: string[] }>('/auth/mfa/verify', { code })
  return data.backupCodes || []
}
export const mfaDisable = async (password: string): Promise<void> => {
  await api.post('/auth/mfa/disable', { password })
}
export const mfaBackupCodesRemaining = async (): Promise<number> => {
  const { data } = await api.get<{ remaining: number }>('/auth/mfa/backup-codes')
  return data.remaining ?? 0
}
export const mfaRegenerateBackupCodes = async (password: string): Promise<string[]> => {
  const { data } = await api.post<{ backupCodes: string[] }>('/auth/mfa/backup-codes/regenerate', { password })
  return data.backupCodes || []
}
export const adminListUsers = async (): Promise<AdminUser[]> => {
  const { data } = await api.get<AdminUser[]>('/auth/users')
  return data || []
}
export const adminCreateUser = async (username: string, password: string, role: 'admin' | 'user' | 'guest'): Promise<void> => {
  await api.post('/auth/users', { username, password, role })
}
export const adminDeleteUser = async (id: number): Promise<void> => {
  await api.delete(`/auth/users/${id}`)
}
export const adminSetUserStatus = async (id: number, status: 'active' | 'pending' | 'disabled'): Promise<void> => {
  await api.patch(`/auth/users/${id}/status`, { status })
}
export const adminInvite = async (email?: string): Promise<string> => {
  const { data } = await api.post<{ link: string }>('/auth/users/invite', { email: email || '' })
  return data.link
}
// password empty → backend issues a 1h single-use reset link instead.
export const adminResetPassword = async (id: number, password?: string): Promise<{ link?: string }> => {
  const { data } = await api.post<{ link?: string }>(`/auth/users/${id}/reset-password`, { password: password || '' })
  return data
}
export const adminListUserSessions = async (id: number): Promise<SessionInfo[]> => {
  const { data } = await api.get<{ sessions: SessionInfo[] }>(`/auth/users/${id}/sessions`)
  return data.sessions || []
}
export const adminRevokeUserSession = async (id: number, sid: string): Promise<void> => {
  await api.delete(`/auth/users/${id}/sessions/${encodeURIComponent(sid)}`)
}
export const adminRevokeUserSessions = async (id: number): Promise<void> => {
  await api.delete(`/auth/users/${id}/sessions`)
}

// ── Notification settings ──────────────────────────────────────────────────────
export const setNtfyTopic = async (topic: string): Promise<void> => {
  await api.post('/user/ntfy-topic', { topic })
}
export const notifyTest = async (): Promise<void> => {
  await api.post('/user/notify-test')
}

// ── Active sessions ──────────────────────────────────────────────────────────
export type SessionInfo = {
  id: string
  createdAt: string
  expiresAt: string
  remember: boolean
  current: boolean
  userAgent: string
  ip: string
}
export const listSessions = async (currentRefresh: string): Promise<SessionInfo[]> => {
  const { data } = await api.post<{ sessions: SessionInfo[] }>('/auth/sessions', { refresh: currentRefresh })
  return data.sessions || []
}
export const revokeSession = async (id: string): Promise<void> => {
  await api.delete(`/auth/sessions/${encodeURIComponent(id)}`)
}
export const revokeOtherSessions = async (currentRefresh: string): Promise<number> => {
  const { data } = await api.post<{ revoked: number }>('/auth/sessions/revoke-others', { refresh: currentRefresh })
  return data.revoked ?? 0
}

// Public auth flows (no token needed). These bypass the axios auth interceptor
// concerns since they're called from unauthenticated pages.
export const registerAccount = async (username: string, email: string, password: string, invite?: string) => {
  const { data } = await api.post<{ status: string; invited: boolean; message: string }>('/auth/register', { username, email, password, invite: invite || '' })
  return data
}
export const verifyEmail = async (token: string): Promise<void> => {
  await api.post('/auth/verify-email', { token })
}
export const forgotPassword = async (email: string): Promise<string> => {
  const { data } = await api.post<{ message: string }>('/auth/forgot', { email })
  return data.message
}
export const resetPassword = async (token: string, password: string): Promise<void> => {
  await api.post('/auth/reset', { token, password })
}

// ── Passkey (WebAuthn) ───────────────────────────────────────────────────────
// WebAuthn moves binary blobs (challenge, credential ids, signatures) over JSON
// as base64url. The browser API works with ArrayBuffers, so every "begin" reply
// is decoded into buffers before navigator.credentials.{create,get}, and the
// authenticator's response is re-encoded to base64url before posting "finish".

const b64urlToBuf = (s: string): ArrayBuffer => {
  const pad = s.length % 4 === 0 ? '' : '='.repeat(4 - (s.length % 4))
  const bin = atob((s + pad).replaceAll('-', '+').replaceAll('_', '/'))
  const buf = new Uint8Array(bin.length)
  for (let i = 0; i < bin.length; i++) buf[i] = bin.codePointAt(i) ?? 0
  return buf.buffer
}
const bufToB64url = (buf: ArrayBuffer): string => {
  const bytes = new Uint8Array(buf)
  let bin = ''
  for (const byte of bytes) bin += String.fromCodePoint(byte)
  return btoa(bin).replaceAll('+', '-').replaceAll('/', '_').replaceAll('=', '')
}

export function isPasskeySupported(): boolean {
  return typeof globalThis !== 'undefined' && !!globalThis.PublicKeyCredential && !!navigator.credentials?.create
}

export type PasskeyInfo = { id: string }
export const passkeyList = async (): Promise<PasskeyInfo[]> => {
  const { data } = await api.get<{ passkeys: PasskeyInfo[] }>('/auth/passkey')
  return data.passkeys || []
}
export const passkeyDelete = async (id: string): Promise<void> => {
  await api.delete(`/auth/passkey/${encodeURIComponent(id)}`)
}

// passkeyRegister runs the full enrollment ceremony (authenticated). Throws on
// user cancellation or authenticator error — caller surfaces the message.
export const passkeyRegister = async (): Promise<void> => {
  const { data } = await api.post<{ options: any; session: string }>('/auth/passkey/register/begin')
  const pk = data.options.publicKey
  pk.challenge = b64urlToBuf(pk.challenge)
  pk.user.id = b64urlToBuf(pk.user.id)
  if (Array.isArray(pk.excludeCredentials)) {
    pk.excludeCredentials = pk.excludeCredentials.map((c: any) => ({ ...c, id: b64urlToBuf(c.id) }))
  }
  const cred = (await navigator.credentials.create({ publicKey: pk })) as PublicKeyCredential | null
  if (!cred) throw new Error('passkey cancelada')
  const att = cred.response as AuthenticatorAttestationResponse
  const body = {
    id: cred.id,
    rawId: bufToB64url(cred.rawId),
    type: cred.type,
    response: {
      attestationObject: bufToB64url(att.attestationObject),
      clientDataJSON: bufToB64url(att.clientDataJSON),
    },
  }
  await api.post('/auth/passkey/register/finish', body, { params: { session: data.session } })
}

export type PasskeyTokenBundle = {
  access: string
  refresh: string
  expiresAt: string
  user: any
}

// passkeyAuthenticate runs the login assertion ceremony (public) and returns the
// token bundle. The caller (AuthContext) persists the tokens + sets the user.
export const passkeyAuthenticate = async (username: string, remember: boolean): Promise<PasskeyTokenBundle> => {
  const { data } = await api.post<{ options: any; session: string }>('/auth/passkey/login/begin', { username })
  const pk = data.options.publicKey
  pk.challenge = b64urlToBuf(pk.challenge)
  if (Array.isArray(pk.allowCredentials)) {
    pk.allowCredentials = pk.allowCredentials.map((c: any) => ({ ...c, id: b64urlToBuf(c.id) }))
  }
  const assertion = (await navigator.credentials.get({ publicKey: pk })) as PublicKeyCredential | null
  if (!assertion) throw new Error('passkey cancelada')
  const r = assertion.response as AuthenticatorAssertionResponse
  const body = {
    id: assertion.id,
    rawId: bufToB64url(assertion.rawId),
    type: assertion.type,
    response: {
      authenticatorData: bufToB64url(r.authenticatorData),
      clientDataJSON: bufToB64url(r.clientDataJSON),
      signature: bufToB64url(r.signature),
      userHandle: r.userHandle ? bufToB64url(r.userHandle) : '',
    },
  }
  const { data: bundle } = await api.post<PasskeyTokenBundle>('/auth/passkey/login/finish', body, {
    params: { username, session: data.session, remember: remember ? '1' : '' },
  })
  return bundle
}
