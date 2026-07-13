# Análise do projeto JackUI — 2026-07-13

## Resumo executivo

O JackUI é um servidor multimídia self-hosted que pesquisa torrents via Jackett,
inicia a reprodução antes do download terminar e entrega o conteúdo ao navegador
por reprodução direta, transcode progressivo ou HLS. O sistema combina backend
Go/Gin, frontend React/Vite, PostgreSQL, anacrolix/torrent e ffmpeg em um único
binário distribuído principalmente por Docker.

O projeto está funcional e tecnicamente saudável: as suítes Go e frontend passam,
o build é concluído e os principais gates de segurança estão presentes na CI. Os
maiores riscos atuais não são falhas funcionais imediatas, mas segurança da
configuração Git local, cobertura insuficiente, dívida de complexidade no frontend
e a entrega do HLS Phase 2 (**M2b agora na `main`**, validação Safari pendente).

As evidências desta análise foram coletadas inicialmente na branch
`refactor/r3-local-api-decomp`, no commit `ed6ce14`. **Atualização 2026-07-13:**
a execução do plano de melhorias concluiu na branch `refactor/r3-stream-api-decomp`
(commits `6161832`…`a9f36e9`). O documento preexistente
`docs/AUDIT_FINDINGS_2026-07-10.md` foi preservado.

## Execução do plano de melhorias (2026-07-13)

| Entrega | Resultado | Detalhe |
|---|---|---|
| M2b HLS | ✅ já na `main` | PRs #520–#522: `EXT-X-MEDIA`, toggle admin, `hls.audioTrack` |
| R3 `stream.ts` | ✅ | 646→17 ln barrel + 9 módulos (`stream-core`, `stream-health`, …) |
| R3 `local.ts` | ✅ já na `main` | 667→350 ln |
| Perf #5 batch downloads | ✅ já existia | `downloadBatchCreate`; `PERFORMANCE.md` marcado feito |
| UX PR2 | ✅ | `AsyncState`, `StatusBanner`, `RetryPanel` em Discover/Search/Library/Local |
| CA-3.2 | 🟡 | `scripts/ci-stability-audit.sh`; audit 3-run local = 100%; formal: 20 runs ARM |
| Concurrency CI | ✅ já ok | `ci.yml` / `release.yml` / `mutation.yml` |
| R4 branches | 🟡 | `scripts/branch-hygiene.sh`; 7 branches locais mergeadas removidas |

**Validação pós-execução:** 576 testes vitest + `make test` Go verdes.

---

## O que o projeto faz

O fluxo principal é:

```text
Jackett -> resultados -> torrent/anacrolix -> cache em disco
                                             |-> HTTP Range/direct play
                                             `-> ffmpeg -> HLS/transcode -> navegador
