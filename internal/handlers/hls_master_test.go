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

func countMedia(master, typ string) int {
	n := 0
	for _, line := range strings.Split(master, "\n") {
		if strings.HasPrefix(line, "#EXT-X-MEDIA:TYPE="+typ) {
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
		master := string(buildMasterPlaylist(ladder, src.w, src.h, nil, nil, "", false))
		if got := countStreamInf(master); got != src.want {
			t.Errorf("%dp: %d STREAM-INF, want %d\n%s", src.h, got, src.want, master)
		}
		if !strings.HasPrefix(master, "#EXTM3U") {
			t.Errorf("%dp: master não começa com #EXTM3U", src.h)
		}
	}
}

// URIs de variante são RELATIVAS e batem com a rota v/:variant/index.m3u8.
func TestBuildMasterPlaylistVariantURIs(t *testing.T) {
	master := string(buildMasterPlaylist(transcode.VariantLadder(1080), 1920, 1080, nil, nil, "", false))
	for _, want := range []string{"\nv/0/index.m3u8", "\nv/1/index.m3u8"} {
		if !strings.Contains(master, want) {
			t.Errorf("master sem URI %q:\n%s", want, master)
		}
	}
	if strings.Contains(master, "v0/index.m3u8") {
		t.Errorf("master usa URI legada errada v0/:\n%s", master)
	}
}

// token + native_hls propagados nas URIs de variante.
func TestBuildMasterPlaylistPropagatesTokenAndNative(t *testing.T) {
	master := string(buildMasterPlaylist(transcode.VariantLadder(2160), 3840, 2160, nil, nil, "Tok123", true))
	for _, line := range strings.Split(master, "\n") {
		if strings.HasPrefix(line, "v/") {
			if !strings.Contains(line, "?token=Tok123") || !strings.Contains(line, "native_hls=1") {
				t.Errorf("URI de variante sem token/native_hls: %q", line)
			}
		}
	}
}

// CA-2.2 (áudio): fonte com ≥2 faixas → EXT-X-MEDIA TYPE=AUDIO; a 1ª é DEFAULT
// SEM URI (muxada no variant), as demais têm URI a/{idx}; STREAM-INF referencia
// AUDIO="aud".
func TestBuildMasterPlaylistAudioRenditions(t *testing.T) {
	audio := []streamer.Track{
		{Index: 1, Language: "por", Title: "Português", Default: true},
		{Index: 2, Language: "eng", Title: "English"},
	}
	master := string(buildMasterPlaylist(transcode.VariantLadder(1080), 1920, 1080, audio, nil, "Tok", true))
	if n := countMedia(master, "AUDIO"); n != 2 {
		t.Fatalf("esperava 2 EXT-X-MEDIA AUDIO, achei %d\n%s", n, master)
	}
	lines := strings.Split(master, "\n")
	var def, alt string
	for _, l := range lines {
		if strings.HasPrefix(l, "#EXT-X-MEDIA:TYPE=AUDIO") {
			if strings.Contains(l, "DEFAULT=YES") {
				def = l
			} else {
				alt = l
			}
		}
	}
	if def == "" || strings.Contains(def, "URI=") {
		t.Errorf("faixa default deveria existir SEM URI (muxada): %q", def)
	}
	if !strings.Contains(alt, `URI="a/2/index.m3u8`) || !strings.Contains(alt, "token=Tok") {
		t.Errorf("alternativa deveria ter URI a/2 com token: %q", alt)
	}
	for _, l := range lines {
		if strings.HasPrefix(l, "#EXT-X-STREAM-INF:") && !strings.Contains(l, `AUDIO="aud"`) {
			t.Errorf("STREAM-INF sem AUDIO=aud: %q", l)
		}
	}
}

// 1 faixa de áudio (ou nenhuma) → SEM renditions e SEM AUDIO=aud (M2a: áudio
// muxado no variant, nada a alternar).
func TestBuildMasterPlaylistSingleAudioNoRenditions(t *testing.T) {
	audio := []streamer.Track{{Index: 1, Language: "eng"}}
	master := string(buildMasterPlaylist(transcode.VariantLadder(1080), 1920, 1080, audio, nil, "", false))
	if countMedia(master, "AUDIO") != 0 {
		t.Errorf("1 faixa não deveria gerar EXT-X-MEDIA:\n%s", master)
	}
	if strings.Contains(master, "AUDIO=") {
		t.Errorf("sem renditions não deveria haver AUDIO=aud:\n%s", master)
	}
}

// RESOLUTION derivada do aspect ratio (par); CODECS por tier.
func TestBuildMasterPlaylistResolutionCodecs(t *testing.T) {
	master := string(buildMasterPlaylist(transcode.VariantLadder(1080), 1920, 1080, nil, nil, "", false))
	for _, want := range []string{"RESOLUTION=1920x1080", "RESOLUTION=1280x720", `CODECS="avc1.4d4028,mp4a.40.2"`, `CODECS="avc1.4d401f,mp4a.40.2"`} {
		if !strings.Contains(master, want) {
			t.Errorf("master sem %q:\n%s", want, master)
		}
	}
}

// Dims desconhecidas (0,0) → RESOLUTION omitida, master ainda válido.
func TestBuildMasterPlaylistUnknownDimsOmitsResolution(t *testing.T) {
	master := string(buildMasterPlaylist(transcode.VariantLadder(1080), 0, 0, nil, nil, "", false))
	if strings.Contains(master, "RESOLUTION=") {
		t.Errorf("dims 0 deveria omitir RESOLUTION:\n%s", master)
	}
	if countStreamInf(master) != 2 {
		t.Errorf("ainda deveria ter 2 STREAM-INF:\n%s", master)
	}
}

// writeMasterPlaylist: content-type + no-store + corpo com STREAM-INF.
func TestWriteMasterPlaylist(t *testing.T) {
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodGet, "/x?token=T", nil)

	writeMasterPlaylist(c, transcode.VariantLadder(1080), 1920, 1080, nil, nil)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}
	if ct := w.Header().Get("Content-Type"); !strings.Contains(ct, "mpegurl") {
		t.Errorf("Content-Type = %q, want mpegurl", ct)
	}
	if cc := w.Header().Get("Cache-Control"); !strings.Contains(cc, "no-store") {
		t.Errorf("Cache-Control = %q, want no-store", cc)
	}
	if countStreamInf(w.Body.String()) != 2 {
		t.Errorf("body sem 2 STREAM-INF:\n%s", w.Body.String())
	}
}

