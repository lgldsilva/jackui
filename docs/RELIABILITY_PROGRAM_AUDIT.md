# Auditoria do programa de confiabilidade — issue #80

Registro de fechamento do programa aprovado na [issue #80](https://github.com/lgldsilva/jackui/issues/80)
("confiabilidade, performance e consistência"). Uma linha por fase: **o que foi
entregue, em qual PR, e qual é a evidência** de que continua valendo depois do merge.

- Baseline do programa: `origin/main@e2998102` (cobertura Go observada: 83,8%;
  frontend sem gate de cobertura).
- Política de integração: um PR por fase, aprovação explícita antes de cada merge,
  fase seguinte sempre a partir da `origin/main` atualizada.

## Quadro das fases

| Fase | Entrega | PR | Evidência viva |
|---|---|---|---|
| **P0.1** Fundação reproduzível de CI ARM | Stack descartável de CI em ARM64 (`scripts/ci-arm*.sh`, `Dockerfile.ci`, `docker-compose.ci.yml`) + retry de download de módulos contra flakes do `proxy.golang.org` | [#81](https://github.com/lgldsilva/jackui/pull/81), [#89](https://github.com/lgldsilva/jackui/pull/89) | Job **ARM CI stack** em todo PR e push na `main` |
| **P0.2** Confiabilidade backend / limites externos | Cap de resposta nos clientes externos (Jackett, TMDB, OpenSubtitles), `MaxBytesReader` global nas rotas JSON, `IdleTimeout`/`MaxHeaderBytes` no `http.Server`, drain-and-close nos POSTs de ntfy | [#90](https://github.com/lgldsilva/jackui/pull/90) | Teste por cap novo (body > cap falha com erro explícito; body gigante → 413) |
| **P1.1a** Resiliência Electron | Restart do servidor Go embutido com backoff limitado; `render-process-gone`; diálogo em falha de startup | [#94](https://github.com/lgldsilva/jackui/pull/94) | `electron/` com testes + `tsc -p electron` no CI |
| **P1.1b** Versionamento Electron | Fonte única de versão, ldflags no `build-electron.sh`, `version.json` fora do git, gate `tsc` no CI | [#95](https://github.com/lgldsilva/jackui/pull/95) | `/status` reporta version/commit/buildTime; job de frontend roda o `tsc` do Electron |
| **P1.2a** Gate de cobertura frontend | `@vitest/coverage-v8` + thresholds, `--coverage` no `ci-container.sh` emitindo lcov, `sonar.javascript.lcov.reportPaths` | [#91](https://github.com/lgldsilva/jackui/pull/91), [#98](https://github.com/lgldsilva/jackui/pull/98) | Cobertura do `web/` chegando no SonarCloud a cada PR |
| **P1.2b** Dependency scan em PR | `trivy fs --severity HIGH,CRITICAL --exit-code 1` como job próprio | [#92](https://github.com/lgldsilva/jackui/pull/92) | Job **Dependency scan (Trivy fs)**, bloqueante |
| **P1.2c** Mutation testing | Workflow noturno do gremlins portado do Gitea; stub de `ui/dist` pra compilar sem build de frontend; testes que dependem de ffmpeg passam a fazer skip no runner sem ffmpeg | [#93](https://github.com/lgldsilva/jackui/pull/93), [#97](https://github.com/lgldsilva/jackui/pull/97), [#99](https://github.com/lgldsilva/jackui/pull/99) | Workflow **Mutation (nightly)**, score no step summary + artefato |
| **P1.3** Acessibilidade frontend | `axe`/`jest-axe` nos testes, `eslint-plugin-jsx-a11y` no lint, correção dos interativos aninhados | [#96](https://github.com/lgldsilva/jackui/pull/96) | Testes de a11y + regra de lint no job de frontend |
| **P2.1** Performance medida e manutenibilidade | `/debug/pprof` opt-in e sempre autenticado; benchmarks no CI com delta de `benchstat` contra o merge base; `sonar-out/` deixa de ser resíduo enganoso | [#100](https://github.com/lgldsilva/jackui/pull/100) | Job **Benchmarks (informational)**; [PERFORMANCE.md](PERFORMANCE.md) |
| **P2.2** Consolidação e documentação | Este documento; `CI_CONCURRENCY_PLAN` arquivado; `POSTGRES_MIGRATION` sem referência a Jenkins | este PR | [archive/README.md](archive/README.md) |

## O que ficou de fora, de propósito

Não é dívida escondida — é escopo que foi avaliado e adiado com motivo:

- **Threshold de mutation score.** O workflow noturno segue **não-bloqueante**.
  O antigo `--threshold-efficacy 50` valia para runners do homelab; o baseline em
  runner GitHub-hosted só pôde ser medido depois de
  [#99](https://github.com/lgldsilva/jackui/pull/99). Primeira execução limpa
  (`main@8d46f528`, run 32799273373):

  | Métrica | Valor |
  |---|---|
  | Test efficacy | 72,8% |
  | Mutant coverage | 88,1% |
  | Mutantes | 467 (340 killed · 127 lived · 63 not covered) |
  | Tempo | ~116s |

  Com folga sobre o threshold antigo — reativar o gate é um follow-up de uma
  linha, depois de algumas noites confirmarem que o número é estável.
- **Mutation em pacotes com Postgres** (`downloads`, `streamer`): exige um Postgres
  por mutante. Só faz sentido depois de medir o tempo do baseline atual.
- **Benchmarks como gate.** Runner compartilhado tem variância grande demais;
  o job publica o delta e nunca reprova por número.
- **N+1 #11 e #12** (cache status e audio meta batch) seguem latentes no
  [PERFORMANCE.md](PERFORMANCE.md) — são melhorias, não riscos.
- **Deploy automático.** O release publica a imagem em `ghcr.io`; o deploy
  continua manual (`make deploy-auto`) porque o compose de prod é
  hand-maintained e costuma estar atrás do gluetun. Consequência conhecida:
  **env var nova no compose do repo não chega em prod sozinha.**

## Como verificar que o programa continua de pé

```bash
# gates que rodam em todo PR
gh pr checks <n>          # ARM CI stack · Dependency scan · SonarCloud · CodeQL · Benchmarks

# localmente, antes de subir
make test
cd web && npm test
golangci-lint run --new-from-rev=origin/main
```

O CI ativo está descrito em [CICD.md](CICD.md); pipelines e scripts do Gitea
ficaram em [archive/](archive/) e não são fonte de verdade.
