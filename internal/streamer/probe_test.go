package streamer

import (
	"io"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/anacrolix/torrent/metainfo"
)

func TestIsImageSubtitle(t *testing.T) {
	tests := []struct {
		codec string
		want  bool
	}{
		{"hdmv_pgs_subtitle", true},
		{"dvd_subtitle", true},
		{"dvdsub", true},
		{"pgssub", true},
		{"xsub", true},
		{"subrip", false},
		{"ass", false},
		{"webvtt", false},
		{"h264", false},
		{"aac", false},
		{"", false},
	}
	for _, tc := range tests {
		got := isImageSubtitle(tc.codec)
		if got != tc.want {
			t.Errorf("isImageSubtitle(%q) = %v, want %v", tc.codec, got, tc.want)
		}
	}
}

func TestParseProbeOutput(t *testing.T) {
	json := `{
		"streams": [
			{"index": 0, "codec_type": "video", "codec_name": "hevc", "channels": 0},
			{"index": 1, "codec_type": "audio", "codec_name": "aac", "channels": 2, "tags": {"language": "eng"}},
			{"index": 2, "codec_type": "audio", "codec_name": "ac3", "channels": 6, "tags": {"language": "por"}},
			{"index": 3, "codec_type": "subtitle", "codec_name": "subrip", "tags": {"language": "por"}},
			{"index": 4, "codec_type": "subtitle", "codec_name": "hdmv_pgs_subtitle", "tags": {"language": "eng"}}
		],
		"format": {"duration": "120.500"}
	}`
	result, err := parseProbeOutput([]byte(json))
	if err != nil {
		t.Fatalf("parseProbeOutput: %v", err)
	}
	if len(result.Audio) != 2 {
		t.Errorf("expected 2 audio tracks, got %d", len(result.Audio))
	}
	if len(result.Subtitles) != 2 {
		t.Errorf("expected 2 subtitle tracks, got %d", len(result.Subtitles))
	}
	if result.DurationSec != 120.5 {
		t.Errorf("DurationSec = %f, want 120.5", result.DurationSec)
	}
	if !result.Subtitles[1].Image {
		t.Error("expected PGS subtitle to be marked as image")
	}
	if result.Subtitles[0].Image {
		t.Error("expected subrip subtitle NOT to be marked as image")
	}
	if result.Audio[0].Codec != "aac" {
		t.Errorf("audio[0] codec = %q, want 'aac'", result.Audio[0].Codec)
	}
	if result.Audio[0].Language != "eng" {
		t.Errorf("audio[0] lang = %q, want 'eng'", result.Audio[0].Language)
	}
}

func TestParseProbeOutput_Empty(t *testing.T) {
	json := `{"streams":[],"format":{}}`
	result, err := parseProbeOutput([]byte(json))
	if err != nil {
		t.Fatalf("parseProbeOutput empty: %v", err)
	}
	if len(result.Audio) != 0 {
		t.Errorf("expected 0 audio, got %d", len(result.Audio))
	}
	if len(result.Subtitles) != 0 {
		t.Errorf("expected 0 subtitles, got %d", len(result.Subtitles))
	}
	if result.DurationSec != 0 {
		t.Errorf("DurationSec = %f, want 0", result.DurationSec)
	}
}

func TestParseProbeOutput_InvalidJSON(t *testing.T) {
	_, err := parseProbeOutput([]byte(`not json`))
	if err == nil {
		t.Fatal("expected error for invalid JSON")
	}
}

func TestParseProbeOutput_DefaultDisposition(t *testing.T) {
	json := `{
		"streams": [
			{"index": 0, "codec_type": "audio", "codec_name": "aac", "disposition": {"default": 1, "forced": 0}},
			{"index": 1, "codec_type": "subtitle", "codec_name": "subrip", "disposition": {"default": 0, "forced": 1}}
		],
		"format": {}
	}`
	result, err := parseProbeOutput([]byte(json))
	if err != nil {
		t.Fatalf("parseProbeOutput: %v", err)
	}
	if !result.Audio[0].Default {
		t.Error("expected audio track to be default")
	}
	if result.Audio[0].Forced {
		t.Error("expected audio track NOT to be forced")
	}
	if !result.Subtitles[0].Forced {
		t.Error("expected subtitle track to be forced")
	}
}

