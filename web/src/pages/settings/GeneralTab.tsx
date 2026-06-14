import { useState, useEffect } from 'react'
import { useTranslation } from 'react-i18next'
import { Loader2, Wifi, CheckCircle, XCircle } from 'lucide-react'
import { AppConfig, getStatus } from '../../api/client'
import { useTransitionConfig, CROSSFADE_MIN, CROSSFADE_MAX, type TransitionMode } from '../../components/player/transition'

// TransitionCard — preferência de transição entre faixas de música (Off / Gapless
// / Crossfade) + duração do crossfade. Não-admin (é preferência do player, salva
// em localStorage). Só afeta áudio direct-play; HLS/vídeo seguem com corte normal.
function TransitionCard() {
  const { t } = useTranslation()
  const { mode, setMode, crossfadeSec, setSec } = useTransitionConfig()
  return (
    <section className="card flex flex-col gap-4">
      <h2 className="text-lg font-semibold text-text-primary">{t('player.transition.title')}</h2>
      <div>
        <select
          value={mode}
          onChange={e => setMode(e.target.value as TransitionMode)}
          className="bg-surface-tertiary border border-strong rounded-lg px-3 py-1.5 text-base sm:text-sm text-text-primary focus:outline-none focus:border-green-500 w-64"
        >
          <option value="off">{t('player.transition.off')}</option>
          <option value="gapless">{t('player.transition.gapless')}</option>
          <option value="crossfade">{t('player.transition.crossfade')}</option>
        </select>
      </div>
      {mode === 'crossfade' && (
        <div>
          <label htmlFor="crossfade-sec" className="block text-sm text-text-secondary mb-1.5">
            {t('player.transition.seconds', { sec: crossfadeSec })}
          </label>
          <input
            id="crossfade-sec" type="range" min={CROSSFADE_MIN} max={CROSSFADE_MAX} step={1}
            value={crossfadeSec}
            onChange={e => setSec(Number(e.target.value))}
            className="w-64"
          />
        </div>
      )}
      <p className="text-text-muted text-[11px]">{t('player.transition.hint')}</p>
    </section>
  )
}

function EnvBadge({ envVar }: { readonly envVar: string }) {
  return (
    <span
      className="ml-2 text-[10px] bg-amber-500/15 text-amber-400 border border-amber-500/30 px-1.5 py-0.5 rounded inline-flex items-center gap-1"
      title="Gerenciado por variável de ambiente"
    >
      <span className="opacity-70">ENV</span>
      <code className="font-mono">{envVar}</code>
    </span>
  )
}

function VersionInfo() {
  const [ver, setVer] = useState<{ version: string; commit: string; date: string; goVersion?: string } | null>(null)
  // Plataforma vem do Electron (getPlatform) — navigator.platform é deprecado (S1874).
  const [platform, setPlatform] = useState('')
  useEffect(() => {
    if (globalThis.electronAPI?.getAppVersion) {
      // App desktop: metadados vêm do main process do Electron.
      globalThis.electronAPI.getAppVersion().then(setVer).catch(() => {})
      globalThis.electronAPI.getPlatform?.().then(setPlatform).catch(() => {})
    } else {
      // Navegador: metadados de build vêm do endpoint público /status (sem
      // Electron, o getAppVersion não existe e o card ficava em "Carregando…").
      getStatus()
        .then(s => setVer({ version: s.version, commit: s.commit, date: s.buildTime, goVersion: s.goVersion }))
        .catch(() => {})
    }
  }, [])
  if (!ver) return <p className="animate-pulse">Carregando…</p>
  // Sem ldflags (dev / `go run`) os metadados vêm como "dev"/"unknown" — evita
  // "Invalid Date" e o slice de string vazia.
  const parsed = ver.date ? new Date(ver.date) : null
  const buildLabel = parsed && !Number.isNaN(parsed.getTime())
    ? parsed.toLocaleString('pt-BR')
    : (ver.date || '—')
  return (
    <>
      <p>Versão: <span className="text-text-primary">{ver.version || '—'}</span></p>
      <p>Commit: <span className="text-text-primary font-mono">{ver.commit ? ver.commit.slice(0, 12) : '—'}</span></p>
      <p>Build: <span className="text-text-primary">{buildLabel}</span></p>
      {ver.goVersion && <p>Go: <span className="text-text-primary">{ver.goVersion}</span></p>}
      {platform && <p>Plataforma: <span className="text-text-primary">{platform}</span></p>}
    </>
  )
}

