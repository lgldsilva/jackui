package streamer

// cov_str2_test.go — extra coverage for pure/utility helpers and DB-store error
// paths in streamer.go / probe.go / art.go / favorites.go / metadata_cache.go
// that need no live torrent. Every identifier is prefixed with `str2` to avoid
// colliding with the existing *_test.go helpers (which are reused where possible).

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/anacrolix/torrent/metainfo"

	"github.com/lgldsilva/jackui/internal/dbtest"
)

// ─── art.go: buildImageCandidates / sortCandsByPreference pure ranking ───────

// str2Cands builds candidates directly (no torrent) to exercise the sort
// comparator's three tie-breakers: preferred flag, preferRank, then size.
func Test_str2_sortCandsByPreference_AllTieBreakers(t *testing.T) {
	cands := []imgCandidate{
		{idx: 0, size: 100, preferred: false, preferRank: -1}, // unpreferred, small
		{idx: 1, size: 999, preferred: false, preferRank: -1}, // unpreferred, large
		{idx: 2, size: 100, preferred: true, preferRank: 3},   // preferred, mid rank
		{idx: 3, size: 100, preferred: true, preferRank: 9},   // preferred, top rank
	}
	sortCandsByPreference(cands)

	// Highest preferRank among preferred wins outright.
	if cands[0].idx != 3 {
		t.Fatalf("expected idx 3 (top rank) first, got %d", cands[0].idx)
	}
	// Then the other preferred one.
	if cands[1].idx != 2 {
		t.Fatalf("expected idx 2 (preferred) second, got %d", cands[1].idx)
	}
	// Among the unpreferred pair, the larger size wins.
	if cands[2].idx != 1 {
		t.Fatalf("expected idx 1 (larger) third, got %d", cands[2].idx)
	}
	if cands[3].idx != 0 {
		t.Fatalf("expected idx 0 (smaller) last, got %d", cands[3].idx)
	}
}

// str2 covers buildImageCandidates over a nil slice (the no-files branch) — the
// existing TestBuildImageCandidates_Empty does the same but we keep an
// independent assertion that the empty result also sorts without panicking.
func Test_str2_buildImageCandidates_NilThenSort(t *testing.T) {
	cands := buildImageCandidates(nil)
	if len(cands) != 0 {
		t.Fatalf("expected 0 candidates from nil files, got %d", len(cands))
	}
	sortCandsByPreference(cands) // must be a no-op, not a panic
}

// ─── art.go: SaveArtBytes / ReadArtBytes error + success ─────────────────────

// str2 SaveArtBytes fails when DataDir is actually a regular file: MkdirAll on a
// path whose parent is a file returns an error.
func Test_str2_SaveArtBytes_MkdirError(t *testing.T) {
	dir := t.TempDir()
	notADir := filepath.Join(dir, "iam-a-file")
	if err := os.WriteFile(notADir, []byte("x"), 0o644); err != nil {
		t.Fatalf("seed file: %v", err)
	}
	// DataDir points at the file → MkdirAll(DataDir/.art) must fail.
	s := &Streamer{cfg: Config{DataDir: notADir}}
	if _, err := s.SaveArtBytes(metainfo.Hash{0x01}, []byte("data")); err == nil {
		t.Fatal("expected SaveArtBytes to fail when DataDir is a file")
	}
}

// str2 round-trips SaveArtBytes → ReadArtBytes to cover the happy path of both
// and assert the relative path is inside the .art dir.
func Test_str2_SaveAndReadArtBytes_RoundTrip(t *testing.T) {
	s := &Streamer{cfg: Config{DataDir: t.TempDir()}}
	payload := []byte("jpeg-bytes-str2")
	rel, err := s.SaveArtBytes(metainfo.Hash{0xDE, 0xAD}, payload)
	if err != nil {
		t.Fatalf("SaveArtBytes: %v", err)
	}
	if filepath.Dir(rel) != artDirName {
		t.Fatalf("rel %q not under %q", rel, artDirName)
	}
	got, err := s.ReadArtBytes(rel)
	if err != nil {
		t.Fatalf("ReadArtBytes: %v", err)
	}
	if string(got) != string(payload) {
		t.Fatalf("round-trip mismatch: %q != %q", got, payload)
	}
}

// str2 ReadArtBytes rejects a path that does not begin with the .art prefix.
func Test_str2_ReadArtBytes_OutsideDir(t *testing.T) {
	s := &Streamer{cfg: Config{DataDir: t.TempDir()}}
	if _, err := s.ReadArtBytes("elsewhere/x.jpg"); err == nil {
		t.Fatal("expected error for path outside .art dir")
	}
}