func TestParseProbeOutput_UnknownStreamType(t *testing.T) {
	json := `{
		"streams": [
			{"index": 0, "codec_type": "data", "codec_name": "timestamp"}
		],
		"format": {}
	}`
	result, err := parseProbeOutput([]byte(json))
	if err != nil {
		t.Fatalf("parseProbeOutput: %v", err)
	}
	if len(result.Audio) != 0 || len(result.Subtitles) != 0 {
		t.Errorf("expected 0 tracks for data-only stream, got audio=%d sub=%d", len(result.Audio), len(result.Subtitles))
	}
}

func TestParseProbeOutput_FormatDuration(t *testing.T) {
	json := `{
		"streams": [],
		"format": {"duration": "abc"}
	}`
	result, err := parseProbeOutput([]byte(json))
	if err != nil {
		t.Fatalf("parseProbeOutput: %v", err)
	}
	if result.DurationSec != 0 {
		t.Errorf("expected 0 duration for unparseable string, got %f", result.DurationSec)
	}
}

func TestSaveAndReadArtBytes(t *testing.T) {
	dir := t.TempDir()
	s := &Streamer{cfg: Config{DataDir: dir}}

	hash := metainfo.HashBytes([]byte("test-hash-content"))
	data := []byte("fake-jpeg-data")

	rel, err := s.SaveArtBytes(hash, data)
	if err != nil {
		t.Fatalf("SaveArtBytes: %v", err)
	}
	if rel == "" {
		t.Fatal("expected non-empty relative path")
	}

	read, err := s.ReadArtBytes(rel)
	if err != nil {
		t.Fatalf("ReadArtBytes: %v", err)
	}
	if string(read) != string(data) {
		t.Errorf("read data = %q, want %q", string(read), string(data))
	}
}


func TestReadArtBytes_NonExistent(t *testing.T) {
	dir := t.TempDir()
	s := &Streamer{cfg: Config{DataDir: dir}}

	_, err := s.ReadArtBytes(".art/nonexistent.jpg")
	if err == nil {
		t.Error("expected error for non-existent file")
	}
}

func TestComputeOSHash_SmallFile(t *testing.T) {
	r := &readSeekerWrapper{data: []byte("small")}
	_, err := computeOSHash(r, 5)
	if err == nil {
		t.Error("expected error for file smaller than 64KB")
	}
}

func TestComputeOSHash_Valid(t *testing.T) {
	data := make([]byte, 128*1024)
	for i := range data {
		data[i] = byte(i % 256)
	}
	r := &readSeekerWrapper{data: data}
	hash, err := computeOSHash(r, int64(len(data)))
	if err != nil {
		t.Fatalf("computeOSHash: %v", err)
	}
	if hash == "" {
		t.Error("expected non-empty hash")
	}
	if len(hash) != 16 {
		t.Errorf("hash length = %d, want 16 hex chars", len(hash))
	}
}

func TestComputeOSHash_ZeroFile(t *testing.T) {
	r := &readSeekerWrapper{data: []byte{}}
	_, err := computeOSHash(r, 0)
	if err == nil {
		t.Error("expected error for zero-length file")
	}
}

func TestComputeOSHash_Exact64K(t *testing.T) {
	data := make([]byte, 64*1024)
	for i := range data {
		data[i] = byte(i)
	}
	r := &readSeekerWrapper{data: data}
	hash, err := computeOSHash(r, int64(len(data)))
	if err != nil {
		t.Fatalf("computeOSHash: %v", err)
	}
	if hash == "" {
		t.Error("expected non-empty hash for 64KB file")
	}
}

func TestComputeFileOSHash(t *testing.T) {
	data := make([]byte, 128*1024)
	for i := range data {
		data[i] = byte(i % 256)
	}
	r := &readSeekerWrapper{data: data}
	result, err := ComputeFileOSHash(r, int64(len(data)))
	if err != nil {
		t.Fatalf("ComputeFileOSHash: %v", err)
	}
	if result.Hash == "" {
		t.Error("expected non-empty hash")
	}
	if result.Size != int64(len(data)) {
		t.Errorf("Size = %d, want %d", result.Size, len(data))
	}
}

