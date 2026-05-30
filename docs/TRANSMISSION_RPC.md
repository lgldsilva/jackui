# Transmission RPC — Compatibilidade com *arr

## Visão Geral

O JackUI agora expõe um endpoint `/transmission/rpc` compatível com o protocolo RPC
do Transmission, permitindo que o **Sonarr**, **Radarr** e **Prowlarr** (e qualquer
cliente que fale Transmission RPC) enxerguem o JackUI como se fosse um daemon
Transmission.

Isso elimina a necessidade de rodar o Transmission separadamente — o JackUI
gerencia os downloads diretamente via seu worker interno (anacrolix/torrent),
compartilhando o mesmo storage.

## Arquitetura

```
Sonarr/Radarr/Prowlarr
        │
        ▼  POST /transmission/rpc
┌───────────────────────────────┐
│  internal/transmissionrpc/    │ ← Gin handler (fora do /api, sem JWT)
│  ┌─────────────────────────┐  │
│  │  dispatch():            │  │
│  │  session-get            │  │
│  │  session-stats          │  │
│  │  torrent-add            │  │
│  │  torrent-get            │  │
│  │  torrent-set            │  │
│  │  torrent-remove         │  │
│  │  port-test              │  │
│  └─────────────────────────┘  │
└──────────────┬────────────────┘
               │
    ┌──────────┴──────────┐
    ▼                     ▼
internal/downloads/     internal/streamer/
(Store SQLite)         (anacrolix/torrent)
```

## Endpoints RPC Implementados

### session-get
Retorna configuração da sessão. Usado pelo *arr para testar conectividade.

**Campos:** 61 campos, mesmos nomes do Transmission 4.1.1 (rpc-version 19).
Inclui: `version`, `rpc-version`, `rpc-version-semver`, `download-dir`,
`download-dir-free-space`, `seedRatioLimit`, `units`, `speed-limit-*`,
`peer-limit-*`, `queue-*`, `script-torrent-*`, etc.

### session-stats
Retorna estatísticas da sessão. Usado pelo Prowlarr para testar conectividade.

**Campos:** `activeTorrentCount`, `downloadSpeed`, `uploadSpeed`,
`pausedTorrentCount`, `torrentCount`, `cumulative-stats`, `current-stats`.

### torrent-add
Adiciona um torrent para download. Aceita:

| Tipo | Exemplo |
|------|---------|
| Magnet URI | `magnet:?xt=urn:btih:<hash>&dn=...` |
| InfoHash puro | `abcdef0123456789abcdef0123456789abcdef01` |
| URL .torrent | `https://example.com/file.torrent` |

**Parâmetros:** `filename` (obrigatório), `download-dir` (→ mapeado como
category), `paused` (inicia pausado).

**Resposta:** `{ "torrent-added": { "id", "hashString", "name" } }`

### torrent-get
Lista torrents com campos selecionáveis. Usado pelo *arr para polling de
progresso.

**50 campos suportados:** `id`, `hashString`, `name`, `status` (TR_STATUS_*),
`totalSize`, `percentDone`, `rateDownload`, `rateUpload`, `downloadDir`,
`addedDate`, `doneDate`, `error`, `errorString`, `leftUntilDone`, `haveValid`,
`peersConnected`, `eta`, `isFinished`, `isStalled`, `labels`, `trackers`,
`uploadRatio`, `queuePosition`, `bandwidthPriority`, `recheckProgress`,
`secondsDownloading`, `secondsSeeding`, `files`, `fileStats`, etc.

**Filtro:** `ids` (opcional) — array de IDs ou único ID.

### torrent-set
Modifica propriedades de torrents existentes.

**Comandos:** `paused` (pause/resume), `labels` (→ category), `seedRatioLimit`,
`seedRatioMode`, `bandwidthPriority`.

### torrent-remove
Remove torrents da fila de downloads.

**Parâmetros:** `ids` (obrigatório), `delete-local-data` (opcional, não
implementado — os dados on-disk permanecem até o LRU do streamer limpar).

### Outros métodos

| Método | Comportamento |
|--------|---------------|
| `port-test` | Retorna `{ "port-is-open": true }` |
| `blocklist-update` | No-op, retorna `{ "blocklist-size": 0 }` |
| `free-space` | Retorna espaço livre no `download-dir` |
| `torrent-set-location` | No-op (aceito sem erro) |
| `torrent-rename-path` | No-op (aceito sem erro) |

## Autenticação

O endpoint segue o handshake do Transmission:

1. Request sem `X-Transmission-Session-Id` → **HTTP 409** com o header
   `X-Transmission-Session-Id` e `X-Transmission-Rpc-Version`
2. Cliente retorna com o header → request processado