// ─── art.go / sidecar.go: not-active error paths (no torrent needed) ─────────

func Test_str2_TorrentImage_NotActive(t *testing.T) {
	s := &Streamer{active: map[metainfo.Hash]*entry{}}
	if _, _, err := s.TorrentImage(context.Background(), metainfo.Hash{0x09}); err == nil {
		t.Fatal("expected TorrentImage to error when torrent not active")
	}
}

func Test_str2_Sidecars_NotActive(t *testing.T) {
	s := &Streamer{active: map[metainfo.Hash]*entry{}}
	if _, err := s.Sidecars(metainfo.Hash{0x09}, -1); err == nil {
		t.Fatal("expected Sidecars to error when torrent not active")
	}
}

func Test_str2_ReadSidecar_NotActive(t *testing.T) {
	s := &Streamer{active: map[metainfo.Hash]*entry{}}
	if _, _, err := s.ReadSidecar(context.Background(), metainfo.Hash{0x09}, 0); err == nil {
		t.Fatal("expected ReadSidecar to error when torrent not active")
	}
}

// ─── sidecar.go: detectLanguage edge cases (pure) ────────────────────────────

func Test_str2_detectLanguage_DirFallbackAndNone(t *testing.T) {
	// No hint in basename, but the directory carries the language → matched.
	if got := detectLanguage("Subs/Portuguese/track1.srt"); got != "pt" {
		t.Fatalf("dir-based detect = %q, want pt", got)
	}
	// pt-BR is more specific and must win over the plain pt pattern.
	if got := detectLanguage("movie.pt-BR.srt"); got != "pt-BR" {
		t.Fatalf("pt-BR detect = %q, want pt-BR", got)
	}
	// Nothing recognizable → empty.
	if got := detectLanguage("randomfile.srt"); got != "" {
		t.Fatalf("no-hint detect = %q, want empty", got)
	}
}

// ─── probe.go: parseProbeOutput / isImageSubtitle pure paths ─────────────────

// str2 parses a stream payload that includes an image subtitle, a text subtitle
// (with tags), and audio with a default disposition + channels — covering the
// audio/subtitle/image branches and the format-duration parse in one shot.
func Test_str2_parseProbeOutput_MixedStreams(t *testing.T) {
	js := `{
		"streams": [
			{"index":0,"codec_type":"audio","codec_name":"ac3","channels":6,
			 "tags":{"language":"por","title":"Dublado"},
			 "disposition":{"default":1,"forced":0}},
			{"index":1,"codec_type":"subtitle","codec_name":"subrip",
			 "tags":{"language":"eng"},"disposition":{"default":0,"forced":1}},
			{"index":2,"codec_type":"subtitle","codec_name":"hdmv_pgs_subtitle",
			 "disposition":{"default":0,"forced":0}},
			{"index":3,"codec_type":"video","codec_name":"h264"}
		],
		"format":{"duration":"123.45"}
	}`
	res, err := parseProbeOutput([]byte(js))
	if err != nil {
		t.Fatalf("parseProbeOutput: %v", err)
	}
	if len(res.Audio) != 1 || res.Audio[0].Channels != 6 || !res.Audio[0].Default {
		t.Fatalf("audio parse wrong: %+v", res.Audio)
	}
	if res.Audio[0].Language != "por" || res.Audio[0].Title != "Dublado" {
		t.Fatalf("audio tags lost: %+v", res.Audio[0])
	}
	if len(res.Subtitles) != 2 {
		t.Fatalf("expected 2 subtitles, got %d", len(res.Subtitles))
	}
	if !res.Subtitles[0].Forced {
		t.Fatalf("forced disposition lost on text sub")
	}
	if !res.Subtitles[1].Image {
		t.Fatalf("expected PGS sub flagged as image")
	}
	if res.DurationSec != 123.45 {
		t.Fatalf("duration parse = %v, want 123.45", res.DurationSec)
	}
}

// str2 covers the bad-duration branch: an unparsable duration leaves DurationSec
// at its zero value without erroring.
func Test_str2_parseProbeOutput_BadDuration(t *testing.T) {
	js := `{"streams":[],"format":{"duration":"not-a-number"}}`
	res, err := parseProbeOutput([]byte(js))
	if err != nil {
		t.Fatalf("parseProbeOutput: %v", err)
	}
	if res.DurationSec != 0 {
		t.Fatalf("expected 0 duration on parse failure, got %v", res.DurationSec)
	}
}

