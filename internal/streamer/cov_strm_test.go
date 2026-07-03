package streamer

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/anacrolix/torrent/bencode"
	"github.com/anacrolix/torrent/metainfo"
)

// strmHaveFFmpeg reports whether ffmpeg/ffprobe are on PATH. The probe/extract
// success paths shell out to them; without the binaries we skip those tests
// rather than fail on CI runners that lack ffmpeg.
func strmHaveFFmpeg(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("ffmpeg"); err != nil {
		t.Skip("ffmpeg not on PATH; skipping ffmpeg-dependent test")
	}
	if _, err := exec.LookPath("ffprobe"); err != nil {
		t.Skip("ffprobe not on PATH; skipping ffprobe-dependent test")
	}
}

// strmGenVideo synthesizes a tiny 1-second H.264 + AAC MP4 via ffmpeg's lavfi
// test sources so the probe/thumbnail success paths have a real container to
// read. Returns the path; skips the test if generation fails.
func strmGenVideo(t *testing.T) string {
	t.Helper()
	strmHaveFFmpeg(t)
	out := filepath.Join(t.TempDir(), "strm_clip.mp4")
	cmd := exec.Command("ffmpeg",
		"-hide_banner", "-loglevel", "error",
		"-f", "lavfi", "-i", "testsrc=duration=1:size=160x120:rate=10",
		"-f", "lavfi", "-i", "sine=frequency=440:duration=1",
		"-c:v", "libx264", "-pix_fmt", "yuv420p",
		"-c:a", "aac",
		"-shortest",
		"-y", out,
	)
	if b, err := cmd.CombinedOutput(); err != nil {
		t.Skipf("ffmpeg could not synthesize test clip (%v): %s", err, string(b))
	}
	return out
}

// strmGenAudioWithArt synthesizes a tiny MP3 with an embedded cover-art frame so
// ExtractArtwork has a real picture stream to pull. Skips if generation fails.
func strmGenAudioWithArt(t *testing.T) string {
	t.Helper()
	strmHaveFFmpeg(t)
	dir := t.TempDir()
	// Embed a JPEG cover specifically: ExtractArtwork uses `-c copy -f mjpeg`,
	// which only succeeds when the attached picture is already MJPEG/JPEG.
	cover := filepath.Join(dir, "cover.jpg")
	mkCover := exec.Command("ffmpeg",
		"-hide_banner", "-loglevel", "error",
		"-f", "lavfi", "-i", "color=c=red:s=64x64:d=1",
		"-frames:v", "1", "-y", cover,
	)
	if b, err := mkCover.CombinedOutput(); err != nil {
		t.Skipf("ffmpeg could not make cover (%v): %s", err, string(b))
	}
	out := filepath.Join(dir, "strm_song.mp3")
	cmd := exec.Command("ffmpeg",
		"-hide_banner", "-loglevel", "error",
		"-f", "lavfi", "-i", "sine=frequency=440:duration=1",
		"-i", cover,
		"-map", "0:a", "-map", "1:v",
		"-c:a", "libmp3lame", "-c:v", "copy",
		"-id3v2_version", "3",
		"-metadata:s:v", "title=Album cover",
		"-metadata:s:v", "comment=Cover (front)",
		"-y", out,
	)
	if b, err := cmd.CombinedOutput(); err != nil {
		t.Skipf("ffmpeg could not synthesize mp3 with art (%v): %s", err, string(b))
	}
	return out
}

// strmGenVideoWithSubs synthesizes an MKV carrying a soft subtitle track so
// ExtractSubtitle's success path runs.
func strmGenVideoWithSubs(t *testing.T) string {
	t.Helper()
	strmHaveFFmpeg(t)
	dir := t.TempDir()
	srt := filepath.Join(dir, "subs.srt")
	if err := os.WriteFile(srt, []byte("1\n00:00:00,000 --> 00:00:01,000\nhello world\n"), 0o644); err != nil {
		t.Fatalf("write srt: %v", err)
	}
	out := filepath.Join(dir, "strm_subbed.mkv")
	cmd := exec.Command("ffmpeg",
		"-hide_banner", "-loglevel", "error",
		"-f", "lavfi", "-i", "testsrc=duration=1:size=160x120:rate=10",
		"-i", srt,
		"-c:v", "libx264", "-pix_fmt", "yuv420p",
		"-c:s", "srt",
		"-shortest",
		"-y", out,
	)
	if b, err := cmd.CombinedOutput(); err != nil {
		t.Skipf("ffmpeg could not synthesize mkv with subs (%v): %s", err, string(b))
	}
	return out
}

