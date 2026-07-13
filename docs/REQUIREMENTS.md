# Requisitos & Plano de Correção — JackUI

> Gerado por deepwork (sessão 2026-07-09). Revisado por @oracle.
> **Atualizado 2026-07-11**: prod `v0.91.0` (commit `27d9ba0`) · M0, M0.5, M1 e M3 **entregues e no ar** · M2 aberto.
> Diagnóstico original (2026-07-10): M0 (PR-1+PR-2+PR-3) concluído (#508); M0.5 auditoria concluída; M1/M2/M3 abertos.

## Visão geral

O projeto está saudável (gate CVE CRITICAL ativo, auth declarativo, SonarQube+Trivy+Dependency-Track no release).
Este documento organiza o trabalho em **5 marcos sequenciais por risco crescente**.

## Estado de entrega (2026-07-11)

| Marco | Estado | Evidência |
|---|---|---|
| **M0** — Docs | ✅ entregue | #508 (PR-2/3) + #511 (PR-1 `JACKUI_DL_AUTO_PROMOTE_ARR`); CA-0.1 vazio |
| **M0.5** — Segurança | ✅ entregue | auditoria 2026-07-10 (`SECURITY.md`, `design-decisions.md`) |
| **M1** — God-classes | ✅ entregue | #510 (16 Go) + #512 (9 `.tsx`) + #514 (fix Sonar); **0 arquivos `.go`/`.tsx` >600 ln na main** |
| **M2** — HLS Phase 2 | ⬜ **aberto** | único milestone restante; feature (ver seção M2) |
| **M3** — Testes | ✅ entregue | #513; 34/37 `time.Sleep` removidos; CA-3.2 script + audit local 3/3 (100%) |

> Deploy confirmado: prod respondendo em `v0.91.0`, commit `27d9ba0` (= head da `main`), `Release / sonar` e `Release / deliver` = success.
> Débitos residuais e follow-ups: ver seção **"Débito residual & follow-ups"** ao final.

---

## Marcos (sequência final)

```
M0 ✅ Docs     → M0.5 ✅ Segurança → M1 ✅ Decompor god-classes → M2 ⬜ HLS Phase 2 → M3 ✅ Testes
(risco zero)     (auditoria)         (risco médio/alto)           (feature)          (baixo)
```

### M0 — Alinhamento de documentação  ·  *3 PRs separados*

**PR-1 · README vars** ✅ (bulk em #508; última var `JACKUI_DL_AUTO_PROMOTE_ARR` em #511)
- `README.md` documentava dirs inexistentes; código usa `JACKUI_STREAM_DIR/DOWNLOAD_DIR/SHARED_DIR` (`internal/config/config_env.go`).
- Todas as vars faltantes documentadas (`JWT_SECRET`, `PG_*`, `SMTP_*`, `NTFY_*`, `DL_*`, `AI_*`, `ADMIN_*`, `HLS_VOD_MODE`, `PEER_PORT`, `METRICS_TOKEN`, `STORAGE_BACKEND`, `EXTERNAL_MOUNTS`, `GLUETUN_*`, `LOCAL_*`, `LOG_FORMAT`, `BASE_URL`, `CONTROL_TOKEN`, `DL_AUTO_PROMOTE_ARR`).
- **CA-0.1 ✅**: `diff <(vars no config_env.go) <(vars no README)` → vazio (ignorando prefixo genérico `JACKUI_PG_`).

**PR-2 · design-decisions.md** ✅
- `§126` descrevia "SQLite (modernc), one writer" como vigente → projeto migrou p/ PostgreSQL.
- §126 atualizado + refs SQLite limpas para contexto histórico.
- **CA-0.2**: "SQLite" só em contexto histórico ✅

**PR-3 · CHANGELOG backfill + redirect** ✅
- ~70 releases catalogadas (`v0.65.0→v0.90.2`) em português, com hashes.
- Redirect no topo: "A partir da **v0.81.0**" (Gitea Releases começam nessa tag, não v0.65.0).
- Fonte: `GET /releases` (v0.81.0+) + `git log` (v0.65.0→v0.80.4).
- **CA-0.3**: CHANGELOG cobre todas as tags até `v0.90.2` ✅

### M0.5 — Auditoria de superfície de segurança  ·  ✅ *concluída 2026-07-10*
- **233 rotas** inventariadas em `cmd/server/routes.go` + `internal/transmissionrpc/handler.go`.
- Cobertura de `auth.Required`/`AdminOnly`/`GuestRestrict` confirmada — **zero rotas sensíveis sem auth**.
- CORS: `AllowAllOrigins = true` — aceitável para SPA server-less atrás de reverse proxy.
- CSRF: ausente por design (SPA + JWT Bearer, não cookies). Session-id do Transmission RPC é o único CSRF.
- **CA-0.5.1 ✅**: inventário completo em `SECURITY.md`.
- **CA-0.5.2 ✅**: decisão documentada em `docs/design-decisions.md` — aceitar risco sem generic rate-limiter; implementar no reverse proxy se necessário.

### M1 — Decomposição de god-classes  ·  ✅ **ENTREGUE (2026-07-11)**  ·  *risco médio/alto*
> **Resultado**: #510 (16 arquivos Go) + #512 (9 `.tsx`), extração mecânica sem mudança de comportamento.
> - **CA-1.1 ✅** — nenhum `.go`/`.tsx` (não-teste) >600 ln na main (verificado pós-merge).
> - **CA-1.2 ✅** — complexidade ≤15: reduções em `confirmDownloads`/`SearchFilterFields` (frontend) e `streamer_gc.go`/`browser_cleanup.go` (Go); zero NOSONAR novo. As 20 `new_violations` que a decomposição reexpôs no Sonar (bloquearam o deploy) foram zeradas em #514.
> - **CA-1.3 ✅** — cobertura preservada: 541→559 testes vitest verdes (npm ci completou deps), 2517+ testes Go verdes.
> - ⚠️ Fora do escopo do CA-1.1 (só `.go`/`.tsx`): `web/src/api/local.ts` (667) e `stream.ts` (644) seguem >600 — candidatos a follow-up.
>
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

### M2 — HLS Master Playlist Phase 2  ·  ⬜ **ABERTO — próximo milestone**  ·  *requisito de produto (único `[ ]` do Roadmap, README:164)*
> Não iniciado. Agora desbloqueado (M1 decompôs `hls.go` e `PlayerModal`). É feature: exige validação E2E com MKV multi-stream real — recomenda-se sessão dedicada, não fechar às pressas.
- Multi-resolução **on-demand** (variantes só se fonte >1080p ou cliente em baixa banda).
- Tracks de áudio/legenda unificados como `#EXT-X-MEDIA` no master (hoje por-sessão).
- Implementado **sobre `hls.go` já decomposto** no M1.
- **Restrições inquebráveis**: `-hls_playlist_type event` (não vod), sem `append_list`, `-muxdelay 0 -muxpreload 0`, H.264 Level 5.2 p/ 4K, semáforo GPU (`gpusem.go`) conta variantes, fallback CPU-decode em `CUDA_ERROR_OUT_OF_MEMORY`.
- **CA-2.1**: master com ≥2 variantes p/ fonte ≥1080p.
- **CA-2.2**: `#EXT-X-MEDIA TYPE=AUDIO` e `TYPE=SUBTITLES` presentes.
- **CA-2.3**: teste E2E com MKV multi-stream valida o manifest.

### M3 — Estabilização de testes  ·  ✅ **ENTREGUE (parcial, 2026-07-11)**  ·  *baixo risco*
> **Resultado**: #513 — 34/37 `time.Sleep` removidos (incl. o de 10s em `jackett/client_more_test.go`, ganho direto no CI). Técnicas: channels de sinal / `sync.WaitGroup` / done-channel onde há hook do produtor; poll determinístico `for time.Now().Before(deadline) { if cond break; <-time.After(Nms) }` onde não há (cede CPU, sem busy-spin nem wall-clock cego). Sem dep externa (testify NÃO é usado no projeto).
> - **CA-3.1 ✅ (parcial)** — 3 `time.Sleep` mantidos, justificados: `transcode/hls_test.go` (janela Seek/Read p/ regressão STSC/STCO), `ai/benchmark_test.go` e `ai/client_test.go` (observação de concorrência/ausência-de-concorrência onde uma barreira causaria deadlock).
> - **CA-3.2 ✅ (parcial)** — `scripts/ci-stability-audit.sh`: audit local 3/3 go+vitest = 100% (2026-07-13). Rodar `./scripts/ci-stability-audit.sh 20` no runner ARM para fechar formalmente.
> - ⚠️ `metrics.StartWorker` passou a retornar `<-chan struct{}` (aditivo, caller ignora) como hook determinístico de shutdown.
- **Quick-fix isolado**: `internal/jackett/client_more_test.go:105` (`time.Sleep(10*time.Second)`) → channel sync (ganho imediato de ~10s no CI).
- `internal/transcode/duration_retry_test.go` (5 sleeps), `localcache/cache_test.go`, `watchlist/sched_test.go`, `transfer/tracker_test.go` → `testify.Eventually`/channels.
- **CA-3.1**: zero `time.Sleep` em testes.
- **CA-3.2**: CI pass-rate ≥99% em 20 runs sem retry.

---

## Backlog não-prioritário (riscos cegos investigados — sem ação imediata)
- **PostgreSQL escala**: 26 índices nas migrations, pool pgx — saudável. Vale um `EXPLAIN` nas queries críticas em sessão futura.
- **CVEs**: SECURITY.md confirma Trivy `--severity CRITICAL --exit-code 1` pré-push — sem débito conhecido.
- **API contracts**: sem OpenAPI/Swagger; Transmission RPC (Sonarr/Radarr) tem testes. Monitorar breaking changes.

## Débito residual & follow-ups (aberto após a entrega de 2026-07-11)

Requisitos remanescentes, em ordem de prioridade:

- **R1 — M2 (HLS Master Playlist Phase 2)** · *feature*: implementar sobre `hls.go` decomposto (ver seção M2). CA-2.1/2.2/2.3. Sessão dedicada com validação E2E de MKV multi-stream.
- **R2 — CA-3.2 (estabilidade de CI)**: script `scripts/ci-stability-audit.sh` adicionado; audit local 3-run = 100%. Formal: 20 runs no runner (`./scripts/ci-stability-audit.sh 20`).
- **R3 — `.ts` >600 ln**: ✅ `local.ts` + `stream.ts` decompostos (barrel + módulos irmãos; `stream.ts` 646→17 ln).
- **R4 — Higiene de branches**: `scripts/branch-hygiene.sh --delete-merged` remove locais já mergeadas; remotes órfãs (`feat/i18n`, etc.) exigem `git push origin --delete` manual após revisão.

## Fora de escopo deste plano
- Backup/restore do PostgreSQL (operação de homelab, não de código).
- Observabilidade/alertas (Prometheus existe via `JACKUI_METRICS_TOKEN`; tuning é operacional).
