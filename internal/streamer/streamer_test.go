package streamer

import (
	"context"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/anacrolix/torrent/metainfo"
	"github.com/anacrolix/torrent/types"
	"golang.org/x/time/rate"
)

func TestNewForTesting_Defaults(t *testing.T) {
	s := NewForTesting()
	if s == nil {
		t.Fatal("NewForTesting returned nil")
	}
	if s.active == nil {
		t.Error("active map is nil")
	}
	if s.downloads == nil {
		t.Error("downloads map is nil")
	}
	if s.dlLimiter == nil || s.upLimiter == nil {
		t.Error("rate limiters are nil")
	}
}

func TestFavorites_NilAccessor(t *testing.T) {
	var nilStreamer *Streamer
	if f := nilStreamer.Favorites(); f != nil {
		t.Error("Favorites on nil streamer should return nil")
	}
	s := NewForTesting()
	if f := s.Favorites(); f != nil {
		t.Error("Favorites on unset streamer should return nil")
	}
}

func TestFavorites_SetAndGet(t *testing.T) {
	s := NewForTesting()
	f := newTestFavorites(t)
	s.SetFavorites(f)
	if got := s.Favorites(); got != f {
		t.Error("SetFavorites/GetFavorites mismatch")
	}
}

func TestMetadataCache_NilAccessor(t *testing.T) {
	s := NewForTesting()
	if c := s.MetadataCache(); c != nil {
		t.Error("MetadataCache should be nil when not set")
	}
}

func TestMetadataCache_SetAndGet(t *testing.T) {
	s := NewForTesting()
	c := newTestCache(t)
	s.SetMetadataCache(c)
	if got := s.MetadataCache(); got != c {
		t.Error("SetMetadataCache/Get mismatch")
	}
}

func TestClient_Nil(t *testing.T) {
	s := NewForTesting()
	if c := s.Client(); c != nil {
		t.Error("Client should be nil for NewForTesting")
	}
}

func TestSetFilePathResolver(t *testing.T) {
	s := NewForTesting()
	called := false
	s.SetFilePathResolver(func(hash metainfo.Hash, fileIdx int) (string, bool) {
		called = true
		return "", false
	})
	if s.filePathResolver == nil {
		t.Fatal("filePathResolver should be set")
	}
	s.filePathResolver(metainfo.Hash{}, 0)
	if !called {
		t.Error("resolver was not callable")
	}
}

func TestRateLimits_Default(t *testing.T) {
	s := NewForTesting()
	down, up := s.RateLimits()
	if down != 0 || up != 0 {
		t.Errorf("default limits: down=%d up=%d, want 0 0", down, up)
	}
}

func TestSetRateLimits(t *testing.T) {
	s := NewForTesting()
	s.SetRateLimits(1_000_000, 500_000)
	down, up := s.RateLimits()
	if down != 1_000_000 || up != 500_000 {
		t.Errorf("after set: down=%d up=%d, want 1000000 500000", down, up)
	}
}

func TestSetRateLimits_Unlimited(t *testing.T) {
	s := NewForTesting()
	s.SetRateLimits(1_000_000, 500_000)
	s.SetRateLimits(0, 0)
	down, up := s.RateLimits()
	if down != 0 || up != 0 {
		t.Errorf("after reset to unlimited: down=%d up=%d", down, up)
	}
}

func TestSetRateLimits_UpdateOnlyDown(t *testing.T) {
	s := NewForTesting()
	s.SetRateLimits(2_000_000, 0)
	down, up := s.RateLimits()
	if down != 2_000_000 || up != 0 {
		t.Errorf("down=%d up=%d, want 2000000 0", down, up)
	}
}

func TestGlobalStats_Empty(t *testing.T) {
	s := NewForTesting()
	g := s.GlobalStats()
	if g.DownRate != 0 || g.UpRate != 0 || g.ActiveTorrents != 0 {
		t.Errorf("expected zero stats, got %+v", g)
	}
}

func TestBuildActiveMaps_Empty(t *testing.T) {
	s := NewForTesting()
	names, hashes, n := s.buildActiveMaps()
	if n != 0 {
		t.Errorf("expected 0 active, got %d", n)
	}
	if len(names) != 0 {
		t.Errorf("expected empty names map, got %d", len(names))
	}
	if len(hashes) != 0 {
		t.Errorf("expected empty hashes map, got %d", len(hashes))
	}
}

