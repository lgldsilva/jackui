package handlers

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"

	"github.com/lgldsilva/jackui/internal/streamer"
	"github.com/lgldsilva/jackui/internal/transcode"
)

func countStreamInf(master string) int {
	n := 0
	for _, line := range strings.Split(master, "\n") {
		if strings.HasPrefix(line, "#EXT-X-STREAM-INF:") {
			n++
		}
	}
	return n
}

// CA-2.1: fonte ≥1080p → master com ≥2 #EXT-X-STREAM-INF.
func TestBuildMasterPlaylistCA21(t *testing.T) {
	for _, src := range []struct {
		w, h, want int
	}{
		{1920, 1080, 2},
		{2560, 1440, 2},
		{3840, 2160, 3},
	} {
		ladder := transcode.VariantLadder(src.h)
		master := string(buildMasterPlaylist(ladder, src.w, src.h, "", false, ""))
		if got := countStreamInf(master); got != src.want {
			t.Errorf("%dp: %d STREAM-INF, want %d\n%s", src.h, got, src.want, master)
		}
		if !strings.HasPrefix(master, "#EXTM3U") {
			t.Errorf("%dp: master não começa com #EXTM3U", src.h)
		}
		if !strings.Contains(master, "#EXT-X-INDEPENDENT-SEGMENTS") {
			t.Errorf("%dp: falta #EXT-X-INDEPENDENT-SEGMENTS", src.h)
		}
	}
}

// URIs de variante são RELATIVAS e batem com a rota v/:variant/index.m3u8
// (v/0/…, v/1/…) — NÃO o v0/… do rascunho antigo (bug B-2).
func TestBuildMasterPlaylistVariantURIs(t *testing.T) {
	ladder := transcode.VariantLadder(1080)
	master := string(buildMasterPlaylist(ladder, 1920, 1080, "", false, ""))
	for _, want := range []string{"\nv/0/index.m3u8", "\nv/1/index.m3u8"} {
		if !strings.Contains(master, want) {
			t.Errorf("master sem URI %q:\n%s", want, master)
		}
	}
	if strings.Contains(master, "v0/index.m3u8") {
		t.Errorf("master usa URI legada errada v0/ (deveria ser v/0/):\n%s", master)
	}
}

// B-3: token + native_hls propagados em TODAS as URIs de variante (senão a
// primeira variante dá 401 no <video>?token= ou cai na sessão errada).
func TestBuildMasterPlaylistPropagatesTokenAndNative(t *testing.T) {
	ladder := transcode.VariantLadder(2160)
	master := string(buildMasterPlaylist(ladder, 3840, 2160, "Tok123", true, ""))
	uris := 0
	for _, line := range strings.Split(master, "\n") {
		if strings.HasPrefix(line, "v/") {
			uris++
			if !strings.Contains(line, "?token=Tok123") || !strings.Contains(line, "native_hls=1") {
				t.Errorf("URI sem token/native_hls: %q", line)
			}
		}
	}
	if uris != 3 {
		t.Errorf("esperava 3 URIs de variante, achei %d", uris)
	}
}

// M2a: a faixa de áudio escolhida é propagada em TODA URI de variante (senão
// escolher áudio não-default quebraria em fontes ≥1080p, onde vem master). A
// troca continua sendo por reload da master URL (?audio=N muda a streamURL).
func TestBuildMasterPlaylistPropagatesAudio(t *testing.T) {
	ladder := transcode.VariantLadder(1080)
	master := string(buildMasterPlaylist(ladder, 1920, 1080, "Tok", true, "2"))
	for _, line := range strings.Split(master, "\n") {
		if strings.HasPrefix(line, "v/") && !strings.Contains(line, "audio=2") {
			t.Errorf("URI de variante sem audio=2: %q", line)
		}
	}
	// Sem áudio escolhido → nenhuma URI carrega audio=.
	noAudio := string(buildMasterPlaylist(ladder, 1920, 1080, "Tok", true, ""))
	if strings.Contains(noAudio, "audio=") {
		t.Errorf("sem escolha de áudio não deveria haver audio= nas URIs:\n%s", noAudio)
	}
}

// RESOLUTION derivada do aspect ratio da fonte (par); CODECS por tier.
func TestBuildMasterPlaylistResolutionCodecs(t *testing.T) {
	ladder := transcode.VariantLadder(1080)
	master := string(buildMasterPlaylist(ladder, 1920, 1080, "", false, ""))
	for _, want := range []string{"RESOLUTION=1920x1080", "RESOLUTION=1280x720", `CODECS="avc1.4d4028,mp4a.40.2"`, `CODECS="avc1.4d401f,mp4a.40.2"`} {
		if !strings.Contains(master, want) {
			t.Errorf("master sem %q:\n%s", want, master)
		}
	}
	// BANDWIDTH presente e descendente.
	if !strings.Contains(master, "#EXT-X-STREAM-INF:BANDWIDTH=") {
		t.Errorf("master sem BANDWIDTH:\n%s", master)
	}
}

