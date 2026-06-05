/**
 * Pure decision for the first-run "Jackett não configurado" prompt, extracted
 * from SearchPage so it can be unit-tested without a DOM (vitest runs in node).
 *
 * Two signals feed it:
 *   - statusJackett: the `/api/status` `jackett` field ('ok' when the live ping
 *     succeeds). A transient 5s ping timeout makes this non-'ok' even though the
 *     server is fully configured.
 *   - config: the `/api/config` outcome. `ok` mirrors HTTP response.ok. Because
 *     `/api/config` is admin-only, a non-admin (or any 4xx/5xx/network error)
 *     gets `ok: false` and no `jackettUrl`. "Unreadable" is NOT "unconfigured",
 *     so we must not prompt unless we positively read an empty/default config.
 */
export const DEFAULT_JACKETT_URL = 'http://localhost:9117'

export function shouldPromptJackettSetup(
  statusJackett: string | undefined,
  config: { ok: boolean; jackettUrl?: string; apiKeySet?: boolean },
): boolean {
  if (statusJackett === 'ok') return false
  // A ping TIMEOUT means reachable-but-slow, not unconfigured — never prompt on
  // it. This is the common false "Jackett não configurado": a slow indexer
  // pushes the 5s status ping over the edge on an already-working server.
  if (statusJackett?.startsWith('timeout')) return false
  // Only prompt on a positively-read empty/default config — never when the
  // config endpoint was unreadable, which would falsely prompt on a working,
  // already-configured server (e.g. a non-admin user, or a transient error).
  if (!config.ok) return false
  // A stored API key means Jackett WAS configured — even at the default URL
  // (running it on localhost:9117 is common). That is not "unconfigured".
  if (config.apiKeySet) return false
  const url = config.jackettUrl
  return !url || url === DEFAULT_JACKETT_URL
}