// strmResolverStreamer returns a NewForTesting streamer wired with a
// filePathResolver pointing every (hash, idx) request at the given on-disk path,
// plus a temp DataDir for thumbnail/artwork caching.
func strmResolverStreamer(t *testing.T, path string) *Streamer {
	t.Helper()
	s := NewForTesting()
	s.cfg.DataDir = t.TempDir()
	s.SetFilePathResolver(func(_ metainfo.Hash, _ int) (string, bool) {
		return path, true
	})
	return s
}

// ─── Probe success + cache + error paths ────────────────────────────────────

func Test_strm_Probe_SuccessAndCache(t *testing.T) {
	path := strmGenVideo(t)
	s := strmResolverStreamer(t, path)

	hash := metainfo.HashBytes([]byte("strm-probe-success"))
	res, err := s.Probe(context.Background(), hash, 0)
	if err != nil {
		t.Fatalf("Probe: %v", err)
	}
	if len(res.Audio) == 0 {
		t.Errorf("expected at least one audio track, got %d", len(res.Audio))
	}
	if res.DurationSec <= 0 {
		t.Errorf("expected positive duration, got %f", res.DurationSec)
	}

	// Second call must hit the per-(torrent,file) cache and return the same data.
	res2, err := s.Probe(context.Background(), hash, 0)
	if err != nil {
		t.Fatalf("Probe (cached): %v", err)
	}
	if len(res2.Audio) != len(res.Audio) || res2.DurationSec != res.DurationSec {
		t.Errorf("cached Probe differs: %+v vs %+v", res2, res)
	}
}

func Test_strm_Probe_NotActive(t *testing.T) {
	s := NewForTesting()
	// No resolver, empty active map → resolveProbeInput must error.
	_, err := s.Probe(context.Background(), metainfo.HashBytes([]byte("nope")), 0)
	if err == nil {
		t.Fatal("expected error probing a non-active torrent with no resolver")
	}
}

func Test_strm_Probe_NonMediaYieldsEmpty(t *testing.T) {
	strmHaveFFmpeg(t)
	// Point the resolver at a non-media file. ffprobe still prints an (empty)
	// JSON object to stdout, so Probe succeeds with zero tracks rather than
	// erroring — this exercises runFFprobe + parseProbeOutput over real bytes.
	dir := t.TempDir()
	bogus := filepath.Join(dir, "not_media.bin")
	if err := os.WriteFile(bogus, bytes.Repeat([]byte{0x00, 0xff}, 1024), 0o644); err != nil {
		t.Fatalf("write bogus: %v", err)
	}
	s := strmResolverStreamer(t, bogus)
	res, err := s.Probe(context.Background(), metainfo.HashBytes([]byte("bogus")), 0)
	if err != nil {
		t.Fatalf("Probe of non-media should not error (ffprobe emits {}): %v", err)
	}
	if len(res.Audio) != 0 || len(res.Subtitles) != 0 {
		t.Errorf("expected no tracks for non-media input, got audio=%d sub=%d", len(res.Audio), len(res.Subtitles))
	}
}

// ─── resolveProbeInput error paths ──────────────────────────────────────────

func Test_strm_resolveProbeInput_NotActive(t *testing.T) {
	s := NewForTesting()
	_, err := s.resolveProbeInput(metainfo.HashBytes([]byte("x")), 0, 1024)
	if err == nil || err.Error() != ErrTorrentNotActive {
		t.Fatalf("expected %q, got %v", ErrTorrentNotActive, err)
	}
}

