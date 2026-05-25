# JackUI — Claude Code Instructions

Interface visual moderna para busca de torrents via Jackett.

## Stack

- **Backend**: Go 1.22 + Gin — API REST que proxifica o Jackett
- **Frontend**: React 18 + TypeScript + Vite + TailwindCSS (dark theme)
- **Deploy**: single binary Go com frontend embutido via `//go:embed all:dist`
- **Infra**: Docker no Raspberry Pi (context `raspberrypisrv`, IP `192.168.0.100`)

## Comandos essenciais

```bash
make test          # roda os 60 testes (Go)
make build         # npm build → go build → binário ./jackui
make deploy        # build da imagem no raspberrypisrv + docker compose up -d
make logs          # docker logs -f
make dev-frontend  # Vite em :5173 com proxy para :8989
make dev-backend   # go run ./cmd/server em :8989
```

## Arquitetura

```
web/src/          → frontend React (dev: :5173, prod: embutido no binário)
ui/embed.go       → //go:embed all:dist  (Vite compila para ui/dist/)
cmd/server/main.go → Gin: /api/* + SPA fallback
internal/
  config/         → load YAML + override por env vars
  jackett/        → HTTP client para API Jackett (/api/v2.0/indexers/...)
  downloader/     → interface Client + qBittorrent + Transmission
  handlers/       → search, download, config, clients
```

## Configuração

Dois níveis (env sobrescreve YAML):

| Variável env      | YAML equivalente         | Descrição              |
|-------------------|--------------------------|------------------------|
| `JACKETT_URL`     | `jackett.url`            | URL do Jackett         |
| `JACKETT_API_KEY` | `jackett.api_key`        | API key do Jackett     |
| `JACKUI_PORT`     | `port`                   | Porta do servidor      |

Clientes de download (qBittorrent e Transmission) só via `config.yaml` — usar múltiplas instâncias local/remoto conforme necessidade.

## Jackett no homelab

- Container: `jackett` no Raspberry Pi (`192.168.0.100`)
- Config em: `/portainer/Files/AppData/Config/Jackett/Jackett/ServerConfig.json`
- API key atual em: `.env` (não versionado)

## Clientes de download

Ambos implementam a interface `downloader.Client`:

```go
type Client interface {
    AddMagnet(magnetURI, savePath string) error
    AddTorrentURL(url, savePath string) error
    Name() string
    Type() string
}
```

- **qBittorrent**: Web API v2, login com cookie jar, session reuse
- **Transmission**: RPC JSON, retry 409 para session-id, basic auth

## Testes (60 testes, 4 pacotes)

```
internal/config/      → 5  (load/save/roundtrip/defaults)
internal/jackett/     → 22 (formatAge, parse, params, erros HTTP)
internal/downloader/  → 16 (qbit: login/sessão/savepath; transmission: 409 retry/auth)
internal/handlers/    → 17 (search/indexers 400/502; download: default client, pick by ID)
```

Mocks usam `net/http/httptest` — sem dependências externas de teste.

## Convenções

- Sem comentários exceto onde o WHY não é óbvio
- Erros sempre retornam JSON: `{"error": "mensagem"}`
- Nova feature de download client: implementar `downloader.Client`, registrar em `downloader.New()`
- Nova feature de UI: componente em `web/src/components/`, página em `web/src/pages/`

## Próximas features possíveis

- Histórico de buscas (localStorage ou SQLite)
- Filtros por seeders mínimos / tamanho
- Preview de capa via TMDB/TVDB para filmes e séries
- Integração com Sonarr/Radarr para adicionar diretamente
- Notificação quando torrent terminar de baixar
- Dark/light mode toggle
