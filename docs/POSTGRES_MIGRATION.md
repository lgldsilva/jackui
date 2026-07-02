# Migração SQLite → PostgreSQL

JackUI passou a guardar **todo o estado em um único PostgreSQL** (sidecar), com
schema coeso e chaves estrangeiras cruzando o que eram 14 arquivos SQLite
separados. Só os dados de **auth** (usuários, senhas, MFA, passkeys, refresh
tokens) são preservados na virada; o resto nasce vazio.

## Arquitetura

- `internal/db`: pool pgx + runner golang-migrate (migrations embarcadas em
  `internal/db/migrations/*.sql`). Schema único: `0001_extensions` (unaccent),
  `0002_init` (todas as tabelas + FKs), `0003_history_fts` (busca full-text).
- Todos os stores recebem o `*sql.DB` compartilhado (`deps.db` em `cmd/server/main.go`).
- Config: `JACKUI_DATABASE_URL` (ou `DATABASE_URL`, ou `JACKUI_PG_*`).
- `jackui migrate-auth --from <auth.db> --to <DSN>`: ETL one-time do auth.db legado.

## Deploy local (sem VPN — `make deploy-auto`)

1. No `.env`: defina `POSTGRES_PASSWORD` (e opcional `JACKUI_PG_DIR`).
2. `docker compose up -d postgres` → espera ficar `healthy`.
3. (Se houver auth.db legado a preservar) rode a ETL — veja abaixo.
4. Deploy normal: `make deploy-auto`. O `jackui` sobe com
   `JACKUI_DATABASE_URL=postgres://jackui:<senha>@postgres:5432/jackui`.

## Deploy em PROD (atrás do gluetun — hand-file)

⚠ Em prod o compose é **hand-maintained** em `<prod-config-dir>/docker-compose.yml`
e o Jenkins **só troca a imagem** — mudanças de compose/env NÃO chegam por CI.
Edite à mão:

1. Faça backup do auth.db: `cp .../jackui/auth.db auth.db.bak-$(date +%F)`.
2. Defina `POSTGRES_PASSWORD` (guarde no seu gerenciador de segredos) e
   `JACKUI_PG_DIR` (SSD, não o volume do piece cache).
3. Adicione o serviço `postgres` no formato do overlay gluetun (mesmo netns):
   `network_mode: "container:gluetun-jackui"`, sem `networks`, `depends_on:
   gluetun-jackui: service_started`, healthcheck `pg_isready`, volume
   `${JACKUI_PG_DIR}:/var/lib/postgresql/data`, envs `POSTGRES_*`. (Veja
   `docker-compose.yml` + `docker-compose.gluetun.yml` como referência.)
4. No serviço `jackui`: `depends_on: postgres: condition: service_healthy` e
   `JACKUI_DATABASE_URL=postgres://jackui:<senha>@localhost:5432/jackui?sslmode=disable`
   (loopback no netns do gluetun — NÃO passa pela VPN).
5. Suba só o Postgres e espere o initdb:
   ```
   cd <prod-config-dir>
   docker compose up -d postgres && docker compose ps   # aguarde "healthy"
   ```
6. Rode a ETL do auth (no netns do gluetun, destino localhost:5432):
   ```
   docker compose run --rm --entrypoint /app/jackui jackui \
     migrate-auth --from /data/auth.db \
     --to "postgres://jackui:<senha>@localhost:5432/jackui?sslmode=disable"
   ```
   Valide: `count(*)` por tabela + 1 login + 1 refresh token ativo.
7. **Só depois da imagem nova (com pgx) buildada/pushada pelo Jenkins**, recrie:
   `docker compose up -d --force-recreate jackui` e acompanhe `logs -f jackui`.

Ordem segura: PG up → migrar dados → imagem pgx → recreate jackui. Enquanto a
imagem ainda for SQLite, apontar a env não adianta.

**Rollback:** o `auth.db` continua intacto + reverter a imagem (versão SQLite)
volta ao estado anterior sem perda.

## Testes

Os testes de store rodam contra um Postgres real. Configure
`JACKUI_TEST_DATABASE_URL` (no CI o Jenkinsfile sobe um sidecar `jackui-ci-pg`
com `fsync=off`); sem a env, os testes que dependem de banco fazem `t.Skip`
(`make test` segue verde). O `internal/dbtest` migra um schema por processo e
isola cada teste com `TRUNCATE` (rápido).
