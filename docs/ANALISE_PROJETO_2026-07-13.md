# Análise do Projeto JackUI — 2026-07-13

> Snapshot de produto, arquitetura, saúde e roadmap de melhorias.
> Base: `README.md`, `docs/ARCHITECTURE.md`, `docs/REQUIREMENTS.md`,
> `docs/PERFORMANCE.md`, `docs/HLS_MASTER_PLAYLIST_PLAN.md`,
> `docs/UX_USABILITY_REQUIREMENTS.md`, auditoria de 2026-07-10 e estado
> do repositório em **2026-07-13** (branch `refactor/r3-stream-api-decomp`).

---

## Execução do plano de melhorias (2026-07-13)

Sessão que implementou o roadmap priorizado da análise. Commits na branch
`refactor/r3-stream-api-decomp` (4 commits + chore make):

| Item | Status | Evidência |
|---|---|---|
| **M2b** — HLS áudio/legendas | ✅ já na `main` | PRs #520–#522 (`EXT-X-MEDIA`, toggle admin, `hls.audioTrack` seamless) |
| **R3** — `stream.ts` | ✅ entregue | 646→17 ln (barrel) + 9 módulos irmãos (`stream-core`, `stream-health`, …) |
| **R3** — `local.ts` | ✅ já na `main` | 667→350 ln (commit `ed6ce14`) |
| **Perf #5** — batch create downloads | ✅ já existia | `DownloadsBatchCreate` + `downloadBatchCreate` no `DownloadModal`; `PERFORMANCE.md` atualizado |
| **UX PR2** — estados honestos | ✅ entregue | `AsyncState`, `StatusBanner`, `RetryPanel`; integrado em Discover, Search, Library, Local |
| **CA-3.2** — estabilidade CI | 🟡 script + audit local | `scripts/ci-stability-audit.sh`; 3/3 go+vitest = 100%; formal: `make test-stability RUNS=20` no runner ARM |
| **Concurrency Gitea** | ✅ já configurado | `ci.yml` (cancel PR), `release.yml` / `mutation.yml` (serial, sem cancel) |
| **R4** — higiene branches | 🟡 parcial | `scripts/branch-hygiene.sh --delete-merged`; 7 branches locais mergeadas removidas |

**Testes após execução:** 576 vitest + suite Go verdes (`make test`).

**Próximo passo sugerido:** abrir PR de `refactor/r3-stream-api-decomp` → `main`.

---

## 1. O que o projeto faz

