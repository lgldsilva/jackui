import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import {
  resultKey, appendUnique, nextReconnect, openSearchStream,
  MAX_SSE_RETRIES, BASE_RETRY_DELAY_MS,
  type EventSourceLike, type SearchStreamCallbacks,
} from './searchStream'

describe('resultKey', () => {
  it('prefers infoHash, lowercased', () => {
    expect(resultKey({ infoHash: 'ABCDEF', title: 'x', tracker: 't' })).toBe('abcdef')
  })

  it('falls back to tracker|title|size for hash-less results', () => {
    expect(resultKey({ tracker: 'Amigos', title: 'Filme 1080p', size: 42 }))
      .toBe('amigos|filme 1080p|42')
  })

  it('treats missing size as 0 in the fallback', () => {
    expect(resultKey({ tracker: 'T', title: 'A' })).toBe('t|a|0')
  })

  it('returns empty when nothing identifies the result', () => {
    expect(resultKey({})).toBe('')
    expect(resultKey({ infoHash: '  ' })).toBe('')
  })
})

describe('appendUnique', () => {
  it('appends a new result', () => {
    const list = appendUnique([], { infoHash: 'aaa', title: 'one' })
    expect(list).toHaveLength(1)
  })

  it('returns the SAME reference on a duplicate (replayed cache phase)', () => {
    const base = appendUnique([], { infoHash: 'aaa', title: 'one' })
    const after = appendUnique(base, { infoHash: 'AAA', title: 'one again' })
    expect(after).toBe(base)
  })

  it('dedupes hash-less results via tracker|title|size', () => {
    const base = appendUnique([], { tracker: 'T', title: 'A', size: 1 })
    expect(appendUnique(base, { tracker: 't', title: 'a', size: 1 })).toBe(base)
    expect(appendUnique(base, { tracker: 't', title: 'a', size: 2 })).toHaveLength(2)
  })

  it('always appends results with no dedup key', () => {
    const base = appendUnique([], {})
    expect(appendUnique(base, {})).toHaveLength(2)
  })
})

describe('nextReconnect', () => {
  it('backs off exponentially until the budget is spent', () => {
    expect(nextReconnect(0)).toEqual({ kind: 'retry', attempt: 1, delayMs: BASE_RETRY_DELAY_MS })
    expect(nextReconnect(1)).toEqual({ kind: 'retry', attempt: 2, delayMs: BASE_RETRY_DELAY_MS * 2 })
    expect(nextReconnect(2)).toEqual({ kind: 'retry', attempt: 3, delayMs: BASE_RETRY_DELAY_MS * 4 })
    expect(nextReconnect(MAX_SSE_RETRIES)).toEqual({ kind: 'give-up' })
  })

  it('honours a custom budget and base delay', () => {
    expect(nextReconnect(0, 1, 500)).toEqual({ kind: 'retry', attempt: 1, delayMs: 500 })
    expect(nextReconnect(1, 1, 500)).toEqual({ kind: 'give-up' })
  })
})

// ────────────────────────────────────────────────────────────────────────────
// openSearchStream orchestration, driven by a fake EventSource.

class FakeEventSource implements EventSourceLike {
  listeners = new Map<string, Array<(ev: MessageEvent) => void>>()
  closeCount = 0

  addEventListener(type: string, listener: (ev: MessageEvent) => void) {
    const list = this.listeners.get(type) ?? []
    list.push(listener)
    this.listeners.set(type, list)
  }

  close() { this.closeCount++ }

  emit(type: string, data?: string) {
    for (const l of this.listeners.get(type) ?? []) {
      l({ data } as MessageEvent)
    }
  }
}

function makeHarness() {
  const sources: FakeEventSource[] = []
  const cb: SearchStreamCallbacks = {
    onResult: vi.fn(),
    onLive: vi.fn(),
    onServerError: vi.fn(),
    onDone: vi.fn(),
    onGiveUp: vi.fn(),
  }
  const handle = openSearchStream('/api/search/stream?q=x', cb, {
    makeEventSource: () => {
      const es = new FakeEventSource()
      sources.push(es)
      return es
    },
  })
  return { sources, cb, handle, last: () => sources[sources.length - 1] }
}

