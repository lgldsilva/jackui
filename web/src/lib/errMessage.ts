// errMessage extracts the most useful human-readable message from an unknown
// thrown value. The backend returns errors as JSON {"error": "..."}; axios
// surfaces that at err.response.data.error. We prefer it over err.message,
// which for an HTTP failure is the generic "Request failed with status code 500"
// (useless to the user). Falls back to err.message, then String(err).
//
// Extracted from DownloadsPage so every catch across the app can show the real
// backend message instead of re-inlining this chain (or dropping it entirely
// via `instanceof Error ? err.message : ...`).
export function errMessage(err: unknown): string {
  const ax = err as { response?: { data?: { error?: unknown } }; message?: unknown }
  const backend = ax?.response?.data?.error
  if (typeof backend === 'string' && backend) return backend
  if (typeof ax?.message === 'string' && ax.message) return ax.message
  return String(err)
}