func TestComputeFileOSHash_Small(t *testing.T) {
	r := &readSeekerWrapper{data: []byte("tiny")}
	_, err := ComputeFileOSHash(r, 5)
	if err == nil {
		t.Error("expected error for small file")
	}
}

type readSeekerWrapper struct {
	data   []byte
	offset int
}

func (r *readSeekerWrapper) Read(p []byte) (int, error) {
	if r.offset >= len(r.data) {
		return 0, nil
	}
	n := copy(p, r.data[r.offset:])
	r.offset += n
	return n, nil
}

func (r *readSeekerWrapper) Seek(offset int64, whence int) (int64, error) {
	switch whence {
	case io.SeekStart:
		r.offset = int(offset)
	case io.SeekCurrent:
		r.offset += int(offset)
	case io.SeekEnd:
		r.offset = len(r.data) + int(offset)
	}
	if r.offset < 0 {
		r.offset = 0
	}
	if r.offset > len(r.data) {
		r.offset = len(r.data)
	}
	return int64(r.offset), nil
}

func TestActiveEntry_NilForEmpty(t *testing.T) {
	s := NewForTesting()
	hash := metainfo.Hash{}
	e := s.activeEntry(hash)
	if e != nil {
		t.Error("expected nil activeEntry for empty streamer")
	}
}

func TestProbeHealthAsync_EmptyMagnetNoOp(t *testing.T) {
	s := NewForTesting()
	hash := metainfo.Hash{}
	s.ProbeHealthAsync(hash, "")
}

func TestDefaultMetadataCachePath(t *testing.T) {
	got := DefaultMetadataCachePath("/data")
	expected := filepath.Join("/data", ".metadata-cache.db")
	if got != expected {
		t.Errorf("got %q, want %q", got, expected)
	}
}

func TestDefaultMetadataCachePath_Empty(t *testing.T) {
	got := DefaultMetadataCachePath("")
	if got == "" {
		t.Error("expected non-empty path")
	}
}

func TestMetadataCache_NewAndClose(t *testing.T) {
	dir := t.TempDir()
	cache, err := NewMetadataCache(filepath.Join(dir, "meta.db"))
	if err != nil {
		t.Fatalf("NewMetadataCache: %v", err)
	}
	if err := cache.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
}

func TestMetadataCache_SetAndGetMeta(t *testing.T) {
	dir := t.TempDir()
	cache, err := NewMetadataCache(filepath.Join(dir, "meta.db"))
	if err != nil {
		t.Fatalf("NewMetadataCache: %v", err)
	}
	defer cache.Close()

	info := &TorrentInfo{
		InfoHash:  "test-hash",
		Name:      "Test Movie",
		TotalSize: 1024,
		Files: []FileInfo{
			{Index: 0, Path: "test.mkv", Size: 1024, IsVideo: true},
		},
		PrimaryFile: 0,
	}
	if err := cache.Set(info); err != nil {
		t.Fatalf("Set: %v", err)
	}

	got := cache.Get("test-hash")
	if got == nil {
		t.Fatal("expected non-nil CachedMeta")
	}
	if got.Name != "Test Movie" {
		t.Errorf("Name = %q, want 'Test Movie'", got.Name)
	}
	if got.TotalSize != 1024 {
		t.Errorf("TotalSize = %d, want 1024", got.TotalSize)
	}
	if len(got.Files) != 1 {
		t.Errorf("len(Files) = %d, want 1", len(got.Files))
	}
}

func TestMetadataCache_GetNonExistent(t *testing.T) {
	dir := t.TempDir()
	cache, err := NewMetadataCache(filepath.Join(dir, "meta.db"))
	if err != nil {
		t.Fatalf("NewMetadataCache: %v", err)
	}
	defer cache.Close()

	got := cache.Get("nonexistent")
	if got != nil {
		t.Error("expected nil for non-existent key")
	}
}

