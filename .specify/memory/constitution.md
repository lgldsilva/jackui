# JackUI Constitution

## Core Principles

### I. Git Workflow (NON-NEGOTIABLE)

Nunca commitar direto na `main`. Todo trabalho acontece em branch criada a
partir de uma `main` atualizada (pull antes de commit), integrada via Pull
Request com Conventional Commits. É proibido usar `AI_STANDARDS_SKIP` ou
contornar os hooks de git sem autorização humana explícita.

### II. Pirâmide de Testes (NON-NEGOTIABLE)

Cobertura de linha e branch **≥ 90%** com pirâmide completa
(unit → integration → e2e). Lógica de negócio exige também testes de mutação
e property-based testing. Um bug corrigido ganha primeiro o teste que o
reproduz.

### III. Quality Gates Antes do Merge (NON-NEGOTIABLE)

Todo PR passa pela CI ARM (`scripts/ci-arm.sh all`: gofmt, go vet, go test,
tsc, eslint, golangci-lint, build e smoke E2E do servidor), pelo quality gate
do SonarQube/SonarCloud e pelo OWASP dependency-check. Zero vulnerabilidades
de alta severidade sem mitigação documentada.

### IV. Segurança de Conteúdo Não Confiável

Torrents e uploads são hostis por padrão. Resolução de caminho passa
obrigatoriamente por `Browser.ResolvePathFor`/`ResolvePath` (rejeita `..`,
caminhos absolutos e symlinks que escapem do mount); mounts `:usersubpath`
isolam `{mount}/{username}/...` e novos endpoints usam `lh.ScopePath` +
`Browser.ResolvePathFor`, nunca concatenação manual. Inputs externos são
sanitizados antes de logar (`httpshared.SanitizeForLog`, `SanitizeInt`,
`SanitizeIntSlice`). Respostas de conteúdo ativo (EPUB, SVG) carregam
`X-Content-Type-Options: nosniff` e `Content-Security-Policy: sandbox`.

### V. Restrições Operacionais do Homelab

- CI pesada roda em ARM via Docker context lido de `.env`
  (`JACKUI_CI_*`); nunca fixar host, máquina ou daemon em código/scripts.
- No overlay Gluetun, `jackui` e `postgres` dividem o netns do
  `gluetun-jackui`: o banco é `localhost:5432`, não `postgres:5432`.
- O scheduler de banda compara janelas `HH:MM` com o relógio local do
  container; `TZ` deve estar definido (padrão `America/Sao_Paulo`).
- Shutdown tem deadline rígido de 20s (`cleanupHardDeadline`); o watchdog
  força `os.Exit(0)` e o boot seguinte reconcilia estado
  (`RescueStuckMoving`, `resumeSeeding`, verificação de pieces).

## Restrições de Stack

Backend em Go (`cmd/server`, `internal/...`), frontend em React/TypeScript
(`web/`, build Vite para `ui/dist`, embed via `ui/embed.go`), desktop em
Electron (`electron/`). Banco Postgres. A configuração PostCSS do frontend
permanece ESM (`web/postcss.config.mjs`) enquanto `web/package.json` não
declarar `"type": "module"`. A stack de CI (`Dockerfile.ci`,
`docker-compose.ci.yml`, `scripts/ci-arm*.sh`) é descartável e idêntica para
execução manual e runners; divergências entre local, ARM e CI são bugs.

## Fluxo de Desenvolvimento

1. Branch a partir de `main` atualizada; mudanças mínimas e escopadas ao
   pedido.
2. Validar E2E localmente (build, testes, smoke) antes de declarar pronto ou
   pedir teste manual.
3. Abrir PR; aguardar CI + Sonar verdes; merge com branch remota deletada.
4. Exceções a smells de design exigem justificativa registrada no código
   (`NOSONAR: razão`) ou em ADR.

## Governance

Esta constituição prevalece sobre práticas conflitantes. Emendas exigem PR
com documentação da mudança e, quando aplicável, plano de migração. Todo
PR/review deve verificar conformidade; complexidade adicional precisa de
justificativa. Orientação operacional em tempo de execução: `AGENTS.md` na
raiz do repositório.

**Version**: 1.0.0 | **Ratified**: 2026-08-24 | **Last Amended**: 2026-08-24
