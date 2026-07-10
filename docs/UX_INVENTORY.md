# UX-0 — Fundação: Inventário e Baseline

> Gerado em 2026-07-10 via análise estática do frontend.
> Branch: `feature/ux-usability-foundations` (commit `7b7fef0`)

## 1. Interativos — inventário

**Total de elementos interativos detectados:** ~1.216 (soma de buttons, links,
inputs, handlers, roles, aria, tabIndex em `web/src`).

### 1.1 Cards com interativos aninhados (prioridade crítica)

| Arquivo | Problema |
|---|---|
| `ResultCard.tsx` | `<a>` card link contém múltiplos `<button>` internos |
| `PlaylistDetailPage.tsx` | Row `role="button"` + `tabIndex=0` com botões de ação internos |
| `player/FileTree.tsx`, `FileRow.tsx` | Row button + download control `role="button"` aninhados |
| `DownloadGroupCard.tsx` | Card clicável com ações internas |
| `HistoryPage.tsx` | Result rows clicáveis com action buttons internos |

### 1.2 Botões icônicos sem nome acessível

| Arquivo | Observação |
|---|---|
| `ResultCard.tsx` | Alguns icon buttons sem `aria-label` (só `title`) |
| `HistoryPage.tsx` | Icon buttons sem label |
| `DownloadGroupCard.tsx` | Ações sem label |
| `SettingsPage.tsx` | Alguns controles sem label |
| Componentes de Downloads | Vários cards com icon buttons não rotulados |

### 1.3 Boa cobertura de aria

- Controles do player: bem rotulados
- Close buttons de modais: maioria com aria-label
- Ações de busca: maioria com label

---

## 2. Estados assíncronos — cobertura por página

### 2.1 Cobertura forte (loading + error + empty)

| Página | Loading | Error | Empty | Retry |
|---|---|---|---|---|
| PlaylistsPage | ✅ | ✅ | ✅ | ❌ |
| FavoritesPage | ✅ | ✅ | ✅ | ✅ (refresh) |
| SearchPage | ✅ | ✅ | ✅ | ✅ (auto retry budget) |
| LocalPage | ✅ | ✅ | ✅ | ❌ |
| SettingsPage | ✅ | ✅ | ✅ | ❌ |

### 2.2 Cobertura parcial (loading + empty, sem error)

| Página | Loading | Error | Empty | Risco |
|---|---|---|---|---|
| LibraryPage | ✅ | ❌ (silent catch) | ✅ | Fetch failure vira lista vazia |
| DiscoverPage | ✅ | ❌ | ✅ | Falha silencia |
| WatchlistPage | ✅ | ❌ | ✅ | Nenhum estado de erro |
| PlaylistDetailPage | ✅ | ❌ | ✅ | Nenhum estado de erro |
| ActiveTab, SeedingTab | ✅ | ❌ | ✅ | Filhos de DownloadsPage |

### 2.3 Cobertura fraca

| Página | Loading | Error | Empty | Risco |
|---|---|---|---|---|
| StatsPage | ✅ | ❌ | ❌ | Só loading |
| DownloadsPage (página) | ✅ | toasts apenas | implícito | Erros viram toast, sem banner de página |

### 2.4 Silent failure patterns

- **LibraryPage:** `libraryList(...).catch(() => {})` → falha vira lista vazia.
- **FavoritesPage:** `reloadFavsQuiet()` engole erros → UI obsoleta.
- **DownloadsPage:** `loadTorrents`, `loadFilterOptions`, `localMounts`,
  `getDownloadsQueueSettings` engolem erros → dados parciais/obsoletos.
- **HistoryPage:** alguns catches viram `[]`.

### 2.5 Loader/spinner usado

- `Loader2` (ícone de loader) — padrão em toda a base.
- `BrowseResultsSkeleton` — apenas em HistoryPage.

### 2.6 Componentes de erro

- Banners/cards inline vermelhos: PlaylistsPage, FavoritesPage, LocalPage,
  SettingsPage.
- Banners de erro específicos de busca: amarelo (soft) + vermelho (hard).
- `ViewerError` nos viewers (archive/comic/epub/image).

---

## 3. i18n — auditoria

### 3.1 Saúde geral

