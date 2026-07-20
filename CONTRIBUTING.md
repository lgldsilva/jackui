# Contributing to JackUI

Thanks for considering a contribution! This document covers the workflow and the quality bar.

## Ground rules

- **Never commit directly to `main`** — every change goes through a pull request.
- **Conventional Commits** for titles and commit messages: `feat(player): ...`, `fix(ci): ...`, `refactor(handlers): ...`.
- Branch names follow `<type>/<slug>`, e.g. `fix/hls-seek-restart`.
- Keep PRs small and focused. Incremental cuts beat big-bang rewrites.

## Fork and PR workflow

If you don't have push access to this repository:

1. Fork the repository on GitHub
2. Clone your fork: `git clone https://github.com/YOUR_USERNAME/jackui.git`
3. Add the upstream remote: `git remote add upstream https://github.com/lgldsilva/jackui.git`
4. Create a branch for your changes: `git checkout -b feature/your-change`
5. Make your changes and commit with conventional commits
6. Push to your fork: `git push origin feature/your-change`
7. Open a pull request from your fork to this repository

## Before you push

Run the local gates — they mirror what CI enforces:

```bash
make test                 # full Go suite (requires a reachable PostgreSQL for DB-backed tests)
cd web && npm test        # vitest (pure-function tests)
cd web && npx tsc --noEmit && npm run build
gofmt -l . && go vet ./...
golangci-lint run --new-from-rev=origin/main  # cognitive complexity gate (≤ 15)
```

Quality expectations:

- **Tests accompany code.** New logic ships with unit tests; bug fixes ship with a regression test.
- **Cognitive complexity ≤ 15** per function (gocognit). If you must exceed it, justify with a `//nolint:gocognit // reason` comment.
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
