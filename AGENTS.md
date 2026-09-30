# Global instructions — see the generated block below.

## Remote CI on ARM

- Run heavy builds and tests on ARM via Docker context, never hardcoding a host,
  machine name or daemon in code and scripts.
- Read the context and stack names from `.env`; document the values in
  `.env.example`. The canonical names are `JACKUI_CI_DOCKER_CONTEXT`,
  `JACKUI_CI_COMPOSE_PROJECT`, `JACKUI_CI_RUNNER_LABELS`, `JACKUI_CI_IMAGE` and
  `JACKUI_CI_POSTGRES_PORT`.
- Use `docker --context "$JACKUI_CI_DOCKER_CONTEXT" compose` in scripts, to
  support local contexts, remote SSH and other Docker daemons.
- The same CI image and stack must serve manual runs and the GitHub Actions
  runners (and any self-hosted runner), avoiding divergence between the local
  machine, ARM and CI.
- Keep `.env` out of Git; do not include credentials, internal hostnames
  or context-specific values in versioned files.

## JackUI operational pitfalls

These rules complement the general ai-standards instructions with the hidden
scenarios found while operating the project.

### Gluetun network and database

- When using the `docker-compose.gluetun.yml` overlay, the `jackui` and
  `postgres` services share the network namespace of the `gluetun-jackui`
  container. In that mode the database host must be `localhost:5432`, not
  `postgres:5432`.
- The compose merge already overrides `JACKUI_DATABASE_URL` in the overlay; never
  hardcode hosts/credentials in the repo. Sensitive values and Docker-specific
  contexts live only in `.env` (gitignored).

### Timezone and bandwidth scheduler

- The bandwidth scheduler (`streamer.StartBandwidthScheduler`,
  `downloads.BandwidthWindow`) compares `HH:MM` windows against `time.Now()`, i.e.
  against the container's local time.
- Always set `TZ` in `.env` (compose default is `America/Sao_Paulo`). Without
  `TZ` the container runs in UTC and windows like "23:00-06:00" shift by 3h.

### Per-user isolation and subfolders (UserSubpath)

- Mounts with the `:usersubpath` suffix isolate each user under
  `{mount}/{username}/...`. Path resolution always goes through
  `Browser.ResolvePathFor`/`ResolvePath`, which reject `..` traversal,
  absolute paths and symlinks escaping the mount.
- Never concatenate paths manually in new endpoints; use
  `lh.ScopePath` + `Browser.ResolvePathFor` to respect both the mount base
  and the user's subdirectory.

### Archive preview

- The `/api/preview/*` endpoint reads bytes on demand from incomplete torrents
  (zip/cbz/epub) via `FileReader`. If peers fail to deliver the zip's central
  directory within the reader's timeout, the request may block or return an
  unexpected EOF.
- Active-content responses (EPUB, SVG) carry
  `X-Content-Type-Options: nosniff` and `Content-Security-Policy: sandbox` to
  neutralize malicious scripts inside archives.

### Shutdown and network down

- `cmd/server/main.go` enforces a hard 20s deadline (`cleanupHardDeadline`)
  on shutdown. The anacrolix client teardown can hang indefinitely when
  announcing to the DHT/trackers while the VPN/network is down.
- The watchdog forces `os.Exit(0)` once the deadline is exceeded, allowing Docker
  to recreate the container; the next boot reconciles state via
  `RescueStuckMoving`, `resumeSeeding` and piece verification.

### Log sanitization

- Use `httpshared.SanitizeForLog` (strings), `SanitizeInt`/`SanitizeIntSlice`
  (ints) for external inputs before logging. This prevents log injection and
  structured-log pollution, and helps pass CodeQL/Sonar audits.

### "Stop" semantics (stop-seed) and auto-seed

- "Stop" (`POST /api/downloads/:id/stop-seed` and the batch) **removes the row**
  from the downloads list (files stay on disk), for any status — there is no
  longer an "On disk due to stop" state. `DeleteScoped` + `DropSeed` +
  `worker.Remove` in the same handler.
- `worker.Remove` uses the `dropSeed` seam (= `Streamer.DropSeed`), which deletes
  the persisted auto-seed (`seeds` table); lifecycle drops (move/tick) go through
  the `drop` seam, which preserves it. Swapping one for the other resurrects
  torrents on the next boot via `resumeSeeding`.
- Favorites import ships with "also download" **off by default**
  (`favorites.alsoDownload`); downloading is an explicit choice on each import.
- `seed_stopped_at` keeps marking stopped rows via `StreamDrop` (the streaming
  cards' trash) and promote-without-reseed; `autoSeedCompleted` respects the flag
  so it does not reactivate them at boot.

### Security barriers and CodeQL models

- `#nosec` and any scanner-finding suppression/bypass are forbidden: a
  finding is fixed at the cause (code) or with a test covering the path.
- Path/URL/log guards live in named, tested functions
  (`Browser.ResolvePath`, `sanitizeSidecarName`, `sessionDir`,
  `sanitizeJackettURL`, `SanitizeForLog`, ...). They are declared to
  CodeQL in `.github/codeql/extensions/barriers.yml` — data extensions
  loaded by auto-discovery, no pack or extra config.
- Contract: EVERY function listed in `barriers.yml` needs a unit test
  proving the guarantee. A new barrier in the model without a test = PR rejected. If a
  function changes semantics, the test breaks before the model becomes a lie.

## Cursor Cloud specific instructions

- `.cursor/cloud-install.sh` installs PostgreSQL 16, Node 24.19.0 (same release as `Dockerfile.ci`), and golangci-lint v2.13.1, then runs `npm ci` and `npm run build` in `web/`. The frontend build is required because `ui/embed.go` embeds `all:dist`, so `go run ./cmd/server` fails when `ui/dist` is missing.
- Node is unpacked to `/usr/local/lib/jackui-node`. The agent `PATH` finds `/exec-daemon/node` first, so install and start symlink `node`, `npm`, and `npx` there.
- `.cursor/cloud-start.sh` starts PostgreSQL without systemd (`pg_ctlcluster`), creates the peer-auth role `ubuntu` and database `jackui`, and writes `~/.jackui/dev.env`. Source that file before `make dev-backend`. The DSN is quoted because it contains `&`.
- Auth in that env file matches `make dev-backend`: `JACKUI_AUTH_ENABLED=0` and `JACKUI_ALLOW_INSECURE_AUTH=1`. Stream, download, and library directories live under `~/.jackui`.
- The first start copies `config.yaml.example` to `config.yaml` when the file is missing. `config.yaml` is gitignored.
- UI dev server: `make dev-frontend` (Vite on :5173 proxies `/api`, `/status`, and `/healthz` to :8989).
- Jackett, TMDB, and AI keys are optional. Without them, search and posters stay empty; `GET /healthz` still reports the database and the streamer.
- Do not run `make deploy-*` from a cloud agent. Those targets SSH to the homelab.

## Frontend

- `web/postcss.config.js` was renamed to `.mjs` to avoid the Node 24
  `[MODULE_TYPELESS_PACKAGE_JSON]` warning. Keep the PostCSS config
  as ESM until the project declares `"type": "module"` in
  `web/package.json`.

<!-- BEGIN ai-standards (generated by sync-agents.sh — DO NOT EDIT; source: /Users/luizg/.config/ai-standards) -->
@/Users/luizg/.config/ai-standards/AGENTS.md
<!-- END ai-standards -->
