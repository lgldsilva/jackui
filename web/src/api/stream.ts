// Torrent streaming (anacrolix → HTTP with Range): add/probe/info/health/art,
// favorites, Transmission-style controls and the URL builders (/api/stream/*).
// Local files are detected by the pseudo info-hash and routed to ./local —
// PlayerModal doesn't distinguish torrent from local. Extracted from client.ts (#417).
//
// This file remains the single entry point (`client.ts` does
// `export * from './stream'`): it re-exports the sibling modules below so NO
// external import breaks.
export * from './stream-types'
export * from './stream-browser'
export * from './stream-core'
export * from './stream-health'
export * from './stream-controls'
export * from './stream-settings'
export * from './stream-favorites'
export * from './stream-probe'
export * from './stream-urls'