func Test_strm_resolveProbeInput_ResolverHit(t *testing.T) {
	s := strmResolverStreamer(t, "/tmp/whatever.mkv")
	pi, err := s.resolveProbeInput(metainfo.Hash{}, 0, 1024)
	if err != nil {
		t.Fatalf("resolveProbeInput: %v", err)
	}
	if pi.input != "/tmp/whatever.mkv" {
		t.Errorf("input = %q, want /tmp/whatever.mkv", pi.input)
	}
	if pi.stdin != nil || pi.closeFn != nil {
		t.Error("resolver hit should yield a bare path input (no stdin/closeFn)")
	}
}

// ─── ExtractThumbnail success + caching ─────────────────────────────────────

func Test_strm_ExtractThumbnail_SuccessAndCache(t *testing.T) {
	path := strmGenVideo(t)
	s := strmResolverStreamer(t, path)
	hash := metainfo.HashBytes([]byte("strm-thumb"))

	jpeg, fromCache, err := s.ExtractThumbnail(context.Background(), hash, 0, 0)
	if err != nil {
		t.Fatalf("ExtractThumbnail: %v", err)
	}
	if fromCache {
		t.Error("first extraction should not be from cache")
	}
	if len(jpeg) == 0 {
		t.Fatal("expected non-empty thumbnail JPEG")
	}

	// Second call at the same 10s bucket must come from the disk cache.
	jpeg2, fromCache2, err := s.ExtractThumbnail(context.Background(), hash, 0, 3)
	if err != nil {
		t.Fatalf("ExtractThumbnail (cached): %v", err)
	}
	if !fromCache2 {
		t.Error("second extraction in the same bucket should be cached")
	}
	if !bytes.Equal(jpeg, jpeg2) {
		t.Error("cached thumbnail differs from freshly extracted one")
	}
}

func Test_strm_ExtractThumbnail_NegativeSecondsClamped(t *testing.T) {
	path := strmGenVideo(t)
	s := strmResolverStreamer(t, path)
	hash := metainfo.HashBytes([]byte("strm-thumb-neg"))
	// Negative atSeconds is clamped to 0 internally; should still succeed.
	jpeg, _, err := s.ExtractThumbnail(context.Background(), hash, 0, -5)
	if err != nil {
		t.Fatalf("ExtractThumbnail negative: %v", err)
	}
	if len(jpeg) == 0 {
		t.Error("expected thumbnail even with negative seconds (clamped to 0)")
	}
}

func Test_strm_ExtractThumbnail_NotActive(t *testing.T) {
	s := NewForTesting()
	s.cfg.DataDir = t.TempDir()
	_, _, err := s.ExtractThumbnail(context.Background(), metainfo.Hash{}, 0, 0)
	if err == nil {
		t.Fatal("expected error for non-active torrent without resolver")
	}
}

// ─── ExtractArtwork success + negative cache ────────────────────────────────

func Test_strm_ExtractArtwork_Success(t *testing.T) {
	path := strmGenAudioWithArt(t)
	s := strmResolverStreamer(t, path)
	hash := metainfo.HashBytes([]byte("strm-art"))

	art, fromCache, err := s.ExtractArtwork(context.Background(), hash, 0)
	if err != nil {
		t.Fatalf("ExtractArtwork: %v", err)
	}
	if fromCache {
		t.Error("first artwork extraction should not be cached")
	}
	if len(art) == 0 {
		t.Fatal("expected embedded artwork bytes")
	}

	// Second call hits the disk cache.
	art2, fromCache2, err := s.ExtractArtwork(context.Background(), hash, 0)
	if err != nil {
		t.Fatalf("ExtractArtwork (cached): %v", err)
	}
	if !fromCache2 {
		t.Error("second artwork extraction should be cached")
	}
	if !bytes.Equal(art, art2) {
		t.Error("cached artwork differs")
	}
}

