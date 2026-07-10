# Requisitos & Plano de Correção — JackUI

> Gerado por deepwork (sessão 2026-07-09). Revisado por @oracle.
> Estado: prod `v0.90.2` · 0 issues/PRs/milestones abertos · CI ativo (Sonar+Trivy+DT).
> Diagnóstico: PR-2 e PR-3 concluídos. Próximo: PR-1 (README vars).

## Visão geral

O projeto está saudável (3 NOSONAR, 0 TODOs técnicos, gate CVE CRITICAL ativo, auth declarativo existe).
Este documento organiza o trabalho restante em **5 marcos sequenciais por risco crescente**.

---

## Marcos (sequência final)

```
M0  Docs        → M0.5 Segurança → M1 Decompor god-classes → M2 HLS Phase 2 → M3 Testes
(risco zero)      (auditoria)       (risco médio/alto)        (feature)        (baixo)
```

### M0 — Alinhamento de documentação  ·  *3 PRs separados*

**PR-1 · README vars** (CRÍTICO — desinformação ativa)
- `README.md:94-96` documenta `JACKUI_CACHE_DIR/STORAGE_DIR/CONFIG_DIR` → **inexistentes** no código.
- Código usa `JACKUI_STREAM_DIR/DOWNLOAD_DIR/SHARED_DIR` (`internal/config/config_env.go:152-153,193-199`).
- Documentar as ~41 vars faltantes: `JACKUI_JWT_SECRET`, `PG_*` (7), `SMTP_*` (5), `NTFY_*` (3), `DL_*` (8), `AI_*`, `ADMIN_*`, `HLS_VOD_MODE`, `PEER_PORT`, `METRICS_TOKEN`, `STORAGE_BACKEND`, `EXTERNAL_MOUNTS`, `GLUETUN_*`, `LOCAL_*`, `LOG_FORMAT`, `BASE_URL`, `CONTROL_TOKEN`...
- **CA-0.1**: `diff <(vars no config_env.go) <(vars no README)` → vazio.

**PR-2 · design-decisions.md** ✅
- `§126` descrevia "SQLite (modernc), one writer" como vigente → projeto migrou p/ PostgreSQL.
- §126 atualizado + refs SQLite limpas para contexto histórico.
- **CA-0.2**: "SQLite" só em contexto histórico ✅

**PR-3 · CHANGELOG backfill + redirect** ✅
- ~70 releases catalogadas (`v0.65.0→v0.90.2`) em português, com hashes.
- Redirect no topo: "A partir da **v0.81.0**" (Gitea Releases começam nessa tag, não v0.65.0).
- Fonte: `GET /releases` (v0.81.0+) + `git log` (v0.65.0→v0.80.4).
- **CA-0.3**: CHANGELOG cobre todas as tags até `v0.90.2` ✅

### M0.5 — Auditoria de superfície de segurança  ·  *read-only → decisão*
- Inventariar as **229 rotas** registradas e confirmar cobertura de `auth.Required`/`AdminOnly`
  (já existe em `cmd/server/routes.go:210,229,589` — é confirmar/documentar).
- Avaliar **rate-limiting** em `/api/*` (hoje só há throttle de torrent bytes/s).
- Confirmar CORS (`routes.go:154`) e CSRF (só Transmission RPC hoje).
- **CA-0.5.1**: inventário de rotas com coluna "auth required"; zero sensíveis sem auth.
- **CA-0.5.2**: decisão de rate-limit documentada (implementar ou aceitar risco c/ justificativa).

### M1 — Decomposição de god-classes  ·  *risco médio/alto · rede de segurança: 3205 funções de teste + auditoria #416*
> Feito ANTES do HLS Phase 2 pois `hls.go` + `PlayerModal` são onde a feature aterrissa.
> "Slow down to speed up" (@oracle).

**Backend Go** (>600 ln, 15 arquivos):
- `internal/streamer/streamer.go` (1164) · `internal/downloads/worker.go` (1079) · `internal/handlers/stream.go` (1037)
- + `downloads.go (852)`, `local_play.go (848)`, `transcode/hls.go (844)`, `cmd/server/wiring.go (828)`, `auth/store.go (814)`, `ai/client.go (763)`, `handlers/auth.go (762)`, `local/local.go (758)`, `local/browser.go (744)`, `transmissionrpc/views.go (732)`, `downloads/aggregate.go (713)`, `downloads/store.go (636)`.

**Frontend React** 🔴 (piores que o backend):
- `web/src/components/PlayerModal.tsx` (**1875**) · `pages/DownloadsPage.tsx` (1384) · `pages/SearchPage.tsx` (1098) · `pages/LocalPage.tsx` (1014) · `pages/FavoritesPage.tsx` (1005) · +7 entre 450-751.

- **CA-1.1**: nenhum `.go`/`.tsx` (não-teste) > 600 linhas.
- **CA-1.2**: complexidade cognitiva ≤15/função; zero NOSONAR novo.
- **CA-1.3**: cobertura de testes não cai (baseline registrada antes do refactor).

### M2 — HLS Master Playlist Phase 2  ·  *requisito de produto (único `[ ]` do Roadmap, README:164)*
- Multi-resolução **on-demand** (variantes só se fonte >1080p ou cliente em baixa banda).
- Tracks de áudio/legenda unificados como `#EXT-X-MEDIA` no master (hoje por-sessão).
- Implementado **sobre `hls.go` já decomposto** no M1.
- **Restrições inquebráveis**: `-hls_playlist_type event` (não vod), sem `append_list`, `-muxdelay 0 -muxpreload 0`, H.264 Level 5.2 p/ 4K, semáforo GPU (`gpusem.go`) conta variantes, fallback CPU-decode em `CUDA_ERROR_OUT_OF_MEMORY`.
- **CA-2.1**: master com ≥2 variantes p/ fonte ≥1080p.
- **CA-2.2**: `#EXT-X-MEDIA TYPE=AUDIO` e `TYPE=SUBTITLES` presentes.
- **CA-2.3**: teste E2E com MKV multi-stream valida o manifest.

### M3 — Estabilização de testes  ·  *baixo risco*
- **Quick-fix isolado**: `internal/jackett/client_more_test.go:105` (`time.Sleep(10*time.Second)`) → channel sync (ganho imediato de ~10s no CI).
- `internal/transcode/duration_retry_test.go` (5 sleeps), `localcache/cache_test.go`, `watchlist/sched_test.go`, `transfer/tracker_test.go` → `testify.Eventually`/channels.
- **CA-3.1**: zero `time.Sleep` em testes.
- **CA-3.2**: CI pass-rate ≥99% em 20 runs sem retry.

---

## Backlog não-prioritário (riscos cegos investigados — sem ação imediata)
- **PostgreSQL escala**: 26 índices nas migrations, pool pgx — saudável. Vale um `EXPLAIN` nas queries críticas em sessão futura.
- **CVEs**: SECURITY.md confirma Trivy `--severity CRITICAL --exit-code 1` pré-push — sem débito conhecido.
- **API contracts**: sem OpenAPI/Swagger; Transmission RPC (Sonarr/Radarr) tem testes. Monitorar breaking changes.

## Fora de escopo deste plano
- Backup/restore do PostgreSQL (operação de homelab, não de código).
- Observabilidade/alertas (Prometheus existe via `JACKUI_METRICS_TOKEN`; tuning é operacional).