```

Além do streaming de torrents, o JackUI oferece:

- downloads internos e integração com qBittorrent/Transmission;
- compatibilidade Transmission RPC para Sonarr, Radarr e Prowlarr;
- reprodução e administração de arquivos locais e mounts remotos;
- biblioteca, favoritos, histórico, playlists e watchlist em PostgreSQL;
- autenticação JWT, passkeys, múltiplos usuários e administração;
- legendas, múltiplas faixas de áudio, capítulos e player de música;
- enriquecimento TMDB, recomendações e limpeza opcional de títulos por IA;
- frontend React embutido no binário Go;
- deploy Docker para CPU, NVIDIA, VAAPI e ambientes com VPN/Gluetun.

## Arquitetura

### Backend

- `cmd/server`: composição da aplicação, inicialização e rotas Gin.
- `internal/streamer`: cliente torrent, leitura seekable, cache e saúde do swarm.
- `internal/transcode`: pipelines ffmpeg e gerenciamento de sessões HLS.
- `internal/handlers`: API HTTP e integração dos casos de uso.
- `internal/downloads`: fila, scheduler e worker agregados por torrent.
- `internal/auth` e `internal/db`: autenticação e persistência PostgreSQL.
- `internal/transmissionrpc`: camada de compatibilidade com o ecossistema `*arr`.

### Frontend

- React 18, TypeScript, Vite e Tailwind.
- Vitest e Testing Library para testes.
- `PlayerProvider` mantém a reprodução viva durante a navegação.
- `hls.js` atende os caminhos HLS em navegadores compatíveis.
- Internacionalização em português e inglês com react-i18next.

### Operação

- Imagens Docker separadas para CPU, NVIDIA e VAAPI.
- Gitea Actions para PRs e releases.
- SonarQube, gitleaks, gosec, govulncheck e Trivy na pipeline.
- Deploy self-hosted integrado ao ambiente de homelab.

## Pontos fortes

1. **Produto completo:** o projeto resolve pesquisa, aquisição, reprodução,
   transcode, biblioteca e integração `*arr` em uma única aplicação.
2. **Arquitetura documentada:** os fluxos críticos e as restrições de Safari/HLS,
   ffmpeg e torrent seekable estão registrados em `docs/`.
3. **Boa base de testes:** existem 314 arquivos de teste Go e 65 arquivos de teste
   frontend.
4. **Refatoração recente relevante:** god files reduzidos; `local.ts` 350 ln;
   **`stream.ts` decomposto** em barrel (17 ln) + módulos irmãos (2026-07-13).
5. **Segurança na CI:** há análise de segredos, SAST, vulnerabilidades Go, Sonar e
   scan da imagem antes da publicação.
6. **HLS M2 completo na `main`:** M2a multi-res (#516) + **M2b** áudio/legendas
   (`EXT-X-MEDIA`, `hls.audioTrack`, #520–#522).
7. **Estabilização dos testes:** 3 `time.Sleep` residuais; script CA-3.2 com audit
   local 100% (3 runs).
8. **UX PR2 entregue:** estados assíncronos unificados (`AsyncState`, banners com
   `role=alert`/`status`, retry padronizado).

## Achados e melhorias prioritárias

### P0 — Remover credencial das URLs Git

Os remotes `origin` e `gitea` armazenam uma credencial diretamente nas URLs do
`.git/config`. Embora o arquivo não seja versionado, a credencial pode aparecer em
logs, diagnósticos e cópias de configuração.

Ações recomendadas:

1. revogar e rotacionar o token atual;
2. remover a credencial das URLs dos remotes;
3. usar SSH, macOS Keychain ou Git Credential Manager;
4. manter URLs sem segredo embutido.

### P1 — Criar gates reais de cobertura

A cobertura Go global medida foi **56,0% de statements**, abaixo do padrão de 90%.
A CI produz `coverage.out`, mas não reprova quando a cobertura fica abaixo de um
limite. O frontend também não possui provider ou thresholds de cobertura no
Vitest.

Pacotes Go que merecem prioridade:

| Pacote | Cobertura |
|---|---:|
| `internal/playlists` | 0,0% |
| `internal/push` | 0,0% |
| `internal/library` | 7,1% |
| `internal/history` | 11,6% |
| `internal/tmdb` | 17,1% |
| `internal/downloads` | 17,7% |
| `internal/watchlist` | 27,9% |
| `internal/auth` | 36,3% |
| `internal/handlers` | 47,7% |
| `internal/streamer` | 58,3% |

Ações recomendadas:

1. impedir regressão da cobertura global;
2. exigir cobertura alta para código novo;
3. elevar gradualmente a meta por pacote;
4. priorizar autenticação, downloads, streamer e TMDB;
5. adicionar `@vitest/coverage-v8` e thresholds ao frontend.

### P1 — ~~Concluir HLS Master Playlist Phase 2~~ ✅ M2b na `main`

M2a (#516) e M2b (#520–#522) estão integrados. Pendente apenas validação
operacional Safari/iOS com `JACKUI_HLS_MEDIA_RENDITIONS=1` em homelab.

Branches auxiliares `feat/hls-audiotrack-frontend` e `origin/omos/feat-hls-master-m2b`
podem ser removidas após confirmar merge na `main`.

### P1 — Corrigir dependências circulares no frontend

O build termina com avisos do Rollup sobre dependências circulares entre chunks.
Os símbolos `withToken` e `fetchMediaToken` são definidos em `api/http.ts`,
reexportados pelo barrel `api/client.ts` e importados por módulos que participam do
mesmo grafo.

O bundler informa que a ordem de execução pode quebrar. A correção preferencial é
importar esses helpers diretamente de `api/http.ts`, reduzindo o acoplamento com o
barrel `client.ts`.

O chunk de `hls.js` também chega a aproximadamente 522 kB. Deve-se confirmar que o
carregamento continua realmente lazy e que o player não penaliza a carga inicial.

### P2 — Eliminar o baseline de complexidade

O lint passa, mas sete violações antigas são rebaixadas para warning em
`web/eslint.config.mjs`:

| Arquivo | Complexidade máxima observada |
|---|---:|
| `PlayerProvider.tsx` | 26 |
| `ResultCard.tsx` | 24 |
| `MoveFolderModal.tsx` | 18 |
| `StreamCacheCard.tsx` | 18 |
| `VideoPlayerElement.tsx` | 18 |
| `useFilteredResults.ts` | 17 |
| `seriesGroup.ts` | 16 |

A lista de exceções deve diminuir progressivamente até que todas as violações sejam
tratadas como erro.

Apesar da evolução recente, ainda existem arquivos de produção acima de 300
linhas. Os maiores hotspots atuais (pós-R3, 2026-07-13):

- `cmd/server/routes.go`: 604 linhas;
- `internal/handlers/downloads_promote.go`: 591 linhas;
- `web/src/pages/SearchPage.tsx`: 582 linhas;
- `internal/handlers/local/local_subtitles.go`: 579 linhas;
- `web/src/api/stream-types.ts`: 163 linhas (maior módulo R3; barrel `stream.ts` = 17 ln).

### P2 — Ampliar a estratégia de testes

A workflow de mutation testing existe, mas exclui a maior parte dos subsistemas e
aceita eficácia mínima de 50%. Não foi encontrado uso sistemático de bibliotecas
property-based nem uma suíte Playwright para os fluxos do usuário.

Ações recomendadas:

1. expandir mutation testing para regras puras de parser, downloads e streamer;
2. adicionar testes property-based para parsing, agrupamento, ordenação e
   serialização;
3. criar E2E de navegador para login, busca, abertura do player e download;
4. adicionar `npm audit --audit-level=high` ao gate frontend;
5. tornar falhas de SBOM/Dependency-Track visíveis, pois hoje são best-effort.

### P3 — Higiene de branches e remotes

**Executado (2026-07-13):** `scripts/branch-hygiene.sh --delete-merged` removeu 7
branches locais já mergeadas (`feat/hls-audiotrack-frontend`, `refactor/r3-local-api-decomp`,
`feat/auto-download-next`, etc.).

**Pendente:**

1. remover branches remotas órfãs (`feat/i18n`, `feat/chapters`, …) após revisão;
2. consolidar remotes `origin` / `gitea` duplicados;
3. worktree `feature/ux-usability-foundations` impede delete local — remover worktree
   se não for mais necessário.

## Validações executadas

| Validação | Resultado |
|---|---|
| `go test -p 4 -timeout 20m ./internal/...` | passou |
| Cobertura Go | ~56% |
| `npm test` | **67 arquivos, 576 testes** passaram (pós UX PR2) |
| `npm run build` | passou com avisos de chunks/ciclos |
| `npm run lint` | passou com sete warnings legados |
| `go vet ./internal/...` | passou |
| `gofmt -l .` | saída vazia |
| `npm run check:i18n` | passou |
| `scripts/ci-stability-audit.sh 3` | **100%** go + vitest (CA-3.2 parcial) |
| `make test` | passou (pós-execução R3+UX) |

Não foram executados deploy, SonarQube remoto, Trivy ou E2E no ambiente de
produção durante esta análise.

## Ordem recomendada de execução (atualizada 2026-07-13)

1. Rotacionar a credencial Git e corrigir os remotes (P0 segurança — inalterado).
2. **Merge PR** `refactor/r3-stream-api-decomp` → `main` (R3 + UX PR2 + scripts).
3. Perf #6 — art resolve batch na biblioteca.
4. UX PR3 — simplificação de busca/cards/download.
5. `make test-stability RUNS=20` no runner ARM (CA-3.2 formal).
6. Implementar gate incremental de cobertura Go e frontend.
7. Remover as sete exceções de complexidade ESLint.
8. Expandir mutation, property-based e E2E de navegador.
9. Limpar remotes órfãs (`scripts/branch-hygiene.sh` + `git push origin --delete`).

## Veredito final

O JackUI já é um produto utilizável, com arquitetura consistente, amplo conjunto de
funcionalidades e boa automação de build e segurança. A execução de 2026-07-13
fechou **R3 (`stream.ts`)**, **UX PR2**, confirmou **M2b e batch downloads** na
`main`, e adicionou **scripts de estabilidade e higiene de branches**. O melhor
retorno agora é integrar a branch de trabalho, atacar Perf #6 e fortalecer gates
quantitativos (cobertura, CA-3.2 formal).

*Atualizado em 2026-07-13 após execução do plano de melhorias.*
