# Requisitos de UX, Usabilidade, Interface e Acessibilidade — JackUI

> Estado: proposta de implementação
> Origem: auditoria estática do frontend e documentação em 2026-07-10.
> Escopo: melhorar a experiência de busca, reprodução, downloads, biblioteca,
> arquivos locais, configuração e operação do JackUI.
>
> Não substitui `docs/REQUIREMENTS.md`: complementa o roadmap técnico existente.

## 1. Objetivo

Tornar o JackUI previsível, acessível e fácil de operar tanto para usuários
técnicos quanto para pessoas que apenas querem encontrar, reproduzir, baixar e
organizar mídia.

A prioridade é:

1. eliminar barreiras reais de acessibilidade e interações inválidas;
2. deixar claro quando o sistema está carregando, vazio, indisponível ou mal configurado;
3. reduzir a complexidade percebida dos fluxos de busca e download;
4. guiar a primeira configuração e o diagnóstico operacional;
5. preservar a capacidade avançada do produto sem expor detalhes técnicos antes
   de serem necessários.

## 2. Princípios de produto

- **Ação primária clara:** cada tela e card deve ter uma ação principal evidente.
- **Divulgação progressiva:** opções avançadas ficam disponíveis, mas não disputam
  atenção com Reproduzir, Baixar, Abrir ou Salvar.
- **Estados honestos:** vazio, carregando, indisponível e erro são estados distintos.
- **Operação compreensível:** falhas de Jackett, mounts, transcode, disco ou rede
  devem ter diagnóstico acionável.
- **Mobile first:** qualquer ação importante deve ser realizável por toque com uma mão.
- **Acessibilidade por padrão:** teclado, leitor de tela, foco e contraste não são
  melhorias opcionais.
- **PT-BR e inglês completos:** não haverá strings de interface fixas fora do i18n.

## 3. Escopo

### Incluído

- Busca e cards de resultado.
- Downloads, seleção de arquivos e confirmação de enfileiramento.
- Biblioteca, favoritos e arquivos locais.
- Navegação global e Configurações.
- Sheets, modais, diálogos e player.
- Estados de loading, vazio, erro, sucesso e retry.
- Onboarding inicial.
- Página consolidada de saúde/diagnóstico.
- Acessibilidade e internacionalização da interface.

### Fora de escopo

- Reescrita do protocolo Transmission RPC.
- HLS Master Playlist Phase 2, salvo exposição de estado/erro no UI.
- Redesenho visual completo ou troca do design system.
- Mudança de arquitetura de autenticação, streaming ou armazenamento.
- Telemetria externa obrigatória.

## 4. Marcos e dependências

| Marco | Nome | Depende de | Risco |
|---|---|---|---|
| UX-0 | Fundação e baseline | nenhum | baixo |
| UX-1 | Acessibilidade estrutural | UX-0 | médio |
| UX-2 | Estados e feedback confiáveis | UX-0 | baixo |
| UX-3 | Simplificação dos fluxos principais | UX-1, UX-2 | médio |
| UX-4 | Onboarding e diagnóstico | UX-2 | médio |
| UX-5 | Validação com usuários e refinamento | UX-1 a UX-4 | baixo |

---

## UX-0 — Fundação, inventário e baseline

### Objetivo

Estabelecer métricas e padrões de implementação antes de modificar as telas.

### Requisitos

1. Inventariar todos os componentes interativos:
   - links;
   - botões;
   - inputs;
   - tabs;
   - menus;
   - modais;
   - sheets;
   - toasts;
   - cards clicáveis.
2. Mapear todos os estados assíncronos por tela:
   - loading;
   - sucesso;
   - vazio;
   - erro recuperável;
   - erro não recuperável;
   - permissão negada;
   - dependência não configurada.
3. Identificar strings não traduzidas nas telas.
4. Registrar baseline de:
   - Lighthouse Accessibility;
   - teclado nos fluxos principais;
   - viewport mobile de 360 px;
   - viewport desktop de 1280 px;
   - comportamento em tema claro e escuro.
5. Definir primitives compartilhados:
   - `AccessibleDialog` ou evolução do `Sheet`;
   - `AsyncState`;
   - `IconButton`;
   - `RetryPanel`;
   - `EmptyState`;
   - `StatusBanner`.

### Critérios de aceite

- Documento com mapa de telas, estados e componentes reutilizáveis.
- Lista de toda interação aninhada ou sem nome acessível.
- Baseline reproduzível de acessibilidade e responsividade.
- Nenhuma mudança funcional entregue neste marco.

---

## UX-1 — Acessibilidade estrutural

