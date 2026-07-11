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

**233 rotas** registradas em `cmd/server/routes.go` + `internal/transmissionrpc/handler.go`.

| Categoria | Quantidade | Auth |
|---|---|---|
| Admin (user management) | 9 | REQUIRED + ADMIN |
| API (protegida) | ~207 | REQUIRED + GuestRestrict |
| Auth self-service | 9 | Público (login/register/forgot) |
| Public endpoints | 3 | Público (`/healthz`, `/status`, `/api/auth/config`) |
| Especiais | 3 | Auth alternativo (static token / condicional) |
| Transmission RPC | 2 | Session-token próprio |

**Conclusão: zero rotas sensíveis sem autenticação** quando `JACKUI_AUTH_ENABLED=1`. A proteção é estrutural — todo o grupo `/api` recebe `auth.Required` + `auth.GuestRestrict` como middleware global (`cmd/server/routes.go:209-212`). Rotas administrativas têm `auth.AdminOnly()` adicional.

### CORS

- `AllowAllOrigins = true` — aceitável para SPA server-less que pode ser acessada de qualquer reverse proxy.
- Métodos limitados a GET/POST/PUT/DELETE/OPTIONS; headers controlados.

### CSRF

- **Não há CSRF genérico** — o app é SPA (não server-rendered) e usa autenticação via Bearer JWT (não cookies), portanto não é vulnerável a CSRF clássico.
- O `?token=` fallback em media routes é estritamente restrito a paths de mídia (`isMediaPath`).
- O único CSRF existente é o session-id do Transmission RPC para compatibilidade com *arr stack.

### Rate-limiting

**Não há rate-limiting genérico nos endpoints `/api/*`.** Os únicos limitadores existentes são:
- **Login lockout**: 5 tentativas → 15 min lock (`internal/auth/lockout.go`)
- **Bandwidth throttling**: rate.Limiter do anacrolix (apenas bytes de torrent)
- **AI client RPM cap**: opcional por model provider

**Decisão (CA-0.5.2):** Aceitar o risco de ausência de rate-limiting genérico, com as seguintes justificativas:
1. O deployment suportado é atrás de reverse proxy em rede confiante; rate-limiting é mais bem implementado na camada de proxy (nginx/Caddy) para cenários de exposição externa.
2. Adicionar rate-limiting no Go introduziria complexidade de configuração, estado compartilhado e decisões de sliding window vs. fixed window sem benefício claro para o modelo de deploy atual.
3. Endpoints de auth (register, forgot, reset) têm lockout no login — o vetor mais crítico. Spam de email via register/forgot seria mitigado por um CAPTCHA no frontend quando necessário.
4. Consumo de APIs externas (TMDB, Jackett, OpenSubtitles) já tem tratamento ad-hoc via RPM config por provider.

**Riscos aceitos:**
- Enumeração de usuários via `/api/auth/register`, `/api/auth/forgot`, `/api/auth/reset`
- Abuso de `/api/search` e `/api/tmdb/*` sem throttle podendo sobrecarregar serviços externos
- `/api/subtitles/download/:fileId` sem throttle
