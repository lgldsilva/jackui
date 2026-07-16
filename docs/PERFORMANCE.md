# Performance — the N+1 backlog (frontend loops → backend batch)

> Auditoria de 3 frentes (player/local, busca/biblioteca, geral+backend) dos loops
> em que o **frontend faz uma chamada de backend por item de uma lista**, quando
> **uma chamada batch** resolveria tudo. Sibling de [design-decisions.md](design-decisions.md).

## O princípio (diretriz do projeto)

**Lista no cliente = UMA chamada batch no backend, não um loop N+1.**

Causa-raiz arquitetural: o fetch vive no **componente-folha** (o card/row/badge/grupo de
playlist) em vez de no **dono da lista**. Cada folha é auto-suficiente e dirigida por hook
(`useTmdbMatch`, `useThumbnail`, `useTrack`, `SeedBadge`, `usePlaylistTracks.resolveOne`),
o que é composição React limpa mas **garante uma requisição por folha** — nenhuma camada
enxerga "todos os hashes/paths/títulos da tela de uma vez". Há dedupe parcial em escopo de
módulo (`tmdbSessionCache`, `localProbeCache` no servidor), mas só colapsa duplicatas
exatas, não os N itens distintos. O backend reflete a mesma lacuna histórica: tinha
`/downloads/batch/{pause,resume,delete}` mas nenhum batch para metadata, health, play-probe,
art-resolve ou favorites.

**Padrão de correção (uniforme):** (1) subir o fetch da folha pro dono da lista; (2) adicionar
um endpoint batch no servidor que faz o loop da lógica per-item existente sob concorrência
limitada; (3) semear um cache compartilhado uma vez; (4) as folhas leem do cache (zero fetch).

## Backlog priorizado

Legenda: ✅ = entregue · ⬜ = pendente.

| # | N+1 | Onde (frontend → API) | Chamadas/uso | Batch | Estado |
|---|-----|----------------------|--------------|-------|--------|
| **1** | Lista de faixas local: 1 ffprobe por arquivo | `usePlaylistTracks` skeleton local + `LocalPage playPlaylist` → GET `/api/local/play` | ~47 p/ álbum de 47 | **POST `/api/local/play/batch`** {mount,paths[],forceHLS?}; `skeletonGroup` marca local 'ready' sem fetch, `playPlaylist` pré-aquece a pasta 1× | ✅ **feito** (batch handler + skeleton + pre-warm) |
| **2** | `tmdbMatch` por card (Search/History/Favorites/Watchlist/Library) | `ResultCard useTmdbMatch`/`useThumbnail` → GET `/tmdb/match` | ~30-50 por grade | **POST `/tmdb/match/batch`** {titles[]}; `tmdbMatch` faz coalescing (~40ms) → 1 POST; semeia o cache de `tmdb.ts` | ✅ **feito** (coalescing + `matchTitlesConcurrent`) |
| **3** | `streamHealth` (peek) por `SeedBadge` | `SeedBadge.tsx` peek → GET `/stream/health/:hash` | ~N (até 2N na busca) | **POST `/api/stream/health/batch`** {hashes[]} (peek-only); `streamHealth(peek)` faz coalescing → 1 POST | ✅ **feito** (peek batch; probe segue single) |
| **4** | Metadata de playlist (torrent) warm-cache: 1 `/stream/metadata` por item | `usePlaylistTracks.resolveOne` → GET `/stream/metadata/:hash` (+`/stream/add` frio) | N por playlist de N | **`POST /api/stream/metadata/batch`** {hashes[]}; só `streamAdd` nos misses frios | ✅ **feito** (batch warm + `metadataPeekCache`; `streamAdd` só nos misses) |
| 5 | Download create por arquivo (sem batch-create) | `AddTorrentModal`/`DownloadModal` picks.map → POST `/downloads` | M por pick; N×M | **POST `/api/downloads/batch`** {items[]} | ✅ **feito** (`DownloadsBatchCreate` + `downloadBatchCreate` no `DownloadModal`/`AddTorrentModal`) |
| 6 | `resolveArt` por tile da biblioteca no miss (204) | `LibraryPage` onArtError→resolveArt → POST `/stream/art/:hash/resolve` | até ~50 resolves frios | **`POST /api/stream/art/resolve/batch`** + batch após `libraryList` | ✅ **feito** |
| 7 | `streamDrop` por download no delete em massa | `DownloadsPage.tsx` targets.map→streamDrop → DELETE `/stream/:hash` | N | **`POST /api/stream/drop/batch`** {hashes[]} | ✅ **feito** |
| 8 | Art `<img>`: 1 round-trip por card | `Thumbnail`/`LibraryPage` streamArtURL → GET `/stream/art/:hash` | ~50+/página | resolve some com #2/#6 (retornar artUrl/artStatus → `<img>` só monta se há art). Bytes ficam por-`<img>` (HTTP/2) | ⬜ |
| 9 | `favoriteSetFolder`/`favoriteRemove` por nome | `FavoritesPage.tsx` names.map → PATCH/DELETE | 1 por favorito | **`POST /api/stream/favorites/batch/{folder,remove}`** | ✅ **feito** |
| 10 | `downloadStopSeed` por item | `DownloadsPage.tsx` ds.map → POST `/downloads/:id/stop-seed` | 1 por item | **`POST /api/downloads/batch/stop-seed`** {ids[]} | ✅ **feito** |
| 11 | Status de cache por arquivo (latente) | `LocalCacheButton` → GET `/api/local/cache/status` | 1 hoje; ~N se badge por-row | **GET `/api/local/cache/status/folder`** | ⬜ |
| 12 | Tags de áudio por faixa (latente) | `MusicPanel useTrack` → `/api/local/audio/meta` | 1 hoje; ~N se sidebar exibir tags | **`/api/local/audio/meta/batch`** | ⬜ |

