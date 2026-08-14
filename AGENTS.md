# Instruções globais — ver bloco gerado abaixo.

## CI remoto em ARM

- Execute builds e testes pesados no ARM por Docker context, nunca fixando host,
  nome de máquina ou daemon no código e nos scripts.
- Leia o contexto e os nomes da stack de `.env`; documente os valores em
  `.env.example`. Os nomes canônicos são `JACKUI_CI_DOCKER_CONTEXT`,
  `JACKUI_CI_COMPOSE_PROJECT`, `JACKUI_CI_RUNNER_LABELS`, `JACKUI_CI_IMAGE` e
  `JACKUI_CI_POSTGRES_PORT`.
- Use `docker --context "$JACKUI_CI_DOCKER_CONTEXT" compose` nos scripts, para
  suportar contextos locais, SSH remotos e outros daemons Docker.
- A mesma imagem e stack de CI devem servir à execução manual e aos runners do
  GitHub Actions (e qualquer runner self-hosted), evitando divergência entre a
  máquina local, o ARM e o CI.
- Mantenha `.env` fora do Git; não inclua credenciais, nomes de hosts internos
  ou contextos específicos em arquivos versionados.

## Armadilhas operacionais do JackUI

Estas regras complementam as instruções gerais do ai-standards com os cenários
ocultos encontrados durante a operação do projeto.

### Rede Gluetun e banco de dados

- Ao usar o overlay `docker-compose.gluetun.yml`, os serviços `jackui` e
  `postgres` compartilham o namespace de rede do container `gluetun-jackui`.
  Nesse modo o host do banco deve ser `localhost:5432`, não `postgres:5432`.
- O compose merge já sobrescreve `JACKUI_DATABASE_URL` no overlay; nunca
  hardcode hosts/credenciais no repo. Valores sensíveis e contextos Docker
  específicos vivem apenas no `.env` (gitignored).

### Fuso horário e scheduler de banda

- O agendador de banda (`streamer.StartBandwidthScheduler`,
  `downloads.BandwidthWindow`) compara janelas `HH:MM` com `time.Now()`, ou
  seja, com o horário local do container.
- Sempre defina `TZ` no `.env` (padrão do compose é `America/Sao_Paulo`). Sem
  `TZ` o container roda em UTC e janelas como "23:00-06:00" deslocam 3h.

### Isolamento de usuários e subpastas (UserSubpath)

- Mounts com suffixo `:usersubpath` isolam cada usuário em
  `{mount}/{username}/...`. A resolução de caminho passa obrigatoriamente por
  `Browser.ResolvePathFor`/`ResolvePath`, que rejeitam travessia via `..`,
  caminhos absolutos e symlinks que escapem do mount.
- Nunca concatene caminhos manualmente em novos endpoints; use
  `lh.ScopePath` + `Browser.ResolvePathFor` para respeitar tanto o mount base
  quanto o subdiretório do usuário.

### Preview de arquivos compactados

- O endpoint `/api/preview/*` lê bytes sob demanda de torrents incompletos
  (zip/cbz/epub) via `FileReader`. Se os peers não entregarem o diretório
  central do zip dentro do timeout do leitor, a requisição pode bloquear ou
  retornar EOF inesperado.
- As respostas de conteúdo ativo (EPUB, SVG) carregam
  `X-Content-Type-Options: nosniff` e `Content-Security-Policy: sandbox` para
  neutralizar scripts maliciosos dentro de arquivos compactados.

### Shutdown e rede caída

- `cmd/server/main.go` impõe um deadline rígido de 20s (`cleanupHardDeadline`)
  no shutdown. O teardown do cliente anacrolix pode travar indefinidamente ao
  anunciar saída no DHT/rastreadores quando a VPN/rede cai.
- O watchdog força `os.Exit(0)` ao exceder o deadline, permitindo que o Docker
  recrie o container; o próximo boot reconcilia estado via
  `RescueStuckMoving`, `resumeSeeding` e verificação de pieces.

### Sanitização de logs

- Use `httpshared.SanitizeForLog` (strings), `SanitizeInt`/`SanitizeIntSlice`
  (ints) para inputs externos antes de logar. Isso evita log injection e
  poluição de logs estruturados, além de ajudar a passar em auditorias
  CodeQL/Sonar.

## Frontend

- `web/postcss.config.js` foi renomeado para `.mjs` para evitar o warning
  `[MODULE_TYPELESS_PACKAGE_JSON]` do Node 24. Mantenha a configuração PostCSS
  como ESM enquanto o projeto não declarar `"type": "module"` no
  `web/package.json`.

<!-- BEGIN ai-standards (gerado por sync-agents.sh — NÃO EDITE; fonte: /Users/luizg/.config/ai-standards) -->
@/Users/luizg/.config/ai-standards/AGENTS.md
<!-- END ai-standards -->
