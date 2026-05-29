# CI/CD — JackUI

Pipeline automática: **push na `main` → Jenkins testa/escaneia/builda/publica → Watchtower redeploya.**

```
Gitea (push main)
   │ webhook
   ▼
Jenkins @ oracle-desktop ──► go test ──► frontend build ──► SonarQube (quality gate)
   │                                                          └─ SBOM → Dependency-Track
   ▼
docker build (Dockerfile.nvidia) ──► Trivy (falha em CRITICAL) ──► push gitea.raspberrypi.lan/lgldsilva/jackui:nvidia
                                                                              │
                                                                              ▼
                                                          Watchtower @ raspberrypi-srv  ──► pull + recreate (mesmo env/volumes)
```

Segredos vêm do **HashiCorp Vault** (`secret/jackui`), nunca do repo. Build e deploy são **ARM64** ponta-a-ponta (oracle-desktop builda, raspberrypi-srv roda).

## Por que Watchtower (e não SSH-deploy)
O Jenkins só **builda + publica** a imagem no registry do Gitea. O Watchtower (já rodando no raspberrypi-srv) observa o registry e recria o container com a **mesma config** (env/volumes/ports do compose). Vantagens: sem chave SSH de deploy no Jenkins, sem descasamento de arquitetura, rollout desacoplado do build. **Limitação:** mudou env/volume/porta → editar o compose no servidor e `up -d` à mão (Watchtower só troca a imagem).

## Setup único (uma vez)

### 1. Vault — provisionar segredos
```bash
vault kv put secret/jackui \
  gitea_user=lgldsilva  gitea_token=<token-com-write:package> \
  sonar_token=<sonar>    dt_user=admin  dt_pass=<dt>
```
O `gitea_token` precisa do escopo **`write:package`** (push no registry de container do Gitea).

### 2. Jenkins — job + plugins
- Plugins: **Docker Pipeline**, **HashiCorp Vault**, **Gitea**.
- Configurar o Vault global (URL `https://vault.raspberrypi.lan`, auth AppRole/token).
- Criar um *Multibranch Pipeline* (ou Pipeline) apontando pro repo Gitea; usa o `Jenkinsfile` da raiz.
- O agent/controller precisa do `/var/run/docker.sock` (o controller no oracle-desktop já tem, GID 999).

### 3. Gitea — webhook
Repo → Settings → Webhooks → Gitea/Jenkins:
`https://jenkins.raspberrypi.lan/gitea-webhook/post` (evento *Push* + *Pull Request*).

### 4. Compose do servidor — apontar pro registry + habilitar Watchtower
No `raspberrypi-srv:/portainer/Files/AppData/Config/jackui/docker-compose.yml`:
```yaml
services:
  jackui:
    image: gitea.raspberrypi.lan/lgldsilva/jackui:nvidia   # era jackui:${JACKUI_TAG}
    labels:
      - com.centurylinklabs.watchtower.enable=true
```
E garantir login no registry pro pull (`docker login gitea.raspberrypi.lan` no host, ou um `~/.docker/config.json` montado no Watchtower). Depois disso o deploy é automático.

## Quality gates que QUEBRAM o build
- **SonarQube**: `-Dsonar.qualitygate.wait=true` (cobertura de frontend excluída — `sonar.coverage.exclusions=web/**`).
- **Trivy**: `--exit-code 1 --severity HIGH,CRITICAL --ignore-unfixed` (CVE sem fix não bloqueia).
- **Dependency-Track**: upload do SBOM; políticas de violação configuradas no DT podem ser promovidas a gate depois.

## Rodar localmente (sem Jenkins)
`make sonar-scan` faz a parte do Sonar. O resto reproduz os comandos do `Jenkinsfile`.
