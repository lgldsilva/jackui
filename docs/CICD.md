# CI/CD — JackUI

Automated pipeline: **push/PR → Jenkins (on the home server, amd64) tests, scans,
builds the image, pushes to the Gitea registry, and deploys via the local
`docker.sock` (`docker compose up -d --force-recreate`).** No SSH and no Portainer
stack — prod is a hand-maintained compose file, and the container runs behind gluetun.

```
Gitea (push to main / PR)
   │ webhook  (currently flaky → builds are often triggered manually / SCM poll)
   ▼
Jenkins @ home server (amd64, /var/run/docker.sock mounted)
   ├─ Backend test        (golang:1.26-alpine, --platform linux/amd64)
   ├─ Frontend build      (node:24-alpine)
   ├─ SonarQube           (quality gate, -Dsonar.qualitygate.wait=true)
   ├─ SBOM → Dependency-Track  (cdxgen → DT upload)
   ├─ Build & Push        (docker build on the local amd64 daemon (docker.sock),
   │                       pushes gitea.raspberrypi.lan/lgldsilva/jackui:nvidia)
   ├─ Trivy               (fails on CRITICAL, --ignore-unfixed)
   ├─ Deploy              (local docker.sock: pull + retag +
   │                       docker compose -f <hand-file> up -d --force-recreate jackui)
   └─ Old-tag cleanup     (prune old registry tags)
```

Secrets come from **Jenkins credentials**, never the repo: `jackui-sonar-token`,
`jackui-dt` (Dependency-Track), `jackui-gitea` (registry, needs `write:package`),
`jackui-ci-bot` (PR approval). The Gitea registry / Sonar / DT live on
**oracle-desktop** (`10.228.143.12`); the build + deploy run **locally** on the
amd64 home-server Jenkins via `docker.sock` (no SSH).

## Build & deploy run locally (amd64, docker.sock)

Jenkins runs **on the amd64 home server** with `/var/run/docker.sock` mounted, so the
`docker build`, `docker push` and the deploy all talk to the local daemon directly —
**no SSH, no cross-arch emulation**. (The Jenkinsfile header comments still describe
the old arm64-controller-over-SSH model — that's stale; the stages run local.) The
`--platform linux/amd64` flags on the Sonar/Trivy/test containers are no-ops on the
amd64 host (no qemu/binfmt needed).

## Deploy mechanism

The `Deploy` stage runs on the local daemon:

```bash
docker pull  ${IMAGE}:nvidia
docker tag   ${IMAGE}:nvidia jackui:nvidia
docker compose -f /portainer/Files/AppData/Config/jackui/docker-compose.yml \
  up -d --force-recreate jackui
```

> [!IMPORTANT]
> **It is NOT a Portainer stack.** Despite the `/portainer/Files/AppData/Config/...`
> path, the Portainer API does **not** list jackui — that path is just a directory
> convention. Production is plain `docker compose up -d --force-recreate` against a
> **hand-maintained** compose file on the host, separate from the repo's
> `docker-compose.yml`. Jenkins only swaps the **image** (`pull` + retag + `up -d`); it
> does **not** copy the repo compose. So **any new env var / volume / port from the repo
> compose does NOT reach prod by itself** — edit the server-side hand-file and `up -d`.

> [!WARNING]
> **Prod runs BEHIND gluetun** (its own ProtonVPN tunnel), even though the repo ships VPN
> as an opt-in overlay (`docker-compose.gluetun.yml`, `make deploy-auto-vpn`) and
> CLAUDE.md calls it opt-in. The hand-file folds that overlay in: the jackui service uses
> `network_mode: "container:gluetun-jackui"` and seeds on Proton's **forwarded port**.
> JackUI reads that port from gluetun's control API on boot
> (`JACKUI_GLUETUN_CONTROL_URL=http://localhost:8000`) and `watchForwardedPort`
> (`cmd/server/main.go`) triggers a graceful restart when Proton rotates the port;
> `restart: unless-stopped` then recreates the process so it rebinds.

### Low-footprint ("Balanceado") profile lives in the hand-file

The host is shared (Jackett/Ollama/Jellyfin/*arr), so prod runs a low-consumption tuning
that exists **only in the server-side compose**, not in any repo compose:

| Env | Effect |
|---|---|
| `GOGC=75`, `GOMEMLIMIT=1600MiB`, `GOMAXPROCS=2` | caps the Go heap/GC + scheduler |
| `JACKUI_MAX_CONNS=40` | peer conns per torrent (`Stream.MaxConnsPerTorrent`) |
| `JACKUI_PEERS_HIGH=200` | peer high-water mark (`Stream.PeersHighWater`) |

The app's real heap is ~222 MiB; most of the reported RSS is reclaimable page cache.
Because these live in the hand-file, **a repo change to defaults won't move prod**.

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