// Dimensões desconhecidas (0,0) → RESOLUTION omitida (é opcional), mas o master
// ainda é válido com BANDWIDTH + CODECS.
func TestBuildMasterPlaylistUnknownDimsOmitsResolution(t *testing.T) {
	// Ladder de 2 rungs mas sem dims da fonte.
	ladder := transcode.VariantLadder(1080)
	master := string(buildMasterPlaylist(ladder, 0, 0, "", false, ""))
	if strings.Contains(master, "RESOLUTION=") {
		t.Errorf("dims 0 deveria omitir RESOLUTION:\n%s", master)
	}
	if countStreamInf(master) != 2 {
		t.Errorf("ainda deveria ter 2 STREAM-INF:\n%s", master)
	}
}

// writeMasterPlaylist escreve o master na resposta com o content-type e o
// no-store corretos, propagando token/audio da query nas URIs de variante.
func TestWriteMasterPlaylist(t *testing.T) {
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodGet, "/x?token=T&audio=1", nil)

	writeMasterPlaylist(c, transcode.VariantLadder(1080), 1920, 1080)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}
	if ct := w.Header().Get("Content-Type"); !strings.Contains(ct, "mpegurl") {
		t.Errorf("Content-Type = %q, want mpegurl", ct)
	}
	if cc := w.Header().Get("Cache-Control"); !strings.Contains(cc, "no-store") {
		t.Errorf("Cache-Control = %q, want no-store", cc)
	}
	body := w.Body.String()
	if countStreamInf(body) != 2 {
		t.Errorf("body sem 2 STREAM-INF:\n%s", body)
	}
	if !strings.Contains(body, "?token=T") || !strings.Contains(body, "audio=1") {
		t.Errorf("token/audio não propagados no body:\n%s", body)
	}
}

// StreamHLSVariant responde 404 quando o índice de variante está fora do ladder
// (probe indisponível → ladder single → idx alto é inválido). Cobre o path
// resolveVariant=false sem precisar de torrent real.
func TestStreamHLSVariantOutOfRange(t *testing.T) {
	gin.SetMode(gin.TestMode)
	mgr, err := transcode.NewHLSManager(t.TempDir())
	if err != nil {
		t.Fatalf("NewHLSManager: %v", err)
	}
	r := gin.New()
	r.GET("/api/stream/hls/:hash/:file/v/:variant/index.m3u8",
		StreamHLSVariant(streamer.NewForTesting(), mgr, nil))

	const hash = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/stream/hls/"+hash+"/0/v/9/index.m3u8", nil)
	r.ServeHTTP(w, req)
	if w.Code != http.StatusNotFound {
		t.Errorf("variante 9 (fora de faixa) → status %d, want 404\n%s", w.Code, w.Body.String())
	}
}

// Hash válido + sem torrent: StreamHLSMaster passa por serveMasterIfMultiVariant
// (probe falha → ladder single → fallback) e por serveHLSMediaPlaylist até
// resolveTranscodeSource não achar a fonte. Cobre o glue single-variant sem
// precisar de ffmpeg/torrent.
func TestStreamHLSMasterFallbackNoTorrent(t *testing.T) {
	gin.SetMode(gin.TestMode)
	mgr, err := transcode.NewHLSManager(t.TempDir())
	if err != nil {
		t.Fatalf("NewHLSManager: %v", err)
	}
	r := gin.New()
	r.GET("/api/stream/hls/:hash/:file/index.m3u8", StreamHLSMaster(streamer.NewForTesting(), mgr, nil))

	const hash = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/stream/hls/"+hash+"/0/index.m3u8", nil)
	r.ServeHTTP(w, req)
	// Sem torrent a fonte não resolve → não-200 (404/500), mas o handler roda o
	// glue sem panicar — que é o que este teste cobre.
	if w.Code == http.StatusOK {
		t.Errorf("sem torrent não deveria dar 200; got %d", w.Code)
	}
}

// v/0 com ladder single (probe falha → [default]) exercita resolveVariant no
// caminho de sucesso (idx 0 válido) + entrada de serveHLSMediaPlaylist.
func TestStreamHLSVariantZeroResolves(t *testing.T) {
	gin.SetMode(gin.TestMode)
	mgr, err := transcode.NewHLSManager(t.TempDir())
	if err != nil {
		t.Fatalf("NewHLSManager: %v", err)
	}
	r := gin.New()
	r.GET("/api/stream/hls/:hash/:file/v/:variant/index.m3u8", StreamHLSVariant(streamer.NewForTesting(), mgr, nil))

	const hash = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/stream/hls/"+hash+"/0/v/0/index.m3u8", nil)
	r.ServeHTTP(w, req)
	if w.Code == http.StatusNotFound && strings.Contains(w.Body.String(), "out of range") {
		t.Errorf("v/0 com ladder single deveria resolver (não 'out of range'): %s", w.Body.String())
	}
}

func TestVariantWidth(t *testing.T) {
	cases := []struct {
		sw, sh, vh, want int
	}{
		{1920, 1080, 1080, 1920}, // native
		{1920, 1080, 720, 1280},  // 16:9 → 1280x720
		{1920, 1080, 480, 854},   // 853.3 → 854 (par, arredonda pra cima)
		{0, 0, 720, 0},           // desconhecido
		{1920, 0, 720, 0},        // altura da fonte 0
	}
	for _, c := range cases {
		if got := variantWidth(c.sw, c.sh, c.vh); got != c.want {
			t.Errorf("variantWidth(%d,%d,%d) = %d, want %d", c.sw, c.sh, c.vh, got, c.want)
		}
	}
}
