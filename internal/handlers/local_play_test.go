package handlers

import (
	"strings"
	"testing"
)

// TestClassifyForBrowser pins the direct-play vs HLS decision so a future tweak
// to the codec/container whitelist doesn't accidentally route MKV/HEVC through
// the browser (which would resurface the "Hobbit em mkv não toca" failure mode
// the local-HLS path was added to fix).
func TestClassifyForBrowser(t *testing.T) {
	cases := []struct {
		name      string
		probe     localProbe
		wantDirect bool
		// matchReason is a substring expected in the rejection reason; "" means
		// no reason expected (direct-play).
		matchReason string
	}{
		{
			name:      "mp4_h264_aac_direct",
			probe:     localProbe{Container: "mov", VideoCodec: "h264", AudioCodec: "aac"},
			wantDirect: true,
		},
		{
			name:        "matroska_hevc_ac3_hls",
			probe:       localProbe{Container: "matroska", VideoCodec: "hevc", AudioCodec: "ac3"},
			wantDirect:  false,
			matchReason: "container=matroska",
		},
		{
			name:        "mp4_hevc_aac_hls",
			probe:       localProbe{Container: "mov", VideoCodec: "hevc", AudioCodec: "aac"},
			wantDirect:  false,
			matchReason: "vcodec=hevc",
		},
		{
			name:        "mp4_h264_ac3_hls",
			probe:       localProbe{Container: "mp4", VideoCodec: "h264", AudioCodec: "ac3"},
			wantDirect:  false,
			matchReason: "acodec=ac3",
		},
		{
			name:        "webm_vp9_opus_direct",
			probe:       localProbe{Container: "webm", VideoCodec: "vp9", AudioCodec: "opus"},
			wantDirect:  true,
		},
		{
			name:        "av1_in_mp4_hls",
			probe:       localProbe{Container: "mp4", VideoCodec: "av1", AudioCodec: "aac"},
			wantDirect:  false,
			matchReason: "vcodec=av1",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			gotDirect, gotReason := classifyForBrowser(tc.probe)
			if gotDirect != tc.wantDirect {
				t.Errorf("direct=%v, want %v (reason=%q)", gotDirect, tc.wantDirect, gotReason)
			}
			if tc.matchReason != "" && !strings.Contains(gotReason, tc.matchReason) {
				t.Errorf("reason=%q, want substring %q", gotReason, tc.matchReason)
			}
		})
	}
}

// TestLocalSessionKeyStable ensures the (mount, path) → key derivation is a
// stable function — same input always yields the same session key, different
// inputs differ. Without this, two viewers of the same file would spawn
// duplicate ffmpeg sessions (manager dedupes by exact key).
func TestLocalSessionKeyStable(t *testing.T) {
	a := localSessionKey("Downloads", "movies/The.Hobbit.mkv")
	b := localSessionKey("Downloads", "movies/The.Hobbit.mkv")
	if a != b {
		t.Errorf("session key not stable: %s vs %s", a, b)
	}
	c := localSessionKey("Downloads", "movies/Other.mkv")
	if a == c {
		t.Errorf("different paths produced same key %s", a)
	}
	if !strings.HasPrefix(a, "local-") {
		t.Errorf("expected local- prefix, got %s", a)
	}
}

// TestBuildLocalVODPlaylistShape mirrors the torrent-side guard:
// ceil(duration/segDur) segment lines, EXT-X-ENDLIST present, and each segment
// line is the URL the segURL builder produced (so the token reaches the segment
// endpoint).
func TestBuildLocalVODPlaylistShape(t *testing.T) {
	segURL := func(name string) string {
		return "/api/local/hls/seg?mount=M&path=p.mkv&seg=" + name + "&token=TOK"
	}
	pl := string(buildLocalVODPlaylist(30, segURL))
	if !strings.Contains(pl, "#EXT-X-PLAYLIST-TYPE:VOD") {
		t.Error("playlist must be VOD")
	}
	if !strings.Contains(pl, "#EXT-X-ENDLIST") {
		t.Error("missing ENDLIST")
	}
	// 30/4 = 7.5 → 8 segments
	if got := strings.Count(pl, "seg_"); got != 8 {
		t.Errorf("expected 8 segments, got %d\n%s", got, pl)
	}
	if !strings.Contains(pl, "seg_00007.ts&token=TOK") {
		t.Errorf("missing tokenised last segment; got:\n%s", pl)
	}
}