func TestMetadataCache_SetAndGetArt(t *testing.T) {
	dir := t.TempDir()
	cache, err := NewMetadataCache(filepath.Join(dir, "meta.db"))
	if err != nil {
		t.Fatalf("NewMetadataCache: %v", err)
	}
	defer cache.Close()

	art := &CachedArt{
		Source:    "tmdb",
		PosterURL: "https://example.com/poster.jpg",
		TmdbID:    123,
	}
	if err := cache.SetArt("hash123", art); err != nil {
		t.Fatalf("SetArt: %v", err)
	}

	got := cache.GetArt("hash123")
	if got == nil {
		t.Fatal("expected non-nil CachedArt")
	}
	if got.Source != "tmdb" {
		t.Errorf("Source = %q, want 'tmdb'", got.Source)
	}
	if got.PosterURL != "https://example.com/poster.jpg" {
		t.Errorf("PosterURL = %q", got.PosterURL)
	}
}

func TestMetadataCache_GetArtNonExistent(t *testing.T) {
	dir := t.TempDir()
	cache, err := NewMetadataCache(filepath.Join(dir, "meta.db"))
	if err != nil {
		t.Fatalf("NewMetadataCache: %v", err)
	}
	defer cache.Close()

	got := cache.GetArt("nonexistent")
	if got != nil {
		t.Error("expected nil for non-existent art")
	}
}

func TestMetadataCache_SetAndGetHealth(t *testing.T) {
	dir := t.TempDir()
	cache, err := NewMetadataCache(filepath.Join(dir, "meta.db"))
	if err != nil {
		t.Fatalf("NewMetadataCache: %v", err)
	}
	defer cache.Close()

	if err := cache.SetHealth("h1", 5, 10); err != nil {
		t.Fatalf("SetHealth: %v", err)
	}

	h := cache.GetHealth("h1")
	if h == nil {
		t.Fatal("expected non-nil health")
	}
	if h.Seeders != 5 {
		t.Errorf("Seeders = %d, want 5", h.Seeders)
	}
	if h.Peers != 10 {
		t.Errorf("Peers = %d, want 10", h.Peers)
	}
	if h.CheckedAt.IsZero() {
		t.Error("CheckedAt is zero")
	}
}

func TestMetadataCache_GetHealthFallback(t *testing.T) {
	dir := t.TempDir()
	cache, err := NewMetadataCache(filepath.Join(dir, "meta.db"))
	if err != nil {
		t.Fatalf("NewMetadataCache: %v", err)
	}
	defer cache.Close()

	h := cache.GetHealth("nonexistent")
	if h != nil {
		t.Error("expected nil health for non-existent key")
	}
}

func TestTorrentImage_NotActive(t *testing.T) {
	s := NewForTesting()
	_, _, err := s.TorrentImage(nil, metainfo.Hash{})
	if err == nil {
		t.Error("expected error for non-active torrent")
	}
}

func TestHealthSnapshot_UnknownHash(t *testing.T) {
	dir := t.TempDir()
	c, err := NewMetadataCache(filepath.Join(dir, "meta.db"))
	if err != nil {
		t.Fatalf("NewMetadataCache: %v", err)
	}
	defer c.Close()

	s := NewForTesting()
	s.cache = c

	var h metainfo.Hash
	h.FromHexString("aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa")
	health, active := s.HealthSnapshot(h)
	if active {
		t.Error("expected active=false")
	}
	if health != nil {
		t.Error("expected nil health for unknown hash")
	}
}

func TestMetainfoPath(t *testing.T) {
	dir := t.TempDir()
	s := &Streamer{cfg: Config{DataDir: dir}}
	s.metainfoDir = filepath.Join(dir, ".metainfo")
	hash := metainfo.HashBytes([]byte("test"))
	got := s.MetainfoPath(hash)
	if !strings.HasSuffix(got, hash.HexString()+".torrent") {
		t.Errorf("MetainfoPath = %q, doesn't end with hash.torrent", got)
	}
}

func TestRegisterDownload_Empty(t *testing.T) {
	s := NewForTesting()
	s.RegisterDownload("")
	if len(s.downloads) != 0 {
		t.Errorf("expected 0 downloads after empty register, got %d", len(s.downloads))
	}
}

func TestRegisterDownload_AndUnregister(t *testing.T) {
	s := NewForTesting()
	s.RegisterDownload("test-name")
	if !s.IsDownloadProtected("test-name") {
		t.Error("expected test-name to be protected")
	}
	s.UnregisterDownload("test-name")
	if s.IsDownloadProtected("test-name") {
		t.Error("expected test-name to NOT be protected after unregister")
	}
}