> #1/#4 são as duas metades do N+1 do `usePlaylistTracks` (local vs torrent) — o **#1 já
> caiu**, falta o #4 pro lado torrent. #6/#8 são o split resolve(JSON, batchável) vs
> bytes(`<img>`, não-batchável) do mesmo grid da biblioteca.

## O que já foi entregue (#1/#2/#3)

Os três N+1 de maior impacto (player local + grades TMDB + swarm health) já foram fechados:

- **#1 — `POST /api/local/play/batch`** (`internal/handlers/local/local_play.go`): resolve
  vários paths de um mount com concorrência limitada (`localPlayBatchConcurrency=4`, teto
  `localPlayBatchMax=500`), reusando o mesmo `synthesizeLocalInfo`/`localProbeCache` do GET
  singular. No front, `skeletonGroup` (`usePlaylistTracks.ts`) monta cada item local já
  `'ready'` a partir do pseudo-hash **sem chamada de backend**, e `LocalPage.playPlaylist`
  dispara `localPlayBatch(mount, paths)` uma vez pra pré-aquecer as URLs direct
  (`localPlayableURLCache`). Abrir álbum de 47 faixas = **1** POST em vez de **47** GETs.
- **#2 — `POST /tmdb/match/batch`** (`internal/handlers/tmdb.go`): `matchTitlesConcurrent`
  resolve N títulos (cache 30d server-side) com concorrência 6. No front, `tmdbMatch`
  (`web/src/api/tmdb.ts`) faz **coalescing**: enfileira os títulos numa janela de ~40ms e
  dispara **um** POST, semeando o cache de sessão que cada card lê. Um endpoint mata o N+1
  em 5 páginas (Search/History/Favorites/Watchlist/Library).
- **#3 — `POST /api/stream/health/batch`** (`internal/handlers/stream.go`): peek-only (não
  dispara re-probe), casa a forma do GET singular. `streamHealth(peek)` (`web/src/api/stream.ts`)
  faz coalescing → um POST por página de lista. O caminho de **probe** (re-sondar swarm
  inativo) segue como GET singular de propósito — é sob demanda, não uma varredura de lista.

## Por onde continuar

**#6 (`POST /api/stream/art/resolve/batch`)** entregue — `LibraryPage` dispara 1 batch após `libraryList`; `onArtError` permanece como fallback.

**#7 (`POST /api/stream/drop/batch`)** entregue — `StreamDropBatch` dedupe de hashes + `CloseForHash` por torrent; front (`useDownloadActions`) usa `streamDropBatch` no delete em massa e “remover concluídos” em vez de N `DELETE /stream/:hash`.

**#9 (`POST /api/stream/favorites/batch/{folder,remove}`)** entregue — multi-select move/delete na `FavoritesPage` faz **1** POST em vez de N PATCH/DELETE por nome; cap 500; resposta `{affected,total,failed}`.

**#10 (`POST /api/downloads/batch/stop-seed`)** entregue — um POST com `ids[]`; backend `DropSeed` uma vez por `info_hash` único; `onStopSeedMany` no front.

Próximo alvo: **#8** (art `<img>` só monta quando há art).

## Worked example — `POST /api/local/play/batch` (#1, referência)

> Mantido como o modelo do padrão (o #1 seguiu exatamente esta forma).

**Rota:** `POST /api/local/play/batch`, ao lado do GET `/api/local/play` (mesmo grupo/middleware).
O GET singular continua intacto pro now-playing.

**Request:**
```json
{ "mount": "music", "paths": ["Album/01.flac", "Album/02.flac"], "forceHLS": false }
```
Limitado server-side (`> 500 paths → 413`) pra não disparar ffprobe ilimitado.

**Handler Go** (`internal/handlers/local/local_play.go`, mesmo pacote do `LocalPlay`):
faz probe com **concorrência limitada** (`sem := make(chan struct{}, 4)`) chamando o mesmo
resolvedor (`resolveBatchItem` → `synthesizeLocalInfo`, ffprobe memoizado pelo
`localProbeCache`), e devolve `{items:[{path,kind,url,vcodec,acodec,container,error?}]}`.
Erro por-arquivo é inline (um arquivo ruim não derruba o lote). `forceHLS` só afeta vídeo
(no iOS o front manda `forceHLS: isIOS()` pra pré-aquecer a URL HLS que o vídeo realmente usa).

**Frontend:** `LocalPage.playPlaylist` resolve a pasta UMA vez no clique e pré-aquece;
`skeletonGroup` marca cada grupo local `'ready'` sem fetch; o driver de ativação em
background vira no-op pra playlists locais (as N chamadas ffprobe somem). Grupos de torrent
seguem o caminho `streamMetadata` (será coberto por #4).