func Test_strm_ExtractArtwork_NoArtNegativeCache(t *testing.T) {
	// A plain video with no embedded cover yields empty bytes + nil error, and
	// writes an .empty negative-cache marker that the second call short-circuits.
	path := strmGenVideo(t)
	s := strmResolverStreamer(t, path)
	hash := metainfo.HashBytes([]byte("strm-art-none"))

	art, fromCache, err := s.ExtractArtwork(context.Background(), hash, 0)
	if err != nil {
		t.Fatalf("ExtractArtwork (no art): %v", err)
	}
	if fromCache {
		t.Error("first no-art extraction is not cached")
	}
	if len(art) != 0 {
		t.Errorf("expected empty artwork for video with no cover, got %d bytes", len(art))
	}

	// Negative-cache marker now exists; second call returns (nil, true, nil).
	art2, fromCache2, err := s.ExtractArtwork(context.Background(), hash, 0)
	if err != nil {
		t.Fatalf("ExtractArtwork (negative cache): %v", err)
	}
	if !fromCache2 {
		t.Error("expected negative cache hit on second no-art call")
	}
	if len(art2) != 0 {
		t.Error("negative cache should return empty bytes")
	}
}

func Test_strm_ExtractArtwork_NotActive(t *testing.T) {
	s := NewForTesting()
	s.cfg.DataDir = t.TempDir()
	_, _, err := s.ExtractArtwork(context.Background(), metainfo.Hash{}, 0)
	if err == nil {
		t.Fatal("expected error for non-active torrent without resolver")
	}
}

// ─── ExtractSubtitle success + error paths ──────────────────────────────────

func Test_strm_ExtractSubtitle_Success(t *testing.T) {
	path := strmGenVideoWithSubs(t)
	s := strmResolverStreamer(t, path)
	hash := metainfo.HashBytes([]byte("strm-subs"))

	// Probe to learn the subtitle stream's absolute index.
	res, err := s.Probe(context.Background(), hash, 0)
	if err != nil {
		t.Fatalf("Probe: %v", err)
	}
	if len(res.Subtitles) == 0 {
		t.Skip("ffmpeg build produced no soft subtitle track")
	}
	vtt, err := s.ExtractSubtitle(context.Background(), hash, 0, res.Subtitles[0].Index)
	if err != nil {
		t.Fatalf("ExtractSubtitle: %v", err)
	}
	if !bytes.Contains(vtt, []byte("WEBVTT")) {
		t.Errorf("expected WEBVTT output, got %q", string(vtt))
	}
}

func Test_strm_ExtractSubtitle_NotActive(t *testing.T) {
	s := NewForTesting()
	_, err := s.ExtractSubtitle(context.Background(), metainfo.Hash{}, 0, 0)
	if err == nil || err.Error() != ErrTorrentNotActive {
		t.Fatalf("expected %q, got %v", ErrTorrentNotActive, err)
	}
}

func Test_strm_ExtractSubtitle_BadTrackErrors(t *testing.T) {
	path := strmGenVideo(t)
	s := strmResolverStreamer(t, path)
	hash := metainfo.HashBytes([]byte("strm-subs-bad"))
	// Stream index 99 doesn't exist → ffmpeg's -map fails.
	_, err := s.ExtractSubtitle(context.Background(), hash, 0, 99)
	if err == nil {
		t.Fatal("expected error mapping a nonexistent subtitle track")
	}
}

// ─── primary-file selection helpers ─────────────────────────────────────────

func Test_strm_pickEpisodeStart(t *testing.T) {
	files := []FileInfo{
		{Index: 0, Path: "Show S01E03.mkv", IsVideo: true, Size: 100},
		{Index: 1, Path: "Show S01E01.mkv", IsVideo: true, Size: 100},
		{Index: 2, Path: "Show S02E01.mkv", IsVideo: true, Size: 100},
		{Index: 3, Path: "Featurette behind the scenes S00E00.mkv", IsVideo: true, Size: 9999},
	}
	idx, ok := pickEpisodeStart(files)
	if !ok {
		t.Fatal("expected to detect an episode start")
	}
	if idx != 1 {
		t.Errorf("idx = %d, want 1 (lowest season/episode, extras excluded)", idx)
	}
}

func Test_strm_pickEpisodeStart_TooFew(t *testing.T) {
	files := []FileInfo{
		{Index: 0, Path: "Movie S01E01.mkv", IsVideo: true},
		{Index: 1, Path: "Movie S01E02.mkv", IsVideo: true},
	}
	if _, ok := pickEpisodeStart(files); ok {
		t.Error("expected false with fewer than 3 episodes")
	}
}

