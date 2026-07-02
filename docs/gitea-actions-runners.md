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
      GITEA_INSTANCE_URL: https://gitea.raspberrypi.lan
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

⚠ **TLS interno:** os nós precisam confiar na CA do `gitea.raspberrypi.lan` (mesma
exigência dos builds ARM atuais no Jenkins).

## Secrets/vars a criar (Settings → Actions)

Secrets: `SONAR_TOKEN`, `SONAR_HOST_URL`, `DT_API`, `DT_USER`, `DT_PASS`,
`REGISTRY_TOKEN` (PAT `write:package`), `PROD_COMPOSE` (caminho do compose de prod),
e opcional `TELEGRAM_BOT_TOKEN`/`TELEGRAM_CHAT_ID`.
Vars: `REGISTRY=gitea.raspberrypi.lan`.

## Ativação

Os workflows nascem com `on: workflow_dispatch` (manual) para não gerar checks
pendentes sem runner. Depois de validar os runners:
1. Trocar os triggers para `pull_request` (ci.yml) e `push: branches:[main]` (release.yml).
2. Ativar **branch protection** na `main` exigindo os checks `CI / backend` e `CI / frontend`.
3. Rodar Jenkins + Actions em paralelo por alguns merges; então descomissionar o Jenkins.
