# Changelog

Todas as mudanças notáveis do JackUI. Formato baseado em [Keep a Changelog](https://keepachangelog.com),
versionamento [SemVer](https://semver.org).

> A versão segue as **tags do Jenkins** — semver auto-incrementado por Conventional Commits a cada
> merge na `main` (é o que aparece em `/status`). As entradas `0.2.0`/`0.3.0` no fim são do **esquema
> manual antigo**, anterior à automação; as versões intermediárias (0.4–0.58) não foram catalogadas
> individualmente — a reconstrução por release começa em `0.59.0`.

## [0.64.0] — 2026-06-25

### Adicionado
- **Baixar pack inteiro como UMA linha** (#356): com TODOS os arquivos marcados (o caso comum de um
  pack), o download enfileira 1 linha "torrent inteiro" (`fileIndex = -2`, file priorities do
  anacrolix) em vez de N linhas por-arquivo — um pack de 778 arquivos vira **1 linha**. Selecionar um
  subconjunto continua batch (1 linha por arquivo), preservando a granularidade.

### Corrigido
- **Menu "Local" 2x não some mais com os mounts** (#357): clicar a nav zerava o `?mount=` e o
  auto-select do 1º mount só rodava uma vez; no mobile o seletor vive dentro do bloco do mount ativo e
  sumia. Agora a re-seleção é reativa.

## [0.63.1] — 2026-06-25

### Performance
- **`GET /api/downloads` rápido** (#355): novo `Streamer.LiveStats` — O(1) por torrent (sample de taxa
  + seeders, **sem** o `buildInfo` O(arquivos)) no enrich da lista. Num pack de 778 arquivos a resposta
  caiu de **2–17 s para ~1 s**. O contador da página passou a ser **por torrent**, não por linha.

### Corrigido
- **Watchdog de shutdown** (#355): `runCleanup` força `os.Exit(0)` após 20 s se o cleanup
  (anacrolix/DHT) travar com a rede caída — antes pendurava o processo num **502 eterno** (causou um
  outage); com `restart: unless-stopped` o Docker recria o container.

## [0.63.0] — 2026-06-25

### Adicionado
- **Ordenar downloads** por velocidade de download/upload e por seeds (#352).
- **Filtrar a lista de arquivos** local por status — baixando / concluído (#353).

### Performance
- **CPU ocioso reduzido** (#354): o HLS do ffmpeg fecha ao sair o **último** viewer (libera o slot de
  GPU-decode) em vez de sobreviver até o reap de 5 min; 3 polls do front pausam com a aba oculta; perfil
  de baixo consumo (`GOGC`/`GOMEMLIMIT`/`GOMAXPROCS` + peer caps) documentado.

## [0.62.2] — 2026-06-25

### Corrigido
- **Seed via metainfo em cache** (#351): o re-seed usa um magnet de info_hash puro → `resolveMagnet`
  cache-first → semeia in-place. Acaba com o erro "auto-seed failed: .torrent URL 404".

### Performance
- **Seek instantâneo em vídeo baixado** (#350): `/api/local/file` serve um arquivo de disco LOCAL via
  `http.ServeFile` (sendfile); só mount remoto/FUSE (`isRemoteFS`) usa a Session com read-ahead de 16 MB.

## [0.62.1] — 2026-06-25

### Corrigido
- **Lista O(N) travando a UI** + **transcode GPU robusto** (#349): o enrich da lista dedupe por
  info_hash; um semáforo limita decoders CUDA e a sessão excedente cai para **decode em CPU** em
  `CUDA_ERROR_OUT_OF_MEMORY` em vez de falhar.

## [0.62.0] — 2026-06-25

### Adicionado
- **Torrent multi-arquivo como UMA unidade** (#347): o scheduler conta **1 slot por torrent**, e o
  worker faz 1 verify + 1 completion-move + 1 AI-rename + 1 ciclo de stall por torrent (fim do consumo
  O(N) de CPU/RAM que dava OOM em packs grandes).
- **Enqueue em lote + UI agrupada por torrent** — 1 card por torrent (#348).

### Corrigido
- **Completions travadas em "moving"** (move-not-found) após o agrupamento por categoria (#346):
  `completion_dest` congelado + fallback sem-categoria.

## [0.61.0] — 2026-06-24

### Adicionado
- **Categoria como pasta agrupadora** por padrão (`…/<user>/<categoria>/…`) (#345).

## [0.60.0] — 2026-06-24

### Adicionado
- Botão **"Ver arquivos"** no card de torrent streaming (completo/semeando) (#342).
- Botão **"Abrir no local"** nos downloads concluídos (#344).
- Indicador **"baixando"** em arquivos/pastas incompletos (`.part`) (#343).

## [0.59.1] — 2026-06-24

### Corrigido
- **Pastas de download duplicadas** (`Name (1)`, `(2)`…) causadas pela migração `UserSubpath` (#341).

## [0.59.0] — 2026-06-24

Release grande — acumulou ~4 dias desde a v0.58.7 (a saga de playback no iOS + uma frente de
downloads/seed + endurecimento de auth/boot/shutdown).

### Adicionado

#### Player / Música
- **Capa no Now Playing** (mediaSession artwork 96/512); **faixa de áudio selecionável no HLS**
  (torrent + local); **mini-player de áudio** que vira barra no footer (arrastar/expandir).

#### Downloads / Seed
- **Escolher o destino do download** (picker com browse); **baixar direto no bulk** + re-seed na
  promoção; **auto-seed de downloads completos** de trackers de seed; **seed seletivo por tracker** +
  painel de peers; **storage por-torrent** aponta pro local real (seed sem re-baixar).

#### Busca / Infra
- **Reordenar abas de pesquisa** arrastando; **card abre em nova aba** + player full-viewport no
  deep-link; **retry com backoff** em chamadas externas idempotentes; **endpoint de refresh da
  peer-port** integrado ao routing do gluetun.

### Corrigido

#### Player (iOS / Safari)
- **Playback nativo no iOS** acertado após várias iterações: vídeo e áudio tocam com o `src` setado no
  gesto (sem `play()` programático nem `v.load()`), saindo do `readyState 2`; vídeo local no iOS via
  **HLS/remux** (MP4 progressive trava no WebKit); auto-avanço entre faixas; **legenda embutida/sidecar
  no Safari** (HLS nativo); legendas em bottom-sheet no mobile; shuffle/repeat de faixa + dock desktop.

#### Auth / Confiabilidade
- **Não desloga no deploy**: janela de graça na rotação de refresh + tolera falha transitória; exige
  `jwt_secret` no boot (auth ON); `busy_timeout(5000)` em todos os stores SQLite; `/healthz` checa o
  streamer; **graceful restart** no watcher de porta do VPN (para HLS/workers/transfers antes de sair).

#### Downloads / Streamer
- **Fallback p/ magnet por info_hash** quando a URL `.torrent` dá 404; move idempotente p/ single-file
  (já-no-destino = sucesso); `resumeSeeding` espera o `FilePathResolver` no boot (senão o seed resumido
  cai em 0%); completion in-memory no `relocatedStorage` (evita conflito de bolt no DataDir).

#### Busca / Nav / UI
- Botões do card não disparam o link do card (abrir player); só restaura a rota salva no PWA (refresh
  não redireciona); botão "parar" em transfers cancela de verdade (era no-op); chips de filtro no topo
  da lista; limpar filtros revela resultados de contagem desconhecida + card não vaza com nome longo;
  logo não navega (só dispara o easter egg); reabrir o app restaura a playlist no boot frio (sem `?play`).

---

> As entradas abaixo são do **esquema de numeração manual antigo** (anterior à automação de versão por
> tags); ficam aqui como histórico. Não confundir com o `0.x` das tags atuais.

## [0.3.0] — 2026-06-05

### Adicionado
- **Tema claro/escuro** em toda a UI (tailwind dark/light variants + toggle),
  com tokens de cor semânticos nos componentes e páginas (#96).

### Corrigido
- **Favorites**: import de `.torrent` em lote não trava mais — a conversão
  byte→base64 era O(n²) e estourava em arquivos reais ("importar 4 torrents
  falha") (#94).

### Manutenção
- `.gemini/` adicionado ao `.gitignore` (lixo de ferramentas de IA) (#95).
- Auditoria open-source: histórico git está **limpo** (sem segredos commitados);
  LICENSE/CONTRIBUTING/SECURITY ficam para quando a publicação for decidida.

## [0.2.0] — 2026-06-05

Onda de correções de bugs (caça exploratória + auditoria) e melhorias de
robustez/UX. 11 PRs (#82–#92).

### Adicionado
- **UX mobile**: reforma da navegação mobile, toque de 1 ação na linha do
  arquivo, downloads multi-arquivo, melhorias na LocalPage (#82).
- **Streaming — viewer-lease**: stream-only para de seedar à toa logo após
  fechar o player, mas sobrevive enquanto houver espectadores (protege
  co-watchers) (#82).
- **Local — promover em lote**: um único modal aplica destino + renomeação IA a
  N arquivos numa só chamada (fim da fila um-a-um) (#82).
- **Local — limpar pastas vazias**: botão que remove subpastas vazias
  recursivamente (#82).
- **Thumbnails locais**: limite de concorrência + cache persistente +
  negative-cache (não re-gera HDR 4K que falha) (#82).

### Corrigido
- **Player**: thumbnail de hover preso ao trocar de vídeo; race do `streamAdd`
  que sobrescrevia o vídeo novo; `ErrorBoundary` global (fim das telas brancas);
  reset de `artFailed` por infoHash (#82, #89).
- **Move local**: recusa sobrescrever item de mesmo nome no destino (perda de
  dados silenciosa) e preserva o mtime no fallback cross-device (#82).
- **Auth**: fecha o TOCTOU na rotação de refresh token + detecção de reuso
  (revoga a sessão em replay) (#85).
- **Streamer**: TOCTOU em `HealthSnapshot` (panic) lido sob lock; falhas de
  `persistMetainfo` logadas; `verifiedFiles` purgado por hash no ciclo de vida
  (em vez de wipe-2000 que re-hashava ativos) (#86, #90).
- **Art**: negative-cache evita re-rodar IA+TMDB+web-search a cada card (#88).
- **Parser**: falso-positivo de Season ("Ocean's 11"→S11) e de ano ("Blade
  Runner 2049"→2049); corrige MediaKind/match TMDB (#87).
- **Transmission RPC**: `torrent-set` sem `ids` aplica a todos; `torrent-add`
  respeita `labels`→categoria; JSON-RPC 2.0 sem `params` volta a funcionar
  (#84, #90).
- **Busca**: para o falso "Jackett não configurado" (timeout transitório / URL
  default com API key salva) (#91).
- **Logout**: limpa os dados de modo incógnito na hora (privacidade) (#90).

### Melhorado (rename IA)
- O `renamer` usa o parser regex como hint confiável (S/E/ano), com **override**
  do S/E (coerência de série — episódios `S01E0x` caem todos em Season 1) e
  **fallback** quando a IA falha (nunca dá hard-error) (#92).

### Refatorado
- Quebra do god-file `client.ts` em módulos por domínio (barrel) (#83).
- Correção de code smells e robustez do monorepo (#84).