### Problema

Há elementos interativos aninhados, modais sem gestão completa de foco,
botões puramente icônicos sem nome acessível e áreas de toque pequenas.

### Componentes prioritários

- `web/src/components/ResultCard.tsx`
- `web/src/pages/LibraryPage.tsx`
- `web/src/pages/SearchPage.tsx`
- `web/src/components/Sheet.tsx`
- `web/src/components/TrailerModal.tsx`
- `web/src/components/PlayerModal.tsx`
- `web/src/components/ConfirmDialog.tsx`
- `web/src/components/DownloadModal.tsx`
- `web/src/pages/SettingsPage.tsx`

### Requisitos funcionais

#### UX-1.1 — Sem interativos aninhados

1. Um card de resultado não deve ser simultaneamente link/botão contêiner de
   outros botões.
2. A área de abertura/reprodução deve ser irmã da área de ações secundárias.
3. Cards da biblioteca devem usar estrutura semântica:
   - `article` como contêiner;
   - link ou botão principal explícito;
   - menu/botões auxiliares adjacentes.
4. Abas de busca devem seguir o padrão ARIA:
   - contêiner `role="tablist"`;
   - cada aba com `role="tab"`;
   - painel com `role="tabpanel"`;
   - controle de fechar adjacente, nunca interno à aba.

#### UX-1.2 — Primitive único para diálogos e sheets

Todo modal/sheet deve:

1. ter `role="dialog"` e `aria-modal="true"`;
2. possuir título associado via `aria-labelledby`;
3. possuir descrição associada quando necessária via `aria-describedby`;
4. receber foco inicial ao abrir;
5. manter Tab e Shift+Tab dentro do diálogo;
6. fechar com Escape, exceto quando uma operação crítica estiver em andamento;
7. restaurar foco ao controle que abriu o diálogo;
8. bloquear interação e leitura do conteúdo de fundo;
9. preservar scroll de forma segura;
10. nunca depender apenas de cor ou ícone para comunicar ação destrutiva.

#### UX-1.3 — Botões por ícone e toque

1. Todo botão por ícone deve ter `aria-label` localizado.
2. `title` não substitui nome acessível.
3. Ações tocáveis devem ter área mínima de 44×44 px.
4. Ações destrutivas devem exibir texto ou confirmação explícita.
5. Ações principais devem ter rótulo visível sempre que houver espaço.

### Critérios de aceite

- Zero casos de elementos interativos aninhados nas telas auditadas.
- Navegação completa por teclado em Busca, Biblioteca, Local, Downloads,
  Settings e Player.
- Todo modal retorna o foco ao seu acionador.
- Todo botão icônico possui nome acessível.
- Testes de componente cobrem foco, Escape, Tab/Shift+Tab e restauração de foco.

---

## UX-2 — Estados honestos e feedback acionável

### Problema

Falhas de rede ou de dependências podem aparecer como listas vazias; parte dos
spinners e mensagens não é anunciada a leitores de tela; retries não são
uniformes.

### Requisitos

#### UX-2.1 — Modelo único de estado assíncrono

Toda consulta remota deve distinguir:

| Estado | Mensagem esperada | Ação disponível |
|---|---|---|
| Carregando | "Carregando [recurso]…" | aguardar/cancelar quando aplicável |
| Vazio | "Nenhum item encontrado" | alterar filtro ou voltar |
| Não configurado | "Configure [dependência] para usar este recurso" | abrir Settings |
| Erro recuperável | "Não foi possível carregar [recurso]" | tentar novamente |
| Sem permissão | "Você não tem permissão…" | orientação apropriada |
| Erro crítico | descrição curta + identificador técnico copiável | retry/diagnóstico |

#### UX-2.2 — Erros por domínio

1. Jackett indisponível:
   - explicar que a busca não pôde ser feita;
   - oferecer retry;
   - oferecer atalho para configuração quando o usuário for admin.
2. TMDB/Discover indisponível:
   - não mostrar "nenhum título";
   - manter conteúdo já carregado quando possível;
   - exibir falha parcial por seção.
3. Downloads:
   - erro de enqueue deve informar se a causa foi cliente, espaço, torrent,
     autenticação ou rede quando o backend fornecer o dado.
4. Local/mount:
   - distinguir mount vazio, mount indisponível e permissão insuficiente.
5. Player/transcode:
   - mostrar etapa atual: preparando, carregando stream, transcodificando,
     bufferizando ou falhou;
   - expor detalhe técnico apenas em "Ver diagnóstico".

#### UX-2.3 — Acessibilidade de feedback

