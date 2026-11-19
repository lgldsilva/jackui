package streamer

import (
	"context"
	"testing"
	"time"

	"github.com/lgldsilva/jackui/internal/dbtest"
)

func newTestCache(t *testing.T) *MetadataCache {
	t.Helper()
	c, err := NewMetadataCache(dbtest.NewDB(t))
	if err != nil {
		t.Fatalf("NewMetadataCache: %v", err)
	}
	t.Cleanup(func() { c.Close() })
	return c
}

func TestArtRoundTrip(t *testing.T) {
	c := newTestCache(t)
	const hash = "aabbccddee"

	if got := c.GetArt(context.Background(), hash); got != nil {
		t.Fatalf("expected nil art before SetArt, got %+v", got)
	}

	want := &CachedArt{Source: "tmdb", PosterURL: "https://img/x.jpg", TmdbID: 42}
	if err := c.SetArt(context.Background(), hash, want); err != nil {
		t.Fatalf("SetArt: %v", err)
	}
	got := c.GetArt(context.Background(), hash)
	if got == nil || got.Source != "tmdb" || got.PosterURL != want.PosterURL || got.TmdbID != 42 {
		t.Fatalf("GetArt = %+v, want %+v", got, want)
	}
}

func TestArtNegativeFresh(t *testing.T) {
	c := newTestCache(t)
	const hash = "negativecachehash"

	// No row at all → not fresh (should attempt a resolve).
	if c.ArtNegativeFresh(context.Background(), hash, time.Hour) {
		t.Fatal("missing row must not read as a fresh negative marker")
	}
	// A real art source is never a negative marker.
	_ = c.SetArt(context.Background(), hash, &CachedArt{Source: "tmdb", PosterURL: "x"})
	if c.ArtNegativeFresh(context.Background(), hash, time.Hour) {
		t.Fatal("a real art source must not read as a negative marker")
	}
	// A just-written "none" marker → fresh (suppress re-resolve).
	_ = c.SetArt(context.Background(), hash, &CachedArt{Source: ArtSourceNone})
	if !c.ArtNegativeFresh(context.Background(), hash, time.Hour) {
		t.Fatal("a recent 'none' marker should read as fresh")
	}
	// Past the TTL → not fresh (re-resolve allowed).
	if c.ArtNegativeFresh(context.Background(), hash, 0) {
		t.Fatal("a zero TTL should make any marker stale")
	}
}

// Caching metadata and art are independent writes keyed by the same info_hash;
// neither must clobber the other's columns.
func TestArtAndMetadataDoNotClobber(t *testing.T) {
	c := newTestCache(t)
	const hash = "1234567890"

	// Art first (creates an art-only row with name='').
	if err := c.SetArt(context.Background(), hash, &CachedArt{Source: "frame", Path: ".art/1234567890.jpg"}); err != nil {
		t.Fatalf("SetArt: %v", err)
	}
	// Then metadata for the same hash.
	info := &TorrentInfo{
		InfoHash: hash,
		Name:     "The Matrix 1999",
		Files:    []FileInfo{{Index: 0, Path: "matrix.mkv", Size: 100, IsVideo: true}},
	}
	if err := c.Set(context.Background(), info); err != nil {
		t.Fatalf("Set: %v", err)
	}

	// Art survived the metadata write.
	if art := c.GetArt(context.Background(), hash); art == nil || art.Source != "frame" {
		t.Fatalf("art lost after Set: %+v", art)
	}
	// Metadata survived the prior art-only row.
	if meta := c.Get(context.Background(), hash); meta == nil || meta.Name != "The Matrix 1999" || len(meta.Files) != 1 {
		t.Fatalf("metadata wrong after art row: %+v", meta)
	}

	// Upgrading art (frame → torrent) must not touch the metadata name.
	if err := c.SetArt(context.Background(), hash, &CachedArt{Source: "torrent", Path: ".art/1234567890.jpg"}); err != nil {
		t.Fatalf("SetArt upgrade: %v", err)
	}
	if meta := c.Get(context.Background(), hash); meta == nil || meta.Name != "The Matrix 1999" {
		t.Fatalf("metadata name clobbered by art upgrade: %+v", meta)
	}
}

func TestHealthRoundTrip(t *testing.T) {
	c := newTestCache(t)
	const hash = "ffeeddccbb"

	if got := c.GetHealth(context.Background(), hash); got != nil {
		t.Fatalf("expected nil health before probe, got %+v", got)
	}
	if err := c.SetHealth(context.Background(), hash, 12, 30); err != nil {
		t.Fatalf("SetHealth: %v", err)
	}
	got := c.GetHealth(context.Background(), hash)
	if got == nil || got.Seeders != 12 || got.Peers != 30 || !got.Available {
		t.Fatalf("GetHealth = %+v, want seeders=12 peers=30 available=true", got)
	}
	if got.CheckedAt.IsZero() {
		t.Fatal("CheckedAt should be set")
	}
	// Zero seeders/peers → not available.
	_ = c.SetHealth(context.Background(), hash, 0, 0)
	if got := c.GetHealth(context.Background(), hash); got == nil || got.Available {
		t.Fatalf("0/0 should be unavailable, got %+v", got)
	}
}

func TestArtSourceRank(t *testing.T) {
	if !(ArtSourceRank("torrent") > ArtSourceRank("tmdb") && ArtSourceRank("tmdb") > ArtSourceRank("frame") && ArtSourceRank("frame") > ArtSourceRank("")) {
		t.Fatalf("rank order broken: torrent=%d tmdb=%d frame=%d none=%d",
			ArtSourceRank("torrent"), ArtSourceRank("tmdb"), ArtSourceRank("frame"), ArtSourceRank(""))
	}
	if r := ArtSourceRank("web"); r != 2 {
		t.Errorf("web rank = %d, want 2", r)
	}
}