**JackUI** é um **media server self-hosted** que transforma torrents em streaming
no browser **antes do download terminar**. Começou como front visual do
[Jackett](https://github.com/Jackett/Jackett) e cresceu para um stack completo:
busca → BitTorrent→HTTP com Range → transcode on-demand → playback (Safari via HLS).

| Capacidade | Como funciona |
|---|---|
| **Busca** | Jackett → resultados agrupados por `infoHash`, enriquecidos com TMDB/arte |
| **Play imediato** | `anacrolix/torrent` → HTTP Range → `<video>` (ou HLS no Safari) |
| **Transcode** | ffmpeg on-demand (NVENC / VAAPI / QSV / libx264) para HEVC/AV1/AC3/DTS |
| **Fila de downloads** | Worker assíncrono, **1 torrent = 1 slot**, auto-seed, rename por AI |
| **Biblioteca** | Continue Watching, playlists, watchlists + ntfy, favoritos, mounts locais/rclone |
| **Integração *arr** | Transmission-RPC opt-in (Sonarr / Radarr / Prowlarr) |
| **Auth** | JWT opcional, MFA, passkeys, roles admin / user / guest |
| **Deploy** | binário único com UI embutida + Docker (prod atrás de gluetun) |

### Fluxo mental

```
Busca (Jackett) → Play / Download
       │
       ▼
anacrolix (swarm) → piece cache (LRU)
       │
       ├─ codec OK?  → HTTP Range (direct play)
       └─ codec ruim? → ffmpeg → HLS (Safari) / progressive
```

---

## 2. Stack

| Camada | Tecnologia |
|---|---|
| Backend | Go 1.25, Gin, anacrolix/torrent |
| Transcode | ffmpeg (NVENC / VAAPI / QSV / libx264), HLS para Safari |
| Frontend | React 18 + TypeScript + Vite + Tailwind, i18n pt/en |
| Embed | `//go:embed all:dist` em `ui/embed.go` |
| Storage | PostgreSQL (`JACKUI_DATABASE_URL`); piece cache em disco |
| Desktop | Electron opcional (`electron/`) |
| Deploy | Docker; CI Gitea Actions + SonarQube + Trivy + Dependency-Track |

---

## 3. Arquitetura (resumo)

```
web/src/          React 18 + TS + Vite + Tailwind (i18n pt/en)
ui/embed.go       //go:embed dist → binary único
cmd/server/       Gin, wiring, workers
internal/
  streamer/       coração BitTorrent + Range + probe + art
  transcode/      HLS sessions, GPU semaphore, variantes ABR
  downloads/      fila + aggregate-by-torrent
  handlers/       maior superfície HTTP (~15k linhas de produção)
  auth, tmdb, ai, local, transmissionrpc, …
PostgreSQL        estado durável (migrations golang-migrate)
```

### Pacotes Go por tamanho aproximado (produção, 2026-07-13)

| Pacote | Linhas (aprox.) | Papel |
|---|---:|---|
| `internal/handlers` | ~15 000 | HTTP API |
| `internal/streamer` | ~5 700 | BitTorrent, cache, probe |
| `internal/downloads` | ~4 900 | Fila + worker + aggregate |
| `internal/ai` | ~3 200 | Title cleanup + benchmark |
| `internal/transcode` | ~2 700 | ffmpeg / HLS |
| `internal/transmissionrpc` | ~2 500 | Compat *arr* |
| `internal/auth` | ~1 500 | JWT, MFA, passkeys |

### Invariantes críticos (não regredir)

1. **VOD é o default** — não “consertar” seek virando live/EVENT.
2. **Safari = HLS** com `-muxdelay 0 -muxpreload 0` (stall em `t=0`).
3. **Fonte do ffmpeg tem que ser seekable** (loopback HTTP com Range, não pipe).
4. **Lista de downloads O(torrents), nunca O(files)** — `LiveStats`, não `buildInfo`.
5. **Shutdown com hard deadline (20s)** — VPN/DHT pode travar o `Close` para sempre.
6. **Um torrent = uma unidade de fila** — scheduler/worker em `aggregate.go`.
7. **Media token** (`?token=`) só em rotas de mídia (`/api/stream/*`, subtitles, local).

Documentação de referência: `docs/ARCHITECTURE.md`, `docs/design-decisions.md`.

---

## 4. O que está forte

1. **Produto real, não demo** — busca → play → fila → biblioteca → *arr* → Electron.
2. **Engenharia de streaming madura** — gotchas Safari, GPU OOM → CPU fallback,
   VOD sintético, media token para `<video>`.
3. **Modelo de downloads correto** — aggregate-by-torrent evita OOM em packs grandes.
4. **CI/CD com gates reais** — testes, Sonar (`new_coverage ≥ 80%`, `new_violations = 0`),
   Trivy CRITICAL, SBOM; deploy bloqueado se gate falha.
5. **Refactor M0/M1/M3 entregues** — god-files `.go`/`.tsx` saíram de 1000–1800 linhas
   para **tudo < 600** (exceto APIs `.ts` de follow-up R3).
6. **N+1 de maior impacto já atacados** — batch TMDB match, stream health peek,
   local play batch (`docs/PERFORMANCE.md` #1–#4).
7. **HLS multi-res (M2a) na main** — master playlist com variantes (`#516`);
   E2E de CI aliviado (`#517`).
8. **Docs acima da média** — arquitetura, decisões, requisitos, performance,
   segurança, planos HLS/UX.

---

## 5. Estado dos marcos (jul/2026)

Fonte canônica: `docs/REQUIREMENTS.md` (atualizado 2026-07-11) + commits recentes.

| Marco | Estado | Evidência / nota |
|---|---|---|
| **M0** — Docs | ✅ | README/env alinhados (#508, #511) |
| **M0.5** — Segurança | ✅ | 233 rotas inventariadas; `SECURITY.md` |
| **M1** — God-classes | ✅ | 0 `.go`/`.tsx` prod > 600 ln (#510, #512, #514) |
| **M2** — HLS Phase 2 | ✅ entregue | M2a (#516) + **M2b** (#520–#522): `EXT-X-MEDIA`, `hls.audioTrack` |
| **M3** — Testes | ✅ | 34/37 `time.Sleep` removidos; CA-3.2 script + audit local 3/3 = 100% |
| **R3** — API modules | ✅ | `local.ts` 350 ln + **`stream.ts` 17 ln barrel** (9 módulos irmãos) |
| **UX PR2** — Estados | ✅ | `AsyncState` / `StatusBanner` / `RetryPanel` em 4 páginas |

Prod reportado em torno de **v0.91.x**. Cobertura Go global histórica ~55%;
o gate de **new_coverage 80%** protege o *diff*, não o legado.

### Arquivos de produção ainda grandes (≥ 500 ln, pós-execução 2026-07-13)

| Arquivo | Linhas (aprox.) | Nota |
|---|---:|---|
| `cmd/server/routes.go` | 604 | no limite; candidatos a sub-registradores |
| `internal/handlers/downloads_promote.go` | 591 | ok |
| `web/src/pages/SearchPage.tsx` | 582 | ok pós-M1 |
| `web/src/pages/FavoritesPage.tsx` | 564 | ok pós-M1 |
| `web/src/components/PlayerModal.tsx` | 542 | ok pós-M1 (era 1875) |
| `web/src/api/stream-types.ts` | 163 | maior módulo R3 (barrel `stream.ts` = 17 ln) |

---

## 6. Onde melhorar (priorizado)

### P0 — Produto / valor para o usuário

#### 6.1 ~~Fechar M2b~~ ✅ entregue (2026-07-13)

- **Estado**: M2b integrado na `main` via PRs #520–#522.
- **Inclui**: `#EXT-X-MEDIA TYPE=AUDIO/SUBTITLES`, toggle admin `hlsMediaRenditions`,
  troca seamless via `hls.audioTrack` no player.
- **Pendente operacional**: validação Safari/iOS em homelab com renditions ativas
  (`JACKUI_HLS_MEDIA_RENDITIONS=1`).

#### 6.2 UX / usabilidade

- Plano em `docs/UX_USABILITY_REQUIREMENTS.md` (UX-0…UX-5).
- **UX PR1** (a11y foundations): já na `main` (`useDialogFocus`, `Sheet`, etc.).
- **UX PR2** ✅ (2026-07-13): `AsyncState`, `StatusBanner`, `RetryPanel`;
  Discover distingue erro TMDB de vazio com retry; Search/Library/Local usam banners
  acessíveis (`role=alert` / `role=status`).
- **Próximo**: UX PR3 (simplificar busca/cards/download).

#### 6.3 N+1 restantes de performance de lista

| # | Item | Estado |
|---|---|---|
| 5 | `POST /api/downloads/batch` create | ✅ **feito** |
| 6 | art resolve batch / `artStatus` na library | ⬜ próximo alvo |
| 7 | stream drop batch no delete em massa | ⬜ |
| 9 | favorites batch folder/remove | ⬜ |
| 10 | stop-seed batch | ⬜ |
| 8, 11, 12 | art `<img>`, cache status, audio meta | latentes |

---

### P1 — Saúde de código e operação

#### 6.4 ~~Completar R3 — `stream.ts`~~ ✅ entregue (2026-07-13)

- Barrel `stream.ts` (17 ln) re-exporta: `stream-types`, `stream-core`, `stream-health`,
  `stream-favorites`, `stream-controls`, `stream-settings`, `stream-probe`, `stream-urls`,
  `stream-browser`.
- `local.ts` importa módulos específicos (menos acoplamento ao barrel).

#### 6.5 CA-3.2 (estabilidade de CI)

- **Script**: `scripts/ci-stability-audit.sh` + `make test-stability [RUNS=20]`.
- **Audit local** (2026-07-13): 3/3 go + 3/3 vitest = **100%**.
- **Formal**: rodar 20 runs no runner ARM e registrar em `REQUIREMENTS.md`.
- Concurrency nos workflows Gitea **já estava** configurado.

#### 6.6 Higiene de branches (R4)

- **Script**: `scripts/branch-hygiene.sh --delete-merged` + `make branch-hygiene DELETE=1`.
- Removidas localmente: `feat/hls-audiotrack-frontend`, `refactor/r3-local-api-decomp`,
  `refactor/m1-react-decomposition`, `feat/auto-download-next`, entre outras mergeadas.
- Remotes órfãs (`feat/i18n`, etc.): revisar manualmente antes de `git push origin --delete`.

#### 6.6 Cobertura Go nos pacotes quentes

- `handlers`, `streamer`, `downloads`, `transcode` concentram risco de concorrência
  (piece mutex, lifecycle HLS, aggregate worker).
- Gate de *new* coverage ajuda; legado ~55% global ainda esconde bugs.

#### 6.7 Observabilidade operacional no produto

- Prometheus já existe (`/api/metrics`).
- Falta “produto de ops”: Jackett down, disco cheio, GPU saturada, rotação de porta
  gluetun, pool PostgreSQL. Alinha com UX-4 (diagnóstico).

#### 6.8 Alinhar deploy repo vs prod

- Repo default: `make deploy-auto` **sem VPN**.
- Prod: compose **hand-maintained + gluetun**; env novas no compose do repo
  **não chegam** sozinhas em prod.
- Melhorias: compose canônico de prod no repo (ou script de sync), checklist de env
  no release, healthcheck real.

---

### P2 — Médio prazo

| Item | Por quê |
|---|---|
| **OpenAPI / contrato de API** | ~239 rotas; breaking changes front/*arr* mais fáceis sem schema |
| **Higiene de branches** | R4 parcial — script + cleanup local; remotes pendentes |
| **Sub-registradores de rotas** | `routes.go` ~604 ln; agrupar por domínio como `handlers/local/` |
| **Backup/restore PostgreSQL** | Estado da app não está no piece cache; ops de homelab |
| **First-run sem Jackett** | Dependência forte; fallback magnet-only / Prowlarr nativo ajuda onboarding |
| **Mobile / PWA** | Manifest + SW existem; validar touch real (player, downloads, sheets) |

---

## 7. Roadmap sugerido (atualizado pós-execução 2026-07-13)

```
Concluído     →  R3 stream.ts, UX PR2, M2b (main), batch downloads #5
Agora         →  PR refactor/r3-stream-api-decomp → main
Imediato      →  Perf #6 (art resolve batch) + UX PR3
Contínuo      →  CA-3.2 formal (20 runs ARM) + remotes órfãs + prod compose sync
```

### Critérios de sucesso por frente

| Frente | Done when |
|---|---|
| ~~R3~~ | ✅ `stream.ts` barrel + módulos; vitest verde |
| ~~M2b~~ | ✅ na `main`; validar Safari com renditions ativas |
| ~~Perf #5~~ | ✅ batch create em uso |
| Perf #6 | library list sem N `resolveArt` frios |
| CA-3.2 formal | `make test-stability RUNS=20` ≥ 99% no runner |
| UX PR3 | ações primárias claras + menu "Mais ações" |

---

## 8. Fora de escopo desta análise

- Redesenho visual completo / troca de design system.
- Reescrita do protocolo Transmission-RPC.
- Mudança de stack (Go/React/PostgreSQL).
- Telemetria externa obrigatória.
- Expansão comercial / multi-tenant SaaS.

---

## 9. Veredito

O JackUI **já é um media server completo e bem pensado**: streaming pré-download,
HLS para Safari, fila agregada, *arr*, AI rename, auth sério e CI com dentes.
A fase “fazer funcionar” e a fase “não explodir com god-files e N+1 óbvios” estão
em grande parte **feitas**.

O próximo salto de qualidade concentra-se em:

1. **integrar a branch R3+UX** (`refactor/r3-stream-api-decomp`) na `main`;
2. **deixar listas baratas** (Perf #6 art resolve batch);
3. **UX PR3–PR5** (fluxos, onboarding, diagnóstico);
4. **alinhar ops** (repo↔prod, CA-3.2 formal 20 runs, remotes órfãs).

Operacionalmente, o maior risco silencioso é o **descompasso repo↔prod (VPN/compose/env)**
e a **falta de medição de flakiness de CI**.

---

## 10. Referências internas

| Documento | Uso |
|---|---|
| `README.md` | Visão operacional e config |
| `docs/ARCHITECTURE.md` | Forma do sistema |
| `docs/design-decisions.md` | Porquês (HLS, VOD, auth, …) |
| `docs/REQUIREMENTS.md` | Marcos M0–M3 e débito residual |
| `docs/HLS_MASTER_PLAYLIST_PLAN.md` | Plano M2a/M2b |
| `docs/PERFORMANCE.md` | Backlog N+1 |
| `docs/UX_USABILITY_REQUIREMENTS.md` | Plano UX/a11y |
| `docs/AUDIT_FINDINGS_2026-07-10.md` | Auditoria anterior (parcialmente superada) |
| `SECURITY.md` | Superfície de auth e riscos aceitos |
| `Claude.md` / `AGENTS.md` | Convenções de desenvolvimento |

---

*Atualizado em 2026-07-13 após execução do plano (R3, UX PR2, scripts CI/R4).*
