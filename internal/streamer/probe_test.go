package streamer

import (
	"io"
	"testing"
	"time"

	"github.com/anacrolix/torrent/metainfo"

	"github.com/lgldsilva/jackui/internal/dbtest"
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

func TestParseProbeOutput_VideoDimensions(t *testing.T) {
	json := `{
		"streams": [
			{"index": 0, "codec_type": "video", "codec_name": "hevc", "width": 1920, "height": 1080},
			{"index": 1, "codec_type": "audio", "codec_name": "aac", "channels": 2}
		],
		"format": {"duration": "10"}
	}`
	result, err := parseProbeOutput([]byte(json))
	if err != nil {
		t.Fatalf("parseProbeOutput: %v", err)
	}
	if result.VideoWidth != 1920 || result.VideoHeight != 1080 {
		t.Errorf("dims = %dx%d, want 1920x1080", result.VideoWidth, result.VideoHeight)
	}
}

// Fonte sem vídeo (só áudio) → dimensões 0 (ladder cai para single-variant).
func TestParseProbeOutput_NoVideoZeroDims(t *testing.T) {
	json := `{"streams":[{"index":0,"codec_type":"audio","codec_name":"aac","channels":2}],"format":{}}`
	result, err := parseProbeOutput([]byte(json))
	if err != nil {
		t.Fatalf("parseProbeOutput: %v", err)
	}
	if result.VideoWidth != 0 || result.VideoHeight != 0 {
		t.Errorf("dims = %dx%d, want 0x0", result.VideoWidth, result.VideoHeight)
	}
}

// A capa/thumbnail embutida (2º stream de vídeo, ex. 600x900) NÃO deve
// sobrescrever as dimensões do vídeo principal — só o primeiro conta.
func TestParseProbeOutput_IgnoresCoverArtDims(t *testing.T) {
	json := `{
		"streams": [
			{"index": 0, "codec_type": "video", "codec_name": "h264", "width": 1280, "height": 720},
			{"index": 1, "codec_type": "video", "codec_name": "mjpeg", "width": 600, "height": 900}
		],
		"format": {}
	}`
	result, err := parseProbeOutput([]byte(json))
	if err != nil {
		t.Fatalf("parseProbeOutput: %v", err)
	}
	if result.VideoWidth != 1280 || result.VideoHeight != 720 {
		t.Errorf("dims = %dx%d, want 1280x720 (primeiro vídeo)", result.VideoWidth, result.VideoHeight)
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
	if result.Chapters == nil {
		t.Error("Chapters must be a non-nil slice")
	}
	if len(result.Chapters) != 0 {
		t.Errorf("expected 0 chapters, got %d", len(result.Chapters))
	}
	if result.DurationSec != 0 {
		t.Errorf("DurationSec = %f, want 0", result.DurationSec)
	}
}

func TestParseProbeOutput_Chapters(t *testing.T) {
	json := `{
		"streams": [{"index": 0, "codec_type": "video", "codec_name": "h264"}],
		"chapters": [
			{"id": 0, "start_time": "0.000000", "end_time": "300.000000", "tags": {"title": "Intro"}},
			{"id": 1, "start_time": "300.000000", "end_time": "1200.500000", "tags": {"title": "Part 2"}},
			{"id": 2, "start_time": "1200.500000", "end_time": "1800.000000"}
		],
		"format": {"duration": "1800.0"}
	}`
	result, err := parseProbeOutput([]byte(json))
	if err != nil {
		t.Fatalf("parseProbeOutput: %v", err)
	}
	if len(result.Chapters) != 3 {
		t.Fatalf("expected 3 chapters, got %d", len(result.Chapters))
	}
	if result.Chapters[0].Title != "Intro" || result.Chapters[0].StartSec != 0 {
		t.Errorf("chapter0 = %+v", result.Chapters[0])
	}
	if result.Chapters[1].StartSec != 300 || result.Chapters[1].EndSec != 1200.5 {
		t.Errorf("chapter1 times = %+v", result.Chapters[1])
	}
	// A chapter with no tags keeps an empty title (the UI falls back to "Capítulo N").
	if result.Chapters[2].Title != "" {
		t.Errorf("chapter2 title = %q, want empty", result.Chapters[2].Title)
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

func TestMetadataCache_NewAndClose(t *testing.T) {
	cache, err := NewMetadataCache(dbtest.NewDB(t))
	if err != nil {
		t.Fatalf("NewMetadataCache: %v", err)
	}
	if err := cache.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
}

func TestMetadataCache_SetAndGetMeta(t *testing.T) {
	cache, err := NewMetadataCache(dbtest.NewDB(t))
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
	cache, err := NewMetadataCache(dbtest.NewDB(t))
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
	cache, err := NewMetadataCache(dbtest.NewDB(t))
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
	cache, err := NewMetadataCache(dbtest.NewDB(t))
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
	cache, err := NewMetadataCache(dbtest.NewDB(t))
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
	cache, err := NewMetadataCache(dbtest.NewDB(t))
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
	c, err := NewMetadataCache(dbtest.NewDB(t))
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
	c, _ := NewMetadataCache(dbtest.NewDB(t))
	defer c.Close()
	s.SetMetadataCache(c)
	if s.MetadataCache() == nil {
		t.Error("expected non-nil cache after SetMetadataCache")
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

func TestLabelFromPriority_Unknown(t *testing.T) {
	got := labelFromPriority(100)
	if got != "normal" {
		t.Errorf("got %q, want 'normal'", got)
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
	s.PauseAll() // sem torrents ativos: não deve dar panic
}

func TestResumeAll_Empty(t *testing.T) {
	s := NewForTesting()
	s.ResumeAll()
}

func TestPause_Empty(t *testing.T) {
	s := NewForTesting()
	if err := s.Pause(metainfo.Hash{}); err == nil {
		t.Error("Pause de hash inativa deveria retornar erro 'not active'")
	}
}

func TestResume_Empty(t *testing.T) {
	s := NewForTesting()
	if err := s.Resume(metainfo.Hash{}); err == nil {
		t.Error("Resume de hash inativa deveria retornar erro 'not active'")
	}
}

func TestSetPriority_Empty(t *testing.T) {
	s := NewForTesting()
	if err := s.SetPriority(metainfo.Hash{}, "normal"); err == nil {
		t.Error("SetPriority de hash inativa deveria retornar erro 'not active'")
	}
}

func TestDrop_Empty(t *testing.T) {
	s := NewForTesting()
	s.Drop(metainfo.Hash{}) // hash inativa: no-op sem panic
}

func TestClearAll_Empty(t *testing.T) {
	s := NewForTesting()
	if err := s.ClearAll(); err != nil {
		t.Errorf("ClearAll sem DataDir/torrents deveria ser no-op limpo, got %v", err)
	}
}

func TestClearEntry_Empty(t *testing.T) {
	s := NewForTesting()
	if err := s.ClearEntry("nonexistent"); err != nil {
		t.Errorf("ClearEntry de entrada inexistente deveria ser no-op limpo, got %v", err)
	}
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
	s, err := newTestStreamer(t, cfg)
	if err != nil {
		t.Fatalf("New with config: %v", err)
	}
	if s == nil {
		t.Fatal("expected non-nil streamer")
	}
	s.Close()
}

func TestNewWithDefaults(t *testing.T) {
	s, err := newTestStreamer(t, Config{})
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
	s, err := newTestStreamer(t, cfg)
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

// TestClassifyTranscode é o guard do #16: a decisão de transcode é por CODEC
// (navegador-agnóstica), não por nome de arquivo. MKV/HEVC/AV1/AC3/DTS → HLS;
// MP4/H.264/AAC → direct-play.
func TestClassifyTranscode(t *testing.T) {
	cases := []struct {
		container, vcodec, acodec string
		want                      bool
	}{
		{"mov", "h264", "aac", false},     // MP4/H.264/AAC → direct
		{"mp4", "h264", "aac", false},     //
		{"matroska", "h264", "aac", true}, // MKV → HLS
		{"mp4", "hevc", "aac", true},      // HEVC → HLS
		{"mp4", "av1", "aac", true},       // AV1 → HLS
		{"mp4", "h264", "ac3", true},      // AC3 → HLS
		{"mp4", "h264", "dts", true},      // DTS → HLS
		{"webm", "vp9", "opus", false},    // VP9/Opus/webm → direct
		{"", "", "", false},               // desconhecido → não força
	}
	for _, c := range cases {
		got, reason := classifyTranscode(c.container, c.vcodec, c.acodec)
		if got != c.want {
			t.Errorf("classifyTranscode(%q,%q,%q)=%v(%q), want %v", c.container, c.vcodec, c.acodec, got, reason, c.want)
		}
	}
}
