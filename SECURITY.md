# Security Policy

## Supported versions

Only the latest release is supported with security fixes.

## Reporting a vulnerability

Please **do not open a public issue** for security problems.

Use the repository's **private vulnerability reporting** ("Report a vulnerability" under the Security tab on GitHub) so the report stays private until a fix is available. Include:

- a description of the issue and its impact;
- steps to reproduce (a proof of concept helps);
- the version/commit you tested.

You should get an initial response within a week. Please allow a reasonable window for a fix before public disclosure.

## Scope notes

- JackUI is **not hardened for direct public-internet exposure**. The supported deployment is behind a reverse proxy on a trusted network, ideally with auth enabled (`JACKUI_AUTH_ENABLED=1`).
- Reports about torrent/media content itself are out of scope — JackUI is a neutral tool; what you access with it is your responsibility (see the Legal section in the README).

## Security audit (M0.5) — 2026-07-10

### Route inventory

**233 routes** registered in `cmd/server/routes.go` + `internal/transmissionrpc/handler.go`.

| Category | Count | Auth |
|---|---|---|
| Admin (user management) | 9 | REQUIRED + ADMIN |
| API (protected) | ~207 | REQUIRED + GuestRestrict |
| Auth self-service | 9 | Public (login/register/forgot) |
| Public endpoints | 3 | Public (`/healthz`, `/status`, `/api/auth/config`) |
| Special | 3 | Alternative auth (static token / conditional) |
| Transmission RPC | 2 | Own session token |

**Conclusion: zero sensitive routes without authentication** when `JACKUI_AUTH_ENABLED=1`. The protection is structural — the whole `/api` group gets `auth.Required` + `auth.GuestRestrict` as global middleware (`cmd/server/routes.go:209-212`). Administrative routes additionally carry `auth.AdminOnly()`.

### CORS

- `AllowAllOrigins = true` — acceptable for a server-less SPA that can be reached from any reverse proxy.
- Methods limited to GET/POST/PUT/DELETE/OPTIONS; headers are controlled.

### CSRF

- **There is no generic CSRF surface** — the app is an SPA (not server-rendered) and uses Bearer JWT authentication (not cookies), so it is not vulnerable to classic CSRF.
- The `?token=` fallback on media routes is strictly restricted to media paths (`isMediaPath`).
- The only CSRF in place is the Transmission RPC session-id, for *arr stack compatibility.

### Rate-limiting

**There is no generic rate-limiting on the `/api/*` endpoints.** The only limiters in place are:
- **Login lockout**: 5 attempts → 15 min lock (`internal/auth/lockout.go`)
- **Bandwidth throttling**: anacrolix rate.Limiter (torrent bytes only)
- **AI client RPM cap**: optional, per model provider

**Decision (CA-0.5.2):** Accept the risk of having no generic rate-limiting, with the following justifications:
1. The supported deployment is behind a reverse proxy on a trusted network; rate-limiting is best implemented at the proxy layer (nginx/Caddy) for external exposure scenarios.
2. Adding rate-limiting in Go would introduce configuration complexity, shared state, and sliding-window vs. fixed-window decisions with no clear benefit for the current deployment model.
3. Auth endpoints (register, forgot, reset) have login lockout — the most critical vector. Email spam via register/forgot would be mitigated by a frontend CAPTCHA when needed.
4. External API consumption (TMDB, Jackett, OpenSubtitles) is already handled ad hoc via per-provider RPM config.

**Accepted risks:**
- User enumeration via `/api/auth/register`, `/api/auth/forgot`, `/api/auth/reset`
- Abuse of `/api/search` and `/api/tmdb/*` without throttling, potentially overloading external services
- `/api/subtitles/download/:fileId` without throttling
