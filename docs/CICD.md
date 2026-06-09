# CI/CD — JackUI

Automated pipeline: **push/PR → Jenkins tests, scans, builds (natively on the target),
pushes to the Gitea registry, and ssh-deploys to the home server.**

```
Gitea (push to main / PR)
   │ webhook  (currently flaky → builds are often triggered manually)
   ▼
Jenkins @ oracle-desktop (arm64 controller)
   ├─ Backend test        (golang:1.26-alpine)
   ├─ Frontend build      (node:24-alpine)
   ├─ SonarQube           (quality gate, -Dsonar.qualitygate.wait=true)
   ├─ SBOM → Dependency-Track  (cdxgen → DT upload)
   ├─ Build & Push        (ssh → raspberrypi-srv builds the image NATIVELY (amd64),
   │                       pushes gitea.raspberrypi.lan/lgldsilva/jackui:nvidia)
   ├─ Trivy               (fails on CRITICAL, --ignore-unfixed)
   ├─ Deploy              (ssh → raspberrypi-srv: docker compose up -d --force-recreate jackui)
   └─ Old-tag cleanup     (prune old registry tags)
```

Secrets come from **Jenkins credentials**, never the repo: `jackui-sonar-token`,
`jackui-dt` (Dependency-Track), `jackui-gitea` (registry, needs `write:package`),
`jackui-deploy` (ssh key), `jackui-ci-bot` (PR approval). The Gitea registry / Sonar /
DT live on **oracle-desktop** (`10.228.143.12`); the build and deploy SSH to
**raspberrypi-srv** (`10.228.143.1`).

## Why build on the target (not on the Jenkins controller)

The controller (oracle-desktop) is **arm64**; the deploy target (raspberrypi-srv) is
**amd64**. Emulating amd64 under qemu on the controller OOM-killed the build, so the
`docker build` runs **natively on raspberrypi-srv over SSH** and pushes to the Gitea
registry. Deploy is a second SSH that recreates the container from the home-server
compose.

> [!WARNING]
> The Sonar stage runs the scanner as `docker run --platform linux/amd64` on the arm64
> controller, which needs qemu **binfmt** registered. binfmt is **not auto-registered
> after a reboot** — install `qemu-user-static` on the controller so `systemd-binfmt`
> restores it on boot, otherwise the Sonar stage fails with `exec format error` and the
> gate reads a stale prior analysis.

## Deploy mechanism

The `Deploy` stage SSHes to raspberrypi-srv and runs:

```bash
docker compose -f /portainer/Files/AppData/Config/jackui/docker-compose.yml \
  up -d --force-recreate jackui
```

> [!IMPORTANT]
> The **production compose is the one on the server** (`/portainer/Files/AppData/Config/jackui/docker-compose.yml`),
> separate from the repo's `docker-compose.yml`. Jenkins only does `up -d` against it —
> it does **not** copy the repo compose. So **env/volume/port changes must be made in
> that server-side compose** (then `up -d`); the repo compose is a reference.

## One-time setup

1. **Jenkins** — plugins: Docker Pipeline, Gitea. Controller needs `/var/run/docker.sock`.
   Create a Pipeline / Multibranch job pointing at the Gitea repo (uses the root
   `Jenkinsfile`). Add the credentials listed above.
2. **Gitea webhook** — Repo → Settings → Webhooks → `https://jenkins.raspberrypi.lan/gitea-webhook/post`
   (Push + Pull Request events).
3. **Registry login on the target** — `docker login gitea.raspberrypi.lan` on
   raspberrypi-srv so the deploy can pull.
4. **Server compose** — point the image at `gitea.raspberrypi.lan/lgldsilva/jackui:nvidia`.

## Quality gates that BREAK the build

- **SonarQube**: `-Dsonar.qualitygate.wait=true`. New-code conditions: `new_coverage ≥ 80`,
  `new_violations = 0`, `new_duplicated_lines_density ≤ 3`. Coverage excludes
  `web/**`, `cmd/**`, `electron/**` (UI/glue, no unit tests).
- **Trivy**: `--severity CRITICAL --exit-code 1 --ignore-unfixed` (a CVE with no fix
  doesn't block; it also prints HIGH for visibility).
- **Dependency-Track**: SBOM upload; DT policies can be promoted to a gate later.

## Running the gates locally (no Jenkins)

Validate by the diff **before** pushing — a CI cycle is ~12 min, a failed gate on a
prod deploy is expensive.

```bash
# build + tests + coverage
go build ./... && go test ./internal/... -coverprofile=/tmp/c.out
go tool cover -func=/tmp/c.out | tail -1
cd web && npx tsc --noEmit && npm run build && cd ..

# new-code complexity/violations on YOUR diff only (mirrors Sonar "new code")
golangci-lint run --new-from-rev=gitea/main ./...   # gocognit + gocritic + staticcheck
```

`make sonar-scan` reproduces the Sonar step; the rest mirrors the `Jenkinsfile`.
