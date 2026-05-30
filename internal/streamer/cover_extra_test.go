package streamer

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/anacrolix/torrent/metainfo"
	"golang.org/x/time/rate"
)

func TestStatusForLocked_Paused_Extra(t *testing.T) {
	want := statusForLocked(&entry{paused: true})
	if want != "paused" {
		t.Errorf("got %q, want %q", want, "paused")
	}
}

func TestFmtBytes_Zero_Extra(t *testing.T) {
	if got := fmtBytes(0); got != "0 B" {
		t.Errorf("got %q, want %q", got, "0 B")
	}
	if got := fmtBytes(1023); got != "1023 B" {
		t.Errorf("got %q", got)
	}
}

func TestFmtBytes_KB_Extra(t *testing.T) {
	got := fmtBytes(2048)
	if !strings.HasPrefix(got, "2.00 K") {
		t.Errorf("got %q", got)
	}
}

func TestFirstChars_Short_Extra(t *testing.T) {
	got := firstChars("hello", 10)
	if got != "hello" {
		t.Errorf("got %q, want %q", got, "hello")
	}
}

func TestFirstChars_Long_Extra(t *testing.T) {
	got := firstChars("hello world this is long", 5)
	if got != "hello..." {
		t.Errorf("got %q, want %q", got, "hello...")
	}
}

func TestAugmentNameToHashFromMetainfo_NoDir_Extra(t *testing.T) {
	s := NewForTesting()
	s.metainfoDir = ""
	m := make(map[string]string)
	s.augmentNameToHashFromMetainfo(m)
}

func TestAugmentNameToHashFromMetainfo_NonExistentDir_Extra(t *testing.T) {
	s := NewForTesting()
	s.metainfoDir = "/nonexistent"
	m := make(map[string]string)
	s.augmentNameToHashFromMetainfo(m)
}

func TestAugmentNameToHashFromMetainfo_InvalidFile_Extra(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "invalid.torrent"), []byte("not bencode"), 0o644)

	s := NewForTesting()
	s.metainfoDir = dir
	m := make(map[string]string)
	s.augmentNameToHashFromMetainfo(m)
}

func TestStats_NonExistentDataDir_Extra(t *testing.T) {
	s := NewForTesting()
	s.cfg.DataDir = "/nonexistent"

	stats, err := s.Stats()
	if err != nil {
		t.Fatalf("Stats: %v", err)
	}
	if stats == nil {
		t.Fatal("Stats returned nil")
	}
	if stats.NumActive != 0 {
		t.Errorf("expected 0 active, got %d", stats.NumActive)
	}
}

func TestStats_EmptyDataDir_Extra(t *testing.T) {
	s := NewForTesting()
	s.cfg.DataDir = t.TempDir()

	stats, err := s.Stats()
	if err != nil {
		t.Fatalf("Stats: %v", err)
	}
	if stats == nil {
		t.Fatal("Stats returned nil")
	}
	if len(stats.Entries) != 0 {
		t.Errorf("expected 0 entries, got %d", len(stats.Entries))
	}
}

func TestStats_WithFiles_Extra(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "entry1"), []byte("data"), 0o644)
	os.MkdirAll(filepath.Join(dir, "entry2"), 0o755)
	os.WriteFile(filepath.Join(dir, "entry2", "file.txt"), []byte("more data"), 0o644)

	s := NewForTesting()
	s.cfg.DataDir = dir

	stats, err := s.Stats()
	if err != nil {
		t.Fatalf("Stats: %v", err)
	}
	if len(stats.Entries) != 2 {
		t.Errorf("expected 2 entries, got %d", len(stats.Entries))
	}
	if stats.TotalSize <= 0 {
		t.Errorf("expected positive total, got %d", stats.TotalSize)
	}
}

func TestEnforceCacheLimit_NoLimit_Extra(t *testing.T) {
	s := NewForTesting()
	s.cfg.MaxCacheSize = 0
	s.enforceCacheLimit()
}

func TestEnforceCacheLimit_UnderLimit_Extra(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "entry"), []byte("small"), 0o644)

	s := NewForTesting()
	s.cfg.DataDir = dir
	s.cfg.MaxCacheSize = 1 << 30
	s.enforceCacheLimit()

	if _, err := os.Stat(filepath.Join(dir, "entry")); os.IsNotExist(err) {
		t.Error("entry was evicted despite being under limit")
	}
}

func TestEnforceCacheLimit_ProtectsFavorites_Extra(t *testing.T) {
	dir := t.TempDir()
	data := make([]byte, 100*1024)
	os.WriteFile(filepath.Join(dir, "fav-entry"), data, 0o644)

	s := NewForTesting()
	s.cfg.DataDir = dir
	s.cfg.MaxCacheSize = 1

	f := newTestFavorites(t)
	f.Add("fav-entry", "hash", "magnet", "manual", 1)
	s.SetFavorites(f)

	s.enforceCacheLimit()

	if _, err := os.Stat(filepath.Join(dir, "fav-entry")); os.IsNotExist(err) {
		t.Error("favorited entry was evicted despite protection")
	}
}

