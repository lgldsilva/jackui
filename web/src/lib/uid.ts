// Unique-id helper safe for INSECURE contexts, without giving up strong randomness.
//
// `crypto.randomUUID()` only exists in a secure context (HTTPS or localhost).
// Served over plain HTTP on a LAN IP (e.g. http://192.168.x.x:8989) the page is
// NOT a secure context, so `crypto.randomUUID` is undefined and calling it throws
// a TypeError — which, mid-render, blanks the whole app.
//
// We degrade gracefully WITHOUT weakening security where it matters:
//   1. randomUUID()      — native, secure contexts (best).
//   2. getRandomValues() — STILL cryptographically strong and, unlike randomUUID,
//                          available in insecure contexts too. We build a proper
//                          RFC-4122 v4 UUID from it. Nunca caímos em Math.random()
//                          (que é PRNG fraco) — getRandomValues existe desde IE11,
//                          inclusive em contexto inseguro (HTTP de LAN).
function uuidFromBytes(b: Uint8Array): string {
  b[6] = (b[6] & 0x0f) | 0x40 // version 4
  b[8] = (b[8] & 0x3f) | 0x80 // variant 10xx
  const h = Array.from(b, x => x.toString(16).padStart(2, '0'))
  return `${h.slice(0, 4).join('')}-${h.slice(4, 6).join('')}-${h.slice(6, 8).join('')}-${h.slice(8, 10).join('')}-${h.slice(10, 16).join('')}`
}

export function uid(): string {
  const c = globalThis.crypto
  if (c && typeof c.randomUUID === 'function') return c.randomUUID()
  return uuidFromBytes(c.getRandomValues(new Uint8Array(16)))
}
