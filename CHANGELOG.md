# Changelog

Todas as mudanças notáveis do JackUI. Formato baseado em [Keep a Changelog](https://keepachangelog.com),
versionamento [SemVer](https://semver.org).

> A versão segue as **tags** — semver auto-incrementado por Conventional Commits a cada
> merge na `main` (é o que aparece em `/status`). As entradas `0.2.0`/`0.3.0` no fim são do **esquema
> manual antigo**, anterior à automação; as versões intermediárias (0.4–0.58) não foram catalogadas
> individualmente.

ℹ️ **A partir da v0.81.0** o changelog detalhado é mantido nas [Releases do GitHub](https://github.com/lgldsilva/jackui/releases).
As seções abaixo consolidam o resumo de cada versão para consulta rápida junto ao histórico anterior.

## [0.90.2] — 2026-07-06

### Correções
- use vpn-gateway compose (parent that includes jackui+gluetun) (6b5eeb5)
- use server-managed compose instead of repo compose for deploy (3e55279)
- remove existing jackui container before compose up to avoid name conflict (18c3b01)
- add --force-recreate to avoid container name conflict with server-managed compose (c1dfdec)
- use --no-deps jackui to avoid conflicting with server-managed postgres (213536e)

### Manutenção / CI
- add concurrency policy section to github-actions-runners.md (19702ed)
- add concurrency control to all workflows (5b1246c)

## [0.90.1] — 2026-07-06

### Correções
- visibility picker button not responding when selecting restricted mode (be6ecce)

## [0.90.0] — 2026-07-06

### Novidades
- metadata batch warm + docs aligned with PostgreSQL (d3ca474)

### Correções
- ease PG test flakes on loaded ARM runners (1bd6ce5)

### Manutenção / CI
- retrigger #3 (8f1d17e)
- retrigger after flaky backend timeouts (f8884c6)

## [0.89.0] — 2026-07-06

### Novidades
- auto-download next file when current finishes streaming (68d5198)

## [0.88.1] — 2026-07-06

### Correções
- clear SonarQube new-code violations on main (8256110)
- remove duplicate return in Transmission start/stop handlers (c3da8c0)
- SSE convergence guard + Transmission streamer best-effort (b1ff06c)
- treat ErrNoRows in createOne idempotency read (1a83497)
- hardening from deep analysis (recheck, perf, cohesion) (18d2305)

## [0.88.0] — 2026-07-05

### Novidades
- RefCountPath para segurança de arquivos linked no dedup (8ce27da)

## [0.87.0] — 2026-07-05

### Novidades
- UI 'você já tem N de M' no fluxo de baixar (#23 fase 2) (4dc59fa)

### Manutenção / CI
- trata 'security:' como tipo releasable (patch bump) (e334991)

## [0.86.0] — 2026-07-05

### Novidades
- endpoints de dedup 'você já tem' + link (#23 fase 1c) (f12bfaf)

## [0.85.0] — 2026-07-05

### Novidades
- auto-link de arquivo já baixado no worker (#23 fase 1b-worker) (08e59a7)

## [0.84.0] — 2026-07-05

### Novidades
- colunas/queries de dedup no store (#23 fase 1b-store) (652da4c)

## [0.83.0] — 2026-07-05

### Novidades
- plumbing de dedup cross-torrent — FilePieceCheck/FingerprintFile (#23 fase 1a) (4d0b9c2)

## [0.82.0] — 2026-07-05

### Novidades
- identidade de arquivo p/ dedup cross-torrent (#23 fase 0) (537e1ec)

### Manutenção / CI
- caminho de áudio iOS (<audio> dedicado) + backlog N+1 batch (2c65b93)

### Outros
- security(gosec): triar 116 findings + tornar o gate bloqueante (#480) (c9e007e)

## [0.81.4] — 2026-07-04

### Correções
- não passa skeletonGroup direto pro .map (Sonar S7727) (3d4bd41)

### Performance
- mata o N+1 da lista de faixas local (skeleton + batch pré-aquece) (c155057)

### Manutenção / CI
- ajusta chamadores de localPlayVideoResp p/ a nova assinatura (ctx, forceHLS) (6976482)

## [0.81.3] — 2026-07-04

### Correções
- reduz complexidade cognitiva de TmdbMatchBatch (S3776) (6b896ab)

### Performance
- batch /tmdb/match + /stream/health peek via coalescing (25ff539)

### Manutenção / CI
- allowlist gitleaks false positives + gosec status green (195ec20)
- bump go.mod to 1.26.4 (stdlib CVEs) + gosec non-blocking (1c873af)
- converge with semidx — security gates, go-version-file, mutation (b77284a)

## [0.81.2] — 2026-07-04

### Correções
- reclassify não quebra com item de erro no preview em lote (688438f)

## [0.81.1] — 2026-07-04

### Correções
- não relançar HLS por prefetch do Safari no início (anti-thrash) (37ca58d)
- legendas embutidas num seletor único (dropdown) em vez de N botões (6bdc33a)

### Refatoração
- extrair árvore de pastas + import do FavoritesPage p/ lib/favoritesTree.ts (1065→1005) (8053f15)
- extrair lógica de abas do SearchPage p/ lib/searchTabs.ts (1267→1099) (5630395)
- quebrar o god-file benchmark.go (1176→560) por tema (1be243b)
- quebrar o god-file config.go (1097→419) por tema (fa84b8d)
- quebrar o god-file streamer.go (1923→1158) por tema (6991e1e)
- quebrar o god-file store.go (1212→624) por tema (7756168)
- quebrar o god-file hls.go (1266→830) por tema (a6120bd)
- quebrar o god-file worker.go (1578→1078) por tema (b76aa9e)
- quebrar o god-file local.go (1676→749) em arquivos por tema (f75f60a)

### Manutenção / CI
- exigir Postgres estável antes do go test (fim do flake connection refused) (b14ee23)
- detectar breaking só como footer (não em prosa) (6ffc160)

## [0.81.0] — 2026-07-04

### Manutenção / CI
- publish-release não-fatal (não bloqueia o deploy) (f9d3a94)
- versionamento releasable + Gitea Releases com changelog (8ca9256)

## [0.80.4] — 2026-07-04

### Correções
- fix(handlers): agrupar deps dos handlers de promote em struct (S107) (5c5837b)
- fix(ci): trust homelab CA instead of disabling TLS for Node steps (7eff310)

### Refatoração
- refactor(handlers): extrair subpacote handlers/local via pacote neutro httpshared (414e762)

## [0.80.3] — 2026-07-03

### Refatoração
- refactor(web): extrair componentes da LocalPage + windowing/memo no EntryRow (fb27eaf)

## [0.80.2] — 2026-07-03

### Refatoração
- refactor(web): extrair hooks de estado do PlayerModal p/ components/player (604754f)

## [0.80.1] — 2026-07-03

### Refatoração
- refactor(web): extrair cards e tabs do DownloadsPage p/ components/downloads (4e9d320)

## [0.80.0] — 2026-07-03

### Novidades
- feat(web): i18n — internar PT restante + lint anti-regressão (check:i18n) (fe8716a)

### Correções
- fix(web): zerar as 8 new_violations do Sonar do PR i18n (#457) (5424255)

## [0.79.5] — 2026-07-03

### Correções
- fix(web): usar export…from p/ withToken no barrel client.ts (Sonar S7763) (c16a244)

### Refatoração
- refactor(streamer): Probe unificado — handlers reusam ProbeLocal (Refs #416) (c58cbdb)
- refactor(web): extrair stream/local/subtitles do client.ts (barrel) (994b55b)

## [0.79.4] — 2026-07-03

### Refatoração
- refactor(backend): split god-files (transmissionrpc/worker/hls/main) por concern (83199c5)

## [0.79.3] — 2026-07-03

### Correções
- fix(web): dedup formatadores + toast/notifyError + migrar confirms nativos (972cb4f)

## [0.79.2] — 2026-07-03

### Refatoração
- refactor(streamer): sentinel ErrTorrentNotActive + errors.Is nos handlers (85a1d66)

## [0.79.1] — 2026-07-03

### Manutenção / CI
- ci: estabilizar release — esperar Postgres nos testes + healthcheck de deploy 90s→300s (5b4c6e0)

## [0.79.0] — 2026-07-03

### Novidades
- feat(web): i18n da DiscoverPage (internar strings PT) (4007f6a)

### Refatoração
- refactor(web): extrair componentes folha do DownloadsPage + memoizar playlistView (cc822dd)
- refactor(streamer): extrair SSRF/cache-gc/filepick/ratelimit de streamer.go para arquivos próprios (184b832)

## [0.78.12] — 2026-07-03

### Manutenção / CI
- chore(release): remover Jenkinsfile legado + finalizar sanitização (#408) (bc0646e)

## [0.78.11] — 2026-07-03

### Correções
- fix(web): errMessage compartilhado — mostra a mensagem real do backend (e827524)

## [0.78.10] — 2026-07-03

### Correções
- fix(web): usar ??= em useVisiblePolling (Sonar S6606) (95dd310)

### Performance
- perf(web): code-splitting (hls.js fora do bundle inicial) + pollers pausam em aba oculta (01c764a)

## [0.78.9] — 2026-07-03

### Refatoração
- refactor(handlers): helpers de binding/resposta + queryBool (0b871f8)

## [0.78.8] — 2026-07-03

### Manutenção / Docs
- docs: adicionar templates de issue/PR e THIRD_PARTY-LICENSES (2250a67)

## [0.78.7] — 2026-07-03

### Manutenção / CI
- chore(ci): mover hosts internos dos workflows/scripts para vars/secrets (f86180d)

### Manutenção / Docs
- docs: alinhar versão do Go (1.22 -> 1.25) no README e CLAUDE.md (ffd9b17)

## [0.78.6] — 2026-07-03

### Correções
- fix(ci): fetch-depth 0 no checkout do deliver p/ o semver achar as tags (4a4955b)
- fix(ci): deploy recria o jackui no projeto compose correto + healthcheck (b7119bf)
- fix(watchlist): baseline silenciosa + notificação agregada por watch (e0d3fac)

### Performance
- perf(ci): adicionar cache de volume do Trivy para acelerar o scan (1fc3712)

## [0.78.5] — 2026-07-02

### Correções
- fix(ci): reusar REGISTRY_TOKEN para o bot aprovar o PR (8c9a650)

## [0.78.4] — 2026-07-02

### Correções
- fix(ci): definir REGISTRY e IMAGE no escopo do job deliver com fallback (ead92e7)

## [0.78.3] — 2026-07-02

### Correções
- fix(ci): adicionar hosts do sonar no /etc/hosts do runner (b9fc825)
- fix(ci): usar sonarqube-scanner oficial no workflow de Release (70d167b)

## [0.78.2] — 2026-07-02

### Correções
- fix(ci): executar SonarQube via npx sonar-scanner para compatibilidade com runner ARM64 (c2d6d07)

## [0.78.1] — 2026-07-02

### Correções
- fix(handlers): esperar conclusão da cópia do cache no teste local_cache_folder para evitar diretório temporário ocupado no cleanup (ed270ee)

## [0.78.0] — 2026-07-02

### Novidades
- feat(ci): migrar pipeline de CI/CD para Gitea Actions (0d7a409)
- feat(ci): protótipo dos workflows Gitea Actions (migração do Jenkins) (9b59342)

### Correções
- fix(ci): desabilitar rejeição de TLS no upload/download de artefatos locais para aceitar a CA do homelab (033824c)
- fix(handlers): esperar extração em background finalizar no teste de legenda para evitar erro de diretório temporário ocupado (05e5cd6)
- fix(ci): usar 127.0.0.1 em vez de localhost na URL do banco de teste (7a83a2d)
- fix(ci): formatar comando de limpeza com bloco literal para evitar erro de parse do YAML (67ff52f)
- fix(ci): alterar porta do postgres de teste para 5433 para evitar conflito de rede (fa5daee)
- fix(ci): remover diretivas container dos workflows e usar setups oficiais (5df45c6)

## [0.77.3] — 2026-07-02

### Correções
- fix(quality): zerar as 17 new_violations do quality gate do Sonar (38443e0)
- fix(transcode): single video map on subtitle burn-in + honest UI for image subs (958e2f4)

### Manutenção / CI
- ci: consolidar gates — Sonar fonte única, Trivy pré-push, gofmt/vet/ESLint (20972d8)

## [0.77.2] — 2026-07-02

### Correções
- fix(web): npm audit fix — form-data (HIGH) + react-router open redirect (fe06890)
- fix(ai): replace real adult studio/performer names with synthetic examples (69de72f)

### Manutenção / CI
- chore(release): scrub internal infra references from tracked files (5b63463)
- chore(release): add LICENSE (MIT) + community files + legal disclaimer (9db55da)

## [0.77.1] — 2026-07-01

### Correções
- fix(ai): show cancel button immediately upon starting benchmark (6cffa1a)

## [0.77.0] — 2026-07-01

### Novidades
- feat(ai): per-provider rate limiting so the benchmark doesn't self-throttle (9742ef5)

## [0.76.2] — 2026-07-01

### Correções
- fix(ci): make the SBOM→Dependency-Track upload truly non-gating (1d16af9)

### Manutenção / CI
- ci(jackui): repontar registry/tag/API para github.com (d99bcfc)

## [0.76.1] — 2026-07-01

### Correções
- fix(ai): grade RankBefore by completeness, not a binary incomplete flag (0053e0d)
- fix(ai): square accuracy in composite so it dominates latency (2b6352b)

## [0.76.0] — 2026-07-01

### Novidades
- feat(player): baixar pasta pela árvore + fim do re-expand periódico (ba621f4)

### Correções
- fix(discover): dedupe trending/discover lists to stop card duplication (f7e5eb9)

## [0.75.3] — 2026-07-01

### Correções
- fix(ai): recognize Google's "models/" id prefix so Gemini is actually discovered (6d0cfe5)

## [0.75.2] — 2026-07-01

### Correções
- fix(ai): soften benchmark latency weight (cube root) + correct cloud-model comment (d8c1ca8)

## [0.75.1] — 2026-07-01

### Refatoração
- refactor(ai): move model-selection defaults into config (overridable) (fdf7833)

## [0.75.0] — 2026-07-01

### Novidades
- feat(ai): add Google Gemini free tier to the LLM chain + fix local model pick (7edf7b6)

### Manutenção / CI
- ci(jackui): deploy pelo projeto unificado vpn-gateway (jackui virou service:gluetun) (9867adc)

## [0.74.5] — 2026-07-01

### Correções
- fix(ci): checkout do SBOM no ARM via hostname (IP de LAN nao roteia do cloud) (c3785fd)

### Manutenção / CI
- ci(jackui): roda SBOM em paralelo no ARM e otimiza o cdxgen (002f144)

## [0.74.4] — 2026-06-30

### Correções
- fix(web): surface benchmark cancel/progress and stop mislabeling incomplete runs as failed (f1eb57a)
- fix(ai): make the benchmark resilient — incremental saves, fair ranking, scaled timeouts, cancellable runs (e47db86)

## [0.74.3] — 2026-06-30

### Correções
- fix(ci): remove qualitygate wait property from SonarScanner execution (921aa49)

## [0.74.2] — 2026-06-30

### Correções
- fix(ci): disable qualitygate wait on SonarQube step (df09440)
- fix(ci): corrige path do host nos mounts docker-in-docker (Sonar + cdxgen) (d5412f1)
- fix(downloads): liberar handle do torrent no auto-rename (RSS preso) (019e22a)

### Manutenção / CI
- ci(jenkins): GITEA_API via hostname (github.com) p/ ci-bot approve no ARM (17b74ca)
- ci(jenkins): roda build de PR no agente ARM (offload), main intocada (9c2cc0c)

### Testes
- test(downloads): cobrir rename-antes-reseed e release do handle órfão (d3d354d)

## [0.74.1] — 2026-06-30

### Correções
- fix(transmissionrpc): report dynamic peer-port in session-get instead of hardcoded 51469 (629401b)

## [0.74.0] — 2026-06-30

### Novidades
- feat(favorites): ocultar/mostrar categoria no mobile (easter-egg) (e13fcb9)

## [0.73.0] — 2026-06-30

### Novidades
- feat(favorites): gerenciar categorias e atribuir favoritos no mobile (2ec6997)

### Manutenção / Docs
- docs: fluxo de download unificado + seleção em árvore (tri-state) (5d78d17)

## [0.72.1] — 2026-06-30

### Manutenção / CI
- ci: aponta Sonar/Dependency-Track para o CI tier no OCI ARM (9e94749)

## [0.72.0] — 2026-06-30

### Novidades
- feat(player): botão 'baixar pasta' por arquivo + cache via modal unificado (b7e135b)
- feat(downloads): unificar o fluxo de download em todos os pontos de entrada (08985a1)
- feat(downloads): seleção de arquivos em árvore com tri-state (núcleo) (35a8318)

### Refatoração
- refactor(downloads): extrair pickInitialSelection (complexidade S3776) (44a1521)

## [0.71.0] — 2026-06-29

### Novidades
- feat(player): mostrar enviado/baixado no painel e downloads + fullscreen no desktop (3196d5a)

## [0.70.2] — 2026-06-29

### Correções
- fix(favorites): resolver magnet de favoritos (redirect magnet: + recuperação) (d97aca8)

## [0.70.1] — 2026-06-27

### Manutenção / CI
- chore(config): remover env vars SQLite obsoletas (JACKUI_DB_PATH, JACKUI_STATE_DIR) (88ff867)

## [0.70.0] — 2026-06-27

### Novidades
- feat(db): infra Postgres — compose sidecar, overlay gluetun, CI, docs (a6eb9be)
- feat(db): portar history store + FTS5→tsvector (Postgres) (fe06135)
- feat(db): portar downloads store para PostgreSQL (c017318)
- feat(db): portar stores do streamer (favorites, seeds, metadata_cache) (87bde20)
- feat(db): portar ai/benchmark store para PostgreSQL (f26bff4)
- feat(db): portar watchlist store para PostgreSQL (7c19c40)
- feat(db): portar library store para PostgreSQL (37ac22f)
- feat(db): portar stores transfer e playlists para PostgreSQL (276506c)
- feat(db): portar push store + dbtest.SeedUsers para FKs em testes (7b228db)
- feat(db): portar stores audiometa e tmdb para PostgreSQL (6a25a93)
- feat(db): portar auth store para PostgreSQL + wiring do pool no main (e9b08e9)
- feat(db): subcomando migrate-auth (ETL auth.db SQLite -> Postgres) (8a8f9a5)
- feat(db): schema PostgreSQL unificado com FKs (0002_init) (1bb2822)
- feat(db): fundação PostgreSQL (pool, migrate runner, dbutil.Rebind, dbtest) (3a92f4b)

### Refatoração
- refactor(db): auditoria de coerência — pool tunável + comentários (93b0f03)

### Testes
- test(db): dbtest rápido — schema por processo + TRUNCATE por teste (2f0343f)

## [0.69.0] — 2026-06-26

### Novidades
- feat(transfer): modo de concorrência configurável (auto/serial/parallel) (19b2420)

### Correções
- fix(discover): botão "ignorar recomendação" acessível no touch (c16f631)
- fix(seeds): limpar auto-seed persistido ao remover/parar de seedar (2bd0431)

## [0.68.0] — 2026-06-26

### Novidades
- feat(promote): serializar cópia em HDD (evita seek thrashing) (0de30d8)

## [0.67.0] — 2026-06-26

### Novidades
- feat(transfer): retomar promoção interrompida por restart (boot reconcile) (7b69337)
- feat(transfer): cópia resume-aware (pula o que já foi copiado) (2ac0361)
- feat(promote): paralelizar cópia + fechar o modal na hora (239f67d)

## [0.66.1] — 2026-06-26

### Correções
- fix(promote): promover na aba Downloads é assíncrono (evita 504 em arquivo grande) (838dd3c)

## [0.66.0] — 2026-06-26

### Novidades
- feat(downloads): filtro e ordenação dentro do torrent multi-arquivo (3a7f4aa)

### Correções
- fix(promote): promover whole-torrent (diretório) falhava 'is a directory' (3080185)

## [0.65.2] — 2026-06-26

### Correções
- fix(promote): pasta sem subpastas crashava o modal com 'f.length' (0072406)

## [0.65.1] — 2026-06-26

### Correções
- fix(downloads): recuperar dst.part do anacrolix relocated storage no move (05ef681)

## [0.65.0] — 2026-06-26

### Novidades
- feat(downloads): botão retentar grupo no nível do torrent (6d33c2f)

### Correções
- fix(downloads): move de grupo idempotente + retry em caso de falha (fa0969d)
- fix(downloads): simplify summary stats calculation from torrents directly (c6db94e)
- fix(downloads): deduplicate totalDown rate and align unit display (9fc66f1)

### Manutenção / Docs
- docs(downloads): document torrent-level statistics invariants and double-counting avoidance (8665453)
- docs(changelog): reconstruir por versão real (v0.59.0–v0.64.0) alinhado às tags (090d639)

---

> As entradas abaixo foram reconstruídas na versão anterior do changelog, alinhadas às tags reais, e preservadas como histórico.

## [0.64.0] — 2026-06-25

### Adicionado
- **Baixar pack inteiro como UMA linha** (#356): com TODOS os arquivos marcados (o caso comum de um
  pack), o download enfileira 1 linha "torrent inteiro" (`fileIndex = -2`, file priorities do
  anacrolix) em vez de N linhas por-arquivo — um pack de 778 arquivos vira **1 linha**. Selecionar um
  subconjunto continua batch (1 linha por arquivo), preservando a granularidade.

### Corrigido
- **Menu "Local" 2x não some mais com os mounts** (#357): clicar a nav zerava o `?mount=` e o
  auto-select do 1º mount só rodava uma vez; no mobile o seletor vive dentro do bloco do mount ativo e
  sumia. Agora a re-seleção é reativa.

## [0.63.1] — 2026-06-25

### Performance
- **`GET /api/downloads` rápido** (#355): novo `Streamer.LiveStats` — O(1) por torrent (sample de taxa
  + seeders, **sem** o `buildInfo` O(arquivos)) no enrich da lista. Num pack de 778 arquivos a resposta
  caiu de **2–17 s para ~1 s**. O contador da página passou a ser **por torrent**, não por linha.

### Corrigido
- **Watchdog de shutdown** (#355): `runCleanup` força `os.Exit(0)` após 20 s se o cleanup
  (anacrolix/DHT) travar com a rede caída — antes pendurava o processo num **502 eterno** (causou um
  outage); com `restart: unless-stopped` o Docker recria o container.

## [0.63.0] — 2026-06-25

### Adicionado
- **Ordenar downloads** por velocidade de download/upload e por seeds (#352).
- **Filtrar a lista de arquivos** local por status — baixando / concluído (#353).

### Performance
- **CPU ocioso reduzido** (#354): o HLS do ffmpeg fecha ao sair o **último** viewer (libera o slot de
  GPU-decode) em vez de sobreviver até o reap de 5 min; 3 polls do front pausam com a aba oculta; perfil
  de baixo consumo (`GOGC`/`GOMEMLIMIT`/`GOMAXPROCS` + peer caps) documentado.

## [0.62.2] — 2026-06-25

### Corrigido
- **Seed via metainfo em cache** (#351): o re-seed usa um magnet de info_hash puro → `resolveMagnet`
  cache-first → semeia in-place. Acaba com o erro "auto-seed failed: .torrent URL 404".

### Performance
- **Seek instantâneo em vídeo baixado** (#350): `/api/local/file` serve um arquivo de disco LOCAL via
  `http.ServeFile` (sendfile); só mount remoto/FUSE (`isRemoteFS`) usa a Session com read-ahead de 16 MB.

## [0.62.1] — 2026-06-25

### Corrigido
- **Lista O(N) travando a UI** + **transcode GPU robusto** (#349): o enrich da lista dedupe por
  info_hash; um semáforo limita decoders CUDA e a sessão excedente cai para **decode em CPU** em
  `CUDA_ERROR_OUT_OF_MEMORY` em vez de falhar.

## [0.62.0] — 2026-06-25

### Adicionado
- **Torrent multi-arquivo como UMA unidade** (#347): o scheduler conta **1 slot por torrent**, e o
  worker faz 1 verify + 1 completion-move + 1 AI-rename + 1 ciclo de stall por torrent (fim do consumo
  O(N) de CPU/RAM que dava OOM em packs grandes).
- **Enqueue em lote + UI agrupada por torrent** — 1 card por torrent (#348).

### Corrigido
- **Completions travadas em "moving"** (move-not-found) após o agrupamento por categoria (#346):
  `completion_dest` congelado + fallback sem-categoria.

## [0.61.0] — 2026-06-24

### Adicionado
- **Categoria como pasta agrupadora** por padrão (`…/<user>/<categoria>/…`) (#345).

## [0.60.0] — 2026-06-24

### Adicionado
- Botão **"Ver arquivos"** no card de torrent streaming (completo/semeando) (#342).
- Botão **"Abrir no local"** nos downloads concluídos (#344).
- Indicador **"baixando"** em arquivos/pastas incompletos (`.part`) (#343).

## [0.59.1] — 2026-06-24

### Corrigido
- **Pastas de download duplicadas** (`Name (1)`, `(2)`…) causadas pela migração `UserSubpath` (#341).

## [0.59.0] — 2026-06-24

Release grande — acumulou ~4 dias desde a v0.58.7 (a saga de playback no iOS + uma frente de
downloads/seed + endurecimento de auth/boot/shutdown).

### Adicionado

#### Player / Música
- **Capa no Now Playing** (mediaSession artwork 96/512); **faixa de áudio selecionável no HLS**
  (torrent + local); **mini-player de áudio** que vira barra no footer (arrastar/expandir).

#### Downloads / Seed
- **Escolher o destino do download** (picker com browse); **baixar direto no bulk** + re-seed na
  promoção; **auto-seed de downloads completos** de trackers de seed; **seed seletivo por tracker** +
  painel de peers; **storage por-torrent** aponta pro local real (seed sem re-baixar).

#### Busca / Infra
- **Reordenar abas de pesquisa** arrastando; **card abre em nova aba** + player full-viewport no
  deep-link; **retry com backoff** em chamadas externas idempotentes; **endpoint de refresh da
  peer-port** integrado ao routing do gluetun.

### Corrigido

#### Player (iOS / Safari)
- **Playback nativo no iOS** acertado após várias iterações: vídeo e áudio tocam com o `src` setado no
  gesto (sem `play()` programático nem `v.load()`), saindo do `readyState 2`; vídeo local no iOS via
  **HLS/remux** (MP4 progressive trava no WebKit); auto-avanço entre faixas; **legenda embutida/sidecar
  no Safari** (HLS nativo); legendas em bottom-sheet no mobile; shuffle/repeat de faixa + dock desktop.

#### Auth / Confiabilidade
- **Não desloga no deploy**: janela de graça na rotação de refresh + tolera falha transitória; exige
  `jwt_secret` no boot (auth ON); `busy_timeout(5000)` em todos os stores SQLite; `/healthz` checa o
  streamer; **graceful restart** no watcher de porta do VPN (para HLS/workers/transfers antes de sair).

#### Downloads / Streamer
- **Fallback p/ magnet por info_hash** quando a URL `.torrent` dá 404; move idempotente p/ single-file
  (já-no-destino = sucesso); `resumeSeeding` espera o `FilePathResolver` no boot (senão o seed resumido
  cai em 0%); completion in-memory no `relocatedStorage` (evita conflito de bolt no DataDir).

#### Busca / Nav / UI
- Botões do card não disparam o link do card (abrir player); só restaura a rota salva no PWA (refresh
  não redireciona); botão "parar" em transfers cancela de verdade (era no-op); chips de filtro no topo
  da lista; limpar filtros revela resultados de contagem desconhecida + card não vaza com nome longo;
  logo não navega (só dispara o easter egg); reabrir o app restaura a playlist no boot frio (sem `?play`).

---

> As entradas abaixo são do **esquema de numeração manual antigo** (anterior à automação de versão por
> tags); ficam aqui como histórico. Não confundir com o `0.x` das tags atuais.

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