| Métrica | Valor |
|---|---|
| Chaves em `pt.json` | 1.616 |
| Chaves em `en.json` | 1.616 |
| Sincronia | ✅ Perfeita |
| Biblioteca | `react-i18next` (`useTranslation()`, `Trans`) |

### 3.2 Strings hardcoded encontradas

| Arquivo | Strings |
|---|---|
| `App.tsx` | `Algo deu errado` |
| `SettingsPage.tsx` | `qBittorrent Local`, `http://localhost:8080`, `admin`, `URL` |
| `GeneralTab.tsx` | `ENV`, `Go:`, `Jackett`, `http://localhost:9117` |
| `SearchPage.tsx` | `4K (2160p)`, `1080p`, `720p`, `480p`, `H.265 / HEVC`, `H.264`, `AV1` |
| `QualityBadges.tsx` | `HDR`, `Dolby Vision`, `Dublado`, `Extended/Director's Cut` |
| `FileProgressBar.tsx` | `Na fila…`, `Cancelar` |
| `Sheet.tsx` | `Fechar` (aria-label) |
| `Toast.tsx` | `Fechar` (aria-label) |
| `NotificationsBell.tsx` | `Copiar magnet` (title) |
| `NavHeader.tsx` | `Jack UI` (title) |
| `TranscodeCapabilitiesCard.tsx` | `Hardware Transcoding`, `FFmpeg:`, `NVIDIA`, `VAAPI`, `QSV`, `CPU-only` |

### 3.3 Aria-labels sem tradução

| Arquivo | Label hardcoded |
|---|---|
| `Sheet.tsx` | `Fechar` |
| `Toast.tsx` | `Fechar` |
| `NotificationsBell.tsx` | `Copiar magnet` |

---

## 4. Modais, Sheets e Diálogos — auditoria

### 4.1 Sheet.tsx (primitive central)

| Atributo | Status |
|---|---|
| `role="dialog"` | ✅ |
| `aria-modal="true"` | ✅ |
| `aria-labelledby` | ❌ |
| Focus trap | ❌ |
| Focus restore ao fechar | ❌ |
| Foco inicial ao abrir | ❌ |
| Escape fecha | ✅ (onKeyDown no backdrop) |
| Bloqueia bg | Parcial (sem focus trap real) |
| Título | `<h2>` opcional, não ligado a ARIA |
| Fechamento | close button, backdrop click, Escape |
| Scroll lock | ✅ |

### 4.2 ConfirmDialog.tsx

Usa Sheet — herda os mesmos problemas.

### 4.3 TrailerModal.tsx

| Atributo | Status |
|---|---|
| `role="dialog"` | ✅ |
| `aria-modal` | ❌ |
| `aria-labelledby` | ❌ |
| Focus trap | ❌ |
| Escape fecha | ✅ (global keydown + duplicado) |
| Bloqueio bg | Visual (overlay), sem focus trap |

### 4.4 PlayerModal.tsx

Complexo, usa Sheet para diálogos aninhados. Focus handling manual/incompleto.
Nested overlays precisam de contenção de foco.

### 4.5 Toast.tsx

`aria-live` region — padrão correto para toasts. Não precisa de dialog role.

### 4.6 Outros modais que usam Sheet

AddTorrentModal, AdminResetPasswordModal, AdminUserSessionsModal,
DownloadInspectModal, DownloadModal, FilePreviewModal, LocalPromoteModal,
MoveFolderModal, PlaylistPickerModal, PromoteModal, RenameModal,
ReclassifyFolderModal, TorrentContentsModal, DuplicatesModal.

Todos herdam as limitações de acessibilidade do Sheet.

---

## 5. Priorização para UX-1

### 5.1 Correções imediatas (PR 1)

1. **Sheet** — adicionar focus trap, restore, `aria-labelledby`, foco inicial.
2. **ResultCard** — eliminar interativos aninhados (card não-container de botões).
3. **Icon buttons sem label** — adicionar `aria-label` i18nizado.
4. **Testes de acessibilidade** para os itens acima.

### 5.2 Próximos (PR 2+)

5. **AsyncState** primitive (loading/error/empty padronizados).
6. **Estado de erro** nas páginas sem cobertura (WatchlistPage, PlaylistDetailPage,
   StatsPage, LibraryPage).
7. **Eliminar silent catches** transformando em estados visíveis.
8. **Retry padronizado** em todas as páginas.
9. **i18n das strings hardcoded** + aria-labels.
