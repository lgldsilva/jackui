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

<!-- BEGIN ai-standards (gerado por sync-agents.sh — NÃO EDITE; fonte: /Users/luizg/.config/ai-standards) -->
@/Users/luizg/.config/ai-standards/AGENTS.md
<!-- END ai-standards -->
