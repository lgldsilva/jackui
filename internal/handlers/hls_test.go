package handlers

import (
	"strings"
	"testing"
)

// TestBuildVODPlaylistDeclaresAllSegments is the #61 guard for the synthesised
// finite playlist: it must declare ceil(duration/segDur) segments, mark the
// stream complete with EXT-X-ENDLIST and type VOD (so Safari renders a full
// seekbar), and append the auth token to every segment line (Safari drops the
// master URL's query string when resolving relative segment names).
func TestBuildVODPlaylistDeclaresAllSegments(t *testing.T) {
	// 30s / 4s = 7.5 → ceil = 8 segments.
	pl := string(buildVODPlaylist(30, "TOK", false))

	if !strings.Contains(pl, "#EXT-X-PLAYLIST-TYPE:VOD") {
		t.Error("playlist must be VOD")
	}
	if !strings.Contains(pl, "#EXT-X-ENDLIST") {
		t.Error("playlist must end with ENDLIST so Safari sees a finite, seekable stream")
	}
	if got := strings.Count(pl, "seg_"); got != 8 {
		t.Errorf("expected 8 segment lines for 30s/4s, got %d\n%s", got, pl)
	}
	if !strings.Contains(pl, "seg_00000.ts?token=TOK") || !strings.Contains(pl, "seg_00007.ts?token=TOK") {
		t.Errorf("segments must carry the token; got:\n%s", pl)
	}
	// Last segment is the 2s remainder (30 - 7*4).
	if !strings.Contains(pl, "#EXTINF:2.000,") {
		t.Errorf("trailing partial segment should be 2.000s; got:\n%s", pl)
	}
}

func TestBuildVODPlaylistTokenless(t *testing.T) {
	pl := string(buildVODPlaylist(8, "", false))
	if strings.Contains(pl, "?token=") {
		t.Errorf("no token should mean no query string; got:\n%s", pl)
	}
	if got := strings.Count(pl, "seg_"); got != 2 {
		t.Errorf("expected 2 segments for 8s, got %d", got)
	}
}
