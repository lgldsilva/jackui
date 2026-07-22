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
   │    ├─ backend   gofmt + go vet + go test (+ Postgres service)
   │    ├─ frontend  tsc + eslint + i18n check + vitest + build
   │    └─ lint      golangci-lint --new-from-rev=origin/main
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
make test                                              # Go + frontend
gofmt -l .
go vet ./internal/...
golangci-lint run --new-from-rev=origin/main ./...     # gocognit + friends
cd web && npm test && npm run lint && npm run check:i18n && npm run build
```

Optional Sonar (homelab, if `SONAR_HOST_URL` / token are set):

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

Never hardcode internal hostnames, registry URLs, or credentials in workflows.

## Branch policy

- **Default branch:** `main`
- Prefer PRs over direct pushes (branch protection should require CI).
- Stale feature branches should be deleted after merge (`scripts/branch-hygiene.sh`).

## Historical note

Before 2026-07 the project used Gitea Actions (+ briefly Jenkins) on a homelab
runner with SonarQube, Dependency-Track, Telegram notify and in-place deploy.
Those workflows are preserved under `docs/archive/` for reference only.
