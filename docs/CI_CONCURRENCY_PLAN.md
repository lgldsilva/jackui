# Plano Completo — Concurrency no Gitea Actions (JackUI + Semidx)

## Contexto e problema

Nos dois repositórios (`jackui` e `semidx`), quando há múltiplos commits em sequência no mesmo PR, o CI dispara múltiplas execuções completas dos mesmos gates. Isso gera:

- Fila longa nos runners self-hosted (especialmente ARM).
- Contenção de recursos (CPU, I/O, Postgres de testes, Docker daemon).
- Aumento de flakiness por timeout sob carga.
- Maior tempo de feedback para o commit mais novo (o único que importa).

O objetivo deste plano é fazer o CI processar **apenas o último commit relevante** por PR/branch, sem comprometer release/deploy da `main`.

---

## Objetivo principal

Implementar controle de concorrência via `concurrency` nos workflows do Gitea Actions para:

1. Cancelar execuções antigas do mesmo PR quando chegar um commit novo.
2. Preservar execuções de PRs distintos (sem cancelamento cruzado).
3. Evitar cancelamento indevido de release/deploy em `main`.
4. Aplicar o padrão em `jackui` e `semidx`.

---

## Premissas técnicas

- Instância Gitea: **1.26.2** (suporta `concurrency`).
- Runners: self-hosted, com capacidade limitada e jobs relativamente longos.
- Workflows atuais usam sintaxe de Actions compatível com Gitea.
- Release em `main` é crítico e não deve ser abortado automaticamente.

---

## Escopo

### Repositórios

- `jackui`
- `semidx`

### Workflows alvo

1. `ci.yml` (PR gates)
2. `release.yml` (push/main deploy pipeline)
3. `mutation.yml` (se aplicável)
4. `autotag.yml` (semidx)

### Fora de escopo (nesta fase)

- Troca de infraestrutura de runners.
- Mudança de regras de quality gate.
- Reescrita estrutural dos jobs.
- Alteração de branch protection.

---

## Estratégia de concorrência (recomendada)

## Regras por tipo de workflow

- **CI de PR (`pull_request`)**  
  Cancelar execuções em andamento do mesmo PR.

- **Release/Deploy (`push` em `main`)**  
  Não cancelar execução em andamento por padrão (evitar deploy interrompido).

- **Workflows manuais / mutation / autotag**  
  Definir caso a caso:
  - `mutation`: normalmente manter 1 por branch/ref.
  - `autotag`: geralmente serial por ref/tag, sem cancelamento agressivo.

## Chaves de grupo (group)

### CI de PR

```yaml
concurrency:
  group: ${{ github.workflow }}-pr-${{ github.event.pull_request.number }}
  cancel-in-progress: true
```

Por que assim:

- Isola por workflow + número do PR.
- Commit novo no mesmo PR cancela run antigo.
- PRs diferentes não se cancelam.

### Release de `main`

```yaml
concurrency:
  group: ${{ github.workflow }}-${{ github.ref }}
  cancel-in-progress: false
```

Por que assim:

- Evita duas releases simultâneas da mesma branch.
- Não mata deploy já iniciado.

---

## Matriz de decisão por workflow

| Workflow | Evento | Group sugerido | cancel-in-progress |
|---|---|---|---|
| `ci.yml` | `pull_request` | `${{ github.workflow }}-pr-${{ github.event.pull_request.number }}` | `true` |
| `release.yml` | `push` (`main`) | `${{ github.workflow }}-${{ github.ref }}` | `false` |
| `mutation.yml` | `pull_request` / `workflow_dispatch` | PR: igual CI / dispatch: por ref | `true` para PR, avaliar para dispatch |
| `autotag.yml` (semidx) | push/tag/manual | `${{ github.workflow }}-${{ github.ref }}` | `false` (recomendado) |

---

## Plano de execução (fases)

## Fase 1 — Levantamento

1. Inventariar todos workflows ativos em `jackui/.gitea/workflows/`.
2. Inventariar todos workflows ativos em `semidx/.gitea/workflows/`.
3. Classificar cada workflow por criticidade e tipo (PR gate, release, utilitário).
4. Confirmar eventos de gatilho (`pull_request`, `push`, `workflow_dispatch`, cron).

**Saída esperada:** tabela de workflows + estratégia de concurrency aprovada.

## Fase 2 — Implementação no JackUI