func Test_strm_pickLargestNonExtra(t *testing.T) {
	files := []FileInfo{
		{Index: 0, Path: "sample.mkv", IsVideo: true, Size: 99999},
		{Index: 1, Path: "main.mkv", IsVideo: true, Size: 500},
		{Index: 2, Path: "readme.txt", IsVideo: false, Size: 10},
	}
	idx, ok := pickLargestNonExtra(files)
	if !ok {
		t.Fatal("expected a non-extra video pick")
	}
	if idx != 1 {
		t.Errorf("idx = %d, want 1 (sample excluded as extra)", idx)
	}
}

func Test_strm_pickLargestNonExtra_AllExtras(t *testing.T) {
	files := []FileInfo{
		{Index: 0, Path: "trailer.mkv", IsVideo: true, Size: 100},
		{Index: 1, Path: "no video here.txt", IsVideo: false, Size: 100},
	}
	if _, ok := pickLargestNonExtra(files); ok {
		t.Error("expected false when every video is an extra")
	}
}

func Test_strm_firstVideoIndex(t *testing.T) {
	files := []FileInfo{
		{Index: 0, Path: "a.txt", IsVideo: false},
		{Index: 1, Path: "b.mkv", IsVideo: true},
	}
	if got := firstVideoIndex(files); got != 1 {
		t.Errorf("firstVideoIndex = %d, want 1", got)
	}
	if got := firstVideoIndex([]FileInfo{{Path: "x.txt"}}); got != -1 {
		t.Errorf("firstVideoIndex (no video) = %d, want -1", got)
	}
}

func Test_strm_pickPrimaryFile_FallbackToFirstVideo(t *testing.T) {
	// One video, not an episode, tagged as a sample → episodeStart and
	// largestNonExtra both bail, so it falls back to firstVideoIndex.
	files := []FileInfo{
		{Index: 0, Path: "trailer.mkv", IsVideo: true, Size: 100},
	}
	if got := pickPrimaryFile(files); got != 0 {
		t.Errorf("pickPrimaryFile = %d, want 0 (first-video fallback)", got)
	}
}

// ─── Get / GlobalStats / Stats over the active map ──────────────────────────

func Test_strm_Get_NotFound(t *testing.T) {
	s := NewForTesting()
	_, err := s.Get(metainfo.HashBytes([]byte("absent")))
	if err == nil {
		t.Fatal("expected error for unknown hash")
	}
}

func Test_strm_Stats_WithEntriesAndMetainfo(t *testing.T) {
	dir := t.TempDir()
	s := NewForTesting()
	s.cfg.DataDir = dir
	s.cfg.MaxCacheSize = 1 << 30

	// One on-disk cache dir with a file so dirSizeAndMTime returns > 0.
	contentDir := filepath.Join(dir, "Some.Movie")
	if err := os.MkdirAll(contentDir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(contentDir, "movie.mkv"), bytes.Repeat([]byte("x"), 2048), 0o644); err != nil {
		t.Fatalf("write content: %v", err)
	}

	// A metainfoDir holding a real .torrent so augmentNameToHashFromMetainfo
	// resolves a name→hash mapping it doesn't already have from the active map.
	miDir := filepath.Join(dir, ".metainfo")
	if err := os.MkdirAll(miDir, 0o755); err != nil {
		t.Fatalf("mkdir metainfo: %v", err)
	}
	s.metainfoDir = miDir
	mi := strmBuildMetainfo(t, miDir, "Some.Movie")

	stats, err := s.Stats()
	if err != nil {
		t.Fatalf("Stats: %v", err)
	}
	if stats.MaxSize != 1<<30 {
		t.Errorf("MaxSize = %d, want %d", stats.MaxSize, int64(1)<<30)
	}
	if stats.TotalSize <= 0 {
		t.Errorf("TotalSize = %d, want > 0", stats.TotalSize)
	}
	var found *CacheEntry
	for i := range stats.Entries {
		if stats.Entries[i].Path == "Some.Movie" {
			found = &stats.Entries[i]
			break
		}
	}
	if found == nil {
		t.Fatal("expected a Some.Movie cache entry")
	}
	if found.InfoHash != mi.HashInfoBytes().HexString() {
		t.Errorf("InfoHash = %q, want %q (from metainfo augment)", found.InfoHash, mi.HashInfoBytes().HexString())
	}
}

