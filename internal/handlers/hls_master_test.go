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

// bmp is buildMasterPlaylist with renditions ON (M2b) — emitting
// EXT-X-MEDIA is still gated by the track count inside the builder.
func bmp(ladder []transcode.Variant, w, h int, audio, subs []streamer.Track, token string, native bool) []byte {
	return buildMasterPlaylist(masterOpts{
		ladder: ladder, srcW: w, srcH: h, audio: audio, subs: subs,
		token: token, nativeHLS: native, renditions: true,
	})
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
		master := string(bmp(ladder, src.w, src.h, nil, nil, "", false))
		if got := countStreamInf(master); got != src.want {
			t.Errorf("%dp: %d STREAM-INF, want %d\n%s", src.h, got, src.want, master)
		}
		if !strings.HasPrefix(master, "#EXTM3U") {
			t.Errorf("%dp: master does not start with #EXTM3U", src.h)
		}
	}
}

// Variant URIs are RELATIVE and match the v/:variant/index.m3u8 route.
func TestBuildMasterPlaylistVariantURIs(t *testing.T) {
	master := string(bmp(transcode.VariantLadder(1080), 1920, 1080, nil, nil, "", false))
	for _, want := range []string{"\nv/0/index.m3u8", "\nv/1/index.m3u8"} {
		if !strings.Contains(master, want) {
			t.Errorf("master without URI %q:\n%s", want, master)
		}
	}
	if strings.Contains(master, "v0/index.m3u8") {
		t.Errorf("master uses the wrong legacy URI v0/:\n%s", master)
	}
}

// token + native_hls propagated into the variant URIs.
func TestBuildMasterPlaylistPropagatesTokenAndNative(t *testing.T) {
	master := string(bmp(transcode.VariantLadder(2160), 3840, 2160, nil, nil, "Tok123", true))
	for _, line := range strings.Split(master, "\n") {
		if strings.HasPrefix(line, "v/") {
			if !strings.Contains(line, "?token=Tok123") || !strings.Contains(line, "native_hls=1") {
				t.Errorf("variant URI without token/native_hls: %q", line)
			}
		}
	}
}

// CA-2.2 (audio): source with ≥2 tracks → EXT-X-MEDIA TYPE=AUDIO; the 1st is DEFAULT
// WITHOUT a URI (muxed into the variant), the others have a a/{idx} URI; STREAM-INF references
// AUDIO="aud".
func TestBuildMasterPlaylistAudioRenditions(t *testing.T) {
	audio := []streamer.Track{
		{Index: 1, Language: "por", Title: "Português", Default: true},
		{Index: 2, Language: "eng", Title: "English"},
	}
	master := string(bmp(transcode.VariantLadder(1080), 1920, 1080, audio, nil, "Tok", true))
	if n := countMedia(master, "AUDIO"); n != 2 {
		t.Fatalf("expected 2 EXT-X-MEDIA AUDIO, found %d\n%s", n, master)
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
		t.Errorf("default track should exist WITHOUT a URI (muxed): %q", def)
	}
	if !strings.Contains(alt, `URI="a/2/index.m3u8`) || !strings.Contains(alt, "token=Tok") {
		t.Errorf("alternative should have a a/2 URI with token: %q", alt)
	}
	for _, l := range lines {
		if strings.HasPrefix(l, "#EXT-X-STREAM-INF:") && !strings.Contains(l, `AUDIO="aud"`) {
			t.Errorf("STREAM-INF without AUDIO=aud: %q", l)
		}
	}
}

// Audio with ≥2 tracks emits EXT-X-MEDIA even with renditions=false (M2b audio
// always on). Subtitles stay behind the flag.
func TestBuildMasterPlaylistAudioWithoutSubtitleFlag(t *testing.T) {
	audio := []streamer.Track{
		{Index: 1, Language: "por", Title: "Português"},
		{Index: 2, Language: "eng", Title: "English"},
	}
	subs := []streamer.Track{{Index: 3, Language: "por", Title: "Subs"}}
	master := string(buildMasterPlaylist(masterOpts{
		ladder: transcode.VariantLadder(1080), srcW: 1920, srcH: 1080,
		audio: audio, subs: subs, renditions: false,
	}))
	if n := countMedia(master, "AUDIO"); n != 2 {
		t.Fatalf("audio should be emitted without the flag: %d\n%s", n, master)
	}
	if n := countMedia(master, "SUBTITLES"); n != 0 {
		t.Fatalf("HLS subtitle should not be emitted without the flag: %d\n%s", n, master)
	}
	if !strings.Contains(master, `AUDIO="aud"`) {
		t.Fatalf("STREAM-INF should reference AUDIO=aud:\n%s", master)
	}
}

