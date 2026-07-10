import { WifiOff } from 'lucide-react'
import { useTranslation } from 'react-i18next'
import { saveConfig, testJackettConnection } from '../../api/client'

type Props = {
  readonly setupUrl: string
  readonly setSetupUrl: (v: string) => void
  readonly setupKey: string
  readonly setSetupKey: (v: string) => void
  readonly setupTesting: boolean
  readonly setupError: string
  readonly setupTestOk: boolean
  readonly setShowJackettSetup: (v: boolean) => void
  readonly setSetupTestOk: (v: boolean) => void
  readonly runJackettSetup: (
    action: () => Promise<{ success: boolean; error?: string }>,
    onSuccess: () => void,
  ) => void
  readonly onConfigured: () => void
}

// Banner de primeira execução: pede URL + API key do Jackett quando não há
// indexadores configurados. Extraído do SearchPage (god-file); o estado e o
// runner vivem em useJackettSetup, aqui fica só o JSX.
export function JackettSetupPrompt({
  setupUrl, setSetupUrl, setupKey, setSetupKey,
  setupTesting, setupError, setupTestOk,
  setShowJackettSetup, setSetupTestOk, runJackettSetup, onConfigured,
}: Props) {
  const { t } = useTranslation()
  return (
    <div className="bg-amber-500/10 border border-amber-500/30 rounded-xl p-4 sm:p-6">
      <div className="flex items-start gap-3">
        <WifiOff className="w-6 h-6 text-amber-400 flex-shrink-0 mt-0.5" />
        <div className="flex-1 min-w-0">
          <h3 className="text-amber-700 dark:text-amber-300 font-medium text-sm mb-1">{t('search.no_indexers')}</h3>
          <p className="text-text-secondary text-xs mb-4">
            {t('search.jackett_setup_desc')}
          </p>
          <div className="flex flex-col sm:flex-row gap-2 mb-3">
            <input
              className="input-field flex-1 text-sm"
              placeholder={t('search.jackett_url_placeholder')}
              value={setupUrl}
              onChange={e => setSetupUrl(e.target.value)}
            />
            <input
              className="input-field flex-1 text-sm"
              placeholder={t('search.api_key_placeholder')}
              value={setupKey}
              onChange={e => setSetupKey(e.target.value)}
            />
          </div>
          {setupError && <p className="text-red-400 text-xs mb-2">{setupError}</p>}
          {setupTestOk && <p className="text-green-400 text-xs mb-2">{t('search.conn_ok')}</p>}
          <div className="flex gap-2">
            <button
              onClick={() => runJackettSetup(
                // Test-only: validate URL+key WITHOUT saving, so the user can
                // confirm the port is reachable before committing config.
                () => testJackettConnection({ url: setupUrl.trim(), apiKey: setupKey.trim() }),
                () => setSetupTestOk(true),
              )}
              disabled={setupTesting}
              className="btn-secondary text-sm px-4 py-2"
            >
              {setupTesting ? t('search.testing') : t('search.test')}
            </button>
            <button
              onClick={() => runJackettSetup(
                async () => {
                  await saveConfig({ port: 8989, jackett: { url: setupUrl.trim(), apiKey: setupKey.trim() }, downloadClients: [] })
                  return testJackettConnection()
                },
                () => { setShowJackettSetup(false); onConfigured() },
              )}
              disabled={setupTesting}
              className="btn-primary text-sm px-4 py-2"
            >
              {setupTesting ? t('search.testing') : t('search.save_and_test')}
            </button>
            <button
              onClick={() => setShowJackettSetup(false)}
              className="text-xs text-text-muted hover:text-text-primary px-3 py-2"
            >
              {t('search.ignore')}
            </button>
          </div>
        </div>
      </div>
    </div>
  )
}