// GeneralTab — the "Geral" Settings tab, extracted from the SettingsPage
// god-file. Language + About render for everyone; Jackett/Server/env-override
// cards only for admins (they need /api/config, which 403s for regular users).
export default function GeneralTab({ config, setConfig, isAdmin, testing, testResult, connectionMsg, onTestJackett }: {
  readonly config: AppConfig | null
  readonly setConfig: (c: AppConfig) => void
  readonly isAdmin: boolean
  readonly testing: boolean
  readonly testResult: 'success' | 'error' | null
  readonly connectionMsg: string
  readonly onTestJackett: () => void
}) {
  const { t, i18n } = useTranslation()
  return (
    <>
      <section className="card flex flex-col gap-4">
        <h2 className="text-lg font-semibold text-text-primary">{t('settings.language')}</h2>
        <div>
          <select
            value={i18n.language}
            onChange={e => {
              const newLang = e.target.value
              i18n.changeLanguage(newLang)
              localStorage.setItem('jackui_language', newLang)
            }}
            className="bg-surface-tertiary border border-strong rounded-lg px-3 py-1.5 text-base sm:text-sm text-text-primary focus:outline-none focus:border-green-500 w-64"
          >
            <option value="pt-BR">{t('settings.language_pt')}</option>
            <option value="en-US">{t('settings.language_en')}</option>
          </select>
        </div>
      </section>

      <TransitionCard />

      {isAdmin && config && (
        <>
          <section className="card flex flex-col gap-4">
            <h2 className="text-lg font-semibold text-text-primary">Jackett</h2>
            <div>
              <label htmlFor="jackett-url" className="block text-sm font-medium text-text-primary mb-1.5">
                URL {config.envOverrides?.JACKETT_URL && <EnvBadge envVar="JACKETT_URL" />}
              </label>
              <input id="jackett-url" type="url" value={config.jackett.url}
                onChange={e => setConfig({ ...config, jackett: { ...config.jackett, url: e.target.value } })}
                placeholder="http://localhost:9117" className="input-field" />
            </div>
            <div>
              <label htmlFor="jackett-apikey" className="block text-sm font-medium text-text-primary mb-1.5">
                API Key {config.envOverrides?.JACKETT_API_KEY && <EnvBadge envVar="JACKETT_API_KEY" />}
              </label>
              <input id="jackett-apikey" type="password" autoComplete="off" value={config.jackett.apiKey}
                onChange={e => setConfig({ ...config, jackett: { ...config.jackett, apiKey: e.target.value } })}
                placeholder={config.jackett.apiKeySet ? 'API key configurada — deixe vazio para manter' : 'Sua API key do Jackett'}
                className="input-field font-mono" />
            </div>
            <div className="flex items-center gap-3">
              <button onClick={onTestJackett} disabled={testing}
                className="btn-secondary flex items-center gap-2 disabled:opacity-50">
                {testing ? <Loader2 className="w-4 h-4 animate-spin" /> : <Wifi className="w-4 h-4" />}
                Testar conexão
              </button>
              {testResult && (
                <div className={`flex items-start gap-1.5 text-sm ${testResult === 'success' ? 'text-green-400' : 'text-red-400'}`}>
                  {testResult === 'success'
                    ? <CheckCircle className="w-4 h-4 flex-shrink-0 mt-0.5" />
                    : <XCircle className="w-4 h-4 flex-shrink-0 mt-0.5" />}
                  <span>{connectionMsg}</span>
                </div>
              )}
            </div>
          </section>

          <section className="card flex flex-col gap-4">
            <h2 className="text-lg font-semibold text-text-primary">Servidor</h2>
            <div>
              <label htmlFor="server-port" className="block text-sm font-medium text-text-primary mb-1.5">Porta</label>
              <input id="server-port" type="number" value={config.port}
                onChange={e => setConfig({ ...config, port: Number.parseInt(e.target.value) || 8989 })}
                className="input-field w-32" min={1} max={65535} />
            </div>
          </section>

          {/* Env overrides */}
          {config.envOverrides && Object.keys(config.envOverrides).length > 0 && (
            <section className="card flex flex-col gap-3">
              <h2 className="text-base font-semibold text-text-primary">
                <span>Variáveis de Ambiente</span>
                <span className="ml-2 text-[10px] text-amber-400 font-normal">(prioridade sobre config.yaml)</span>
              </h2>
              <div className="space-y-1.5">
                {Object.entries(config.envOverrides).map(([key, value]) => (
                  <div key={key} className="flex items-center gap-2 text-xs">
                    <code className="text-amber-400 font-mono">{key}</code>
                    <span className="text-text-muted">=</span>
                    <code className="text-text-primary font-mono break-all">{value}</code>
                  </div>
                ))}
              </div>
              <p className="text-text-muted text-[11px]">
                Estas variáveis sobrescrevem o config.yaml ao iniciar.
              </p>
            </section>
          )}
        </>
      )}

      {/* About */}
      <section className="card flex flex-col gap-3">
        <h2 className="text-base font-semibold text-text-primary">Sobre o JackUI</h2>
        <div className="space-y-1.5 text-xs text-text-secondary">
          {/* VersionInfo lida com ambos: Electron (getAppVersion) e
              navegador (GET /status). Antes só renderizava no Electron,
              então o navegador nunca via os metadados de build. */}
          <VersionInfo />
        </div>
      </section>
    </>
  )
}