func TestMasterWarrantedAudioWithoutVideoLadder(t *testing.T) {
	ladder := transcode.VariantLadder(720) // single rung
	audio := []streamer.Track{{Index: 1}, {Index: 2}}
	if !masterWarranted(false, ladder, audio, nil) {
		t.Fatal("2 audio tracks must justify a master even at 720p")
	}
	if masterWarranted(false, ladder, []streamer.Track{{Index: 1}}, []streamer.Track{{Index: 3}}) {
		t.Fatal("a subtitle alone without the flag does not justify a master")
	}
	if !masterWarranted(true, ladder, nil, []streamer.Track{{Index: 3}}) {
		t.Fatal("with the flag, 1 subtitle justifies a master")
	}
}

// 1 audio track (or none) → NO renditions and NO AUDIO=aud (M2a: audio
// muxed into the variant, nothing to switch).
func TestBuildMasterPlaylistSingleAudioNoRenditions(t *testing.T) {
	audio := []streamer.Track{{Index: 1, Language: "eng"}}
	master := string(bmp(transcode.VariantLadder(1080), 1920, 1080, audio, nil, "", false))
	if countMedia(master, "AUDIO") != 0 {
		t.Errorf("1 track should not generate EXT-X-MEDIA:\n%s", master)
	}
	if strings.Contains(master, "AUDIO=") {
		t.Errorf("without renditions there should be no AUDIO=aud:\n%s", master)
	}
}

// RESOLUTION derived from the aspect ratio (par); CODECS per tier.
func TestBuildMasterPlaylistResolutionCodecs(t *testing.T) {
	master := string(bmp(transcode.VariantLadder(1080), 1920, 1080, nil, nil, "", false))
	for _, want := range []string{"RESOLUTION=1920x1080", "RESOLUTION=1280x720", `CODECS="avc1.4d4028,mp4a.40.2"`, `CODECS="avc1.4d401f,mp4a.40.2"`} {
		if !strings.Contains(master, want) {
			t.Errorf("master without %q:\n%s", want, master)
		}
	}
}

// Unknown dims (0,0) → RESOLUTION omitted, master still valid.
func TestBuildMasterPlaylistUnknownDimsOmitsResolution(t *testing.T) {
	master := string(bmp(transcode.VariantLadder(1080), 0, 0, nil, nil, "", false))
	if strings.Contains(master, "RESOLUTION=") {
		t.Errorf("dims 0 should omit RESOLUTION:\n%s", master)
	}
	if countStreamInf(master) != 2 {
		t.Errorf("should still have 2 STREAM-INF:\n%s", master)
	}
}

// writeMaster: content-type + no-store + body with STREAM-INF.
func TestWriteMaster(t *testing.T) {
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodGet, "/x", nil)

	writeMaster(c, masterOpts{ladder: transcode.VariantLadder(1080), srcW: 1920, srcH: 1080})

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
		t.Errorf("body without 2 STREAM-INF:\n%s", w.Body.String())
	}
}

// M2a (renditions=false): NO EXT-X-MEDIA; the chosen track (audioQuery) is
// propagated into the variant URL (audio switch via reload).
func TestBuildMasterPlaylistM2aAudioQuery(t *testing.T) {
	master := string(buildMasterPlaylist(masterOpts{
		ladder: transcode.VariantLadder(1080), srcW: 1920, srcH: 1080,
		token: "T", audioQuery: "2", renditions: false,
	}))
	if countMedia(master, "AUDIO") != 0 {
		t.Errorf("M2a should not have EXT-X-MEDIA:\n%s", master)
	}
	for _, l := range strings.Split(master, "\n") {
		if strings.HasPrefix(l, "v/") && !strings.Contains(l, "audio=2") {
			t.Errorf("M2a: variant without audio=2 propagated: %q", l)
		}
	}
}