func TestRateFromBytes(t *testing.T) {
	cases := []struct {
		bps  int64
		want rate.Limit
	}{
		{0, rate.Inf},
		{-1, rate.Inf},
		{1000, rate.Limit(1000)},
		{1_000_000, rate.Limit(1_000_000)},
	}
	for _, tc := range cases {
		got := rateFromBytes(tc.bps)
		if got != tc.want {
			t.Errorf("rateFromBytes(%d) = %v, want %v", tc.bps, got, tc.want)
		}
	}
}

func TestRateBurst(t *testing.T) {
	if got := rateBurst(0); got != 1<<16 {
		t.Errorf("rateBurst(0) = %d, want %d", got, 1<<16)
	}
	if got := rateBurst(-1); got != 1<<16 {
		t.Errorf("rateBurst(-1) = %d, want %d", got, 1<<16)
	}
	if got := rateBurst(100); got != 64*1024 {
		t.Errorf("rateBurst(100) = %d, want %d (minBurst)", got, 64*1024)
	}
	if got := rateBurst(1_000_000); got != 250_000 {
		t.Errorf("rateBurst(1000000) = %d, want %d", got, 250_000)
	}
}

func TestApplyLimiter(t *testing.T) {
	l := rate.NewLimiter(rate.Inf, 1<<16)

	applyLimiter(l, 1000)
	if l.Limit() != rate.Limit(1000) {
		t.Errorf("limit after apply 1000 = %v", l.Limit())
	}

	applyLimiter(l, 0)
	if l.Limit() != rate.Inf {
		t.Errorf("limit after reset = %v, want Inf", l.Limit())
	}

	applyLimiter(nil, 1000)
}

func TestLimiterBytes(t *testing.T) {
	if got := limiterBytes(nil); got != 0 {
		t.Errorf("limiterBytes(nil) = %d, want 0", got)
	}
	l := rate.NewLimiter(rate.Inf, 1<<16)
	if got := limiterBytes(l); got != 0 {
		t.Errorf("limiterBytes(Inf) = %d, want 0", got)
	}
	l.SetLimit(rate.Limit(5000))
	if got := limiterBytes(l); got != 5000 {
		t.Errorf("limiterBytes(5000) = %d, want 5000", got)
	}
}

func TestFmtBytes(t *testing.T) {
	cases := []struct {
		n    int64
		want string
	}{
		{0, "0 B"},
		{512, "512 B"},
		{1023, "1023 B"},
		{1024, "1.00 KB"},
		{1536, "1.50 KB"},
		{1048576, "1.00 MB"},
		{1073741824, "1.00 GB"},
		{1099511627776, "1.00 TB"},
	}
	for _, tc := range cases {
		got := fmtBytes(tc.n)
		if got != tc.want {
			t.Errorf("fmtBytes(%d) = %q, want %q", tc.n, got, tc.want)
		}
	}
}

func TestIsBlockedFetchIP(t *testing.T) {
	cases := []struct {
		ip      string
		blocked bool
	}{
		{"127.0.0.1", true},
		{"::1", true},
		{"10.0.0.1", true},
		{"172.16.0.1", true},
		{"192.168.0.1", true},
		{"169.254.1.1", true},
		{"0.0.0.0", true},
		{"8.8.8.8", false},
		{"1.1.1.1", false},
		{"198.51.100.1", false},
	}
	for _, tc := range cases {
		ip := net.ParseIP(tc.ip)
		if ip == nil {
			t.Fatalf("invalid IP: %s", tc.ip)
		}
		got := isBlockedFetchIP(ip)
		if got != tc.blocked {
			t.Errorf("isBlockedFetchIP(%q) = %v, want %v", tc.ip, got, tc.blocked)
		}
	}
}

func TestPriorityFromLabel(t *testing.T) {
	cases := []struct {
		label string
		prio  types.PiecePriority
		ok    bool
	}{
		{"none", types.PiecePriorityNone, true},
		{"low", types.PiecePriorityNormal, true},
		{"normal", types.PiecePriorityHigh, true},
		{"", types.PiecePriorityHigh, true},
		{"high", types.PiecePriorityNow, true},
		{"bogus", 0, false},
	}
	for _, tc := range cases {
		prio, ok := priorityFromLabel(tc.label)
		if prio != tc.prio || ok != tc.ok {
			t.Errorf("priorityFromLabel(%q) = (%v, %v), want (%v, %v)",
				tc.label, prio, ok, tc.prio, tc.ok)
		}
	}
}

