package handlers

import (
	"strings"
	"testing"

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
		master := string(buildMasterPlaylist(ladder, src.w, src.h, "", false))
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
	master := string(buildMasterPlaylist(ladder, 1920, 1080, "", false))
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
	master := string(buildMasterPlaylist(ladder, 3840, 2160, "Tok123", true))
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

// RESOLUTION derivada do aspect ratio da fonte (par); CODECS por tier.
func TestBuildMasterPlaylistResolutionCodecs(t *testing.T) {
	ladder := transcode.VariantLadder(1080)
	master := string(buildMasterPlaylist(ladder, 1920, 1080, "", false))
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
	master := string(buildMasterPlaylist(ladder, 0, 0, "", false))
	if strings.Contains(master, "RESOLUTION=") {
		t.Errorf("dims 0 deveria omitir RESOLUTION:\n%s", master)
	}
	if countStreamInf(master) != 2 {
		t.Errorf("ainda deveria ter 2 STREAM-INF:\n%s", master)
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
