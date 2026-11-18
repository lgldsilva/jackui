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
   │    ├─ SonarCloud consumes the immutable ARM coverage artifact
   │    └─ E2E smoke (Playwright) `scripts/e2e-smoke.sh` — PR and push@main only
   │
   ├─ CodeQL (.github/workflows/codeql.yml)
   │    on: push@main, pull_request, weekly
   │    └─ Go + JavaScript/TypeScript security-and-quality
   │
   └─ Release (.github/workflows/release.yml)
        on: push tags v*, workflow_dispatch (main or a v* tag only)
        ├─ semver          scripts/semver.sh + scripts/release-guard.sh
        │                  (a published <semver> is never overwritten from another commit)
        ├─ ci              calls ci.yml; build/push needs it green
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

### Playwright page smoke

The `E2E smoke (Playwright)` job (`e2e-smoke` in `ci.yml`) runs on
`ubuntu-latest`, outside the ARM image, so the pinned CI image carries no
browser. It builds the web bundle and the server binary, boots the server with
auth **enabled** against the job's Postgres service, and runs
`web/e2e/pages.smoke.spec.ts` on Chromium: every SPA route renders with no
uncaught JS error, protected routes redirect anonymous visitors to `/login`,
and the admin logs in through the real form. On failure the HTML report,
traces, screenshots and the server log are uploaded as `jackui-e2e-smoke`.

It is **not a required check** yet and is skipped when `ci.yml` is called from
the release workflow, so it cannot block a merge or a release until it has
proven stable.

Locally, against a **disposable** database. The script runs the server on port
18989 (it refuses a port that is already in use) from an empty directory with a
scrubbed environment, and never reads `config.yaml` or `.env`. Two guards run
before anything is built or started:

- `E2E_DATABASE_URL` must be the URL form with an explicit database
  (`postgres://user:pass@host[:port]/<database>[?params]`) whose name is `test`
  or carries it as an underscore-separated token (`jackui_test`, `test_jackui`).
  Keyword DSNs, URLs without a database path and fragments are rejected: the
  server migrates the schema and starts every worker against that database.
- Outside CI, `E2E_ALLOW_HOST_NETWORK=1` is required. The server has no switch
  to disable its BitTorrent client, so the smoke process opens the default peer
  port and joins the DHT on the host's network — not something to do by
  accident on a machine that keeps torrent traffic behind a VPN.

```bash
(cd web && ./node_modules/.bin/playwright install chromium)
E2E_ALLOW_HOST_NETWORK=1 \
E2E_DATABASE_URL='postgres://user:pass@127.0.0.1:55432/jackui_test?sslmode=disable' \
  make e2e-smoke
```

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
