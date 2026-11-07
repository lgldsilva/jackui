# JackUI e2e specs

E2E suite for the sweep's route inventory, three layers:

- `./` — Go HTTP-integration specs for every registered API route (no browser)
- `../web/e2e/` — Playwright page smoke for every SPA route (see its header)
- `./load/k6.js` — k6 load profile (reads ramp/spike, net-zero writes, logins)

## Layout (Go specs)

| File | What it pins |
| --- | --- |
| `e2e_test.go` | harness: wait healthz, admin login, transport helpers |
| `routes_public_test.go` | routes outside the authed `/api` group |
| `routes_core_test.go` | search/config/AI/history/library/subtitles/TMDB/auth |
| `routes_stream_test.go` | streamer + HLS + sidecars + preview viewer |
| `routes_downloads_local_test.go` | downloads queue, local library, watchlists, playlists |
| `gated_off_test.go` | feature-gated routes (pprof/transmission-rpc/oauth) are NOT registered |
| `authz_test.go` | 401 for anonymous on every `/api` route, 403 for plain users on admin routes |
| `flows_test.go` | user journeys: register→delete, playlist lifecycle, login/refresh/logout, favorites folders |

Run with `-tags e2e` so the suite stays out of `go test ./...`.

## Running against a stack

Env:

- `E2E_BASE_URL` — stack base URL (default `http://127.0.0.1:8989`)
- `E2E_ADMIN_USER` / `E2E_ADMIN_PASSWORD` — admin seeded on the stack

Local:

```sh
E2E_BASE_URL=http://127.0.0.1:8989 \
E2E_ADMIN_PASSWORD='…' \
go test -tags e2e ./e2e/ -v
```

From a machine without a Go toolchain, against a Docker-based stack:

```sh
docker run --rm --network <stack_network> \
  -v "$PWD":/work -w /work \
  -e E2E_BASE_URL=http://<app_container>:8989 \
  -e E2E_ADMIN_PASSWORD='…' \
  golang:1.26.10-alpine go test -tags e2e ./e2e/
```

## Expected-status convention

Every table entry pins the handler's contract for a deterministic probe:
empty stores, no external providers (Jackett/TMDB/AI point at dead
endpoints on the reference stack), well-formed-but-nonexistent placeholders
(`:id` → `999999`, `:hash` → 40 zeros). A pinned 502/503 documents "this
handler fails because the optional provider is not configured"; if a build
changes one of these statuses, the diff is the regression.

## SPA page smoke (web/e2e)

```sh
cd web
E2E_BASE_URL=http://127.0.0.1:8989 E2E_ADMIN_PASSWORD='…' npx playwright test
```

Every page route renders inside `#route-root` with no uncaught JS error;
protected routes redirect anonymous visitors to `/login`; the admin login
journey runs through the real UI form.

## Load profile (load/k6.js)

```sh
docker run --rm -i -e TARGET=http://HOST:PORT -e ADMIN_PASS='…' \
  grafana/k6 run - < e2e/load/k6.js
```

Full profile ≈ 5m30s: reads ramp to 10 VUs with a 25-VU spike, plus
constant-arrival playlist create/delete (net zero) and login beats.
`SMOKE=1` shortens it to ~20s.
