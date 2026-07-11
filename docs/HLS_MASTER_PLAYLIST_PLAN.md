# Plano de Implementação — M2: HLS Master Playlist Phase 2

> **Status**: ⬜ aberto (próximo milestone do `REQUIREMENTS.md`)
> **Criado**: 2026-07-10 · deepwork session
> **Revisado**: 2026-07-10 · review de impacto (C1–C10) — **não executar a versão pré-review**
> **Arquitetura**: Option B — master sintético + sessões lazy (trancada @oracle)
> **Áudio**: **B1** — renditions audio-only separadas (RFC 8216 / Apple Authoring); B3 (URI AUDIO = AV remux) **rejeitada**
> **Entrega** (decidido 2026-07-11): **stack de 2 PRs separados**. **PR-A = M2a** (multi-res: master `STREAM-INF` lazy; áudio default muxado; troca de faixa continua `?audio=` reload) → entrega CA-2.1, shippable sozinho. **PR-B = M2b** (renditions B1: `TYPE=AUDIO` audio-only + `TYPE=SUBTITLES` WebVTT + `hls.audioTrack` seamless + fallback Safari) sobre a `main` já com M2a → entrega CA-2.2. ⚠️ PR-B é o maior blast radius (L8 🔴): validação E2E/Safari obrigatória antes do merge.
> **Validação**: software local (Apple Silicon) + GPU via Docker (`oracle-desktop`, GTX 1070)
> **Refs**: `docs/REQUIREMENTS.md` §M2 · `docs/design-decisions.md` §Playback · README roadmap · RFC 8216 §3.5/§4.3.4/§8.6 · [Apple HLS Authoring](https://developer.apple.com/documentation/http-live-streaming/hls-authoring-specification-for-apple-devices)

---

## 1. Contexto & estado atual

O endpoint `/api/stream/hls/:hash/:file/index.m3u8` hoje serve uma **media playlist
single-variant** — NÃO um master com `#EXT-X-STREAM-INF`. (O handler chama-se
`StreamHLSMaster`, `handlers/hls.go:109`, por legado; o body é media playlist. O
segmento é `StreamHLSSegment`, `:204`.) Há dois modos:

- **VOD**: `buildVODPlaylist` sintetiza uma playlist fina (todos os segmentos listados
  + `EXT-X-ENDLIST`, dá seekbar ao Safari).
- **EVENT**: `index.m3u8` do próprio ffmpeg.

Um processo ffmpeg por sessão, com `-map 0:v:0 -map 0:a:0?`, escalado a `≤1080p`
(`videoScaleFilter` fixo em `min(1080,ih)` — VAAPI/QSV/CPU).
Faixas de áudio são **por-sessão AV remux**: o cliente passa `?audio=N`, que keyeia
outra sessão ffmpeg (`hlsSessionKey` em `handlers/hls.go`). Legendas texto saem via
`/api/stream/subtrack/...` (`ExtractSubtitle` → WebVTT); PGS é burn-in.

M2 transforma isso num **master playlist multi-variante** com `#EXT-X-STREAM-INF` +
(em M2b) `#EXT-X-MEDIA TYPE=AUDIO/SUBTITLES` **protocol-correct**, com geração de
variantes **on-demand** (lazy: master = probe-only; ffmpeg só no pedido da variant).

### Critérios de aceitação

| CA | Descrição | Como validar | Escopo |
|---|---|---|---|
| **CA-2.1** | Master com ≥2 variantes p/ fonte **≥1080p** | Parser conta `#EXT-X-STREAM-INF` | **M2a** |
| **CA-2.2** | `#EXT-X-MEDIA TYPE=AUDIO` e `TYPE=SUBTITLES` presentes **quando a fonte tiver tracks aplicáveis** (N≥1 text-sub; N≥1 audio — multi-audio usa URI audio-only) | Parser + fixture multi-stream | **M2b** |
| **CA-2.3** | Teste E2E com MKV multi-stream valida o manifest | Fixture sintético 1080p + pipeline real | M2a (STREAM-INF); M2b (MEDIA) |

> **Nota REQUIREMENTS**: a prosa M2 diz "fonte **>**1080p"; o CA-2.1 diz "**≥**1080p".
> Este plano segue o **CA** (≥1080 → 2 tiers). "Cliente em baixa banda" (prosa) **não**
> tem sinal no cliente hoje — coberto indiretamente pelo tier 720p no ladder, sem
> detecção de banda server-side.

### Restrições inquebráveis (de `design-decisions.md`)

- `-hls_playlist_type event` (NÃO `vod` no ffmpeg; a playlist VOD é sintetizada em Go).
- Sem `append_list`.
- `-muxdelay 0 -muxpreload 0` (fix do stall do Safari em t=0 — o muxer MPEG-TS adiciona
  ~1.4s de offset inicial sem isso). Guard: `TestEncodeSpecZeroesPTSBothModes`.
- H.264 Level 5.2 para 4K (`-level:v 5.2` já em `encodeSpec`).
- `gpusem.go` conta **cada sessão** (variant e, em M2b, audio-only) — ladder declarativo ≠ admissão.
- Fallback CPU-decode em `CUDA_ERROR_OUT_OF_MEMORY` (preservado — é por-sessão).
- "Never fix a VOD bug by switching to live" (`design-decisions.md`).

---

## 2. Decisão de arquitetura (trancada pelo @oracle + review)

**Option B — Master sintético em Go + sessões lazy por variante (e por áudio em M2b).**

Cada variant de vídeo é uma instância independente do `HLSSession` existente, com
`effKey` próprio → `Dir` próprio → segmentos isolados. Seek-restart
(`RestartAt`/`EnsureSegment`) funciona **idêntico** por sessão.

### Por que Option B (não Option A — ffmpeg `-var_stream_map` nativo)

| Critério | Option A (ffmpeg nativo) | Option B (master sintético + lazy) |
|---|---|---|
| **Blast radius no seek-restart** | Reescreve o subsistema mais validado | Zero — cada variant = session igual à atual |
| **Isolamento de falha** | 1 processo = ponto único de falha p/ toda a ladder | Crash de 1 variant não afeta as outras |
| **Uso de recursos** | Transcode a ladder inteira desde o início | Só transcode o que o cliente pediu |
| **Padrão já provado** | Novo | Dimensões de session key (`?audio=N`, VOD/EVENT) |
| **Complexidade do master** | ffmpeg gera | Go sintetiza (builder + probe) |

### Áudio: B1 (correto) vs B3 (rejeitado)

| | **B3 (plano original — REJEITADO)** | **B1 (adotado em M2b)** |
|---|---|---|
| URI `TYPE=AUDIO` | Media playlist **AV** (`?audio=N` remux vídeo+áudio) | Media playlist **audio-only** (`-vn`, `audioArgs` / `HLSStartOpts.AudioOnly`) |
| Spec | Viola RFC 8216 §8.6 e Apple Authoring (alternate languages = separate audio streams) | Conforme |
| `hls.audioTrack` seamless | **Não confiável** (double-decode / reject) | ✅ hls.js MSE |
| Safari nativo | Quebra ou ignora | UI + `audioTracks` nativo / painel |
| Reuso código | Session key atual | `audioArgs` já existe em `encodespec.go` |

**M2a (MVP multi-res)** pode shippar **sem** multi-URI AUDIO: variants AV com áudio
default muxado; troca de faixa continua `?audio=` + URL reload (comportamento atual).
CA-2.2 fica para **M2b**.

### Decisões detalhadas

| Dimensão | Decisão | Justificativa |
|---|---|---|
| Multi-variante | Master sintético + sessões lazy | Zero blast radius no seek-restart |
| Master handler | **Probe-only** — **não** chama `GetOrStart` | Lazy real; ffmpeg só em variant/audio/seg |
| Seek-restart | Independente por sessão | Só 1 variant de vídeo ativa por player; ABR cold-start aceito (ver §8) |
| Áudio M2a | Default muxado no variant; `?audio=` legacy | Ship multi-res sem reescrever áudio |
| Áudio M2b (B1) | `TYPE=AUDIO` → sessão **audio-only** por track | Protocolo + seamless hls.js |
| Legendas texto | Mini-playlist WebVTT (RFC §3.5) reusando `ExtractSubtitle` | Desacoplado do vídeo |
| Legendas bitmap (PGS) | Burn-in (sem rendition) | Sem OCR |
| gpuSem | Aquisição independente por sessão | Ladder ≠ admissão |
| ABR | Cold-start de variant aceito; **sem** prewarm no M2 | Tuning `abrEwma*` / prewarm = follow-up |
| EVENT (duration 0) | Fallback **single-variant** media playlist (comportamento atual) | Master multivariant exige duration p/ VOD synth |

---

## 3. Achados da investigação (impacto agregado)

### 3.1 Gap no probe de resolução — BLOQUEADOR

`ffprobeStream` / `classifyStreams` / `ProbeResult` em `streamer/probe.go` **não**
parseiam `width`/`height` — só `videoCodec`. Local reusa `ProbeLocal` →
`parseProbeOutput`, mas `localProbe` / `probeLocalFile` **descartam** height.
**L1**: parse em probe + consumir height no path local do master.

Invalidar ou versionar `probeCache` ao adicionar campos (senão deploys long-running
servem ladder single até restart).

### 3.2 Segment serving é flat — NÃO precisa mudar

`ServeSegment` / `WaitForSegment` / `highestSeg` operam em `sess.Dir`. Session key com
dimensão variant (e `a{audio}` / `ao{audio}` audio-only) → `Dir` distinto → isolamento.
**Zero mudança no path de serving.** ✅

### 3.3 Session key — dimensões

```
Vídeo:  {hash}-{file}-v{variant}[-a{defaultAudio}]   → EffectiveKey → -vod|-evt
Áudio M2b: {hash}-{file}-ao{track}                     → EffectiveKey → -vod|-evt
```

`HLSStartOpts` ganha `Variant` (height/bitrate ou index). Audio-only reusa `AudioOnly` +
`AudioTrack`. **Não** keyar audio-only com o mesmo sufixo `-aN` de AV remux legacy
(colisões de Dir/encodeSpec) — usar prefixo distinto (`-ao{N}`).

### 3.4 Frontend — maior risco (M2b)

Hoje: `setTranscodeAudio` → muda `streamURL` → destroy hls.js → **posição perdida**.

M2b com B1:
- hls.js: `AUDIO_TRACKS_UPDATED` → `hls.audioTrack = idx` (sem reload)
- Safari nativo: **não** tem a mesma API; manter painel + fallback
- Evitar dual-enable: se `TYPE=SUBTITLES` no master, **não** anexar o mesmo track via
  `<track>` React (legendas duplicadas)

M2a: frontend quase intocado no áudio; só passa a carregar master multivariant (sem
`?audio` no master URL quando multi-res).

### 3.5 Subtitle renditions (M2b)

`ExtractSubtitle` → VTT completo. Mini-playlist **não** é o VTT cru como body do
`.m3u8`: RFC 8216 §3.5 exige media playlist com `EXTINF` + segmentos WebVTT (header +
cues; preferível `X-TIMESTAMP-MAP` p/ sync com MPEG-TS).

Forma mínima aceitável: 1 segmento = URI do `/subtrack/...` com `EXTINF:duration` +
`#EXT-X-ENDLIST` + `PLAYLIST-TYPE:VOD`. Validar Safari real.

### 3.6 Local path é paralelo

`local_hls.go` / `LocalHLSMaster` (`local_hls.go:21`) eager-start hoje. Mesmo split master vs variant.
Builder de master **compartilhado** (`transcode` ou `httpshared`) — **não** duplicar
lógica em `handlers/hls_master.go` + `local_hls_master.go`.

### 3.7 Roteamento gin

Prefixo estático `v/` e `sub/` vs `:seg` — httprouter do gin: estático tem prioridade.
Ok se paths forem disjuntos.

### 3.8 Naming / breaking body

`GET .../index.m3u8` passa a devolver **master** (quando multi-variant ou M2b com
MEDIA). Clients que esperavam media playlist (VLC URL, scripts) podem quebrar.
Mitigação opcional: `?legacy=1` ou single-variant continua media-only quando ladder=1
e sem multi-audio renditions.

### 3.9 Scale filter multi-backend

`videoScaleFilter` em `pipeline.go` / uso em `encodespec.go` fixa `min(1080,ih)` em
CPU, VAAPI e QSV. Variantes 720/480 exigem `min(variantH, ih)` nos **três** backends.

### 3.10 Token / native_hls em URIs do master

Todas as URIs relativas do master (variants, audio, subs) devem carregar `token` e
`native_hls` (e `user` no local se aplicável). Helper único tipo `mediaSegQuery` —
senão 401 no primeiro variant sob auth.

---

## 4. Mapa de impacto (touchpoints por camada)

| Camada | Arquivos | Touchpoints | Risco |
|---|---|---|---|
| **L1 Probe** | `streamer/probe.go`, local consumers | width/height, cache invalidation | 🟡 Médio |
| **L2 Ladder+spec** | `encodespec.go`, `pipeline.go` (`videoScaleFilter`), novo `variants.go` | height/bitrate por variant; 3 backends | 🟡→🔴 |
| **L3 Session key** | `handlers/hls.go`, `local/*`, `HLSStartOpts` | `-vN`, `-aoN`, Variant field | 🟢 Baixo |
| **L4 Routing** | `routes.go`, hls handlers, **auth `isMediaPath`** | `v/:id/...`, `a/:track/...`, `sub/:track/...` | 🟡 Médio |
| **L5 Master builder** | **um** `buildMasterPlaylist` compartilhado | probe-only master; token prop; CODECS table | 🟡 Médio |
| **L6 Audio-only (M2b)** | session start com `AudioOnly` | media playlist áudio | 🟡 Médio |
| **L7 Subs (M2b)** | mini-playlist WebVTT | RFC packaging | 🟡 Médio |
| **L8 Frontend** | `stream.ts` (ou `api/hls.ts`), `VideoPlayerElement`, `EmbeddedTracksPanel`, `mediaUrls` | M2a leve; M2b alto | 🔴 Alto (M2b) |
| **L9 Fixture+gates** | `testdata/`, CI | fixture 1080p multi-stream | 🟡 Médio |

### Segment serving — ZERO mudança (confirmado)

- `httpshared/hls.go` (`ServeSegment`, `EnsureVODSegment`)
- `transcode/hls_session.go` (`WaitForSegment`, `highestSeg`, `EnsureSegment`, `RestartAt`)
- `-hls_segment_filename` já usa `e.dir`

### Libs

| Área | Decisão |
|---|---|
| Master m3u8 em Go | stdlib only — **sem** lib m3u8 |
| hls.js | já `^1.6.16` — sem upgrade obrigatório |
| Testes | sem testify (padrão do repo) |
| mediastreamvalidator | opcional homelab; não bloqueia CI |

---

## 5. Roteamento de URLs (URIs alinhadas às rotas)

### Torrent path

```
HOJE:
  GET /api/stream/hls/:hash/:file/index.m3u8   → media playlist single-variant
  GET /api/stream/hls/:hash/:file/:seg         → segmento

M2:
  GET .../index.m3u8                    → MASTER (probe-only; sem GetOrStart)
  GET .../v/:variant/index.m3u8         → media playlist da variant (GetOrStart)
  GET .../v/:variant/:seg               → segmento da variant
  GET .../a/:track/index.m3u8           → media playlist AUDIO-ONLY (M2b)
  GET .../a/:track/:seg                 → segmento áudio (M2b)
  GET .../sub/:track/index.m3u8         → mini-playlist WebVTT (M2b)
  GET .../:seg                          → LEGADO single-variant (backward compat)
```

URIs **relativas** no master (base = `.../file/index.m3u8` → dir `.../file/`):

```
v/0/index.m3u8?token=...&native_hls=1
v/1/index.m3u8?token=...
a/1/index.m3u8?token=...          # M2b — track index absoluto do probe
sub/3/index.m3u8?token=...        # M2b
```

> **Correção C2**: o rascunho antigo usava `v0/index.m3u8` com rota `v/:variant/` —
> resolvia para path errado. Sempre `v/{n}/...`.

### Local path (query-driven)

```
GET /api/local/hls/index.m3u8?mount=M&path=P                    → MASTER (probe-only)
GET /api/local/hls/index.m3u8?mount=M&path=P&variant=N          → media variant
GET /api/local/hls/seg?mount=M&path=P&seg=S&variant=N           → seg variant
GET /api/local/hls/index.m3u8?mount=M&path=P&audio_only=1&audio=T → audio-only (M2b)
GET /api/local/hls/sub?mount=M&path=P&track=T                   → sub playlist (M2b)
```

### Exemplo de master (M2b completo — fonte 1080p, 2 áudios, 1 text-sub)

```
#EXTM3U
#EXT-X-VERSION:6
#EXT-X-INDEPENDENT-SEGMENTS

#EXT-X-MEDIA:TYPE=AUDIO,GROUP-ID="aud",NAME="Português",DEFAULT=YES,AUTOSELECT=YES,LANGUAGE="por",URI="a/1/index.m3u8?token=T"
#EXT-X-MEDIA:TYPE=AUDIO,GROUP-ID="aud",NAME="English",AUTOSELECT=YES,LANGUAGE="eng",URI="a/2/index.m3u8?token=T"
#EXT-X-MEDIA:TYPE=SUBTITLES,GROUP-ID="sub",NAME="English",DEFAULT=YES,AUTOSELECT=YES,LANGUAGE="eng",URI="sub/3/index.m3u8?token=T"

#EXT-X-STREAM-INF:BANDWIDTH=5500000,RESOLUTION=1920x1080,CODECS="avc1.4d4028,mp4a.40.2",AUDIO="aud",SUBTITLES="sub"
v/0/index.m3u8?token=T
#EXT-X-STREAM-INF:BANDWIDTH=3000000,RESOLUTION=1280x720,CODECS="avc1.4d401f,mp4a.40.2",AUDIO="aud",SUBTITLES="sub"
v/1/index.m3u8?token=T
```

**Variants de vídeo em M2b**: preferir **vídeo + áudio default omitido no map de
alternates** — com `AUDIO="aud"`, o cliente usa as renditions `a/*` (Apple: separate
audio). Implementação: variant session com vídeo (+ opcional default audio se player
sem suporte a alternates — testar; se necessário `DEFAULT` audio sem URI no MEDIA
só no single-audio case).

**CODECS/BANDWIDTH/RESOLUTION**: tabela por tier em `variants.go` (não strings soltas
no handler). Profile Main + level por height.

### Ladder de variantes (determinístico, hardcoded)

| Altura da fonte | Variantes | Justificativa |
|---|---|---|
| `≥2160` (4K) | 1080p/5M, 720p/2.8M, 480p/1.4M | 4K não toca H.264 direto no browser de forma confiável |
| `≥1080` | 1080p/5M, 720p/2.8M | CA-2.1 ≥2; 720p cobre banda baixa |
| `<1080` | `{srcHeight}` (single) | Não upscale; body pode continuar media-only se 1 variant e sem M2b multi-rendition |

---

## 6. Plano de execução (deepwork) — M2a depois M2b

### Worktree

```
git worktree add -b omos/feat-hls-master-phase2 .slim/worktrees/hls-master-p2 main
```

Branch da `main` atualizada. Deepwork file: `.slim/deepwork/hls-master-p2.md`.

### M2a — Multi-resolução (CA-2.1 + CA-2.3 parcial)

#### Fase 0 — Setup & baseline
- Worktree + deepwork file
- Snapshot verde: `go build ./...`, `go test ./internal/transcode/... ./internal/handlers/...`,
  `npm ci && npm test`
- **Gate**: baseline verde

#### Fase 1 (L1) — Probe de resolução
- `ffprobeStream` +Width/Height; `classifyStreams`; `ProbeResult` +VideoWidth/Height
- Consumir height no path local do master; versionar/invalidar `probeCache`
- **Gate**: parse de JSON ffprobe; height>0 p/ fixture 1080p

#### Fase 2 (L2) — Ladder + encodeSpec por variante
- `transcode/variants.go`: `Variant{Height,Bitrate,Bandwidth,Codecs,Resolution}`
- `videoScaleFilter(encoder, maxH)` nos 3 backends; `encodeSpec` + maxrate/bufsize
- **Gate**: ladder tiers; args scale por variant; `TestEncodeSpecZeroesPTSBothModes` verde

#### Fase 3 (L3) — Session key + Variant
- `hlsSessionKey(..., variant)`; local + `HLSStartOpts.Variant`
- **Gate**: matrix key variant×vod/evt

#### Fase 4 (L4) — Routing variant
- Rotas `v/:variant/index.m3u8`, `v/:variant/:seg`; legado `:seg`
- `isMediaPath` atualizado se necessário
- **Gate**: rotas resolvem; legado ok

#### Fase 5 (L5) — Master builder (M2a)
- `buildMasterPlaylist` **compartilhado** (preferência: `internal/transcode` ou `httpshared`)
- `StreamHLSMaster` / `LocalHLSMaster`:
  - **probe-only** → se ladder ≥2 (ou política single→media legado): serve master
  - **não** `GetOrStart` no master
- Variant handler: `GetOrStart` + `buildVODPlaylist` / EVENT existente
- Propagar `token` + `native_hls` em todas as URIs
- **Gate (CA-2.1)**: ≥2 `#EXT-X-STREAM-INF` p/ fonte ≥1080p

#### Fase 6a — Frontend mínimo M2a
- Master URL sem forçar `?audio` desnecessário; hls.js carrega master (ABR cold-start ok)
- **Não** migrar `hls.audioTrack` ainda
- Evitar engordar `stream.ts` (644 ln) — preferir `web/src/api/hls.ts` se crescer
- **Gate**: vitest + play manual multi-res (level switch stall aceito)

#### 🔴 Gate oracle — review **PR-A (M2a)** → merge na `main`

> M2a é shippable sozinho: master multi-res + play funcionando, CA-2.1 verde. Merge, e
> só então abre-se o PR-B a partir da `main` atualizada.

### M2b — Renditions corretas (CA-2.2 + seamless áudio) — **PR-B** (após M2a na main)

#### Fase 6b — Audio-only sessions
- Rotas `a/:track/...`; key `-ao{track}`; `AudioOnly` + map track
- Master emite `TYPE=AUDIO` com URI **audio-only** (nunca AV)
- **Gate**: media playlist áudio sem stream de vídeo (ffprobe nos segs)

#### Fase 7 — Subtitle mini-playlist
- `sub/:track/index.m3u8` conforme RFC §3.5; PGS sem rendition
- UI: não duplicar com `<track>` se HLS SUBTITLES ativo
- **Gate (CA-2.2)**: tags presentes no fixture multi-stream

#### Fase 8 — Frontend M2b
- hls.js: `audioTrack` / listeners; Safari: fallback painel
- Estado claro: selected rendition ≠ `transcodeAudio` URL remux (renomear se preciso)
- **Gate**: troca áudio sem reload no Chrome; Safari path documentado

#### Fase 9 — E2E + GPU + gates
- Fixture `multistream.mkv` **1920x1080**, 2 áudios AAC, 1 text sub:
  ```
  ffmpeg -f lavfi -i testsrc=duration=24:size=1920x1080:rate=30 \
         -f lavfi -i sine=f=440:duration=24 \
         -f lavfi -i sine=f=880:duration=24 \
         ... -c:v libx264 -c:a aac -c:s mov_text \
         -metadata:s:a:0 language=eng -metadata:s:a:1 language=por \
         multistream.mkv
  ```
- E2E: master ≥2 STREAM-INF + EXT-X-MEDIA; fetch variant + 1 seg; fetch audio playlist
- GPU: `docker --context oracle-desktop` + `JACKUI_MAX_GPU_TRANSCODES=1` (força sw) + OOM path
- Manual Safari: t=0, seek, áudio, qualidade
- Gates: `go vet`, `go test`, `npm test`, Sonar zero new_violations, Trivy, govulncheck
- **Gate**: tudo verde → PR Gitea

#### Pós-M2: R3 + R4 (housekeeping)
- **R3**: fatiar `web/src/api/local.ts` / `stream.ts` se ainda >600
- **R4**: triar branches órfãs

---

## 7. Validação de GPU via Docker

### Local (Apple Silicon — software only)
- Unit/integration com libx264
- Fixture MKV + parser de manifest

### Homelab (oracle-desktop — GTX 1070, CUDA)
```bash
docker --context oracle-desktop build -f Dockerfile.nvidia -t jackui:hls-m2-test .
docker --context oracle-desktop run --gpus all \
  -e JACKUI_MAX_GPU_TRANSCODES=3 \
  -v ./testdata:/data \
  jackui:hls-m2-test
# Também validar com JACKUI_MAX_GPU_TRANSCODES=1 (sw decode spill)
```

Contextos:
- `oracle-desktop` — GTX 1070 — **validação GPU principal**
- `homeserver` — ARM, sem GPU — software-only

---

## 8. Riscos & mitigações

| # | Risco | Nível | Mitigação |
|---|---|---|---|
| 1 | **Áudio URI = AV (B3)** | 🔴 | **B1 only** em M2b; review bloqueia B3 |
| 2 | **URI `v0` vs rota `v/:id`** | 🔴 | URIs sempre `v/{n}/...` (§5) |
| 3 | **Token/native_hls em relatives** | 🔴 | Helper único; teste 401→200 |
| 4 | **Frontend audio (M2b)** | 🔴 | hls.js API + Safari fallback; não prometer seamless nativo |
| 5 | **ABR cold-start stall** | 🟠 | Aceito no M2; doc UX; follow-up prewarm/tuning |
| 6 | **GPU thrash (variant + audio + cap 3)** | 🟠 | gpuSem + reclaimIdle; testar cap=1 |
| 7 | **WebVTT packaging / dual tracks** | 🟠 | RFC mini-playlist; um path de enable na UI |
| 8 | **Safari t=0 regression** | 🟠 | muxdelay por session; guard existente |
| 9 | **probeCache stale sem height** | 🟡 | versionar/invalidar cache |
| 10 | **Players externos esperam media em index.m3u8** | 🟡 | single-variant = media body; ou `?legacy=1` |
| 11 | **EVENT duration 0** | 🟡 | fallback single media playlist |
| 12 | **Sonar complexity em handlers novos** | 🟡 | builder puro + handlers finos (pós-M1) |
| 13 | **CODECS errados** | 🟡 | tabela por tier em `variants.go` |
| 14 | **mediastreamvalidator local** | 🟢 | parser Go no CI; validator opcional no Mac |

---

## 9. Dependency graph

```
L1 probe height ──► L2 ladder/spec ──► L3 key ──► L4 routes ──► L5 master (M2a)
                                                                    │
                                                    M2a frontend ───┤
                                                                    ▼
                                                        ►►► PR-A (M2a) → merge main
                                                                    │
                              L6 audio-only ──► L7 subs ──► L8 frontend M2b ──► L9 E2E
                                                                    │
                                                                    ▼
                                                        ►►► PR-B (M2b) → merge main
```

L1 bloqueia L2. L5 bloqueia frontend M2a. PR-A (M2a) fecha e merge; PR-B (M2b) nasce da
`main` já com M2a — dois PRs sequenciais, cada um com seu gate/E2E.

---

## 10. Fora de escopo

- Backup/restore PostgreSQL.
- Observabilidade/alertas finas.
- OCR de PGS (burn-in permanece).
- ABR tuning fino hls.js (`abrEwma*`, prewarm de variant adjacente) — follow-up.
- Detecção server-side de “cliente em baixa banda”.
- R2 (CA-3.2 pass-rate CI).
- Option A (`-var_stream_map`).
- B3 (AUDIO URI = playlist AV).

---

## 11. Checklist pré-código (review gate)

- [x] Option B confirmada
- [x] B3 rejeitada; B1 para M2b
- [x] URIs `v/{n}/` alinhadas às rotas
- [x] Master = probe-only
- [x] Token/native_hls em relatives especificado
- [x] ABR cold-start documentado como aceito
- [x] CA-2.2 condicional a tracks da fonte
- [x] EVENT → single-variant fallback
- [x] `isMediaPath` / auth nas rotas novas
- [x] probeCache versionamento
- [x] Refs RFC 8216 + Apple Authoring
- [x] Decisão de ship: **stack de 2 PRs** — PR-A (M2a) shippable → PR-B (M2b) (decidido 2026-07-11)
- [x] Símbolos com casing correto (`StreamHLSMaster`/`StreamHLSSegment`/`LocalHLSMaster`/`LocalHLSSegment`)