func TestClearAll_NonExistentDir_Extra(t *testing.T) {
	s := NewForTesting()
	s.cfg.DataDir = "/nonexistent"

	err := s.ClearAll()
	if err != nil {
		t.Fatalf("ClearAll: %v", err)
	}
}

func TestClearEntry_OutsideDataDir_Extra(t *testing.T) {
	dir := t.TempDir()
	s := NewForTesting()
	s.cfg.DataDir = dir

	err := s.ClearEntry("../outside")
	if err == nil {
		t.Error("expected error for path outside DataDir")
	}
}

func TestDirSizeAndMTime_NonExistent_Extra(t *testing.T) {
	_, _, err := dirSizeAndMTime("/nonexistent_path_12345")
	if err == nil {
		t.Error("expected error for non-existent path")
	}
}

func TestExtractThumbnail_NonExistent_Extra(t *testing.T) {
	s := NewForTesting()
	s.cfg.DataDir = t.TempDir()
	_, _, err := s.ExtractThumbnail(nil, metainfo.Hash{}, 0, 10)
	if err == nil {
		t.Error("expected error for non-existent hash")
	}
}

func TestExtractArtwork_NonExistent_Extra(t *testing.T) {
	s := NewForTesting()
	s.cfg.DataDir = t.TempDir()
	_, _, err := s.ExtractArtwork(nil, metainfo.Hash{}, 0)
	if err == nil {
		t.Error("expected error for non-existent hash")
	}
}

func TestExtractSubtitle_NonExistent_Extra(t *testing.T) {
	s := NewForTesting()
	_, err := s.ExtractSubtitle(nil, metainfo.Hash{}, 0, 0)
	if err == nil {
		t.Error("expected error for non-existent hash")
	}
}

func TestResolveProbeInput_NoResolver_Extra(t *testing.T) {
	s := NewForTesting()
	_, err := s.resolveProbeInput(metainfo.Hash{}, 0, 1024)
	if err == nil {
		t.Error("expected error for non-active torrent without resolver")
	}
}

func TestResolveProbeInput_WithResolver_Extra(t *testing.T) {
	s := NewForTesting()
	s.filePathResolver = func(hash metainfo.Hash, fileIdx int) (string, bool) {
		return "/path/to/file.mp4", true
	}
	pi, err := s.resolveProbeInput(metainfo.Hash{}, 0, 1024)
	if err != nil {
		t.Fatalf("resolveProbeInput with resolver: %v", err)
	}
	if pi.input != "/path/to/file.mp4" {
		t.Errorf("got input %q, want %q", pi.input, "/path/to/file.mp4")
	}
}

func TestSetRateLimits_NilLimiter_Extra(t *testing.T) {
	s := NewForTesting()
	s.dlLimiter = nil
	s.SetRateLimits(1000, 2000)
}

func TestLimiterBytes_Explicit_Extra(t *testing.T) {
	l := rate.NewLimiter(rate.Inf, 1<<16)
	if got := limiterBytes(l); got != 0 {
		t.Errorf("Inf should return 0, got %d", got)
	}
	l.SetLimit(rate.Limit(500))
	if got := limiterBytes(l); got != 500 {
		t.Errorf("got %d, want %d", got, 500)
	}
}

func TestEnsureActive_InvalidMagnet_Extra(t *testing.T) {
	s := NewForTesting()
	_, err := s.EnsureActive(nil, "not-a-magnet")
	if err == nil {
		t.Error("expected error for invalid magnet")
	}
}

func TestPrefetch_NonExistent_Extra(t *testing.T) {
	s := NewForTesting()
	err := s.Prefetch(metainfo.Hash{}, 0)
	if err == nil {
		t.Error("expected error for non-existent hash")
	}
}

func TestHealthSnapshot_NoCache_Extra(t *testing.T) {
	s := NewForTesting()
	health, active := s.HealthSnapshot(metainfo.Hash{})
	if active {
		t.Error("expected active=false")
	}
	if health != nil {
		t.Error("expected nil health")
	}
}

func TestSetFilePathResolver_ThenCall_Extra(t *testing.T) {
	s := NewForTesting()
	called := false
	s.SetFilePathResolver(func(hash metainfo.Hash, fileIdx int) (string, bool) {
		called = true
		return "/path/file.mp4", true
	})
	path, ok := s.filePathResolver(metainfo.Hash{}, 0)
	if !ok || path != "/path/file.mp4" {
		t.Errorf("got (%q, %v), want (%q, %v)", path, ok, "/path/file.mp4", true)
	}
	if !called {
		t.Error("resolver was not called")
	}
}

func TestTorrentImage_NotActive_Extra(t *testing.T) {
	s := NewForTesting()
	_, _, err := s.TorrentImage(nil, metainfo.Hash{})
	if err == nil {
		t.Error("expected error for non-active torrent")
	}
}

func TestReadArtBytes_EmptyRel_Extra(t *testing.T) {
	dir := t.TempDir()
	s := &Streamer{cfg: Config{DataDir: dir}}
	_, err := s.ReadArtBytes("")
	if err == nil {
		t.Error("expected error for empty rel path")
	}
}

