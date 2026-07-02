# Contributing to JackUI

Thanks for considering a contribution! This document covers the workflow and the quality bar.

## Ground rules

- **Never commit directly to `main`** — every change goes through a pull request.
- **Conventional Commits** for titles and commit messages: `feat(player): ...`, `fix(ci): ...`, `refactor(handlers): ...`.
- Branch names follow `<type>/<slug>`, e.g. `fix/hls-seek-restart`.
- Keep PRs small and focused. Incremental cuts beat big-bang rewrites.

## Before you push

Run the local gates — they mirror what CI enforces:

```bash
make test                 # full Go suite (requires a reachable PostgreSQL for DB-backed tests)
cd web && npm test        # vitest (pure-function tests)
cd web && npx tsc --noEmit && npm run build
gofmt -l . && go vet ./...
```

Quality expectations:

- **Tests accompany code.** New logic ships with unit tests; bug fixes ship with a regression test.
- **Cognitive complexity ≤ 15** per function (SonarQube S3776). If you must exceed it, justify with a `// NOSONAR: reason` comment.
- New UI strings use `t()` with keys added to **both** `web/src/locales/pt.json` and `en.json`.
- Backend error responses are JSON `{"error": "..."}`.
- Don't fatten the known god-files (see `CLAUDE.md`) — new components/logic go in their own files.

## Project layout

See [docs/ARCHITECTURE.md](docs/ARCHITECTURE.md) for the canonical architecture and a reading order for new contributors, and [docs/design-decisions.md](docs/design-decisions.md) for why the tricky parts are the way they are (read it before "fixing" anything around HLS/Safari/VOD).

## Reporting bugs / requesting features

Open an issue with:

- what you did, what you expected, what happened;
- server logs if relevant (`JACKUI_LOG_FORMAT=json` helps);
- your deployment flavor (Docker CPU/NVIDIA/VAAPI, browser, whether auth is enabled).

For security issues, **do not open a public issue** — see [SECURITY.md](SECURITY.md).
