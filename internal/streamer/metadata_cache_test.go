package streamer

import (
	"path/filepath"
	"testing"
)

func newTestCache(t *testing.T) *MetadataCache {
	t.Helper()
	c, err := NewMetadataCache(filepath.Join(t.TempDir(), "meta.db"))
	if err != nil {
		t.Fatalf("NewMetadataCache: %v", err)
	}
	t.Cleanup(func() { c.Close() })
	return c
}

func TestArtRoundTrip(t *testing.T) {
	c := newTestCache(t)
	const hash = "aabbccddee"

	if got := c.GetArt(hash); got != nil {
		t.Fatalf("expected nil art before SetArt, got %+v", got)
	}

	want := &CachedArt{Source: "tmdb", PosterURL: "https://img/x.jpg", TmdbID: 42}
	if err := c.SetArt(hash, want); err != nil {
		t.Fatalf("SetArt: %v", err)
	}
	got := c.GetArt(hash)
	if got == nil || got.Source != "tmdb" || got.PosterURL != want.PosterURL || got.TmdbID != 42 {
		t.Fatalf("GetArt = %+v, want %+v", got, want)
	}
}

// Caching metadata and art are independent writes keyed by the same info_hash;
// neither must clobber the other's columns.
func TestArtAndMetadataDoNotClobber(t *testing.T) {
	c := newTestCache(t)
	const hash = "1234567890"

	// Art first (creates an art-only row with name='').
	if err := c.SetArt(hash, &CachedArt{Source: "frame", Path: ".art/1234567890.jpg"}); err != nil {
		t.Fatalf("SetArt: %v", err)
	}
	// Then metadata for the same hash.
	info := &TorrentInfo{
		InfoHash: hash,
		Name:     "The Matrix 1999",
		Files:    []FileInfo{{Index: 0, Path: "matrix.mkv", Size: 100, IsVideo: true}},
	}
	if err := c.Set(info); err != nil {
		t.Fatalf("Set: %v", err)
	}

	// Art survived the metadata write.
	if art := c.GetArt(hash); art == nil || art.Source != "frame" {
		t.Fatalf("art lost after Set: %+v", art)
	}
	// Metadata survived the prior art-only row.
	if meta := c.Get(hash); meta == nil || meta.Name != "The Matrix 1999" || len(meta.Files) != 1 {
		t.Fatalf("metadata wrong after art row: %+v", meta)
	}

	// Upgrading art (frame → torrent) must not touch the metadata name.
	if err := c.SetArt(hash, &CachedArt{Source: "torrent", Path: ".art/1234567890.jpg"}); err != nil {
		t.Fatalf("SetArt upgrade: %v", err)
	}
	if meta := c.Get(hash); meta == nil || meta.Name != "The Matrix 1999" {
		t.Fatalf("metadata name clobbered by art upgrade: %+v", meta)
	}
}

func TestHealthRoundTrip(t *testing.T) {
	c := newTestCache(t)
	const hash = "ffeeddccbb"

	if got := c.GetHealth(hash); got != nil {
		t.Fatalf("expected nil health before probe, got %+v", got)
	}
	if err := c.SetHealth(hash, 12, 30); err != nil {
		t.Fatalf("SetHealth: %v", err)
	}
	got := c.GetHealth(hash)
	if got == nil || got.Seeders != 12 || got.Peers != 30 || !got.Available {
		t.Fatalf("GetHealth = %+v, want seeders=12 peers=30 available=true", got)
	}
	if got.CheckedAt.IsZero() {
		t.Fatal("CheckedAt should be set")
	}
	// Zero seeders/peers → not available.
	_ = c.SetHealth(hash, 0, 0)
	if got := c.GetHealth(hash); got == nil || got.Available {
		t.Fatalf("0/0 should be unavailable, got %+v", got)
	}
}

func TestArtSourceRank(t *testing.T) {
	if !(ArtSourceRank("torrent") > ArtSourceRank("tmdb") && ArtSourceRank("tmdb") > ArtSourceRank("frame") && ArtSourceRank("frame") > ArtSourceRank("")) {
		t.Fatalf("rank order broken: torrent=%d tmdb=%d frame=%d none=%d",
			ArtSourceRank("torrent"), ArtSourceRank("tmdb"), ArtSourceRank("frame"), ArtSourceRank(""))
	}
}