func TestReadSidecar_NonExistent_Extra(t *testing.T) {
	s := NewForTesting()
	_, _, err := s.ReadSidecar(nil, metainfo.Hash{}, 0)
	if err == nil {
		t.Error("expected error for non-existent hash")
	}
}

func TestMetadataCacheGetSetHealth_Extra(t *testing.T) {
	dir := t.TempDir()
	mc, err := NewMetadataCache(filepath.Join(dir, "health.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer mc.Close()

	if err := mc.SetHealth("hash1", 5, 10); err != nil {
		t.Fatalf("SetHealth: %v", err)
	}
	h := mc.GetHealth("hash1")
	if h == nil {
		t.Fatal("expected health, got nil")
	}
	if h.Seeders != 5 || h.Peers != 10 {
		t.Errorf("health: seeders=%d peers=%d, want 5,10", h.Seeders, h.Peers)
	}
}

func TestMetadataCacheGetSetArt_Extra(t *testing.T) {
	dir := t.TempDir()
	mc, err := NewMetadataCache(filepath.Join(dir, "art.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer mc.Close()

	art := &CachedArt{Source: "tmdb", PosterURL: "https://example.com/poster.jpg", TmdbID: 123}
	if err := mc.SetArt("hash2", art); err != nil {
		t.Fatalf("SetArt: %v", err)
	}
	got := mc.GetArt("hash2")
	if got == nil {
		t.Fatal("expected art, got nil")
	}
	if got.Source != "tmdb" {
		t.Errorf("source=%q, want %q", got.Source, "tmdb")
	}
}

func TestDrop_NotProtected_Extra(t *testing.T) {
	s := NewForTesting()
	var h metainfo.Hash
	h.FromHexString("aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa")
	s.Drop(h)
}

func TestMetadataCache_NilGetters_Extra(t *testing.T) {
	var m *MetadataCache
	if got := m.Get("test"); got != nil {
		t.Error("expected nil from nil cache")
	}
	if got := m.GetArt("test"); got != nil {
		t.Error("expected nil from nil cache")
	}
	if got := m.GetHealth("test"); got != nil {
		t.Error("expected nil from nil cache")
	}
	if err := m.Set(nil); err != nil {
		t.Error(err)
	}
	if err := m.Set(&TorrentInfo{}); err != nil {
		t.Error(err)
	}
	if err := m.SetArt("test", nil); err != nil {
		t.Error(err)
	}
	if err := m.SetHealth("test", 0, 0); err != nil {
		t.Error(err)
	}
	if err := m.Close(); err != nil {
		t.Error(err)
	}
}

func TestArtSourceRank_Extra(t *testing.T) {
	if got := ArtSourceRank("torrent"); got != 4 {
		t.Errorf("torrent rank = %d", got)
	}
	if got := ArtSourceRank("unknown"); got != 0 {
		t.Errorf("unknown rank = %d", got)
	}
}

func TestVideoExtensions_Extra(t *testing.T) {
	if !videoExtensions[".mp4"] {
		t.Error(".mp4 should be video extension")
	}
	if videoExtensions[".txt"] {
		t.Error(".txt should NOT be video extension")
	}
}

func TestIsMagnet_EdgeCases_Extra(t *testing.T) {
	if isMagnet("http", "http://example.com") {
		t.Error("http URL should not be recognized as magnet")
	}
	if !isMagnet("magnet:", "magnet:?xt=urn:btih:abc") {
		t.Error("magnet URI should be recognized")
	}
}

func TestCleanSource_Empty_Extra(t *testing.T) {
	got := cleanSource("")
	if got != "" {
		t.Errorf("got %q", got)
	}
}

func TestHealthFreshForConst_Extra(t *testing.T) {
	if HealthFreshFor != 30*time.Minute {
		t.Errorf("HealthFreshFor = %v", HealthFreshFor)
	}
	if healthPeerWait != 6*time.Second {
		t.Errorf("healthPeerWait = %v", healthPeerWait)
	}
}

func TestMinMaxArtBytes_Extra(t *testing.T) {
	if minTorrentImageBytes != 10<<10 {
		t.Errorf("minTorrentImageBytes = %d", minTorrentImageBytes)
	}
	if maxTorrentImageBytes != 8<<20 {
		t.Errorf("maxTorrentImageBytes = %d", maxTorrentImageBytes)
	}
}

func TestNewFavorites_Extra(t *testing.T) {
	f, err := NewFavorites(filepath.Join(t.TempDir(), "fav.db"))
	if err != nil {
		t.Fatalf("NewFavorites: %v", err)
	}
	f.Close()
}

func TestDirSizeAndMTime_EmptyDir_Extra(t *testing.T) {
	dir := t.TempDir()
	size, _, err := dirSizeAndMTime(dir)
	if err != nil {
		t.Fatalf("dirSizeAndMTime: %v", err)
	}
	if size != 0 {
		t.Errorf("expected 0 size for empty dir, got %d", size)
	}
}

func TestApplyLimiter_Nil_Extra(t *testing.T) {
	applyLimiter(nil, 1000)
}
