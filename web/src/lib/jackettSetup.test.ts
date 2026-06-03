import { describe, it, expect } from 'vitest'
import { shouldPromptJackettSetup } from './jackettSetup'

describe('shouldPromptJackettSetup (false "Jackett não configurado" regression)', () => {
  // Repro of the production bug: the search screen shows the setup prompt when
  // /api/status reports jackett != 'ok' (e.g. a transient 5s live-ping timeout)
  // AND /api/config does not yield a jackett.url. Because /api/config is
  // admin-only, a non-admin (or any transient 4xx/5xx/network error) gets an
  // empty/error body, which the current logic misreads as "no url saved" and
  // prompts — even though Jackett IS configured on the server. The prompt must
  // require POSITIVE proof of an empty/default config, not merely an unreadable one.

  it('does NOT prompt when /api/config is unreadable (admin-only 403 / error)', () => {
    // status degraded + config unreadable (ok:false, no url) → cannot conclude unconfigured.
    // CURRENT (buggy) behaviour returns true here — this assertion is the evidence.
    expect(shouldPromptJackettSetup('timeout (5s)', { ok: false, jackettUrl: undefined })).toBe(false)
  })

  it('does NOT prompt when status is degraded but a real url is configured', () => {
    expect(shouldPromptJackettSetup('down: dial tcp ...', { ok: true, jackettUrl: 'http://127.0.0.1:9117' })).toBe(false)
  })

  it('never prompts when jackett is ok', () => {
    expect(shouldPromptJackettSetup('ok', { ok: false, jackettUrl: undefined })).toBe(false)
  })

  it('DOES prompt only on a positively-readable empty/default config (genuine first run)', () => {
    expect(shouldPromptJackettSetup('down', { ok: true, jackettUrl: '' })).toBe(true)
    expect(shouldPromptJackettSetup('down', { ok: true, jackettUrl: 'http://localhost:9117' })).toBe(true)
  })
})
