package handlers

import (
	"context"
	"testing"
	"time"

	"github.com/anacrolix/torrent/metainfo"

	"github.com/lgldsilva/jackui/internal/dbtest"
	"github.com/lgldsilva/jackui/internal/streamer"
)

// negativeResolveEnv builds a streamer + metadata cache with no resolvers
// wired (no TMDB client, no web search, torrent never active), so
// runArtResolve always reaches the "nothing found" tail.
func negativeResolveEnv(t *testing.T) (*streamer.Streamer, *streamer.MetadataCache) {
	t.Helper()
	s := streamer.NewForTesting()
	cache, err := streamer.NewMetadataCache(dbtest.NewDB(t))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { cache.Close() })
	s.SetMetadataCache(cache)
	return s, cache
}

func mustArtHash(t *testing.T, hex string) metainfo.Hash {
	t.Helper()
	h, err := parseHash(hex)
	if err != nil {
		t.Fatalf("parseHash(%q): %v", hex, err)
	}
	return h
}

// TestRunArtResolve_DeadlineNoNegativeMarker: a chain cut short by its 25s
// deadline (slow swarm read starving TMDB/web) must NOT persist the "no art"
// marker — otherwise the entry stays artless for the whole negative TTL even
// though nothing actually answered.
func TestRunArtResolve_DeadlineNoNegativeMarker(t *testing.T) {
	s, cache := negativeResolveEnv(t)
	rctx, cancel := context.WithCancel(context.Background())
	cancel() // deadline/cancel already hit: every source was cut short

	cap := &artCaptureResponder{}
	runArtResolve(rctx, cap, &artResolver{s: s, cache: cache}, mustArtHash(t, "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"), -1, "")

	if cap.Code != 204 {
		t.Errorf("status = %d, want 204", cap.Code)
	}
	if art := cache.GetArt("aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"); art != nil {
		t.Errorf("negative marker persisted on cancelled context: %+v", art)
	}
	if cache.ArtNegativeFresh("aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", time.Hour) {
		t.Error("ArtNegativeFresh = true after cancelled resolve; retry would be blocked")
	}
}

// TestRunArtResolve_CompletedMissPersistsNone: when the chain runs to
// completion and every enabled source genuinely answers "no", the negative
// marker IS persisted (spares repeated AI+TMDB+web calls on future renders).
func TestRunArtResolve_CompletedMissPersistsNone(t *testing.T) {
	s, cache := negativeResolveEnv(t)

	cap := &artCaptureResponder{}
	runArtResolve(context.Background(), cap, &artResolver{s: s, cache: cache}, mustArtHash(t, "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"), -1, "")

	if cap.Code != 204 {
		t.Errorf("status = %d, want 204", cap.Code)
	}
	art := cache.GetArt("bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb")
	if art == nil || art.Source != streamer.ArtSourceNone {
		t.Errorf("GetArt = %+v, want persisted source %q", art, streamer.ArtSourceNone)
	}
}
