package handlers

import (
	"strings"
	"testing"

	"github.com/anacrolix/torrent/metainfo"
)

// hlsSessionKey must isolate sessions by VARIANT and by AUDIO, keeping the legacy
// single-variant key (variant<0 && audio<0) identical to pre-Phase-2. The order
// is `-v` before `-a` (EffectiveKey still appends -vod/-evt afterwards).
func TestHlsSessionKeyMatrix(t *testing.T) {
	var h metainfo.Hash
	h.FromHexString("aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa")
	base := h.HexString() + "-3"

	cases := []struct {
		variant, audio int
		wantSuffix     string
	}{
		{-1, -1, ""},     // legacy single-variant
		{0, -1, "-v0"},   // variant without a chosen audio
		{-1, 2, "-a2"},   // audio without a variant (legacy path with ?audio)
		{1, 2, "-v1-a2"}, // both, in v-then-a order
		{2, 0, "-v2-a0"}, // audio 0 is an explicit choice (>=0)
	}
	seen := map[string]bool{}
	for _, c := range cases {
		got := hlsSessionKey(h, 3, c.variant, c.audio)
		want := base + c.wantSuffix
		if got != want {
			t.Errorf("hlsSessionKey(_,3,%d,%d) = %q, want %q", c.variant, c.audio, got, want)
		}
		if !strings.HasPrefix(got, base) {
			t.Errorf("key %q does not start with %q", got, base)
		}
		if seen[got] {
			t.Errorf("key collision: %q repeated (distinct variants/audios must yield distinct keys)", got)
		}
		seen[got] = true
	}
}