describe('openSearchStream', () => {
  beforeEach(() => { vi.useFakeTimers() })
  afterEach(() => { vi.useRealTimers() })

  it('forwards parsed result/progress/done events', () => {
    const { cb, last } = makeHarness()
    last().emit('result', '{"title":"A","infoHash":"aaa"}')
    expect(cb.onResult).toHaveBeenCalledWith({ title: 'A', infoHash: 'aaa' })

    last().emit('progress', '{"phase":"live"}')
    expect(cb.onLive).toHaveBeenCalledTimes(1)

    last().emit('done', '{"total":1}')
    expect(cb.onDone).toHaveBeenCalledWith({ total: 1 })
    expect(last().closeCount).toBe(1)
  })

  it('ignores malformed frames instead of throwing', () => {
    const { cb, last } = makeHarness()
    last().emit('result', 'not json')
    last().emit('result', '')
    last().emit('result', undefined)
    expect(cb.onResult).not.toHaveBeenCalled()
  })

  it('surfaces the backend named-error event without reconnecting', () => {
    const { sources, cb, last } = makeHarness()
    last().emit('error', '{"message":"Jackett indisponível"}')
    expect(cb.onServerError).toHaveBeenCalledWith('Jackett indisponível')
    expect(sources).toHaveLength(1) // no reconnect — stream is still alive
    expect(cb.onGiveUp).not.toHaveBeenCalled()
  })

  it('reconnects with backoff on a connection drop before done', () => {
    const { sources, cb, last } = makeHarness()
    last().emit('error') // connection error: no data
    expect(last().closeCount).toBe(1)
    expect(sources).toHaveLength(1)

    vi.advanceTimersByTime(BASE_RETRY_DELAY_MS)
    expect(sources).toHaveLength(2) // recreated
    expect(cb.onGiveUp).not.toHaveBeenCalled()

    // The replayed session works: events flow on the new source.
    last().emit('result', '{"infoHash":"bbb"}')
    expect(cb.onResult).toHaveBeenCalledTimes(1)
    last().emit('done', '{}')
    expect(cb.onDone).toHaveBeenCalledTimes(1)
  })

  it('gives up only after the retry budget is exhausted', () => {
    const { sources, cb, last } = makeHarness()
    for (let i = 0; i < MAX_SSE_RETRIES; i++) {
      last().emit('error')
      vi.runAllTimers()
    }
    expect(sources).toHaveLength(MAX_SSE_RETRIES + 1)
    expect(cb.onGiveUp).not.toHaveBeenCalled()

    last().emit('error') // budget spent
    expect(cb.onGiveUp).toHaveBeenCalledTimes(1)
    vi.runAllTimers()
    expect(sources).toHaveLength(MAX_SSE_RETRIES + 1) // no further reconnect
  })

  it('resets the retry budget when data flows again', () => {
    const { cb, last } = makeHarness()
    for (let i = 0; i < MAX_SSE_RETRIES; i++) {
      last().emit('error')
      vi.runAllTimers()
    }
    last().emit('result', '{"infoHash":"ccc"}') // budget back to full
    for (let i = 0; i < MAX_SSE_RETRIES; i++) {
      last().emit('error')
      vi.runAllTimers()
      expect(cb.onGiveUp).not.toHaveBeenCalled()
    }
    last().emit('error')
    expect(cb.onGiveUp).toHaveBeenCalledTimes(1)
  })

  it('close() stops everything: no callbacks, no pending reconnect', () => {
    const { sources, cb, handle, last } = makeHarness()
    last().emit('error') // schedule a reconnect
    handle.close()
    vi.runAllTimers()
    expect(sources).toHaveLength(1) // pending reconnect cancelled

    last().emit('result', '{"infoHash":"ddd"}')
    last().emit('done', '{}')
    expect(cb.onResult).not.toHaveBeenCalled()
    expect(cb.onDone).not.toHaveBeenCalled()
    expect(cb.onGiveUp).not.toHaveBeenCalled()
  })

  it('ignores events arriving after done (server closed, late frames)', () => {
    const { cb, last } = makeHarness()
    last().emit('done', '{}')
    last().emit('result', '{"infoHash":"eee"}')
    last().emit('error')
    expect(cb.onResult).not.toHaveBeenCalled()
    expect(cb.onGiveUp).not.toHaveBeenCalled()
    expect(cb.onDone).toHaveBeenCalledTimes(1)
  })
})
