# Changelog

Todas as mudanças notáveis do JackUI. Formato baseado em [Keep a Changelog](https://keepachangelog.com),
versionamento [SemVer](https://semver.org).

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

### Pendente (backlog para a próxima versão)
- Safari/iOS entrando em modo live (deve ser VOD por padrão).
- Downloads/fila: ordenação, aba "Ativos", quota, iniciar/parar todos.
- History (refresh), Favorites (importar lote), Incognito (toggle global).
- Preparação open-source (segredos no histórico git, IPs internos, LICENSE/docs).