func TestIsDownloadProtected_PartSuffix(t *testing.T) {
	s := NewForTesting()
	s.RegisterDownload("test-name")
	if !s.IsDownloadProtected("test-name.part") {
		t.Error("expected test-name.part to be protected via test-name")
	}
}

func TestIsDownloadProtected_NoMatch(t *testing.T) {
	s := NewForTesting()
	s.RegisterDownload("real-name")
	if s.IsDownloadProtected("other-name") {
		t.Error("expected other-name to NOT be protected")
	}
}

func TestSetFilePathResolver(t *testing.T) {
	s := NewForTesting()
	resolver := FilePathResolver(func(hash metainfo.Hash, fileIdx int) (string, bool) {
		return "/path/to/file", true
	})
	s.SetFilePathResolver(resolver)
	if s.filePathResolver == nil {
		t.Error("expected filePathResolver to be set")
	}
}

func TestSetFavorites(t *testing.T) {
	s := NewForTesting()
	f := &FavoritesStore{}
	s.SetFavorites(f)
	if s.favs == nil {
		t.Error("expected favorites to be set")
	}
}

func TestSetMetadataCache(t *testing.T) {
	s := NewForTesting()
	if s.MetadataCache() != nil {
		t.Error("expected nil cache initially")
	}
	dir := t.TempDir()
	c, _ := NewMetadataCache(filepath.Join(dir, "m.db"))
	defer c.Close()
	s.SetMetadataCache(c)
	if s.MetadataCache() == nil {
		t.Error("expected non-nil cache after SetMetadataCache")
	}
}

func TestClient_Nil(t *testing.T) {
	s := NewForTesting()
	if s.Client() != nil {
		t.Error("expected nil client for testing streamer")
	}
}

func TestEnsureActive_FailsWithNoClient(t *testing.T) {
	s := NewForTesting()
	_, err := s.EnsureActive(nil, "magnet:?xt=urn:btih:abc")
	if err == nil {
		t.Error("expected error with no torrent client")
	}
}

func TestParseMagnet_Valid(t *testing.T) {
	s := &Streamer{}
	hash, name, err := s.ParseMagnet("magnet:?xt=urn:btih:2DBC910B807A892F3385E881D0668B12B722FA83&dn=test")
	if err != nil {
		t.Fatalf("ParseMagnet: %v", err)
	}
	if hash == "" {
		t.Error("expected non-empty hash")
	}
	if name != "test" {
		t.Errorf("name = %q, want 'test'", name)
	}
}


func TestImportTorrentBytes_Invalid(t *testing.T) {
	s := &Streamer{}
	_, _, err := s.ImportTorrentBytes([]byte("not a torrent"))
	if err == nil {
		t.Error("expected error for invalid torrent bytes")
	}
}


func TestRateBurst(t *testing.T) {
	if got := rateBurst(0); got != 65536 {
		t.Errorf("rateBurst(0) = %d, want 65536", got)
	}
	if got := rateBurst(1000); got <= 0 {
		t.Errorf("rateBurst(1000) = %d, want > 0", got)
	}
}

func TestLabelFromPriority_Unknown(t *testing.T) {
	got := labelFromPriority(100)
	if got != "normal" {
		t.Errorf("got %q, want 'normal'", got)
	}
}

func TestPriorityFromLabel(t *testing.T) {
	tests := []struct {
		label string
		ok    bool
	}{
		{"none", true},
		{"low", true},
		{"normal", true},
		{"high", true},
		{"unknown", false},
	}
	for _, tc := range tests {
		_, ok := priorityFromLabel(tc.label)
		if ok != tc.ok {
			t.Errorf("priorityFromLabel(%q) ok=%v, want %v", tc.label, ok, tc.ok)
		}
	}
}

func TestGlobalStats(t *testing.T) {
	s := NewForTesting()
	stats := s.GlobalStats()
	if stats.ActiveTorrents != 0 {
		t.Errorf("ActiveTorrents = %d, want 0", stats.ActiveTorrents)
	}
	if stats.DownRate != 0 || stats.UpRate != 0 {
		t.Errorf("rates should be 0, got down=%d up=%d", stats.DownRate, stats.UpRate)
	}
}

