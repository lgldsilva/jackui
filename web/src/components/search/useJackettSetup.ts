import { useState, useEffect } from 'react'
import { useTranslation } from 'react-i18next'
import { shouldPromptJackettSetup } from '../../lib/jackettSetup'
import { errMessage } from '../../lib/errMessage'

// Estado + lógica do prompt de configuração do Jackett (primeira execução).
// Extraído do SearchPage (god-file): agrupa o estado do formulário, o probe de
// /api/status e o runner compartilhado dos botões "Testar" / "Salvar e Testar".
export function useJackettSetup() {
  const { t } = useTranslation()
  // Jackett connection status — for first-run / config prompt
  const [showJackettSetup, setShowJackettSetup] = useState(false)
  const [setupUrl, setSetupUrl] = useState('')
  const [setupKey, setSetupKey] = useState('')
  const [setupTesting, setSetupTesting] = useState(false)
  const [setupError, setSetupError] = useState('')
  const [setupTestOk, setSetupTestOk] = useState(false)

  useEffect(() => {
    // Check if Jackett is actually configured before showing the setup prompt.
    // If the network request fails (transient Electron GPU crash), don't prompt —
    // the config might already be saved.
    fetch('/api/status')
      .then(r => r.json())
      .then(d => {
        if (d.jackett === 'ok') return
        // Only prompt if there's truly no config saved. /api/config is admin-only,
        // so capture response.ok: an unreadable config (non-admin 403, transient
        // error) must NOT be misread as "unconfigured" — see lib/jackettSetup.ts.
        fetch('/api/config')
          .then(async r => ({ ok: r.ok, body: r.ok ? await r.json() : {} }))
          .then(({ ok, body }) => {
            if (shouldPromptJackettSetup(d.jackett, { ok, jackettUrl: body?.jackett?.url, apiKeySet: body?.jackett?.apiKeySet })) {
              setShowJackettSetup(true)
            }
          })
          .catch(() => {})
      })
      .catch(() => {}) // network error — don't prompt, config might be saved
  }, [])

  // Shared runner for the Jackett setup prompt's "Testar" / "Salvar e Testar"
  // buttons: validates the URL, runs `action` (test-only or save+test) with one
  // transient-network retry, and manages the shared setup* UI state — so the two
  // buttons stay a single skeleton instead of duplicating the retry/try-catch.
  const runJackettSetup = async (
    action: () => Promise<{ success: boolean; error?: string }>,
    onSuccess: () => void,
  ) => {
    if (!setupUrl.trim()) { setSetupError(t('search.enter_jackett_url')); return }
    setSetupTesting(true); setSetupError(''); setSetupTestOk(false)
    const isNetErr = (e: unknown) => e instanceof Error && e.message.includes('Network Error')
    try {
      let d
      try { d = await action() }
      catch (err) {
        if (!isNetErr(err)) throw err
        await new Promise(r => setTimeout(r, 3000))
        d = await action()
      }
      if (d.success) onSuccess()
      else setSetupError(d.error || t('search.connect_failed'))
    } catch (err) {
      setSetupError(errMessage(err))
    }
    setSetupTesting(false)
  }

  return {
    showJackettSetup, setShowJackettSetup,
    setupUrl, setSetupUrl,
    setupKey, setSetupKey,
    setupTesting, setupError, setupTestOk, setSetupTestOk,
    runJackettSetup,
  }
}
