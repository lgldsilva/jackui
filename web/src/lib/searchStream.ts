// Search SSE stream helpers: result dedupe + reconnection policy + EventSource
// orchestration. Lives outside SearchPage.tsx (god-file diet) and keeps the
// decision logic pure so vitest can cover it without a real EventSource.
//
// Why this exists: if the SSE connection drops BEFORE the `done` event (proxy
// read-timeout, flaky network), the old code gave up immediately — the backend
// kept collecting and saving results, so the history ended up with more items
// than the search ever showed. Now we retry with backoff (the backend's cache
// phase re-emits what already arrived; dedupe absorbs the replay) and only
// surface an error after the retry budget is exhausted.

export type StreamResultLike = {
  readonly infoHash?: string
  readonly tracker?: string
  readonly title?: string
  readonly size?: number
}

// resultKey mirrors the backend's dedupKey: infoHash when present (lowercase —
// encodings are already canonicalized server-side), else tracker|title|size for
// hash-less private-tracker entries. Empty string means "cannot dedupe".
export function resultKey(r: StreamResultLike): string {
  const hash = (r.infoHash ?? '').trim().toLowerCase()
  if (hash) return hash
  if (r.tracker && r.title) {
    return `${r.tracker.toLowerCase()}|${r.title.toLowerCase()}|${r.size ?? 0}`
  }
  return ''
}

// appendUnique returns the same array reference when the result is a duplicate
// (so React state updates can short-circuit), or a new array with it appended.
// Results without a dedup key are always appended — we can't tell them apart.
export function appendUnique<T extends StreamResultLike>(list: readonly T[], item: T): readonly T[] {
  const key = resultKey(item)
  if (key && list.some(existing => resultKey(existing) === key)) return list
  return [...list, item]
}

export const MAX_SSE_RETRIES = 3
export const BASE_RETRY_DELAY_MS = 1000

export type ReconnectDecision =
  | { readonly kind: 'retry'; readonly attempt: number; readonly delayMs: number }
  | { readonly kind: 'give-up' }

// nextReconnect is the pure retry policy: exponential backoff (1s, 2s, 4s)
// until the budget is spent. `attempt` is how many retries already happened.
export function nextReconnect(
  attempt: number,
  maxRetries: number = MAX_SSE_RETRIES,
  baseDelayMs: number = BASE_RETRY_DELAY_MS,
): ReconnectDecision {
  if (attempt >= maxRetries) return { kind: 'give-up' }
  return { kind: 'retry', attempt: attempt + 1, delayMs: baseDelayMs * 2 ** attempt }
}

// Minimal EventSource surface so tests can inject a fake.
export type EventSourceLike = {
  close(): void
  addEventListener(type: string, listener: (ev: MessageEvent) => void): void
}

export type SearchStreamCallbacks = {
  readonly onResult: (result: unknown) => void
  readonly onLive: () => void
  readonly onServerError: (message: string) => void
  readonly onDone: (summary: unknown) => void
  readonly onGiveUp: () => void
}

export type SearchStreamOptions = {
  readonly makeEventSource?: (url: string) => EventSourceLike
  readonly maxRetries?: number
  readonly baseDelayMs?: number
}

export type SearchStreamHandle = { close(): void }

// SSE payloads come from the network — a malformed/empty frame must not throw
// out of a listener (an uncaught exception there would leave the tab stuck
// "searching" forever). The generic connection-`error` event isn't even a
// MessageEvent (no .data), so this also disambiguates it from the backend's
// application-level `error` event, which does carry data.
function parseSSE(raw: unknown): Record<string, unknown> | null {
  if (typeof raw !== 'string' || raw === '') return null
  try { return JSON.parse(raw) } catch { return null }
}

// openSearchStream owns the EventSource lifecycle for one search session:
// it forwards events to the callbacks and, when the connection drops before
// `done`, recreates the source with backoff instead of giving up. Receiving
// data resets the retry budget (a long flaky session shouldn't die because of
// three drops spread over minutes). close() ends everything for good — used
// by the Stop button, tab close and unmount.
export function openSearchStream(
  url: string,
  cb: SearchStreamCallbacks,
  opts: SearchStreamOptions = {},
): SearchStreamHandle {
  const makeEventSource = opts.makeEventSource ?? ((u: string) => new EventSource(u))
  const maxRetries = opts.maxRetries ?? MAX_SSE_RETRIES
  const baseDelayMs = opts.baseDelayMs ?? BASE_RETRY_DELAY_MS

  let es: EventSourceLike | null = null
  let closed = false
  let attempts = 0
  let timer: ReturnType<typeof setTimeout> | null = null

  const handleDrop = () => {
    // Take over from the native auto-reconnect so the backoff is ours.
    es?.close()
    es = null
    const decision = nextReconnect(attempts, maxRetries, baseDelayMs)
    if (decision.kind === 'give-up') {
      closed = true
      cb.onGiveUp()
      return
    }
    attempts = decision.attempt
    timer = setTimeout(connect, decision.delayMs)
  }

  const connect = () => {
    if (closed) return
    const source = makeEventSource(url)
    es = source

    source.addEventListener('result', (e) => {
      if (closed) return
      attempts = 0 // data is flowing — reset the retry budget
      const result = parseSSE(e.data)
      if (result) cb.onResult(result)
    })

    source.addEventListener('progress', (e) => {
      if (closed) return
      attempts = 0
      const data = parseSSE(e.data)
      if (data?.phase === 'live') cb.onLive()
    })

    source.addEventListener('done', (e) => {
      if (closed) return
      closed = true
      source.close()
      cb.onDone(parseSSE(e.data))
    })

    source.addEventListener('error', (e) => {
      if (closed) return
      const data = parseSSE(e.data)
      if (data) {
        // Application-level error from the backend — the stream is still
        // alive and `done` will follow; just surface the message.
        cb.onServerError(typeof data.message === 'string' && data.message ? data.message : 'Erro na busca')
        return
      }
      handleDrop()
    })
  }

  connect()

  return {
    close() {
      closed = true
      if (timer !== null) clearTimeout(timer)
      es?.close()
      es = null
    },
  }
}