func TestLimiterBytes_WithNil(t *testing.T) {
	if got := limiterBytes(nil); got != 0 {
		t.Errorf("limiterBytes(nil) = %d, want 0", got)
	}
}

func TestIsDownloadProtected_EmptyDownloads(t *testing.T) {
	s := NewForTesting()
	if s.IsDownloadProtected("anything") {
		t.Error("expected false when downloads map is nil")
	}
}

func TestActiveList_Empty(t *testing.T) {
	s := NewForTesting()
	list := s.ActiveList()
	if len(list) != 0 {
		t.Errorf("expected empty list, got %d", len(list))
	}
}

func TestPauseAll_Empty(t *testing.T) {
	s := NewForTesting()
	s.PauseAll()
}

func TestResumeAll_Empty(t *testing.T) {
	s := NewForTesting()
	s.ResumeAll()
}

func TestPause_Empty(t *testing.T) {
	s := NewForTesting()
	_ = s.Pause(metainfo.Hash{})
}

func TestResume_Empty(t *testing.T) {
	s := NewForTesting()
	_ = s.Resume(metainfo.Hash{})
}

func TestSetPriority_Empty(t *testing.T) {
	s := NewForTesting()
	_ = s.SetPriority(metainfo.Hash{}, "normal")
}

func TestDrop_Empty(t *testing.T) {
	s := NewForTesting()
	s.Drop(metainfo.Hash{})
}

func TestClearAll_Empty(t *testing.T) {
	s := NewForTesting()
	_ = s.ClearAll()
}

func TestClearEntry_Empty(t *testing.T) {
	s := NewForTesting()
	_ = s.ClearEntry("nonexistent")
}

func TestStreamerNilMethods(t *testing.T) {
	var s *Streamer
	if s.Favorites() != nil {
		t.Error("Favorites() should be nil")
	}
}

func TestNewWithConfig(t *testing.T) {
	dir := t.TempDir()
	cfg := Config{DataDir: dir, IdleTimeout: time.Minute, MetadataWait: 2 * time.Second}
	s, err := New(cfg)
	if err != nil {
		t.Fatalf("New with config: %v", err)
	}
	if s == nil {
		t.Fatal("expected non-nil streamer")
	}
	s.Close()
}

func TestNewWithDefaults(t *testing.T) {
	s, err := New(Config{})
	if err != nil {
		return // may fail without torrent client
	}
	if s.cfg.IdleTimeout == 0 {
		t.Error("expected non-zero IdleTimeout")
	}
	s.Close()
}

func TestNewWithDataDir(t *testing.T) {
	dir := t.TempDir()
	cfg := Config{DataDir: dir}
	s, err := New(cfg)
	if err != nil {
		t.Fatalf("New with data dir: %v", err)
	}
	if s.cfg.DataDir != dir {
		t.Errorf("DataDir = %q, want %q", s.cfg.DataDir, dir)
	}
	s.Close()
}

func TestVerifyFile_Empty(t *testing.T) {
	s := NewForTesting()
	err := s.VerifyFile(metainfo.Hash{}, 0)
	if err == nil {
		t.Error("expected error for empty streamer")
	}
}

func TestRecheckFile_Empty(t *testing.T) {
	s := NewForTesting()
	err := s.RecheckFile(metainfo.Hash{}, 0)
	if err == nil {
		t.Error("expected error for empty streamer")
	}
}

func TestPrefetch_Empty(t *testing.T) {
	s := NewForTesting()
	err := s.Prefetch(metainfo.Hash{}, 0)
	if err == nil {
		t.Error("expected error for empty streamer")
	}
}

func TestSetFilePriority_Empty(t *testing.T) {
	s := NewForTesting()
	err := s.SetFilePriority(metainfo.Hash{}, 0, "normal")
	if err == nil {
		t.Error("expected error for empty streamer")
	}
}

func TestStreamerFavorites_Nil(t *testing.T) {
	var s *Streamer
	if s.Favorites() != nil {
		t.Error("nil streamer should return nil Favorites")
	}
}
