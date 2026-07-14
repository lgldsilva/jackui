// Streaming de torrents (anacrolix → HTTP com Range): add/probe/info/health/art,
// favoritos, controles estilo Transmission e os URL builders (/api/stream/*).
// Arquivos locais são detectados pelo pseudo info-hash e roteados pro ./local —
// o PlayerModal não distingue torrent de local. Extraído de client.ts (#417).
//
// Este arquivo continua sendo o ponto de entrada único (`client.ts` faz
// `export * from './stream'`): re-exporta os módulos irmãos abaixo pra que NENHUM
// import externo quebre.
export * from './stream-types'
export * from './stream-browser'
export * from './stream-core'
export * from './stream-health'
export * from './stream-controls'
export * from './stream-settings'
export * from './stream-favorites'
export * from './stream-probe'
export * from './stream-urls'
