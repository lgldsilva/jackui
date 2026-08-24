# CI/CD — JackUI

> **Source of truth is GitHub** — [github.com/lgldsilva/jackui](https://github.com/lgldsilva/jackui).
>
> Active workflows live in [`.github/workflows/`](../.github/workflows/).
> Former Gitea Actions pipelines are archived under [archive/gitea-workflows/](archive/gitea-workflows/).

## Pipeline overview

```
GitHub (PR / push main / tag v*)
   │
   ├─ CI (.github/workflows/ci.yml)
   │    on: pull_request, push@main, workflow_call
   │    ├─ ARM stack `scripts/ci-arm.sh all`
   │    │    └─ Go + Postgres + frontend + golangci + /healthz smoke
   │    └─ SonarCloud consumes the immutable ARM coverage artifact
   │
   ├─ CodeQL (.github/workflows/codeql.yml)
   │    on: push@main, pull_request, weekly
   │    └─ Go + JavaScript/TypeScript security-and-quality
   │
   └─ Release (.github/workflows/release.yml)
        on: push tags v*, workflow_dispatch
        ├─ semver          scripts/semver.sh
        ├─ build amd64     load local (no push)
        ├─ Trivy gate      CRITICAL fails BEFORE registry push
        ├─ push multi-arch ghcr.io/<owner>/jackui:{version,latest} (amd64+arm64)
        ├─ push nvidia     ghcr.io/<owner>/jackui:nvidia (amd64)
        ├─ SBOM            CycloneDX via Trivy → attached to GitHub Release
        └─ GitHub Release  changelog + bom.json
```

## Images

| Tag | Platform | Notes |
|-----|----------|-------|
| `ghcr.io/lgldsilva/jackui:latest` | amd64, arm64 | rolling mainline |
| `ghcr.io/lgldsilva/jackui:<semver>` | amd64, arm64 | immutable release |
| `ghcr.io/lgldsilva/jackui:nvidia` | amd64 | NVENC image (`Dockerfile.nvidia`) |

Pull (after `gh auth login` / PAT with `read:packages` if private):

```bash
docker pull ghcr.io/lgldsilva/jackui:latest
# or
docker pull ghcr.io/lgldsilva/jackui:nvidia
```

## Deploy (homelab)

Release **does not auto-deploy** to the production host yet (that step lived on the
Gitea self-hosted runner). Production remains a **hand-maintained** compose file:

```bash
docker pull  ghcr.io/lgldsilva/jackui:nvidia
docker tag   ghcr.io/lgldsilva/jackui:nvidia jackui:nvidia
docker compose -f <prod-config-dir>/docker-compose.yml \
  up -d --no-deps --force-recreate jackui
```

Or use the Makefile targets from a machine with the right Docker context:

```bash
make deploy-auto        # GPU auto-detect, no VPN
make deploy-auto-vpn    # + gluetun overlay
```

> [!IMPORTANT]
> The deploy only swaps the **image**. New env vars / volumes / ports added to the
> **repo** compose do **not** reach prod by themselves — edit the server-side
> hand-file too.

> [!NOTE]
> The author's production instance runs **behind gluetun** (`network_mode: container:gluetun`).
> `watchForwardedPort` in `cmd/server/main.go` rebinds when the VPN port rotates.

## Local quality gates (before opening a PR)

```bash
# Default context. Override only through the documented JACKUI_CI_* settings.
scripts/ci-arm.sh all
```

The command builds a disposable image whose source is copied into the Docker
build context from a temporary snapshot of the Git index; it never bind-mounts
the checkout. It refuses unstaged tracked changes and untracked non-ignored
files, so stage the exact snapshot before invoking it. It works with a local
Docker context and an SSH/remote context alike. Each run derives a unique Compose
project and image tag, copies coverage, service logs and `/healthz` output into
`$CI_ARTIFACT_DIR` (or a printed temporary directory), then runs only:

```bash
docker --context "$JACKUI_CI_DOCKER_CONTEXT" compose \
  -p <unique-jackui-ci-project> -f docker-compose.ci.yml \
  down --volumes --remove-orphans
```

No production Compose file, volume, network, bind mount or credential is used.
`JACKUI_CI_POSTGRES_PORT=0` is an ephemeral loopback diagnostic port; the test
container always connects to `postgres:5432` on its private network.
On pull requests, the workflow always uses GitHub-hosted `ubuntu-24.04-arm`,
Docker context `default` and the ephemeral port. Trusted pushes and reusable
workflow calls may override runner labels, Docker context and diagnostic port
through repository variables.

Optional Sonar (homelab, with an explicitly configured safe `SONAR_HOST_URL` and token):

```bash
make sonar-scan
```

## Dependabot

[`.github/dependabot.yml`](../.github/dependabot.yml) opens weekly PRs for:

- npm (`/web` and desktop root `/`)
- Go modules
- GitHub Actions
- Docker base images

## Secrets & variables

| Name | Where | Purpose |
|------|-------|---------|
| `GITHUB_TOKEN` | automatic | GHCR push, releases, CodeQL |
| `CODECOV_TOKEN` | optional secret | coverage upload (CI continues if missing) |
| `SONAR_TOKEN` / `SONAR_HOST_URL` | optional (local/homelab) | Sonar quality gate outside GitHub CI |
| `JACKUI_CI_DOCKER_CONTEXT` | optional env/repo variable | Docker context for the disposable CI stack (default `default`) |
| `JACKUI_CI_COMPOSE_PROJECT` | optional env/repo variable | CI project prefix (default `jackui-ci`) |
| `JACKUI_CI_RUNNER_LABELS` | optional GitHub variable | JSON ARM runner labels (default `["ubuntu-24.04-arm"]`) |
| `JACKUI_CI_IMAGE` | optional env/repo variable | Base tag for unique per-run CI images |
| `JACKUI_CI_POSTGRES_PORT` | optional env/repo variable | Ephemeral diagnostic Postgres port (`0` by default) |

Never hardcode internal hostnames, registry URLs, or credentials in workflows.

## Branch policy

- **Default branch:** `main`
- Prefer PRs over direct pushes (branch protection should require CI).
- Stale feature branches should be deleted after merge (`scripts/branch-hygiene.sh`).

## Historical note

Before 2026-07 the project used Gitea Actions (+ briefly Jenkins) on a homelab
runner with SonarQube, Dependency-Track, Telegram notify and in-place deploy.
Those workflows are preserved under `docs/archive/` for reference only.
