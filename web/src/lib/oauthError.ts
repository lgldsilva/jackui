// Maps Google OAuth callback slugs (?oauthError=) onto localized login keys.
// Unknown slugs fall back to the generic message so a new backend slug cannot
// leak its raw identifier into the UI.
export function resolveOAuthError(slug: string, t: (key: string) => string): string {
  const key = `login.oauth_error_${slug}`
  const msg = t(key)
  return msg === key ? t('login.oauth_error_generic') : msg
}
