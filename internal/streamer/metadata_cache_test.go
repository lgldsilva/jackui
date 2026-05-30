package streamer

import (
	"database/sql"
	"path/filepath"
	"testing"

	_ "modernc.org/sqlite"
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
	if r := ArtSourceRank("web"); r != 2 {
		t.Errorf("web rank = %d, want 2", r)
	}
}

func TestMetadataCache_Get_Missing(t *testing.T) {
	c := newTestCache(t)
	if got := c.Get("nonexistent"); got != nil {
		t.Errorf("expected nil for missing hash, got %+v", got)
	}
}

func TestMetadataCache_GetArt_Missing(t *testing.T) {
	c := newTestCache(t)
	if got := c.GetArt("nonexistent"); got != nil {
		t.Errorf("expected nil for missing hash, got %+v", got)
	}
}

func TestMetadataCache_GetHealth_Missing(t *testing.T) {
	c := newTestCache(t)
	if got := c.GetHealth("nonexistent"); got != nil {
		t.Errorf("expected nil for missing hash, got %+v", got)
	}
}

func TestMetadataCache_NilSafe(t *testing.T) {
	var nilC *MetadataCache
	if nilC.Get("hash") != nil {
		t.Error("Get on nil should return nil")
	}
	if nilC.GetArt("hash") != nil {
		t.Error("GetArt on nil should return nil")
	}
	if nilC.GetHealth("hash") != nil {
		t.Error("GetHealth on nil should return nil")
	}
	if err := nilC.Set(nil); err != nil {
		t.Errorf("Set on nil with nil info: %v", err)
	}
	if err := nilC.SetArt("hash", nil); err != nil {
		t.Errorf("SetArt on nil: %v", err)
	}
	if err := nilC.SetHealth("hash", 0, 0); err != nil {
		t.Errorf("SetHealth on nil: %v", err)
	}
	nilC.Close()
}

func TestMetadataCache_SetWithArt_AllEdgeCases(t *testing.T) {
	c := newTestCache(t)

	err := c.Set(&TorrentInfo{
		InfoHash:    "edgetest",
		Name:        "Edge Test",
		TotalSize:   5000,
		PrimaryFile: 0,
		Files:       []FileInfo{{Index: 0, Path: "v.mp4", Size: 5000, IsVideo: true}},
	})
	if err != nil {
		t.Fatalf("Set: %v", err)
	}

	meta := c.Get("edgetest")
	if meta == nil || meta.Name != "Edge Test" || len(meta.Files) != 1 {
		t.Fatalf("Get = %+v", meta)
	}

	art := c.GetArt("edgetest")
	if art != nil {
		t.Errorf("expected nil art before SetArt, got %+v", art)
	}

	if err := c.SetArt("edgetest", &CachedArt{Source: "tmdb", PosterURL: "https://img.jpg"}); err != nil {
		t.Fatalf("SetArt: %v", err)
	}

	art = c.GetArt("edgetest")
	if art == nil || art.Source != "tmdb" {
		t.Errorf("GetArt after SetArt = %+v", art)
	}
}

func TestMetadataCache_ColumnExists_False(t *testing.T) {
	c := newTestCache(t)
	got := columnExists(c.db, "metadata", "nonexistent_column")
	if got {
		t.Error("expected false for nonexistent column")
	}
}

func TestMetadataCache_ColumnExists_True(t *testing.T) {
	c := newTestCache(t)
	if !columnExists(c.db, "metadata", "info_hash") {
		t.Error("expected info_hash column to exist")
	}
}

func TestNewMetadataCache_InvalidPath(t *testing.T) {
	_, err := NewMetadataCache("/nonexistent/foo/meta.db")
	if err == nil {
		t.Error("expected error for invalid path")
	}
}

func TestMetadataCache_ColumnExists_ErrorQuery(t *testing.T) {
	// columnExists with a bad DB connection
	db, err := sql.Open("sqlite", filepath.Join(t.TempDir(), "bad.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if columnExists(db, "metadata", "any") {
		t.Error("expected false for empty DB")
	}
}

func TestMetadataCache_SetHealth_Nil(t *testing.T) {
	var m *MetadataCache
	err := m.SetHealth("hash", 1, 2)
	if err != nil {
		t.Errorf("unexpected error on nil cache: %v", err)
	}
}

func TestMetadataCache_GetHealth_NoHealthData(t *testing.T) {
	c := newTestCache(t)
	c.SetHealth("hash1", 5, 10)
	// hash2 has no health data
	if got := c.GetHealth("hash2"); got != nil {
		t.Error("expected nil for hash with no health data")
	}
}

func TestMetadataCache_GetArt_NilSafe(t *testing.T) {
	var m *MetadataCache
	if got := m.GetArt("hash"); got != nil {
		t.Error("expected nil from nil cache")
	}
}

func TestMetadataCache_Get_NilSafe(t *testing.T) {
	var m *MetadataCache
	if got := m.Get("hash"); got != nil {
		t.Error("expected nil from nil cache")
	}
}

func TestMetadataCache_GetHealth_NilSafe(t *testing.T) {
	var m *MetadataCache
	if got := m.GetHealth("hash"); got != nil {
		t.Error("expected nil from nil cache")
	}
}

func TestArtSourceRank_Default(t *testing.T) {
	if r := ArtSourceRank(""); r != 0 {
		t.Errorf("expected 0 for empty source, got %d", r)
	}
}