func Test_str2_isImageSubtitle(t *testing.T) {
	for _, codec := range []string{"hdmv_pgs_subtitle", "dvd_subtitle", "dvdsub", "pgssub", "xsub"} {
		if !isImageSubtitle(codec) {
			t.Errorf("isImageSubtitle(%q) = false, want true", codec)
		}
	}
	for _, codec := range []string{"subrip", "ass", "webvtt", ""} {
		if isImageSubtitle(codec) {
			t.Errorf("isImageSubtitle(%q) = true, want false", codec)
		}
	}
}

// ─── probe.go: resolveProbeInput resolver-hit + not-active (no torrent) ───────

func Test_str2_resolveProbeInput_ResolverHit(t *testing.T) {
	s := &Streamer{
		filePathResolver: func(metainfo.Hash, int) (string, bool) { return "/tmp/clip.mp4", true },
	}
	pi, err := s.resolveProbeInput(metainfo.Hash{}, 0, 1024)
	if err != nil {
		t.Fatalf("resolveProbeInput resolver hit: %v", err)
	}
	if pi.input != "/tmp/clip.mp4" || pi.stdin != nil {
		t.Fatalf("resolver path should set input + nil stdin, got %+v", pi)
	}
}

func Test_str2_resolveProbeInput_NotActive(t *testing.T) {
	s := &Streamer{active: map[metainfo.Hash]*entry{}}
	if _, err := s.resolveProbeInput(metainfo.Hash{0x01}, 0, 1024); err == nil {
		t.Fatal("expected resolveProbeInput to error when not active")
	}
}

// ─── metadata_cache.go: NewMetadataCache error + store error paths ───────────

// str2NewMetadataCache opens a fresh cache backed by an isolated Postgres schema.
func str2NewMetadataCache(t *testing.T) *MetadataCache {
	t.Helper()
	c, err := NewMetadataCache(dbtest.NewDB(t))
	if err != nil {
		t.Fatalf("NewMetadataCache: %v", err)
	}
	t.Cleanup(func() { _ = c.Close() })
	return c
}

// str2 Get returns nil when the row's files JSON is corrupted (Unmarshal error
// branch) — we poke an invalid JSON blob straight into the column.
func Test_str2_MetadataCache_Get_CorruptFilesJSON(t *testing.T) {
	c := str2NewMetadataCache(t)
	if _, err := c.db.Exec(
		`INSERT INTO metadata(info_hash,name,total_size,files,primary_file) VALUES(?,?,?,?,?)`,
		"corrupt", "n", 10, "{not-json", 0,
	); err != nil {
		t.Fatalf("seed corrupt row: %v", err)
	}
	if got := c.Get("corrupt"); got != nil {
		t.Fatalf("expected nil on corrupt files JSON, got %+v", got)
	}
}

// str2 Get returns a populated snapshot for a well-formed row (happy path that
// also exercises dbutil.ParseTime on cached_at).
func Test_str2_MetadataCache_SetGet_RoundTrip(t *testing.T) {
	c := str2NewMetadataCache(t)
	info := &TorrentInfo{
		InfoHash:    "abc123",
		Name:        "Some Movie",
		TotalSize:   42,
		PrimaryFile: 1,
		Files: []FileInfo{
			{Index: 0, Path: "a.nfo", Size: 2, IsVideo: false},
			{Index: 1, Path: "movie.mkv", Size: 40, IsVideo: true},
		},
	}
	if err := c.Set(info); err != nil {
		t.Fatalf("Set: %v", err)
	}
	got := c.Get("abc123")
	if got == nil {
		t.Fatal("Get returned nil after Set")
	}
	if got.Name != "Some Movie" || got.TotalSize != 42 || got.PrimaryFile != 1 {
		t.Fatalf("Get mismatch: %+v", got)
	}
	if len(got.Files) != 2 || !got.Files[1].IsVideo {
		t.Fatalf("files round-trip wrong: %+v", got.Files)
	}
	if got.CachedAt.IsZero() {
		t.Fatal("expected non-zero CachedAt")
	}
}