1. Loading e resultados: `role="status"` + `aria-live="polite"`.
2. Erros de submit e operação: `role="alert"`.
3. Após falha de formulário:
   - focar resumo de erro, ou
   - focar o primeiro campo inválido.
4. Toasts não devem ser o único lugar para erros críticos.
5. Erros persistentes devem usar banner/painel inline.

### Critérios de aceite

- Nenhum erro de API é convertido silenciosamente em `[]`.
- Cada estado vazio tem significado de resposta bem-sucedida.
- Todo erro recuperável tem retry.
- Estados de loading e erro são anunciados por leitores de tela.
- Testes validam sucesso, vazio, erro, retry e configuração ausente.

---

## UX-3 — Fluxos principais simplificados

### UX-3.1 — Busca e cards de resultados

#### Objetivo

Permitir que a pessoa encontre e execute a intenção principal com pouca carga
cognitiva.

#### Requisitos

1. Ações primárias visíveis:
   - Reproduzir;
   - Baixar.
2. Ações secundárias agrupadas em "Mais ações":
   - copiar magnet;
   - abrir magnet;
   - baixar `.torrent`;
   - enviar a cliente externo;
   - adicionar à playlist;
   - informações técnicas.
3. "Explorar arquivos" fica visível apenas quando relevante para torrent
   multi-arquivo.
4. Filtros avançados em mobile:
   - preservar filtros ativos na abertura do Sheet;
   - mostrar quantidade de filtros ativos;
   - permitir limpar todos;
   - manter CTA claro para aplicar.
5. Termos técnicos precisam de explicação contextual:
   - Seeds: pessoas compartilhando;
   - Leechers: pessoas baixando;
   - Magnet: link de download;
   - codec/HDR: explicação curta opcional.
6. Resultados devem comunicar:
   - quantidade retornada;
   - filtros ativos;
   - itens ocultos por filtro;
   - falha parcial de fontes.

### UX-3.2 — Download

1. Não fechar a confirmação automaticamente em 1,2 s.
2. Após sucesso, mostrar:
   - quantidade de arquivos/torrents;
   - destino;
   - categoria;
   - ação "Ver downloads".
3. Antes do enqueue, quando disponível:
   - tamanho estimado;
   - espaço livre/destino;
   - quantidade de arquivos;
   - aviso para seleção parcial.
4. Operações em massa devem informar progresso, sucesso parcial e falhas por item.
5. Ações irreversíveis exigem confirmação clara.

### UX-3.3 — Navegação e Settings

1. Settings deve preservar a orientação:
   - navegação global persistente, ou
   - breadcrumb + título + botão textual de retorno.
2. O retorno não pode ser apenas ícone.
3. Itens administrativos devem estar claramente separados de preferências pessoais.
4. A navegação mobile deve agrupar destinos menos frequentes sem esconder:
   - Busca;
   - Downloads;
   - Biblioteca;
   - Local;
   - Configurações.
5. Ícones de modo, tema e incógnito devem ter rótulo e estado explícito.

### UX-3.4 — Internacionalização

1. Nenhuma string visível deve permanecer fora de `locales/pt.json` e `en.json`.
2. Cobrir:
   - cache;
   - seed/seeder;
   - leech/leecher;
   - Play;
   - Magnet;
   - torrent;
   - aria-labels;
   - tooltips;
   - mensagens de erro e loading.
3. Usar pluralização adequada para contagens.

### Critérios de aceite

- O fluxo Busca → Reproduzir e Busca → Baixar pode ser feito sem conhecer magnet,
  seed/leech ou opções técnicas.
- O usuário sempre consegue chegar à fila após enfileirar download.
- Navegação é consistente entre telas principais e Settings.
- Check de i18n não encontra strings novas fora dos locales.
- E2E cobre busca, ações do card, filtro mobile, download e retorno à fila.

---

## UX-4 — Onboarding e diagnóstico operacional

### UX-4.1 — Assistente de primeira configuração

#### Fluxo

1. Boas-vindas e explicação curta do produto.
2. Configurar Jackett.
3. Validar conexão ao Jackett.
4. Configurar cliente de download, se aplicável.
5. Configurar diretórios/mounts necessários.
6. Validar permissões, escrita e espaço quando suportado.
7. Testar busca.
8. Testar download ou streaming.
9. Exibir resumo final e próximos passos.

#### Regras

- O progresso deve sobreviver ao refresh.
- Cada passo deve permitir retry.
- Erros devem mostrar como corrigir.
- Usuários não-admin devem ver apenas passos que podem completar.
- O wizard deve poder ser reaberto em Settings.