1. Editar `jackui/.gitea/workflows/ci.yml` com `concurrency` de PR.
2. Editar `jackui/.gitea/workflows/release.yml` com `concurrency` por ref.
3. Revisar `mutation.yml` do `jackui` conforme matriz.
4. Validar sintaxe YAML e consistência com eventos existentes.

**Saída esperada:** PR no `jackui` com mudanças mínimas e explícitas.

## Fase 3 — Implementação no Semidx

1. Editar `semidx/.gitea/workflows/ci.yml`.
2. Editar `semidx/.gitea/workflows/release.yml`.
3. Editar `semidx/.gitea/workflows/autotag.yml` e `mutation.yml` (se aplicável).
4. Validar sintaxe e coerência dos grupos.

**Saída esperada:** PR no `semidx` com política alinhada.

## Fase 4 — Teste de comportamento

Teste obrigatório em cada repo:

1. Abrir PR de teste.
2. Fazer push A (inicia run).
3. Antes de finalizar, fazer push B no mesmo PR.
4. Confirmar:
   - run de A vira `cancelled`;
   - run de B segue e conclui.
5. Abrir outro PR em paralelo e confirmar que não houve cancelamento cruzado.

Teste de release:

1. Merge em `main`.
2. Confirmar release executa completa e não é cancelada indevidamente.

**Saída esperada:** evidência (IDs de run) anexada no PR.

## Fase 5 — Hardening e documentação

1. Atualizar `docs/gitea-actions-runners.md` com política de concurrency.
2. Registrar gotchas de Gitea 1.26.x (se observados).
3. Criar check-list operacional para incidentes de fila.

---

## Critérios de aceite

1. Em PR com múltiplos pushes, apenas último run útil conclui.
2. Sem cancelamento entre PRs diferentes.
3. Release em `main` não aborta por concorrência.
4. Redução perceptível de fila nos runners.
5. Nenhuma regressão de gate/qualidade.

---

## Riscos e mitigação

- **Risco:** grupo mal desenhado cancela workflows errados.  
  **Mitigação:** separar por `github.workflow` + escopo (PR number ou ref).

- **Risco:** release cancelada no meio.  
  **Mitigação:** `cancel-in-progress: false` em `release.yml`.

- **Risco:** comportamento diferente entre Gitea/GitHub syntax context.  
  **Mitigação:** validar em PR real e manter expressões simples.

- **Risco:** excesso de serialização aumentar lead time.  
  **Mitigação:** aplicar cancelamento só onde agrega (CI de PR), não em deploy.

---

## Métricas de sucesso (7 dias)

- Tempo médio de fila por run.
- Tempo total até feedback do último commit no PR.
- Quantidade de runs canceladas automaticamente (esperado: subir).
- Taxa de falhas por timeout em testes (esperado: cair).
- Utilização de runner durante horário de pico.

---

## Rollback plan

Se houver efeito colateral:

1. Reverter apenas bloco `concurrency` no workflow problemático.
2. Reexecutar pipeline para confirmar estabilização.
3. Reaplicar com chave de grupo mais específica.

Rollback deve ser por workflow, não global.

---

## Template de implementação (copiar e adaptar)

```yaml
name: CI

on:
  pull_request:

concurrency:
  group: ${{ github.workflow }}-pr-${{ github.event.pull_request.number }}
  cancel-in-progress: true

jobs:
  # ...
```

```yaml
name: Release

on:
  push:
    branches: [main]

concurrency:
  group: ${{ github.workflow }}-${{ github.ref }}
  cancel-in-progress: false

jobs:
  # ...
```

---

## Prompt pronto para abrir nova sessão

Use este prompt no início da nova sessão:

> Executar o plano de concurrency do `docs/CI_CONCURRENCY_PLAN.md` em `jackui` e `semidx`.  
> 1) aplicar `concurrency` nos workflows conforme matriz;  
> 2) abrir PR em cada repo;  
> 3) validar com teste de dois pushes no mesmo PR (run antigo cancelado, último segue);  
> 4) monitorar até merge e release;  
> 5) atualizar docs operacionais com o padrão final.

---

## Checklist rápido de execução

- [ ] Levantamento dos workflows nos dois repos
- [ ] Ajuste `ci.yml` em `jackui`
- [ ] Ajuste `release.yml` em `jackui`
- [ ] Ajuste workflows equivalentes em `semidx`
- [ ] PR aberto `jackui`
- [ ] PR aberto `semidx`
- [ ] Teste push A/B no mesmo PR (`cancelled` + `success`)
- [ ] Merge dos PRs
- [ ] Verificação de release/main
- [ ] Documentação final atualizada