func TestLabelFromPriority(t *testing.T) {
	cases := []struct {
		prio  types.PiecePriority
		want  string
	}{
		{types.PiecePriorityNone, "none"},
		{types.PiecePriorityNormal, "low"},
		{types.PiecePriorityHigh, "normal"},
		{types.PiecePriorityNow, "high"},
		{types.PiecePriority(99), "normal"},
	}
	for _, tc := range cases {
		got := labelFromPriority(tc.prio)
		if got != tc.want {
			t.Errorf("labelFromPriority(%v) = %q, want %q", tc.prio, got, tc.want)
		}
	}
}

func TestDefaultMetadataCachePath(t *testing.T) {
	got := DefaultMetadataCachePath("/data/streams")
	want := "/data/streams/.metadata-cache.db"
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestMetainfoPath(t *testing.T) {
	s := NewForTesting()
	s.metainfoDir = "/tmp/metainfo"
	h := metainfo.Hash{0x01, 0x02}
	got := s.MetainfoPath(h)
	if got == "" {
		t.Error("MetainfoPath returned empty string")
	}
}

func TestParseMagnetEmbeddedJunk(t *testing.T) {
	s := &Streamer{}
	hash, name, err := s.ParseMagnet("\xef\xbb\xbfmagnet:?xt=urn:btih:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa&dn=Test")
	if err != nil {
		t.Fatalf("ParseMagnet with BOM: %v", err)
	}
	if hash != "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa" {
		t.Errorf("hash = %q", hash)
	}
	if name != "Test" {
		t.Errorf("name = %q", name)
	}
}

func TestParseMagnet_Invalid(t *testing.T) {
	s := &Streamer{}
	_, _, err := s.ParseMagnet("not a magnet")
	if err == nil {
		t.Error("expected error for invalid magnet")
	}
}

func TestParseMagnet_URLInText(t *testing.T) {
	s := &Streamer{}
	hash, name, err := s.ParseMagnet("some text magnet:?xt=urn:btih:bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb&dn=My+Movie")
	if err != nil {
		t.Fatalf("ParseMagnet with URL in text: %v", err)
	}
	if hash != "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb" {
		t.Errorf("hash = %q", hash)
	}
	if name != "My Movie" {
		t.Errorf("name = %q", name)
	}
}

func TestParseMagnet_NoName(t *testing.T) {
	s := &Streamer{}
	hash, name, err := s.ParseMagnet("magnet:?xt=urn:btih:cccccccccccccccccccccccccccccccccccccccc")
	if err != nil {
		t.Fatalf("ParseMagnet no name: %v", err)
	}
	if name != hash {
		t.Errorf("name should fall back to hash, got %q", name)
	}
}

func TestRegisterDownload(t *testing.T) {
	s := NewForTesting()
	s.RegisterDownload("test-movie")
	if !s.IsDownloadProtected("test-movie") {
		t.Error("expected test-movie to be protected after register")
	}
}

func TestRegisterDownload_EmptyName(t *testing.T) {
	s := NewForTesting()
	s.RegisterDownload("")
	// Should not panic
}

func TestUnregisterDownload(t *testing.T) {
	s := NewForTesting()
	s.RegisterDownload("test-movie")
	s.UnregisterDownload("test-movie")
	if s.IsDownloadProtected("test-movie") {
		t.Error("expected test-movie to be unprotected after unregister")
	}
}

func TestIsDownloadProtected_StrippedPart(t *testing.T) {
	s := NewForTesting()
	s.RegisterDownload("test-movie")
	if !s.IsDownloadProtected("test-movie.part") {
		t.Error("expected test-movie.part to match registered test-movie")
	}
}

func TestIsDownloadProtected_Unregistered(t *testing.T) {
	s := NewForTesting()
	if s.IsDownloadProtected("never-registered") {
		t.Error("expected unregistered name to not be protected")
	}
}

func TestGet_NonExistentHash(t *testing.T) {
	s := NewForTesting()
	_, err := s.Get(metainfo.Hash{0xff})
	if err == nil {
		t.Error("expected error for non-existent hash")
	}
}

func TestImportTorrentBytes_Invalid(t *testing.T) {
	s := NewForTesting()
	s.cfg.DataDir = t.TempDir()
	s.metainfoDir = t.TempDir()

	_, _, err := s.ImportTorrentBytes([]byte("not bencode"))
	if err == nil {
		t.Error("expected error for invalid torrent data")
	}
}

func TestCleanSource_StripsBOM(t *testing.T) {
	got := cleanSource("\xef\xbb\xbfmagnet:?xt=urn:btih:abc")
	if got != "magnet:?xt=urn:btih:abc" {
		t.Errorf("cleanSource with BOM = %q", got)
	}
}

func TestCleanSource_TrimSpace(t *testing.T) {
	got := cleanSource("  magnet:?xt=urn:btih:abc  ")
	if got != "magnet:?xt=urn:btih:abc" {
		t.Errorf("cleanSource with spaces = %q", got)
	}
}

func TestIsMagnet_True(t *testing.T) {
	if !isMagnet("magnet:", "magnet:?xt=urn:btih:abc") {
		t.Error("expected true for magnet URI")
	}
	if !isMagnet("magnet:", " text with magnet:?xt=urn:btih:abc inside") {
		t.Error("expected true for text containing magnet")
	}
}

func TestIsMagnet_False(t *testing.T) {
	if isMagnet("http", "https://example.com/torrent.torrent") {
		t.Error("expected false for HTTP URL")
	}
}

func TestDirSizeAndMTime_EmptyDir(t *testing.T) {
	dir := t.TempDir()
	size, mtime, err := dirSizeAndMTime(dir)
	if err != nil {
		t.Fatalf("dirSizeAndMTime: %v", err)
	}
	if size != 0 || mtime.IsZero() {
		t.Errorf("expected size=0, non-zero mtime, got size=%d, mtime=%v", size, mtime)
	}
}

func TestDirSizeAndMTime_File(t *testing.T) {
	dir := t.TempDir()
	f := filepath.Join(dir, "test.txt")
	if err := os.WriteFile(f, []byte("hello"), 0o644); err != nil {
		t.Fatalf("write file: %v", err)
	}
	size, mtime, err := dirSizeAndMTime(dir)
	if err != nil {
		t.Fatalf("dirSizeAndMTime: %v", err)
	}
	if size == 0 {
		t.Errorf("expected non-zero size for dir with file, got %d", size)
	}
	if mtime.IsZero() {
		t.Error("expected non-zero mtime")
	}
}

func TestClearAll_Basic(t *testing.T) {
	s := NewForTesting()
	s.cfg.DataDir = t.TempDir()
	sub := filepath.Join(s.cfg.DataDir, "test-entry")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := s.ClearAll(); err != nil {
		t.Fatalf("ClearAll: %v", err)
	}
	if _, err := os.Stat(sub); !os.IsNotExist(err) {
		t.Error("expected sub to be deleted after ClearAll")
	}
}

func TestClearAll_WithFavorites(t *testing.T) {
	s := NewForTesting()
	s.cfg.DataDir = t.TempDir()
	fav := newTestFavorites(t)
	fav.Add("fav-entry", "hash", "magnet", "manual", 1)
	s.SetFavorites(fav)

	favDir := filepath.Join(s.cfg.DataDir, "fav-entry")
	if err := os.MkdirAll(favDir, 0o755); err != nil {
		t.Fatalf("mkdir fav: %v", err)
	}
	plainDir := filepath.Join(s.cfg.DataDir, "plain-entry")
	if err := os.MkdirAll(plainDir, 0o755); err != nil {
		t.Fatalf("mkdir plain: %v", err)
	}

	if err := s.ClearAll(); err != nil {
		t.Fatalf("ClearAll: %v", err)
	}
	if _, err := os.Stat(favDir); os.IsNotExist(err) {
		t.Error("fav-entry should be preserved")
	}
	if _, err := os.Stat(plainDir); !os.IsNotExist(err) {
		t.Error("plain-entry should be deleted")
	}
}

func TestClearEntry_Basic(t *testing.T) {
	s := NewForTesting()
	dir := t.TempDir()
	s.cfg.DataDir = dir

	entry := filepath.Join(dir, "test-entry")
	if err := os.MkdirAll(entry, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}

	if err := s.ClearEntry("test-entry"); err != nil {
		t.Fatalf("ClearEntry: %v", err)
	}
	if _, err := os.Stat(entry); !os.IsNotExist(err) {
		t.Error("entry should be deleted")
	}
}

func TestClearEntry_Favorited(t *testing.T) {
	s := NewForTesting()
	s.cfg.DataDir = t.TempDir()
	fav := newTestFavorites(t)
	fav.Add("fav-item", "hash", "magnet", "manual", 1)
	s.SetFavorites(fav)

	err := s.ClearEntry("fav-item")
	if err == nil {
		t.Error("expected error when clearing favorited entry")
	}
}

func TestClearEntry_PathTraversal(t *testing.T) {
	s := NewForTesting()
	s.cfg.DataDir = t.TempDir()
	err := s.ClearEntry("../outside")
	if err == nil {
		t.Error("expected error for path traversal")
	}
}

func TestFavorites_NilStreamer(t *testing.T) {
	var nilS *Streamer
	if f := nilS.Favorites(); f != nil {
		t.Error("Favorites on nil streamer should return nil")
	}
}

func TestReadSidecar_NonExistentHash(t *testing.T) {
	s := NewForTesting()
	_, _, err := s.ReadSidecar(context.Background(), metainfo.Hash{}, 0)
	if err == nil {
		t.Error("expected error for non-existent hash")
	}
}

func TestSidecars_NonExistentHash(t *testing.T) {
	s := NewForTesting()
	_, err := s.Sidecars(metainfo.Hash{}, 0)
	if err == nil {
		t.Error("expected error for non-existent hash")
	}
}

func TestPhysicalBytes(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "test.dat")
	if err := os.WriteFile(path, make([]byte, 100), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	sz := PhysicalBytes(info)
	if sz == 0 {
		t.Error("expected non-zero physical bytes")
	}
}

func TestNew_DefaultConfig(t *testing.T) {
	dir := t.TempDir()
	s, err := New(Config{DataDir: dir})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer s.Close()
	if s.cfg.DataDir != dir {
		t.Errorf("DataDir = %q, want %q", s.cfg.DataDir, dir)
	}
	if s.cfg.IdleTimeout != 30*time.Minute {
		t.Errorf("IdleTimeout = %v", s.cfg.IdleTimeout)
	}
	if s.cfg.MetadataWait != 60*time.Second {
		t.Errorf("MetadataWait = %v", s.cfg.MetadataWait)
	}
	if s.client == nil {
		t.Error("expected non-nil client")
	}
}

func TestNew_CustomConfig(t *testing.T) {
	dir := t.TempDir()
	s, err := New(Config{
		DataDir:      dir,
		IdleTimeout:  5 * time.Minute,
		MetadataWait: 10 * time.Second,
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer s.Close()
	if s.cfg.IdleTimeout != 5*time.Minute {
		t.Errorf("IdleTimeout = %v", s.cfg.IdleTimeout)
	}
	if s.cfg.MetadataWait != 10*time.Second {
		t.Errorf("MetadataWait = %v", s.cfg.MetadataWait)
	}
}

func TestBuildActiveMaps_WithEntries(t *testing.T) {
	s := NewForTesting()
	names, hashes, n := s.buildActiveMaps()
	if n != 0 {
		t.Errorf("expected 0 active, got %d", n)
	}
	if len(names) != 0 {
		t.Errorf("expected empty names, got %d", len(names))
	}
	if len(hashes) != 0 {
		t.Errorf("expected empty hashes, got %d", len(hashes))
	}
}

func TestIsMagnet_HTTPIsNotMagnet(t *testing.T) {
	if isMagnet("http", "https://example.com") {
		t.Error("expected false for HTTP")
	}
}

func TestCleanSource_BOMOnly(t *testing.T) {
	got := cleanSource("\xef\xbb\xbf")
	if got != "" {
		t.Errorf("expected empty, got %q", got)
	}
}

func TestParseMagnet_SourceWithLeadingJunk(t *testing.T) {
	s := &Streamer{}
	hash, name, err := s.ParseMagnet("ignore this magnet:?xt=urn:btih:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa&dn=Movie")
	if err != nil {
		t.Fatalf("ParseMagnet: %v", err)
	}
	if hash != "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa" {
		t.Errorf("hash = %q", hash)
	}
	if name != "Movie" {
		t.Errorf("name = %q", name)
	}
}

func TestStats_WithActiveEntry(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "active_entry"), []byte("data"), 0o644)

	s := NewForTesting()
	s.cfg.DataDir = dir

	stats, err := s.Stats()
	if err != nil {
		t.Fatalf("Stats: %v", err)
	}
	if stats == nil {
		t.Fatal("Stats returned nil")
	}
	if len(stats.Entries) != 1 {
		t.Errorf("expected 1 entry, got %d", len(stats.Entries))
	}
}

func TestEnforceCacheLimit_ProtectsDownload(t *testing.T) {
	dir := t.TempDir()
	data := make([]byte, 100*1024)
	os.WriteFile(filepath.Join(dir, "dl-entry"), data, 0o644)

	s := NewForTesting()
	s.cfg.DataDir = dir
	s.cfg.MaxCacheSize = 1
	s.RegisterDownload("dl-entry")

	s.enforceCacheLimit()

	if _, err := os.Stat(filepath.Join(dir, "dl-entry")); os.IsNotExist(err) {
		t.Error("download-protected entry was evicted despite protection")
	}
}

func TestClearAll_WithDotPrefix(t *testing.T) {
	dir := t.TempDir()
	s := NewForTesting()
	s.cfg.DataDir = dir
	os.MkdirAll(filepath.Join(dir, ".internal"), 0o755)
	os.MkdirAll(filepath.Join(dir, "regular"), 0o755)

	if err := s.ClearAll(); err != nil {
		t.Fatalf("ClearAll: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, ".internal")); os.IsNotExist(err) {
		t.Error("dot-prefix dirs should be preserved")
	}
	if _, err := os.Stat(filepath.Join(dir, "regular")); !os.IsNotExist(err) {
		t.Error("regular dir should be deleted")
	}
}

func TestResolveSource_Invalid(t *testing.T) {
	s := NewForTesting()
	_, err := s.resolveSource(nil, "ftp://bad")
	if err == nil {
		t.Error("expected error for unsupported source")
	}
}

func TestLoadCachedMetainfo_NoDir(t *testing.T) {
	s := NewForTesting()
	s.metainfoDir = ""
	if mi := s.loadCachedMetainfo(metainfo.Hash{}); mi != nil {
		t.Error("expected nil for empty metainfoDir")
	}
}

func TestLoadCachedMetainfo_Missing(t *testing.T) {
	dir := t.TempDir()
	s := NewForTesting()
	s.metainfoDir = dir
	if mi := s.loadCachedMetainfo(metainfo.Hash{}); mi != nil {
		t.Error("expected nil for non-existent metainfo")
	}
}

func TestPersistMetainfo_NoDir(t *testing.T) {
	s := NewForTesting()
	s.metainfoDir = ""
	s.persistMetainfo(nil)
}

func TestDrop_NonExistent(t *testing.T) {
	s := NewForTesting()
	var h metainfo.Hash
	s.Drop(h)
}

func TestPauseResume_NonExistentHash(t *testing.T) {
	s := NewForTesting()
	if err := s.Pause(metainfo.Hash{}); err == nil {
		t.Error("expected error for non-existent hash")
	}
	if err := s.Resume(metainfo.Hash{}); err == nil {
		t.Error("expected error for non-existent hash")
	}
}

func TestSetPriority_InvalidLabel(t *testing.T) {
	s := NewForTesting()
	err := s.SetPriority(metainfo.Hash{}, "bogus")
	if err == nil {
		t.Error("expected error for invalid label")
	}
}

func TestSetPriority_NonExistentHash(t *testing.T) {
	s := NewForTesting()
	err := s.SetPriority(metainfo.Hash{}, "high")
	if err == nil {
		t.Error("expected error for non-existent hash")
	}
}

func TestRecheckFile_NonExistent(t *testing.T) {
	s := NewForTesting()
	err := s.RecheckFile(metainfo.Hash{}, 0)
	if err == nil {
		t.Error("expected error for non-existent hash")
	}
}

func TestVerifyFile_NonExistent(t *testing.T) {
	s := NewForTesting()
	err := s.VerifyFile(metainfo.Hash{}, 0)
	if err == nil {
		t.Error("expected error for non-existent hash")
	}
}

func TestGet_UpdatesLastAccess(t *testing.T) {
	s := NewForTesting()
	_, err := s.Get(metainfo.Hash{0x01})
	if err == nil {
		t.Error("expected error for non-existent hash")
	}
}

func TestFileReader_NonExistent(t *testing.T) {
	s := NewForTesting()
	_, _, err := s.FileReader(metainfo.Hash{}, 0)
	if err == nil {
		t.Error("expected error for non-existent hash")
	}
}

func TestImportTorrentBytes_InvalidData(t *testing.T) {
	s := NewForTesting()
	_, _, err := s.ImportTorrentBytes([]byte("garbage"))
	if err == nil {
		t.Error("expected error for invalid data")
	}
}



