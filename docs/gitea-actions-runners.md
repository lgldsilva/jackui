# Gitea Actions — setup dos runners (act_runner)

Setup dos `act_runner` que executam os workflows em `.gitea/workflows/`. Ver o
plano completo da migração (mapa Jenkins → Actions) no doc de planejamento.

## Topologia

| Runner | Nó | Labels | Papel |
|---|---|---|---|
| CI-A | oci-ampere-1 | `arm64` | test / lint / frontend build / Sonar / SBOM |
| CI-B | oci-ampere-2 | `arm64` | idem (paralelismo) |
| Deploy | homeserver | `deploy` | build nvidia (amd64 nativo) + Trivy + push + deploy |

`runs-on: [self-hosted, arm64]` cai nos CI; `runs-on: [self-hosted, deploy]` no
homeserver (onde vivem a GPU/NVENC, o `docker.sock` e o acesso ao compose de prod).

## Registro (por nó)

1. No Gitea: **Settings → Actions → Runners → Create new runner** (nível repo, org
   ou instância) para obter o **REGISTRATION_TOKEN**.
2. Rodar o `act_runner` em container com o `docker.sock` montado (espelha o modelo
   Jenkins; os steps `docker run` — Sonar/cdxgen/Trivy — funcionam igual):

```yaml
# docker-compose.runner.yml (um por nó; ajuste LABELS e nome)
services:
  act_runner:
    image: gitea/act_runner:latest
    restart: always
    environment:
      GITEA_INSTANCE_URL: https://gitea.example.com   # a URL da sua instância do Gitea
      GITEA_RUNNER_REGISTRATION_TOKEN: "<REGISTRATION_TOKEN>"
      GITEA_RUNNER_NAME: "ci-ampere-1"      # ou ci-ampere-2 / deploy-homeserver
      GITEA_RUNNER_LABELS: "arm64"          # no homeserver: "deploy"
    volumes:
      - /var/run/docker.sock:/var/run/docker.sock
      - ./runner-data:/data
```

3. `docker compose -f docker-compose.runner.yml up -d` em cada nó. Confirmar em
   **Settings → Actions → Runners** que os 3 aparecem `Online` com os labels certos.

⚠ **Gotcha do docker.sock (herdado do Jenkins):** `docker run -v "$PWD":/x` dentro
de um job resolve o path **no daemon do host**. Nos workflows, montar via o path do
workspace que o runner expõe, ou preferir actions oficiais que já cuidam disso.

⚠ **TLS interno:** se a instância usa uma CA privada, os nós precisam confiar nela
(mesma exigência dos builds ARM atuais no Jenkins).

## Secrets/vars a criar (Settings → Actions)

O host do próprio Gitea NÃO precisa ser configurado — os workflows o derivam de
`github.server_url`/`github.api_url` (injetados pelo runner).

Secrets: `SONAR_TOKEN`, `DT_API`, `DT_USER`, `DT_PASS`,
`REGISTRY_TOKEN` (PAT `write:package`), `PROD_COMPOSE` (caminho do compose de prod),
e opcional `TELEGRAM_BOT_TOKEN`/`TELEGRAM_CHAT_ID`.
Vars: `REGISTRY` (host do registry), `SONAR_HOST_URL` (URL do SonarQube) e
`SONAR_HOSTS` (linha de `/etc/hosts` "IP hostname", se o host do Sonar não resolver via DNS no runner).

## Política de concorrência (concurrency)

Todos os workflows têm um bloco `concurrency` no nível raiz para evitar que
múltiplos commits no mesmo PR disparem execuções redundantes dos gates.

### Regras por tipo de workflow

| Workflow | group | cancel-in-progress | Efeito |
|---|---|---|---|
| `ci.yml` | `CI-pr-{PR number}` | `true` | Cancela runs antigas do mesmo PR; PRs distintos não se afetam |
| `release.yml` | `Release-refs/heads/main` | `false` | Serializa deploys da main; nunca aborta um deploy em andamento |
| `mutation.yml` | `Mutation-refs/heads/main` | `false` | Serializa runs noturnas; não interrompe mutation em andamento |

### Por que não cancelar release e mutation

`cancel-in-progress: false` em `release.yml` evita que um segundo push na `main`
(ex: hotfix imediato) mate um deploy já em voo — o segundo run entra em fila e
espera. Em `mutation.yml`, interromper uma run de mutação a meio desperdiça horas
de CPU sem resultado útil.

### Checklist operacional — fila travada

Se os runners estiverem sobrecarregados e runs ficarem presas em `pending`:

1. Verificar se o runner está `Online` em **Settings → Actions → Runners**.
2. Se offline: reiniciar o container `act_runner` no nó afetado.
3. Runs de PR antigas (mesmo PR, commit desatualizado) são canceladas
   automaticamente pelo `concurrency` quando chega um commit novo — não é
   necessário cancelar manualmente.
4. Runs de `release.yml` em fila aguardam a anterior terminar (`cancel-in-progress:
   false`). Se a run anterior travou, cancele-a manualmente no Gitea antes de
   re-triggar.
5. Rollback de concurrency: se um grupo mal configurado causar cancelamentos
   indevidos, remova apenas o bloco `concurrency` do workflow problemático e
   re-execute — os outros workflows não são afetados.

## Ativação

Os workflows nascem com `on: workflow_dispatch` (manual) para não gerar checks
pendentes sem runner. Depois de validar os runners:
1. Trocar os triggers para `pull_request` (ci.yml) e `push: branches:[main]` (release.yml).
2. Ativar **branch protection** na `main` exigindo os checks `CI / backend` e `CI / frontend`.
3. Rodar Jenkins + Actions em paralelo por alguns merges; então descomissionar o Jenkins.