// str2 after Close, Set/SetArt/SetHealth all hit the Exec-on-closed-db error
// branch (they return error rather than panicking).
func Test_str2_MetadataCache_WritesAfterClose_Error(t *testing.T) {
	pool := dbtest.NewDB(t)
	c, err := NewMetadataCache(pool)
	if err != nil {
		t.Fatalf("NewMetadataCache: %v", err)
	}
	_ = pool.Close()
	if err := c.Set(&TorrentInfo{InfoHash: "h", Name: "n"}); err == nil {
		t.Error("expected Set to error after Close")
	}
	if err := c.SetArt("h", &CachedArt{Source: "torrent", Path: ".art/h.jpg"}); err == nil {
		t.Error("expected SetArt to error after Close")
	}
	if err := c.SetHealth("h", 1, 2); err == nil {
		t.Error("expected SetHealth to error after Close")
	}
	// Reads after Close must stay nil-safe (no panic), exercising the scan-error
	// branch of Get/GetArt/GetHealth.
	if c.Get("h") != nil || c.GetArt("h") != nil || c.GetHealth("h") != nil {
		t.Error("expected nil reads after Close")
	}
}

// str2 Close on a nil cache is a no-op returning nil.
func Test_str2_MetadataCache_Close_Nil(t *testing.T) {
	var c *MetadataCache
	if err := c.Close(); err != nil {
		t.Fatalf("nil Close: %v", err)
	}
}

// ─── metadata_cache.go: ArtSourceRank + path helpers (pure) ──────────────────

func Test_str2_ArtSourceRank_Order(t *testing.T) {
	if !(ArtSourceRank("torrent") > ArtSourceRank("tmdb") &&
		ArtSourceRank("tmdb") > ArtSourceRank("web") &&
		ArtSourceRank("web") > ArtSourceRank("frame") &&
		ArtSourceRank("frame") > ArtSourceRank("")) {
		t.Fatal("ArtSourceRank ordering broken")
	}
	if ArtSourceRank("bogus") != 0 {
		t.Fatal("unknown source must rank 0")
	}
}

func Test_str2_DefaultPaths(t *testing.T) {
	if got := DefaultMetadataCachePath("/data"); got != filepath.Join("/data", ".metadata-cache.db") {
		t.Fatalf("DefaultMetadataCachePath = %q", got)
	}
	if got := DefaultFavoritesPath("/data"); got != filepath.Join("/data", ".favorites.db") {
		t.Fatalf("DefaultFavoritesPath = %q", got)
	}
}

// ─── favorites.go: store error paths after the pool is closed ────────────────

// str2 after the pool closes, the query-based reads/writes hit their error branches.
func Test_str2_Favorites_OpsAfterClose_Error(t *testing.T) {
	pool := dbtest.NewDB(t)
	dbtest.SeedUsers(t, pool, 1, 2, 3)
	f, err := NewFavorites(pool)
	if err != nil {
		t.Fatalf("NewFavorites: %v", err)
	}
	_ = pool.Close()

	if _, err := f.List(1, false, false); err == nil {
		t.Error("expected List to error after Close")
	}
	if _, err := f.ListFolders(1, false); err == nil {
		t.Error("expected ListFolders to error after Close")
	}
	if _, err := f.CreateFolder(1, "x", nil, false); err == nil {
		t.Error("expected CreateFolder to error after Close")
	}
	if err := f.RenameFolder(1, 1, "x"); err == nil {
		t.Error("expected RenameFolder to error after Close")
	}
	if err := f.MoveFolder(1, 1, nil); err == nil {
		t.Error("expected MoveFolder to error after Close")
	}
	if err := f.DeleteFolder(1, 1); err == nil {
		t.Error("expected DeleteFolder to error after Close")
	}
	if _, err := f.GetFolder(1, 1); err == nil {
		t.Error("expected GetFolder to error after Close")
	}
}

// ─── favorites.go: MoveFolder cycle detection + move-to-root (live store) ────

func Test_str2_MoveFolder_CycleAndRoot(t *testing.T) {
	f := newTestFavorites(t)
	parent, err := f.CreateFolder(1, "parent", nil, false)
	if err != nil {
		t.Fatalf("CreateFolder parent: %v", err)
	}
	child, err := f.CreateFolder(1, "child", &parent.ID, false)
	if err != nil {
		t.Fatalf("CreateFolder child: %v", err)
	}
	// Moving parent under its own descendant must be rejected (cycle walk).
	if err := f.MoveFolder(1, parent.ID, &child.ID); err == nil {
		t.Fatal("expected cycle rejection moving parent under child")
	}
	// Moving the child to root (nil parent) must succeed and clear ParentID.
	if err := f.MoveFolder(1, child.ID, nil); err != nil {
		t.Fatalf("MoveFolder to root: %v", err)
	}
	got, err := f.GetFolder(1, child.ID)
	if err != nil {
		t.Fatalf("GetFolder: %v", err)
	}
	if got.ParentID != nil {
		t.Fatalf("expected nil parent after move to root, got %v", *got.ParentID)
	}
}
