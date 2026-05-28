import { useState } from 'react'
import { useNavigate, useLocation } from 'react-router-dom'
import { LogIn, Loader2, AlertCircle } from 'lucide-react'
import { useAuth } from '../auth/AuthContext'

export default function LoginPage() {
  const { login } = useAuth()
  const nav = useNavigate()
  const location = useLocation()
  const from = (location.state as any)?.from?.pathname || '/'

  const [username, setUsername] = useState('')
  const [password, setPassword] = useState('')
  const [remember, setRemember] = useState(true)
  const [totp, setTotp] = useState('')
  const [mfaStep, setMfaStep] = useState(false)
  const [loading, setLoading] = useState(false)
  const [error, setError] = useState('')

  const submit = async (e: React.FormEvent) => {
    e.preventDefault()
    setLoading(true)
    setError('')
    try {
      await login(username, password, remember, totp)
      nav(from, { replace: true })
    } catch (err: any) {
      // Account has MFA → ask for the 6-digit code and resubmit.
      if (err?.response?.data?.mfaRequired) {
        setMfaStep(true)
        setError(totp ? 'Código inválido, tente de novo.' : '')
      } else {
        setError(err?.response?.data?.error || err.message || 'Falha no login')
      }
    } finally {
      setLoading(false)
    }
  }

  return (
    <div className="min-h-screen bg-gray-900 flex items-center justify-center px-4 safe-top safe-bottom">
      <div className="w-full max-w-sm">
        <div className="flex justify-center mb-6">
          <div className="flex items-center gap-2">
            <span className="text-3xl font-bold text-green-500">Jack</span>
            <span className="text-3xl font-bold text-gray-100">UI</span>
          </div>
        </div>

        <form
          onSubmit={submit}
          className="bg-gray-800 border border-gray-700 rounded-2xl p-6 flex flex-col gap-4 shadow-2xl"
        >
          <div>
            <label className="block text-sm text-gray-400 mb-1.5">Usuário</label>
            <input
              type="text"
              autoFocus
              autoComplete="username"
              value={username}
              onChange={e => setUsername(e.target.value)}
              required
              className="input-field"
            />
          </div>

          <div>
            <label className="block text-sm text-gray-400 mb-1.5">Senha</label>
            <input
              type="password"
              autoComplete="current-password"
              value={password}
              onChange={e => setPassword(e.target.value)}
              required
              className="input-field"
            />
          </div>

          {mfaStep && (
            <div>
              <label className="block text-sm text-gray-400 mb-1.5">Código MFA (app autenticador)</label>
              <input
                type="text"
                inputMode="numeric"
                autoFocus
                autoComplete="one-time-code"
                value={totp}
                onChange={e => setTotp(e.target.value.replace(/\D/g, '').slice(0, 6))}
                placeholder="000000"
                className="input-field tracking-widest text-center font-mono"
              />
            </div>
          )}

          <label className="flex items-center gap-2 cursor-pointer text-sm text-gray-300">
            <input
              type="checkbox"
              checked={remember}
              onChange={e => setRemember(e.target.checked)}
              className="w-4 h-4 accent-green-500"
            />
            Lembrar de mim por 30 dias
          </label>

          {error && (
            <div className="bg-red-500/10 border border-red-500/30 text-red-400 text-sm rounded-lg p-3 flex items-center gap-2">
              <AlertCircle className="w-4 h-4 flex-shrink-0" />
              {error}
            </div>
          )}

          <button
            type="submit"
            disabled={loading || !username || !password}
            className="btn-primary flex items-center justify-center gap-2 disabled:opacity-50"
          >
            {loading ? <Loader2 className="w-4 h-4 animate-spin" /> : <LogIn className="w-4 h-4" />}
            Entrar
          </button>
          <div className="flex items-center justify-between text-xs">
            <button type="button" onClick={() => nav('/register')} className="text-gray-400 hover:text-green-400">Criar conta</button>
            <button type="button" onClick={() => nav('/forgot-password')} className="text-gray-400 hover:text-green-400">Esqueci a senha</button>
          </div>
        </form>

        <p className="text-center text-xs text-gray-600 mt-4">
          JackUI — interface visual para Jackett + streaming de torrents
        </p>
      </div>
    </div>
  )
}