// StreamHLSVariant 404 quando o índice está fora do ladder.
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
		t.Errorf("variante 9 → status %d, want 404\n%s", w.Code, w.Body.String())
	}
}

func TestVariantWidth(t *testing.T) {
	cases := []struct {
		sw, sh, vh, want int
	}{
		{1920, 1080, 1080, 1920},
		{1920, 1080, 720, 1280},
		{1920, 1080, 480, 854},
		{0, 0, 720, 0},
		{1920, 0, 720, 0},
	}
	for _, c := range cases {
		if got := variantWidth(c.sw, c.sh, c.vh); got != c.want {
			t.Errorf("variantWidth(%d,%d,%d) = %d, want %d", c.sw, c.sh, c.vh, got, c.want)
		}
	}
}

// CA-2.2 (legenda): faixas de TEXTO → EXT-X-MEDIA TYPE=SUBTITLES com URI
// sub/{idx}; STREAM-INF referencia SUBTITLES="sub". PGS (Image) é filtrada.
func TestBuildMasterPlaylistSubtitleRenditions(t *testing.T) {
	subs := []streamer.Track{
		{Index: 3, Language: "eng", Codec: "subrip"},
		{Index: 4, Language: "spa", Codec: "hdmv_pgs_subtitle", Image: true}, // PGS → burn-in, sem rendition
	}
	master := string(buildMasterPlaylist(transcode.VariantLadder(1080), 1920, 1080, nil, textSubs(subs), "Tok", true))
	if n := countMedia(master, "SUBTITLES"); n != 1 {
		t.Fatalf("esperava 1 EXT-X-MEDIA SUBTITLES (PGS filtrada), achei %d\n%s", n, master)
	}
	if !strings.Contains(master, `URI="sub/3/index.m3u8`) {
		t.Errorf("master sem URI sub/3:\n%s", master)
	}
	if strings.Contains(master, "sub/4/") {
		t.Errorf("PGS (track 4) NÃO deveria virar rendition:\n%s", master)
	}
	for _, l := range strings.Split(master, "\n") {
		if strings.HasPrefix(l, "#EXT-X-STREAM-INF:") && !strings.Contains(l, `SUBTITLES="sub"`) {
			t.Errorf("STREAM-INF sem SUBTITLES=sub: %q", l)
		}
	}
}

func TestTextSubsFiltersImage(t *testing.T) {
	subs := []streamer.Track{
		{Index: 1, Codec: "subrip"},
		{Index: 2, Codec: "hdmv_pgs_subtitle", Image: true},
		{Index: 3, Codec: "ass"},
	}
	got := textSubs(subs)
	if len(got) != 2 || got[0].Index != 1 || got[1].Index != 3 {
		t.Errorf("textSubs = %+v, want tracks 1 e 3 (sem PGS)", got)
	}
}

// buildSubtitlePlaylist: VOD single-segment WebVTT apontando pro subtrack com token.
func TestBuildSubtitlePlaylist(t *testing.T) {
	pl := string(buildSubtitlePlaylist("abc123", 0, 3, 120.5, "Tok"))
	for _, want := range []string{
		"#EXTM3U", "#EXT-X-PLAYLIST-TYPE:VOD", "#EXT-X-ENDLIST",
		"#EXTINF:120.500,", "/api/stream/subtrack/abc123/0/3?token=Tok",
	} {
		if !strings.Contains(pl, want) {
			t.Errorf("sub playlist sem %q:\n%s", want, pl)
		}
	}
	// TARGETDURATION ≥ EXTINF (ceil).
	if !strings.Contains(pl, "#EXT-X-TARGETDURATION:121") {
		t.Errorf("TARGETDURATION deveria ser 121 (ceil 120.5):\n%s", pl)
	}
}

func TestAudioTrackName(t *testing.T) {
	cases := []struct {
		tr   streamer.Track
		i    int
		want string
	}{
		{streamer.Track{Title: "Comentarios"}, 0, "Comentarios"},
		{streamer.Track{Language: "eng"}, 1, "eng"},
		{streamer.Track{}, 2, "Audio 3"},
	}
	for _, c := range cases {
		if got := audioTrackName(c.tr, c.i); got != c.want {
			t.Errorf("audioTrackName(%+v,%d) = %q, want %q", c.tr, c.i, got, c.want)
		}
	}
}