// Gate: masterWarranted — ≥2 rungs or ≥2 audios always; subtitles only with the flag.
func TestMasterWarranted(t *testing.T) {
	two := transcode.VariantLadder(1080) // 2 rungs
	one := transcode.VariantLadder(720)  // 1 rung
	a2 := []streamer.Track{{}, {}}
	s1 := []streamer.Track{{}}
	cases := []struct {
		name       string
		renditions bool
		ladder     []transcode.Variant
		audio, sub []streamer.Track
		want       bool
	}{
		{"2 rungs always", false, two, nil, nil, true},
		{"1 rung + 2 audios without flag", false, one, a2, nil, true},
		{"1 rung + 1 audio + 1 sub without flag", false, one, []streamer.Track{{}}, s1, false},
		{"1 rung + 2 audios (renditions)", true, one, a2, nil, true},
		{"1 rung + 1 sub (renditions)", true, one, nil, s1, true},
		{"1 rung + 1 audio (renditions)", true, one, []streamer.Track{{}}, nil, false},
	}
	for _, c := range cases {
		if got := masterWarranted(c.renditions, c.ladder, c.audio, c.sub); got != c.want {
			t.Errorf("%s: masterWarranted = %v, want %v", c.name, got, c.want)
		}
	}
}

// StreamHLSVariant 404s when the index is outside the ladder.
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
		t.Errorf("variant 9 → status %d, want 404\n%s", w.Code, w.Body.String())
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

// CA-2.2 (subtitle): TEXT tracks → EXT-X-MEDIA TYPE=SUBTITLES with a
// sub/{idx} URI; STREAM-INF references SUBTITLES="sub". PGS (Image) is filtered.
func TestBuildMasterPlaylistSubtitleRenditions(t *testing.T) {
	subs := []streamer.Track{
		{Index: 3, Language: "eng", Codec: "subrip"},
		{Index: 4, Language: "spa", Codec: "hdmv_pgs_subtitle", Image: true}, // PGS → burn-in, no rendition
	}
	master := string(bmp(transcode.VariantLadder(1080), 1920, 1080, nil, textSubs(subs), "Tok", true))
	if n := countMedia(master, "SUBTITLES"); n != 1 {
		t.Fatalf("expected 1 EXT-X-MEDIA SUBTITLES (PGS filtered), found %d\n%s", n, master)
	}
	if !strings.Contains(master, `URI="sub/3/index.m3u8`) {
		t.Errorf("master without sub/3 URI:\n%s", master)
	}
	if strings.Contains(master, "sub/4/") {
		t.Errorf("PGS (track 4) should NOT become a rendition:\n%s", master)
	}
	for _, l := range strings.Split(master, "\n") {
		if strings.HasPrefix(l, "#EXT-X-STREAM-INF:") && !strings.Contains(l, `SUBTITLES="sub"`) {
			t.Errorf("STREAM-INF without SUBTITLES=sub: %q", l)
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
		t.Errorf("textSubs = %+v, want tracks 1 and 3 (no PGS)", got)
	}
}

// buildSubtitlePlaylist: single-segment WebVTT VOD pointing at the subtrack with the token.
func TestBuildSubtitlePlaylist(t *testing.T) {
	pl := string(buildSubtitlePlaylist("abc123", 0, 3, 120.5, "Tok"))
	for _, want := range []string{
		"#EXTM3U", "#EXT-X-PLAYLIST-TYPE:VOD", "#EXT-X-ENDLIST",
		"#EXTINF:120.500,", "/api/stream/subtrack/abc123/0/3?token=Tok",
	} {
		if !strings.Contains(pl, want) {
			t.Errorf("sub playlist without %q:\n%s", want, pl)
		}
	}
	// TARGETDURATION ≥ EXTINF (ceil).
	if !strings.Contains(pl, "#EXT-X-TARGETDURATION:121") {
		t.Errorf("TARGETDURATION should be 121 (ceil 120.5):\n%s", pl)
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