### UX-4.2 — Painel de saúde

Criar tela de diagnóstico consolidada com:

| Domínio | Estado mínimo |
|---|---|
| Aplicação | versão, uptime, status geral |
| Banco | conectado/indisponível |
| Jackett | configurado, alcançável, último erro |
| Cliente de download | configurado, conectado, fila |
| Armazenamento | mounts, escrita, espaço quando disponível |
| Streaming | sessões, transcodes ativos, fila e falhas |
| Cache | uso, acertos/erros, limpeza |
| Rede/VPN | estado quando aplicável |

Cada item deve oferecer:

- estado: saudável, aviso, indisponível ou não configurado;
- última atualização;
- descrição humana;
- detalhe técnico expansível;
- ação de retry/teste quando segura;
- atalho de configuração pertinente.

### Critérios de aceite

- Usuário admin consegue identificar dependência indisponível sem abrir logs.
- Setup inicial pode ser concluído sem consultar README.
- Cada falha de setup tem instrução e retry.
- Health dashboard não expõe segredos, tokens ou paths sensíveis a usuários sem permissão.

---

## UX-5 — Validação e métricas

### Testes de usabilidade

Validar com 5 a 8 pessoas representativas, incluindo pelo menos:

- uma pessoa técnica que usa torrents frequentemente;
- uma pessoa técnica focada em mídia local;
- uma pessoa com menor familiaridade com magnet/codecs;
- um usuário de celular;
- um usuário de teclado sem mouse.

### Tarefas

1. Configurar o JackUI pela primeira vez.
2. Encontrar um conteúdo reproduzível.
3. Baixar somente arquivos desejados de um pack.
4. Encontrar um download concluído.
5. Diagnosticar por que a busca não funciona.
6. Navegar e fechar o player usando apenas teclado.
7. Alterar idioma/tema e localizar Settings.

### Métricas de sucesso

| Métrica | Meta |
|---|---:|
| Conclusão de Busca → Reproduzir sem ajuda | ≥ 90% |
| Conclusão de Busca → Download sem ajuda | ≥ 85% |
| Identificação correta de falha de dependência | ≥ 80% |
| Sucesso em teclado nos fluxos críticos | 100% |
| Botões sem nome acessível | 0 |
| Interativos aninhados | 0 |
| Strings de UI não traduzidas | 0 |
| Regressões visuais desktop/mobile | 0 críticas |

## 5. Estratégia de entrega

### PR 1 — Fundação de acessibilidade

- Primitive acessível de diálogo/sheet.
- Correção de interativos aninhados.
- `IconButton` acessível.
- Testes de foco e teclado.

### PR 2 — Estados e falhas

- `AsyncState`, `EmptyState`, `RetryPanel`, `StatusBanner`.
- Discover, Search, Library, Downloads e Local.
- Erros anunciáveis e retry.

### PR 3 — Busca, cards e download

- Simplificação de ações.
- Menu "Mais ações".
- Ajustes mobile nos filtros.
- Confirmação persistente de download.
- i18n associado.

### PR 4 — Navegação e Settings

- Navegação consistente.
- Retorno textual/breadcrumb.
- Labels de modo, tema e incógnito.
- Revisão mobile.

### PR 5 — Onboarding e Health Dashboard

- Wizard inicial.
- Diagnóstico por dependência.
- Permissões e proteção de dados sensíveis.
- E2E dos fluxos.

## 6. Qualidade obrigatória

Para cada PR:

- `npm test`;
- `npm run lint`;
- `npx tsc --noEmit`;
- `npm run check:i18n`;
- `npm run build`;
- testes de componente para o comportamento novo;
- Playwright nos fluxos alterados;
- revisão de layout em 360 px, 768 px e 1280 px;
- tema claro e escuro;
- teclado e leitor de tela nos componentes interativos;
- gates de CI, Sonar e segurança verdes.

## 7. Riscos e mitigação

| Risco | Mitigação |
|---|---|
| Refactor grande em componentes extensos | PRs pequenos, testes de caracterização e extrações graduais |
| Regressão no player | Isolar primitive de diálogo do ciclo de streaming e cobrir player com E2E |
| Menu "Mais ações" esconder função frequente | Validar com usuários e telemetria local, manter ações de alto uso visíveis |
| Diagnóstico expor informação sensível | Contrato explícito de dados públicos e autorização admin |
| Wizard excessivamente rígido | Permitir pular/reabrir etapas e exibir estado incompleto |
| Inconsistência PT/EN | CI de i18n e revisão de ambas as locales em cada PR |