func Test_strm_Stats_MissingDataDir(t *testing.T) {
	s := NewForTesting()
	s.cfg.DataDir = filepath.Join(t.TempDir(), "does-not-exist")
	stats, err := s.Stats()
	if err != nil {
		t.Fatalf("Stats on missing dir should not error: %v", err)
	}
	if len(stats.Entries) != 0 {
		t.Errorf("expected 0 entries for missing data dir, got %d", len(stats.Entries))
	}
}

func Test_strm_augmentNameToHashFromMetainfo_NoDir(t *testing.T) {
	s := NewForTesting()
	// metainfoDir empty → augment is a no-op, must not touch the map.
	m := map[string]string{"keep": "abc"}
	s.augmentNameToHashFromMetainfo(m)
	if len(m) != 1 || m["keep"] != "abc" {
		t.Errorf("map mutated unexpectedly: %+v", m)
	}
}

func Test_strm_buildActiveMaps_Empty(t *testing.T) {
	s := NewForTesting()
	names, n2h, n := s.buildActiveMaps()
	if n != 0 || len(names) != 0 || len(n2h) != 0 {
		t.Errorf("expected empty maps, got names=%d n2h=%d n=%d", len(names), len(n2h), n)
	}
}

// ─── ParseMagnet error + ClearEntry path-safety ─────────────────────────────

func Test_strm_ParseMagnet_Invalid(t *testing.T) {
	s := &Streamer{}
	_, _, err := s.ParseMagnet("not-a-magnet")
	if err == nil {
		t.Fatal("expected error for invalid magnet")
	}
}

func Test_strm_ClearEntry_RejectsTraversal(t *testing.T) {
	dir := t.TempDir()
	s := NewForTesting()
	s.cfg.DataDir = dir
	err := s.ClearEntry("../escape")
	if err == nil {
		t.Fatal("expected ClearEntry to reject a path escaping DataDir")
	}
}

func Test_strm_ClearEntry_RemovesFile(t *testing.T) {
	dir := t.TempDir()
	s := NewForTesting()
	s.cfg.DataDir = dir
	target := filepath.Join(dir, "junk")
	if err := os.MkdirAll(target, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := s.ClearEntry("junk"); err != nil {
		t.Fatalf("ClearEntry: %v", err)
	}
	if _, err := os.Stat(target); !os.IsNotExist(err) {
		t.Error("expected junk dir to be removed")
	}
}

// strmBuildMetainfo writes a minimal but valid single-file .torrent into dir
// (named by its info hash, mirroring metainfoPath) and returns its MetaInfo so
// callers can assert the resolved info hash.
func strmBuildMetainfo(t *testing.T, dir, name string) *metainfo.MetaInfo {
	t.Helper()
	// Build a piece-less single-file info dict for "name" so augment can read
	// info.Name back out. We construct it from a scratch content dir.
	content := filepath.Join(t.TempDir(), name)
	if err := os.MkdirAll(filepath.Dir(content), 0o755); err != nil {
		t.Fatalf("mkdir content parent: %v", err)
	}
	if err := os.WriteFile(content, bytes.Repeat([]byte("y"), 4096), 0o644); err != nil {
		t.Fatalf("write content file: %v", err)
	}
	info := metainfo.Info{PieceLength: 1 << 14}
	if err := info.BuildFromFilePath(content); err != nil {
		t.Fatalf("BuildFromFilePath: %v", err)
	}
	// Force the advertised name so it matches the on-disk cache entry.
	info.Name = name
	infoBytes, err := bencode.Marshal(info)
	if err != nil {
		t.Fatalf("bencode.Marshal info: %v", err)
	}
	mi := &metainfo.MetaInfo{InfoBytes: infoBytes}
	h := mi.HashInfoBytes()
	out := filepath.Join(dir, h.HexString()+".torrent")
	f, err := os.Create(out)
	if err != nil {
		t.Fatalf("create torrent: %v", err)
	}
	defer f.Close()
	if err := mi.Write(f); err != nil {
		t.Fatalf("write torrent: %v", err)
	}
	return mi
}