Quando o auth do JackUI está **ligado** (`auth.enabled: true`):
- O cliente precisa enviar **Basic Auth** (username/password do JackUI)
- O session-id só é emitido após credenciais válidas
- Os downloads são associados ao **userID** do usuário autenticado

Quando o auth está **desligado** (`auth.enabled: false`):
- Qualquer request é aceita (não há auth store para validar)
- Todos os downloads são associados ao **userID 0** (sistema)
- Se quiser que os downloads sejam associados a um usuário específico,
  **ative o auth** (`JACKUI_AUTH_ENABLED=1`) — o Transmission RPC endpoint
  usará o mesmo auth store.

## Mapeamento de Status

| JackUI Status | Transmission Status | Código |
|---------------|-------------------|--------|
| `queued` | TR_STATUS_DOWNLOAD_WAIT | 3 |
| `downloading` | TR_STATUS_DOWNLOAD | 4 |
| `completed` | TR_STATUS_SEED | 6 |
| `paused` | TR_STATUS_STOPPED | 0 |
| `failed` | TR_STATUS_STOPPED | 0 |

## Integração com Worker Interno

O método `torrent-add` cria um registro no `internal/downloads/` com
`FileIndex = -1` (sentinela para "auto-pick melhor arquivo"). O worker
(`worker.go`) ao processar esse download:

1. Chama `streamer.EnsureActive()` para adicionar ao swarm anacrolix
2. Aguarda metadados (GotInfo)
3. Executa `pickBestFile()` para selecionar o melhor arquivo:
   - Prioriza vídeo (`.mkv`, `.mp4`, `.avi`, `.mov`, etc.)
   - Depois áudio (`.mp3`, `.flac`, etc.)
   - Maior arquivo como fallback
4. Persiste o FileIndex escolhido via `store.SetFileIndex()`
5. Inicia o download

## Configuração no Sonarr/Radarr

| Campo | Valor |
|-------|-------|
| **Tipo** | Transmission |
| **Host** | IP do servidor JackUI |
| **Porta** | 8989 (ou a porta configurada) |
| **Url Path** | `/transmission/rpc` |
| **Usuário** | (credenciais do JackUI, se auth ligado) |
| **Senha** | (credenciais do JackUI, se auth ligado) |
| **Category** | Ex: `tv-sonarr` (mapeado do `download-dir`) |

## Configuração no Prowlarr

Prowlarr usa `session-stats` e `session-get` para testar conectividade.
A configuração é idêntica ao Sonarr/Radarr.

## Arquivos Modificados/Criados

| Arquivo | Tipo | Descrição |
|---------|------|-----------|
| `internal/transmissionrpc/handler.go` | Novo | Handler Gin + métodos RPC (704 linhas) |
| `internal/transmissionrpc/handler_test.go` | Novo | Testes unitários (186 linhas) |
| `cmd/server/main.go` | Modificado | Inicialização + registro de rota |
| `internal/downloads/worker.go` | Modificado | Suporte a FileIndex=-1 + pickBestFile() |
| `internal/downloads/store.go` | Modificado | Método SetFileIndex() |
| `docs/TRANSMISSION_RPC.md` | Novo | Esta documentação |

## Testes

```bash
# Testes do pacote transmissionrpc
go test ./internal/transmissionrpc/... -v

# Testes do worker (inclui pickBestFile)
go test ./internal/downloads/... -v

# Teste de curl contra servidor rodando
curl -s -X POST http://localhost:8989/transmission/rpc \
  -H "Content-Type: application/json" \
  -d '{"method":"session-get"}'

# Teste de compatibilidade via curl (fluxo 409 → session-id)
SESSION_ID=$(curl -s -D - -X POST http://localhost:8989/transmission/rpc \
  -d '{"method":"session-get"}' 2>&1 | grep X-Transmission-Session-Id \
  | awk '{print $2}' | tr -d '\r')
curl -s -X POST http://localhost:8989/transmission/rpc \
  -H "Content-Type: application/json" \
  -H "X-Transmission-Session-Id: $SESSION_ID" \
  -d '{"method":"session-get"}'
```

## Limitações / TODO

- [ ] `uploadRatio` e `uploadedEver` retornam 0 (não rastreamos upload por torrent)
- [ ] `torrentFile` retorna vazio (não salvamos `.torrent` files em disco)
- [ ] `torrent-set` não persiste `seedRatioLimit` por download (aceito sem erro)
- [ ] `torrent-remove` com `delete-local-data=true` não apaga dados em disco
- [ ] Files `begin_piece`/`end_piece` genéricos (não refletem estrutura real do torrent)
- [ ] Polling frequente do *arr (a cada 10s) sobre `ListAll()` + `streamer.Get()` — pode
      ser custoso com muitos torrents. Considerar cache de resposta.