func TestMetadataCache_Get_Missing(t *testing.T) {
	c := newTestCache(t)
	if got := c.Get(context.Background(), "nonexistent"); got != nil {
		t.Errorf("expected nil for missing hash, got %+v", got)
	}
}

func TestMetadataCache_GetBatch(t *testing.T) {
	c := newTestCache(t)
	const h1, h2 = "aabbccddee", "1122334455"
	if err := c.Set(context.Background(), &TorrentInfo{
		InfoHash: h1, Name: "One", TotalSize: 100,
		Files: []FileInfo{{Index: 0, Path: "a.mkv", Size: 100, IsVideo: true}},
	}); err != nil {
		t.Fatalf("Set h1: %v", err)
	}
	got := c.GetBatch(context.Background(), []string{h1, h2, h1, ""})
	if len(got) != 1 {
		t.Fatalf("GetBatch len = %d, want 1", len(got))
	}
	if meta := got[h1]; meta == nil || meta.Name != "One" {
		t.Fatalf("GetBatch h1 = %+v", meta)
	}
	if _, ok := got[h2]; ok {
		t.Fatal("missing hash should be omitted")
	}
	if batch := c.GetBatch(context.Background(), nil); len(batch) != 0 {
		t.Fatal("nil batch should return empty map")
	}
}

func TestMetadataCache_GetArt_Missing(t *testing.T) {
	c := newTestCache(t)
	if got := c.GetArt(context.Background(), "nonexistent"); got != nil {
		t.Errorf("expected nil for missing hash, got %+v", got)
	}
}

func TestMetadataCache_GetHealth_Missing(t *testing.T) {
	c := newTestCache(t)
	if got := c.GetHealth(context.Background(), "nonexistent"); got != nil {
		t.Errorf("expected nil for missing hash, got %+v", got)
	}
}

func TestMetadataCache_NilSafe(t *testing.T) {
	var nilC *MetadataCache
	if nilC.Get(context.Background(), "hash") != nil {
		t.Error("Get on nil should return nil")
	}
	if nilC.GetArt(context.Background(), "hash") != nil {
		t.Error("GetArt on nil should return nil")
	}
	if nilC.GetHealth(context.Background(), "hash") != nil {
		t.Error("GetHealth on nil should return nil")
	}
	if err := nilC.Set(context.Background(), nil); err != nil {
		t.Errorf("Set on nil with nil info: %v", err)
	}
	if err := nilC.SetArt(context.Background(), "hash", nil); err != nil {
		t.Errorf("SetArt on nil: %v", err)
	}
	if err := nilC.SetHealth(context.Background(), "hash", 0, 0); err != nil {
		t.Errorf("SetHealth on nil: %v", err)
	}
	nilC.Close()
}

func TestMetadataCache_SetWithArt_AllEdgeCases(t *testing.T) {
	c := newTestCache(t)

	err := c.Set(context.Background(), &TorrentInfo{
		InfoHash:    "edgetest",
		Name:        "Edge Test",
		TotalSize:   5000,
		PrimaryFile: 0,
		Files:       []FileInfo{{Index: 0, Path: "v.mp4", Size: 5000, IsVideo: true}},
	})
	if err != nil {
		t.Fatalf("Set: %v", err)
	}

	meta := c.Get(context.Background(), "edgetest")
	if meta == nil || meta.Name != "Edge Test" || len(meta.Files) != 1 {
		t.Fatalf("Get = %+v", meta)
	}

	art := c.GetArt(context.Background(), "edgetest")
	if art != nil {
		t.Errorf("expected nil art before SetArt, got %+v", art)
	}

	if err := c.SetArt(context.Background(), "edgetest", &CachedArt{Source: "tmdb", PosterURL: "https://img.jpg"}); err != nil {
		t.Fatalf("SetArt: %v", err)
	}

	art = c.GetArt(context.Background(), "edgetest")
	if art == nil || art.Source != "tmdb" {
		t.Errorf("GetArt after SetArt = %+v", art)
	}
}

func TestMetadataCache_SetHealth_Nil(t *testing.T) {
	var m *MetadataCache
	err := m.SetHealth(context.Background(), "hash", 1, 2)
	if err != nil {
		t.Errorf("unexpected error on nil cache: %v", err)
	}
}

func TestMetadataCache_GetHealth_NoHealthData(t *testing.T) {
	c := newTestCache(t)
	c.SetHealth(context.Background(), "hash1", 5, 10)
	// hash2 has no health data
	if got := c.GetHealth(context.Background(), "hash2"); got != nil {
		t.Error("expected nil for hash with no health data")
	}
}

func TestMetadataCache_GetArt_NilSafe(t *testing.T) {
	var m *MetadataCache
	if got := m.GetArt(context.Background(), "hash"); got != nil {
		t.Error("expected nil from nil cache")
	}
}

func TestMetadataCache_Get_NilSafe(t *testing.T) {
	var m *MetadataCache
	if got := m.Get(context.Background(), "hash"); got != nil {
		t.Error("expected nil from nil cache")
	}
}

func TestMetadataCache_GetHealth_NilSafe(t *testing.T) {
	var m *MetadataCache
	if got := m.GetHealth(context.Background(), "hash"); got != nil {
		t.Error("expected nil from nil cache")
	}
}

func TestArtSourceRank_Default(t *testing.T) {
	if r := ArtSourceRank(""); r != 0 {
		t.Errorf("expected 0 for empty source, got %d", r)
	}
}
