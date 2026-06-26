# Changelog

Todas as mudanças notáveis do JackUI. Formato baseado em [Keep a Changelog](https://keepachangelog.com),
versionamento [SemVer](https://semver.org).

## [0.4.0] — 2026-06-25

Três semanas de evolução desde a 0.3.0. O grande tema é **downloads**: torrent
multi-arquivo passa a ser tratado como UMA unidade (custo O(N) eliminado),
mais confiabilidade de seed e shutdown. Também: muito trabalho de player/música,
seek local instantâneo, AI benchmark multi-task e ajustes de deploy/infra.

### Adicionado

#### Downloads
- **Torrent multi-arquivo como UMA unidade** (#347/#348): o scheduler conta slot
  **por torrent** (não por arquivo) — um pack de N arquivos ocupa 1 slot e roda
  1 `EnsureActive` + 1 verify + 1 move de conclusão + 1 rename IA + 1 ciclo de
  stall. As prioridades de arquivo do anacrolix selecionam só os escolhidos
  (`Download`; o resto fica `Cancel`/`None`). As linhas por-arquivo continuam
  para granularidade (`internal/downloads/aggregate.go`), e a UI agrupa por
  `infoHash` (1 card por torrent).
- **Baixar pack inteiro como UMA linha** (#356): quando TODOS os arquivos estão
  marcados (o caso comum de um pack), o `DownloadModal`/`AddTorrentModal`
  enfileiram 1 linha "torrent inteiro" (`fileIndex = -2`, file priorities do
  anacrolix) em vez de N linhas por-arquivo — um pack de 778 arquivos vira 1
  linha. Selecionar um subconjunto continua batch (1 linha por arquivo escolhido),
  preservando a granularidade. (`isWholeTorrentSelection`.)
- **Categoria como pasta agrupadora** por padrão (`…/<user>/<categoria>/…`) (#345).
- **Baixar direto no bulk** + picker de destino com browse; re-seed na promoção.
- **Auto-seed de downloads completos** de trackers de seed.
- **Ordenar** por velocidade de download/upload e por seeds (#352).
- **"Ver arquivos"** no card de torrent streaming (completo/parcial) (#342) e
  **"Abrir no local"** nos downloads concluídos (#344).
- **Seed seletivo por tracker** + painel de peers.

#### Player / Música
- **Modo música** com mini-player que vira barra no footer (arrastar/expandir).
- **Capa no Now Playing** (mediaSession artwork 96/512).
- **Faixa de áudio selecionável no HLS** (torrent + local).
- **Continue Watching local** (retoma posição em arquivos locais).
- Capítulos, EQ/visualizer, gapless/crossfade, AirPlay no modo música.

#### Local
- **Indicador "baixando"** em arquivos/pastas incompletos (`.part`).
- **Filtrar a lista de arquivos** por status (baixando / concluído) (#353).
- Cache de arquivo/pasta para mounts rclone.

#### Discover / Busca / Navegação
- **Recomendações** no Discover e trending de música.
- **Reordenar abas de pesquisa** arrastando (#338).
- **Card abre em nova aba** + player full-viewport no deep-link.
- **Navegação lembra aba/filtros/scroll na URL.**

#### Infra / Auth / Observabilidade
- **Endpoint de refresh da peer-port** integrado ao routing do gluetun
  (`watchForwardedPort` reinicia o processo quando a porta forwarded rotaciona).
- **Retry com backoff** em chamadas externas idempotentes (`internal/http`).
- **i18n** ampliado, **métricas Prometheus** e **controle de banda**.
- **Conta para todo usuário** + reset de senha por admin.
- **AI benchmark multi-task**: tarefas rename/identify/schedule, **custo/energia
  no score**, **circuit breaker por provider** e histórico de runs.

### Corrigido

#### Downloads / Seed
- **Seed via metainfo em cache** (#351): `Download.SeedSource()` usa magnet de
  `info_hash` puro → `resolveMagnet` cache-first → semeia in-place. Acaba com o
  erro "auto-seed failed: .torrent URL 404".
- **Completions travadas em "moving"** (`move-not-found`) após o agrupamento (#346):
  `completion_dest` + fallback sem-categoria.
- **Pastas de download duplicadas** (`Name (1)`, `(2)`…) pela migração
  `UserSubpath` (#341).
- **Move idempotente** para single-file (já-no-destino conta como sucesso).
- **Storage por-torrent** aponta para o local real (seed sem re-baixar);
  `resumeSeeding` espera o `FilePathResolver` no boot.

#### Player
- **Vídeo local no iOS via HLS** (remux): MP4 progressive travava no WebKit.
- **Áudio iOS**: `readyState 2`/`AbortError`/contenção de agregação; EQ/Letras
  centralizados; "Letras" só quando há legenda.
- **Legenda embutida/sidecar** não aparecia no Safari (HLS nativo) (#322).
- **Shuffle/repeat de faixa** no áudio + dock desktop (#321).

#### Local / Navegação
- **Menu "Local" 2x não some mais com os mounts** (#357): clicar a nav zerava o
  `?mount=` e o auto-select do 1º mount só rodava uma vez; no mobile o seletor
  vive dentro do bloco `{activeMount && …}` e sumia. Agora a re-seleção é reativa.
- Botões do card não disparam mais o link do card (abrir player).
- Só restaura a rota salva no PWA (refresh não redireciona).
- Botão "parar" em transfers agora cancela a transferência (era no-op).

#### Auth / Confiabilidade / DB
- **Janela de graça na rotação de refresh** (#335): não desloga no deploy.
- Exige `jwt_secret` no boot (auth ON); `busy_timeout(5000)` em todos os stores;
  `/healthz` checa o streamer.

#### Shutdown (causou um outage)
- **Watchdog de shutdown** (#355): `runCleanup` ganha um watchdog de 20s →
  `os.Exit(0)` se o cleanup (anacrolix/DHT) travar com a rede caída. Antes o
  processo pendurava para sempre (502 eterno); com `restart: unless-stopped` o
  Docker recria o container.

### Performance

- **`GET /api/downloads` rápido** (#355): novo `Streamer.LiveStats` (O(1) por
  torrent — sample de taxa + `Stats().ConnectedSeeders`, **sem** o `buildInfo`
  O(arquivos)) no enrich da lista, que ainda deduplica por `info_hash`. Num pack
  grande (Morgpie, 778 arquivos) a resposta caiu de **2-17 s para ~1 s**. O
  contador passou a ser **por torrent** (`countTorrents`), não por linha.
- **CPU ocioso reduzido** (#354): o HLS do ffmpeg fecha quando sai o **último**
  viewer (`ReleaseViewer` reporta `lastViewer`, libera o slot de GPU-decode) em
  vez de sobreviver até o reap de 5 min; 3 polls do front (`TransfersProvider`,
  `PlayerModal`, `DownloadsPage`) pausam com `document.hidden`.
- **Seek instantâneo em vídeo baixado** (#350): `/api/local/file` roteia disco
  LOCAL → `http.ServeFile` (sendfile); só remoto/FUSE (`isRemoteFS`) usa Session
  com read-ahead de 16 MB.
- **GPU transcode robusto** (#349): `gpuSem` limita decoders CUVID em VRAM; em
  `CUDA_ERROR_OUT_OF_MEMORY` a sessão cai para **decode em CPU** em vez de falhar.
- **Health via tracker scrape (BEP 48)** incl. trackers privados.
- **Perfil de baixo consumo "Balanceado"** (aplicado no compose de prod via env,
  NÃO no repo): `GOGC=75`, `GOMEMLIMIT=1600MiB`, `GOMAXPROCS=2`,
  `JACKUI_MAX_CONNS=40`, `JACKUI_PEERS_HIGH=200`. Memória do app (heap) ~222 MiB;
  o restante exibido é page cache reclamável.

### Manutenção / CI/CD

- **Realidade do deploy** (corrige uma premissa comum): prod **não** é uma stack
  do Portainer. O Jenkins faz `docker compose -f
  /Files/AppData/Config/jackui/docker-compose.yml up -d
  --force-recreate jackui` sobre um arquivo **hand-maintained** (a API do
  Portainer não lista o jackui). Prod roda **atrás do gluetun**
  (`network_mode: container:gluetun`, porta forwarded). **Env vars novos no
  compose do repo NÃO chegam em prod** — é preciso editar o arquivo hand-maintained.

## [0.3.0] — 2026-06-05

### Adicionado
- **Tema claro/escuro** em toda a UI (tailwind dark/light variants + toggle),
  com tokens de cor semânticos nos componentes e páginas (#96).

### Corrigido
- **Favorites**: import de `.torrent` em lote não trava mais — a conversão
  byte→base64 era O(n²) e estourava em arquivos reais ("importar 4 torrents
  falha") (#94).

### Manutenção
- `.gemini/` adicionado ao `.gitignore` (lixo de ferramentas de IA) (#95).
- Auditoria open-source: histórico git está **limpo** (sem segredos commitados);
  LICENSE/CONTRIBUTING/SECURITY ficam para quando a publicação for decidida.

## [0.2.0] — 2026-06-05

Onda de correções de bugs (caça exploratória + auditoria) e melhorias de
robustez/UX. 11 PRs (#82–#92).

### Adicionado
- **UX mobile**: reforma da navegação mobile, toque de 1 ação na linha do
  arquivo, downloads multi-arquivo, melhorias na LocalPage (#82).
- **Streaming — viewer-lease**: stream-only para de seedar à toa logo após
  fechar o player, mas sobrevive enquanto houver espectadores (protege
  co-watchers) (#82).
- **Local — promover em lote**: um único modal aplica destino + renomeação IA a
  N arquivos numa só chamada (fim da fila um-a-um) (#82).
- **Local — limpar pastas vazias**: botão que remove subpastas vazias
  recursivamente (#82).
- **Thumbnails locais**: limite de concorrência + cache persistente +
  negative-cache (não re-gera HDR 4K que falha) (#82).

### Corrigido
- **Player**: thumbnail de hover preso ao trocar de vídeo; race do `streamAdd`
  que sobrescrevia o vídeo novo; `ErrorBoundary` global (fim das telas brancas);
  reset de `artFailed` por infoHash (#82, #89).
- **Move local**: recusa sobrescrever item de mesmo nome no destino (perda de
  dados silenciosa) e preserva o mtime no fallback cross-device (#82).
- **Auth**: fecha o TOCTOU na rotação de refresh token + detecção de reuso
  (revoga a sessão em replay) (#85).
- **Streamer**: TOCTOU em `HealthSnapshot` (panic) lido sob lock; falhas de
  `persistMetainfo` logadas; `verifiedFiles` purgado por hash no ciclo de vida
  (em vez de wipe-2000 que re-hashava ativos) (#86, #90).
- **Art**: negative-cache evita re-rodar IA+TMDB+web-search a cada card (#88).
- **Parser**: falso-positivo de Season ("Ocean's 11"→S11) e de ano ("Blade
  Runner 2049"→2049); corrige MediaKind/match TMDB (#87).
- **Transmission RPC**: `torrent-set` sem `ids` aplica a todos; `torrent-add`
  respeita `labels`→categoria; JSON-RPC 2.0 sem `params` volta a funcionar
  (#84, #90).
- **Busca**: para o falso "Jackett não configurado" (timeout transitório / URL
  default com API key salva) (#91).
- **Logout**: limpa os dados de modo incógnito na hora (privacidade) (#90).

### Melhorado (rename IA)
- O `renamer` usa o parser regex como hint confiável (S/E/ano), com **override**
  do S/E (coerência de série — episódios `S01E0x` caem todos em Season 1) e
  **fallback** quando a IA falha (nunca dá hard-error) (#92).

### Refatorado
- Quebra do god-file `client.ts` em módulos por domínio (barrel) (#83).
- Correção de code smells e robustez do monorepo (#84).

### Pendente (backlog para a próxima versão)
- Safari/iOS entrando em modo live (deve ser VOD por padrão).
- Downloads/fila: ordenação, aba "Ativos", quota, iniciar/parar todos.
- History (refresh), Favorites (importar lote), Incognito (toggle global).
- Preparação open-source (segredos no histórico git, IPs internos, LICENSE/docs).
