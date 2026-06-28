package handlers

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/lgldsilva/jackui/internal/downloads"
	"github.com/lgldsilva/jackui/internal/streamer"
)

// hgA prefix on every identifier to avoid collisions with the other test files
// in this package.

const hgAValidHash = "bfb1741ecb8e7641158943545beb97c216158405"

func hgAStore(t *testing.T) *downloads.Store {
	t.Helper()
	s, err := downloads.New(seededPool(t))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

// hgAFavStreamer returns a streamer with a real favorites store wired in so the
// import handler's success path can persist a favorite.
func hgAFavStreamer(t *testing.T) *streamer.Streamer {
	t.Helper()
	favs, err := streamer.NewFavorites(seededPool(t))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { favs.Close() })
	s := streamer.NewForTesting()
	s.SetFavorites(favs)
	return s
}

func hgADo(router *gin.Engine, method, path string, body []byte) *httptest.ResponseRecorder {
	var r *http.Request
	if body != nil {
		r = httptest.NewRequest(method, path, bytes.NewReader(body))
		r.Header.Set("Content-Type", "application/json")
	} else {
		r = httptest.NewRequest(method, path, nil)
	}
	w := httptest.NewRecorder()
	router.ServeHTTP(w, r)
	return w
}

// hgACompletedDownload inserts a completed download whose FilePath points at a
// real file on disk, so promote can actually move it. Returns the created row.
func hgACompletedDownload(t *testing.T, store *downloads.Store, srcDir, fileName string) *downloads.Download {
	t.Helper()
	src := filepath.Join(srcDir, fileName)
	if err := os.WriteFile(src, []byte("payload"), 0o644); err != nil {
		t.Fatal(err)
	}
	d, err := store.Create(downloads.Download{
		InfoHash: hgAValidHash,
		Name:     fileName,
		Magnet:   MagnetPrefix + hgAValidHash,
		FilePath: src,
		FileSize: 7,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.SetFilePath(0, d.ID, src); err != nil {
		t.Fatal(err)
	}
	if err := store.SetStatus(0, d.ID, downloads.StatusCompleted); err != nil {
		t.Fatal(err)
	}
	got, err := store.Get(0, d.ID)
	if err != nil {
		t.Fatal(err)
	}
	return got
}

// ----------------------------------------------------------------------------
// import.go
// ----------------------------------------------------------------------------

func Test_hgA_StreamImport_BadBody(t *testing.T) {
	gin.SetMode(gin.TestMode)
	s := hgAFavStreamer(t)
	router := gin.New()
	router.POST("/api/stream/import", StreamImport(s))

	w := hgADo(router, "POST", "/api/stream/import", []byte("not json"))
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status=%d want 400; body=%s", w.Code, w.Body.String())
	}
}

func Test_hgA_StreamImport_NilFavorites(t *testing.T) {
	gin.SetMode(gin.TestMode)
	s := streamer.NewForTesting() // no favorites wired
	router := gin.New()
	router.POST("/api/stream/import", StreamImport(s))

	body, _ := json.Marshal(importReq{Magnet: MagnetPrefix + hgAValidHash})
	w := hgADo(router, "POST", "/api/stream/import", body)
	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("status=%d want 503; body=%s", w.Code, w.Body.String())
	}
}

func Test_hgA_StreamImport_NoSource(t *testing.T) {
	gin.SetMode(gin.TestMode)
	s := hgAFavStreamer(t)
	router := gin.New()
	router.POST("/api/stream/import", StreamImport(s))

	body, _ := json.Marshal(importReq{})
	w := hgADo(router, "POST", "/api/stream/import", body)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status=%d want 400; body=%s", w.Code, w.Body.String())
	}
}

func Test_hgA_StreamImport_InvalidMagnet(t *testing.T) {
	gin.SetMode(gin.TestMode)
	s := hgAFavStreamer(t)
	router := gin.New()
	router.POST("/api/stream/import", StreamImport(s))

	body, _ := json.Marshal(importReq{Magnet: "this is not a magnet"})
	w := hgADo(router, "POST", "/api/stream/import", body)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status=%d want 400; body=%s", w.Code, w.Body.String())
	}
}

func Test_hgA_StreamImport_BadBase64(t *testing.T) {
	gin.SetMode(gin.TestMode)
	s := hgAFavStreamer(t)
	router := gin.New()
	router.POST("/api/stream/import", StreamImport(s))

	body, _ := json.Marshal(importReq{TorrentB64: "data:application/x-bittorrent;base64,!!!notbase64!!!"})
	w := hgADo(router, "POST", "/api/stream/import", body)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status=%d want 400; body=%s", w.Code, w.Body.String())
	}
}

func Test_hgA_StreamImport_MagnetSuccess(t *testing.T) {
	gin.SetMode(gin.TestMode)
	s := hgAFavStreamer(t)
	router := gin.New()
	router.POST("/api/stream/import", StreamImport(s))

	body, _ := json.Marshal(importReq{
		Magnet: MagnetPrefix + hgAValidHash + "&dn=Star+Wars",
		Name:   "  Custom Name  ",
	})
	w := hgADo(router, "POST", "/api/stream/import", body)
	if w.Code != http.StatusOK {
		t.Fatalf("status=%d want 200; body=%s", w.Code, w.Body.String())
	}
	var resp struct {
		InfoHash string `json:"infoHash"`
		Name     string `json:"name"`
		Magnet   string `json:"magnet"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	if resp.InfoHash != hgAValidHash {
		t.Errorf("infoHash=%q want %q", resp.InfoHash, hgAValidHash)
	}
	// Explicit Name (trimmed) overrides the magnet display name.
	if resp.Name != "Custom Name" {
		t.Errorf("name=%q want %q", resp.Name, "Custom Name")
	}
}

func Test_hgA_StreamImport_MagnetSuccessWithFolder(t *testing.T) {
	gin.SetMode(gin.TestMode)
	s := hgAFavStreamer(t)
	router := gin.New()
	router.POST("/api/stream/import", StreamImport(s))

	folderID := 1
	body, _ := json.Marshal(importReq{
		Magnet:   MagnetPrefix + hgAValidHash + "&dn=Movie",
		FolderID: &folderID,
	})
	w := hgADo(router, "POST", "/api/stream/import", body)
	if w.Code != http.StatusOK {
		t.Fatalf("status=%d want 200; body=%s", w.Code, w.Body.String())
	}
}

func Test_hgA_resolveImportSource_None(t *testing.T) {
	s := streamer.NewForTesting()
	_, _, _, err := resolveImportSource(s, &importReq{})
	if err == nil {
		t.Fatal("expected error when neither magnet nor torrent provided")
	}
}

func Test_hgA_resolveTorrentB64Import_TooLarge(t *testing.T) {
	s := streamer.NewForTesting()
	// 9 MB of zero bytes base64-encoded → exceeds the 8 MB cap.
	big := make([]byte, 9<<20)
	b64 := base64.StdEncoding.EncodeToString(big)
	_, _, _, err := resolveTorrentB64Import(s, b64)
	if err == nil {
		t.Fatal("expected size-cap error")
	}
}

// ----------------------------------------------------------------------------
// downloads_promote.go — pure helpers
// ----------------------------------------------------------------------------

func Test_hgA_BuildPromoteDests(t *testing.T) {
	dests := BuildPromoteDests("/shared", []PromoteDest{{Name: "GDrive", Path: "/mnt/gdrive"}})
	if len(dests) != 2 {
		t.Fatalf("len=%d want 2", len(dests))
	}
	if dests[0].Name != "Biblioteca" || dests[0].Path != "/shared" {
		t.Errorf("first dest = %+v", dests[0])
	}
	if dests[1].Path != "/mnt/gdrive" {
		t.Errorf("second dest = %+v", dests[1])
	}

	// Empty sharedDir → only the extras.
	only := BuildPromoteDests("", []PromoteDest{{Name: "X", Path: "/x"}})
	if len(only) != 1 || only[0].Path != "/x" {
		t.Errorf("empty shared dests = %+v", only)
	}
}

func Test_hgA_resolveTargetBase(t *testing.T) {
	dests := []PromoteDest{{Name: "Biblioteca", Path: "/shared"}, {Name: "G", Path: "/g"}}

	got, err := resolveTargetBase("", "/shared", dests)
	if err != nil || got != "/shared" {
		t.Errorf("empty base: got=%q err=%v", got, err)
	}

	got, err = resolveTargetBase("/g", "/shared", dests)
	if err != nil || got != "/g" {
		t.Errorf("matching base: got=%q err=%v", got, err)
	}

	if _, err := resolveTargetBase("/nope", "/shared", dests); err == nil {
		t.Error("expected error for unknown base")
	}
}

func Test_hgA_sanitizeSubdir(t *testing.T) {
	cases := []struct {
		in      string
		want    string
		wantErr bool
	}{
		{"", "", false},
		{".", "", false},
		{"movies/2026", filepath.Clean("movies/2026"), false},
		{"../escape", "", true},
		{"a/../../b", "", true},
		{"/abs/path", "", true},
	}
	for _, tc := range cases {
		got, err := sanitizeSubdir(tc.in)
		if tc.wantErr && err == nil {
			t.Errorf("sanitizeSubdir(%q) expected error", tc.in)
			continue
		}
		if !tc.wantErr {
			if err != nil {
				t.Errorf("sanitizeSubdir(%q) err=%v", tc.in, err)
			}
			if got != tc.want {
				t.Errorf("sanitizeSubdir(%q)=%q want %q", tc.in, got, tc.want)
			}
		}
	}
}

func Test_hgA_joinIfSub(t *testing.T) {
	if got := joinIfSub("/root", ""); got != "/root" {
		t.Errorf("empty sub = %q", got)
	}
	if got := joinIfSub("/root", "sub"); got != filepath.Join("/root", "sub") {
		t.Errorf("with sub = %q", got)
	}
}

func Test_hgA_listDirs(t *testing.T) {
	dir := t.TempDir()
	_ = os.Mkdir(filepath.Join(dir, "beta"), 0o755)
	_ = os.Mkdir(filepath.Join(dir, "alpha"), 0o755)
	_ = os.Mkdir(filepath.Join(dir, ".hidden"), 0o755)
	_ = os.WriteFile(filepath.Join(dir, "file.txt"), []byte("x"), 0o644)

	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	dirs := listDirs(entries)
	// hidden dirs and files excluded; result sorted.
	if len(dirs) != 2 || dirs[0] != "alpha" || dirs[1] != "beta" {
		t.Errorf("listDirs = %v want [alpha beta]", dirs)
	}

	// Regression: a folder with no subdirs must return a NON-NIL slice so it
	// serializes as JSON [] (not null). A nil slice → null → the UI's
	// dirs.length crashes ("null is not an object (evaluating 'f.length')").
	leaf := t.TempDir()
	_ = os.WriteFile(filepath.Join(leaf, "only-a-file.mkv"), []byte("x"), 0o644)
	leafEntries, err := os.ReadDir(leaf)
	if err != nil {
		t.Fatal(err)
	}
	noDirs := listDirs(leafEntries)
	if noDirs == nil {
		t.Error("listDirs returned nil for a subdir-less folder; want non-nil empty slice (JSON [])")
	}
	if b, _ := json.Marshal(noDirs); string(b) != "[]" {
		t.Errorf("listDirs([]) marshaled to %s, want []", b)
	}
}

func Test_hgA_safeBaseName(t *testing.T) {
	if got := safeBaseName("/a/b/movie.mkv", "fallback"); got != "movie.mkv" {
		t.Errorf("got %q", got)
	}
	if got := safeBaseName("/", "fallback"); got != "fallback" {
		t.Errorf("root fallback got %q", got)
	}
}

func Test_hgA_promoteTargetDir(t *testing.T) {
	o := &promoteOpts{sharedDir: "/shared", targetSubdir: ""}
	got, err := promoteTargetDir(o)
	if err != nil || got != "/shared" {
		t.Errorf("empty subdir: got=%q err=%v", got, err)
	}

	o.targetSubdir = "movies"
	got, err = promoteTargetDir(o)
	if err != nil || got != filepath.Join("/shared", "movies") {
		t.Errorf("with subdir: got=%q err=%v", got, err)
	}

	o.targetSubdir = "../escape"
	if _, err := promoteTargetDir(o); err == nil {
		t.Error("expected error for traversal subdir")
	}
}

func Test_hgA_promoteDestPath_NoRename(t *testing.T) {
	targetDir := "/shared/movies"
	o := &promoteOpts{sharedDir: "/shared", renameIA: false}
	got := promoteDestPath(o, "movie.mkv", &targetDir)
	if got != filepath.Join("/shared/movies", "movie.mkv") {
		t.Errorf("got %q", got)
	}
}

func Test_hgA_movePathJob_File(t *testing.T) {
	src := filepath.Join(t.TempDir(), "src.bin")
	dst := filepath.Join(t.TempDir(), "dst.bin")
	if err := os.WriteFile(src, []byte("hello"), 0o644); err != nil {
		t.Fatal(err)
	}
	st, _ := os.Stat(src)
	if err := movePathJob(src, dst, st, nil, 0, 0); err != nil {
		t.Fatalf("movePathJob file: %v", err)
	}
	if _, err := os.Stat(dst); err != nil {
		t.Errorf("dst not present: %v", err)
	}
	if _, err := os.Stat(src); !os.IsNotExist(err) {
		t.Errorf("src should be gone after move")
	}
}

// Regressão #2105: promover um whole-torrent (file_path = DIRETÓRIO) caía no
// caminho cross-device e tratava o diretório como arquivo único, estourando
// "read ...: is a directory". copyDirAndRemoveJob copia a árvore inteira.
func Test_hgA_copyDirAndRemove_Tree(t *testing.T) {
	src := filepath.Join(t.TempDir(), "Brasiloirinha")
	if err := os.MkdirAll(filepath.Join(src, "sub"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(src, "a.mp4"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(src, "sub", "b.mp4"), []byte("yy"), 0o644); err != nil {
		t.Fatal(err)
	}
	dst := filepath.Join(t.TempDir(), "out")
	st, _ := os.Stat(src)
	if err := copyDirAndRemoveJob(src, dst, st, nil); err != nil {
		t.Fatalf("copyDirAndRemoveJob: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dst, "a.mp4")); err != nil {
		t.Errorf("dst/a.mp4 ausente: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dst, "sub", "b.mp4")); err != nil {
		t.Errorf("dst/sub/b.mp4 ausente: %v", err)
	}
	if _, err := os.Stat(src); !os.IsNotExist(err) {
		t.Errorf("src deveria ser removido após o move da árvore")
	}
}

func Test_hgA_applySeedingAfterPromote_KeepSeeding(t *testing.T) {
	s := streamer.NewForTesting()
	o := &promoteOpts{s: s, keepSeeding: true}
	// keepSeeding=true → Drop + re-add (EnsureActive). The empty magnet makes
	// EnsureActive fail-and-log; the point is it doesn't panic and re-points.
	d := &downloads.Download{ID: 1, InfoHash: hgAValidHash, Name: "x"}
	applySeedingAfterPromote(o, d)
}

func Test_hgA_applySeedingAfterPromote_InvalidHash(t *testing.T) {
	s := streamer.NewForTesting()
	o := &promoteOpts{s: s, keepSeeding: false}
	// Invalid hash → FromHexString fails → returns early, no Drop, no panic.
	applySeedingAfterPromote(o, &downloads.Download{InfoHash: "nothex"})
}

func Test_hgA_applySeedingAfterPromote_EmptyHash(t *testing.T) {
	s := streamer.NewForTesting()
	o := &promoteOpts{s: s, keepSeeding: true}
	// Empty hash → returns immediately (nothing to drop/re-add).
	applySeedingAfterPromote(o, &downloads.Download{InfoHash: ""})
}

// ----------------------------------------------------------------------------
// downloads_promote.go — promoteOne + handlers
// ----------------------------------------------------------------------------

func Test_hgA_promotePrepare_NotFound(t *testing.T) {
	store := hgAStore(t)
	s := streamer.NewForTesting()
	_, err := promotePreparePlan(&promoteOpts{store: store, s: s, sharedDir: t.TempDir(), userID: 0, id: 999})
	if err == nil {
		t.Fatal("expected not-found error")
	}
}

func Test_hgA_promotePrepare_NotCompleted(t *testing.T) {
	store := hgAStore(t)
	s := streamer.NewForTesting()
	d, err := store.Create(downloads.Download{InfoHash: hgAValidHash, Magnet: MagnetPrefix + hgAValidHash, Name: "x"})
	if err != nil {
		t.Fatal(err)
	}
	_, err = promotePreparePlan(&promoteOpts{store: store, s: s, sharedDir: t.TempDir(), userID: 0, id: d.ID})
	if err == nil {
		t.Fatal("expected error for non-completed download")
	}
}

// Prepare valida e devolve o plano; runPromotePlan faz a cópia (job nil = sem
// reporte) + atualiza file_path. Espelha o caminho async do handler.
func Test_hgA_promotePlan_Success(t *testing.T) {
	store := hgAStore(t)
	s := streamer.NewForTesting()
	srcDir := t.TempDir()
	shared := t.TempDir()
	d := hgACompletedDownload(t, store, srcDir, "movie.mkv")

	o := &promoteOpts{store: store, s: s, sharedDir: shared, userID: 0, id: d.ID, keepSeeding: false}
	plan, err := promotePreparePlan(o)
	if err != nil {
		t.Fatalf("promotePreparePlan: %v", err)
	}
	if plan == nil {
		t.Fatal("expected a plan (src != dst)")
	}
	if err := runPromotePlan(o, plan, nil); err != nil {
		t.Fatalf("runPromotePlan: %v", err)
	}
	updated, _ := store.Get(0, d.ID)
	if updated.FilePath != filepath.Join(shared, "movie.mkv") {
		t.Errorf("FilePath=%q", updated.FilePath)
	}
	if _, err := os.Stat(filepath.Join(shared, "movie.mkv")); err != nil {
		t.Errorf("file not moved: %v", err)
	}
}

func Test_hgA_promotePrepare_MissingSrc(t *testing.T) {
	store := hgAStore(t)
	s := streamer.NewForTesting()
	// Create completed row pointing at a file that doesn't exist on disk.
	d, err := store.Create(downloads.Download{InfoHash: hgAValidHash, Magnet: MagnetPrefix + hgAValidHash, Name: "gone.mkv", FilePath: "/nonexistent/gone.mkv"})
	if err != nil {
		t.Fatal(err)
	}
	_ = store.SetStatus(0, d.ID, downloads.StatusCompleted)
	_, err = promotePreparePlan(&promoteOpts{store: store, s: s, sharedDir: t.TempDir(), userID: 0, id: d.ID})
	if err == nil {
		t.Fatal("expected error for missing source file")
	}
}

func Test_hgA_DownloadsPromote_InvalidID(t *testing.T) {
	gin.SetMode(gin.TestMode)
	store := hgAStore(t)
	s := streamer.NewForTesting()
	router := gin.New()
	router.POST("/api/downloads/:id/promote", DownloadsPromote(store, s, nil, nil, t.TempDir(), nil, nil, nil, nil))

	w := hgADo(router, "POST", "/api/downloads/notanumber/promote", []byte("{}"))
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status=%d want 400", w.Code)
	}
}

func Test_hgA_DownloadsPromote_NoSharedDir(t *testing.T) {
	gin.SetMode(gin.TestMode)
	store := hgAStore(t)
	s := streamer.NewForTesting()
	router := gin.New()
	router.POST("/api/downloads/:id/promote", DownloadsPromote(store, s, nil, nil, "", nil, nil, nil, nil))

	w := hgADo(router, "POST", "/api/downloads/1/promote", []byte("{}"))
	if w.Code != http.StatusConflict {
		t.Fatalf("status=%d want 409", w.Code)
	}
}

func Test_hgA_DownloadsPromote_BadBase(t *testing.T) {
	gin.SetMode(gin.TestMode)
	store := hgAStore(t)
	s := streamer.NewForTesting()
	router := gin.New()
	router.POST("/api/downloads/:id/promote", DownloadsPromote(store, s, nil, nil, t.TempDir(), nil, nil, nil, nil))

	body, _ := json.Marshal(promoteReq{TargetBase: "/not/a/dest"})
	w := hgADo(router, "POST", "/api/downloads/1/promote", body)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status=%d want 400; body=%s", w.Code, w.Body.String())
	}
}

func Test_hgA_DownloadsPromote_Success(t *testing.T) {
	gin.SetMode(gin.TestMode)
	store := hgAStore(t)
	s := streamer.NewForTesting()
	srcDir := t.TempDir()
	shared := t.TempDir()
	d := hgACompletedDownload(t, store, srcDir, "promote_me.mkv")

	router := gin.New()
	router.POST("/api/downloads/:id/promote", DownloadsPromote(store, s, nil, nil, shared, nil, nil, nil, nil))

	body, _ := json.Marshal(promoteReq{KeepSeeding: true})
	w := hgADo(router, "POST", "/api/downloads/"+itoa(d.ID)+"/promote", body)
	if w.Code != http.StatusOK {
		t.Fatalf("status=%d want 200; body=%s", w.Code, w.Body.String())
	}
	// A cópia roda em background (tr.Submit → goroutine), então o handler retorna
	// 200 antes de mover. Aguarda o arquivo aparecer no destino (polling curto).
	moved := false
	for i := 0; i < 100; i++ {
		if _, err := os.Stat(filepath.Join(shared, "promote_me.mkv")); err == nil {
			moved = true
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if !moved {
		t.Errorf("file not promoted within timeout (async copy)")
	}
}

func Test_hgA_DownloadsPromoteBatch_NoSharedDir(t *testing.T) {
	gin.SetMode(gin.TestMode)
	store := hgAStore(t)
	s := streamer.NewForTesting()
	router := gin.New()
	router.POST("/api/downloads/promote", DownloadsPromoteBatch(store, s, nil, nil, "", nil, nil, nil, nil))

	w := hgADo(router, "POST", "/api/downloads/promote", []byte("{}"))
	if w.Code != http.StatusConflict {
		t.Fatalf("status=%d want 409", w.Code)
	}
}

func Test_hgA_DownloadsPromoteBatch_BadBody(t *testing.T) {
	gin.SetMode(gin.TestMode)
	store := hgAStore(t)
	s := streamer.NewForTesting()
	router := gin.New()
	router.POST("/api/downloads/promote", DownloadsPromoteBatch(store, s, nil, nil, t.TempDir(), nil, nil, nil, nil))

	w := hgADo(router, "POST", "/api/downloads/promote", []byte("not json"))
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status=%d want 400", w.Code)
	}
}

func Test_hgA_DownloadsPromoteBatch_EmptyIDs(t *testing.T) {
	gin.SetMode(gin.TestMode)
	store := hgAStore(t)
	s := streamer.NewForTesting()
	router := gin.New()
	router.POST("/api/downloads/promote", DownloadsPromoteBatch(store, s, nil, nil, t.TempDir(), nil, nil, nil, nil))

	body, _ := json.Marshal(promoteReq{IDs: []int{}})
	w := hgADo(router, "POST", "/api/downloads/promote", body)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status=%d want 400", w.Code)
	}
}

func Test_hgA_DownloadsPromoteBatch_Mixed(t *testing.T) {
	gin.SetMode(gin.TestMode)
	store := hgAStore(t)
	s := streamer.NewForTesting()
	srcDir := t.TempDir()
	shared := t.TempDir()
	good := hgACompletedDownload(t, store, srcDir, "ok.mkv")

	router := gin.New()
	router.POST("/api/downloads/promote", DownloadsPromoteBatch(store, s, nil, nil, shared, nil, nil, nil, nil))

	// One valid id + one bogus id → promoted 1, failed 1.
	body, _ := json.Marshal(promoteReq{IDs: []int{good.ID, 99999}, KeepSeeding: true})
	w := hgADo(router, "POST", "/api/downloads/promote", body)
	if w.Code != http.StatusOK {
		t.Fatalf("status=%d want 200; body=%s", w.Code, w.Body.String())
	}
	var resp struct {
		Promoted []downloads.Download `json:"promoted"`
		Failed   []map[string]any     `json:"failed"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	if len(resp.Promoted) != 1 {
		t.Errorf("promoted=%d want 1", len(resp.Promoted))
	}
	if len(resp.Failed) != 1 {
		t.Errorf("failed=%d want 1", len(resp.Failed))
	}
}

func Test_hgA_DownloadsPromotePreview_NoSharedDir(t *testing.T) {
	gin.SetMode(gin.TestMode)
	store := hgAStore(t)
	router := gin.New()
	router.POST("/api/downloads/promote/preview", DownloadsPromotePreview(store, nil, nil, "", nil))

	w := hgADo(router, "POST", "/api/downloads/promote/preview", []byte("{}"))
	if w.Code != http.StatusConflict {
		t.Fatalf("status=%d want 409", w.Code)
	}
}

func Test_hgA_DownloadsPromotePreview_BadBody(t *testing.T) {
	gin.SetMode(gin.TestMode)
	store := hgAStore(t)
	router := gin.New()
	router.POST("/api/downloads/promote/preview", DownloadsPromotePreview(store, nil, nil, t.TempDir(), nil))

	w := hgADo(router, "POST", "/api/downloads/promote/preview", []byte("not json"))
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status=%d want 400", w.Code)
	}
}

func Test_hgA_DownloadsPromotePreview_EmptyIDs(t *testing.T) {
	gin.SetMode(gin.TestMode)
	store := hgAStore(t)
	router := gin.New()
	router.POST("/api/downloads/promote/preview", DownloadsPromotePreview(store, nil, nil, t.TempDir(), nil))

	body, _ := json.Marshal(promoteReq{IDs: []int{}})
	w := hgADo(router, "POST", "/api/downloads/promote/preview", body)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status=%d want 400", w.Code)
	}
}

func Test_hgA_DownloadsPromotePreview_BadBase(t *testing.T) {
	gin.SetMode(gin.TestMode)
	store := hgAStore(t)
	router := gin.New()
	router.POST("/api/downloads/promote/preview", DownloadsPromotePreview(store, nil, nil, t.TempDir(), nil))

	body, _ := json.Marshal(promoteReq{IDs: []int{1}, TargetBase: "/bad"})
	w := hgADo(router, "POST", "/api/downloads/promote/preview", body)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status=%d want 400", w.Code)
	}
}

func Test_hgA_previewOneDownload_Errors(t *testing.T) {
	store := hgAStore(t)
	d := &previewDeps{store: store, userID: 0, base: t.TempDir()}

	// Not found.
	res := previewOneDownload(d, 999)
	if res["error"] == nil {
		t.Error("expected not-found error in preview")
	}

	// Empty file_path.
	created, err := store.Create(downloads.Download{InfoHash: hgAValidHash, Magnet: MagnetPrefix + hgAValidHash, Name: "x"})
	if err != nil {
		t.Fatal(err)
	}
	res = previewOneDownload(d, created.ID)
	if res["error"] != "file_path vazio" {
		t.Errorf("error=%v want 'file_path vazio'", res["error"])
	}
}

func Test_hgA_DownloadsPromoteBrowse(t *testing.T) {
	gin.SetMode(gin.TestMode)
	shared := t.TempDir()
	_ = os.Mkdir(filepath.Join(shared, "movies"), 0o755)
	_ = os.Mkdir(filepath.Join(shared, "shows"), 0o755)

	router := gin.New()
	router.GET("/api/downloads/promote/browse", DownloadsPromoteBrowse(shared, nil))

	// Success: list root dirs.
	w := hgADo(router, "GET", "/api/downloads/promote/browse", nil)
	if w.Code != http.StatusOK {
		t.Fatalf("status=%d want 200; body=%s", w.Code, w.Body.String())
	}
	var resp struct {
		Dirs []string `json:"dirs"`
	}
	_ = json.Unmarshal(w.Body.Bytes(), &resp)
	if len(resp.Dirs) != 2 {
		t.Errorf("dirs=%v want 2", resp.Dirs)
	}

	// Nonexistent path → empty dirs, still 200.
	w = hgADo(router, "GET", "/api/downloads/promote/browse?path=missing", nil)
	if w.Code != http.StatusOK {
		t.Fatalf("missing path status=%d want 200", w.Code)
	}
}

func Test_hgA_DownloadsPromoteBrowse_NoSharedDir(t *testing.T) {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.GET("/api/downloads/promote/browse", DownloadsPromoteBrowse("", nil))
	w := hgADo(router, "GET", "/api/downloads/promote/browse", nil)
	if w.Code != http.StatusConflict {
		t.Fatalf("status=%d want 409", w.Code)
	}
}

func Test_hgA_DownloadsPromoteBrowse_BadBase(t *testing.T) {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.GET("/api/downloads/promote/browse", DownloadsPromoteBrowse(t.TempDir(), nil))
	w := hgADo(router, "GET", "/api/downloads/promote/browse?base=/bad", nil)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status=%d want 400", w.Code)
	}
}

func Test_hgA_DownloadsPromoteBrowse_BadPath(t *testing.T) {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.GET("/api/downloads/promote/browse", DownloadsPromoteBrowse(t.TempDir(), nil))
	w := hgADo(router, "GET", "/api/downloads/promote/browse?path=../escape", nil)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status=%d want 400", w.Code)
	}
}

func Test_hgA_DownloadsPromoteDests(t *testing.T) {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.GET("/api/promote/destinations", DownloadsPromoteDests("/shared", []PromoteDest{{Name: "G", Path: "/g"}}))
	w := hgADo(router, "GET", "/api/promote/destinations", nil)
	if w.Code != http.StatusOK {
		t.Fatalf("status=%d want 200", w.Code)
	}
	var dests []PromoteDest
	_ = json.Unmarshal(w.Body.Bytes(), &dests)
	if len(dests) != 2 {
		t.Errorf("dests=%d want 2", len(dests))
	}
}

func Test_hgA_DownloadsStopSeed_InvalidID(t *testing.T) {
	gin.SetMode(gin.TestMode)
	store := hgAStore(t)
	s := streamer.NewForTesting()
	router := gin.New()
	router.POST("/api/downloads/:id/stop-seed", DownloadsStopSeed(store, s))
	w := hgADo(router, "POST", "/api/downloads/notanumber/stop-seed", nil)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status=%d want 400", w.Code)
	}
}

func Test_hgA_DownloadsStopSeed_NotFound(t *testing.T) {
	gin.SetMode(gin.TestMode)
	store := hgAStore(t)
	s := streamer.NewForTesting()
	router := gin.New()
	router.POST("/api/downloads/:id/stop-seed", DownloadsStopSeed(store, s))
	w := hgADo(router, "POST", "/api/downloads/999/stop-seed", nil)
	if w.Code != http.StatusNotFound {
		t.Fatalf("status=%d want 404", w.Code)
	}
}

func Test_hgA_DownloadsStopSeed_Success(t *testing.T) {
	gin.SetMode(gin.TestMode)
	store := hgAStore(t)
	s := streamer.NewForTesting()
	d, err := store.Create(downloads.Download{InfoHash: hgAValidHash, Magnet: MagnetPrefix + hgAValidHash, Name: "x"})
	if err != nil {
		t.Fatal(err)
	}
	router := gin.New()
	router.POST("/api/downloads/:id/stop-seed", DownloadsStopSeed(store, s))
	w := hgADo(router, "POST", "/api/downloads/"+itoa(d.ID)+"/stop-seed", nil)
	if w.Code != http.StatusNoContent {
		t.Fatalf("status=%d want 204", w.Code)
	}
}

// ----------------------------------------------------------------------------
// downloads.go — additional success / detail paths
// ----------------------------------------------------------------------------

func Test_hgA_getDownloadFileStat(t *testing.T) {
	// Empty path → zero value.
	if st := getDownloadFileStat(""); st.Exists {
		t.Error("empty path should not exist")
	}
	// Missing path → zero value.
	if st := getDownloadFileStat("/nonexistent/x"); st.Exists {
		t.Error("missing path should not exist")
	}
	// Real file → Exists true with apparent size.
	f := filepath.Join(t.TempDir(), "f.bin")
	if err := os.WriteFile(f, []byte("12345"), 0o644); err != nil {
		t.Fatal(err)
	}
	st := getDownloadFileStat(f)
	if !st.Exists || st.Apparent != 5 {
		t.Errorf("stat=%+v", st)
	}
}

func Test_hgA_getDownloadTorrentInfo(t *testing.T) {
	s := streamer.NewForTesting()

	// Invalid hash + no trackers → nil.
	if info := getDownloadTorrentInfo(s, "nothex", ""); info != nil {
		t.Errorf("expected nil, got %+v", info)
	}

	// Invalid hash + magnet trackers → synthesized TorrentInfo with trackers.
	magnet := MagnetPrefix + hgAValidHash + "&tr=udp://t.example:1337"
	info := getDownloadTorrentInfo(s, "nothex", magnet)
	if info == nil || len(info.Trackers) != 1 {
		t.Errorf("expected synthesized trackers, got %+v", info)
	}
}

func Test_hgA_DownloadsDetails_NotFound(t *testing.T) {
	gin.SetMode(gin.TestMode)
	store := hgAStore(t)
	s := streamer.NewForTesting()
	router := gin.New()
	router.GET("/api/downloads/:id/details", DownloadsDetails(store, s))
	w := hgADo(router, "GET", "/api/downloads/999/details", nil)
	if w.Code != http.StatusNotFound {
		t.Fatalf("status=%d want 404", w.Code)
	}
}

func Test_hgA_DownloadsDetails_Success(t *testing.T) {
	gin.SetMode(gin.TestMode)
	store := hgAStore(t)
	s := streamer.NewForTesting()
	srcDir := t.TempDir()
	d := hgACompletedDownload(t, store, srcDir, "details.mkv")

	router := gin.New()
	router.GET("/api/downloads/:id/details", DownloadsDetails(store, s))
	w := hgADo(router, "GET", "/api/downloads/"+itoa(d.ID)+"/details", nil)
	if w.Code != http.StatusOK {
		t.Fatalf("status=%d want 200; body=%s", w.Code, w.Body.String())
	}
	var resp struct {
		File downloadFileStat `json:"file"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	if !resp.File.Exists {
		t.Error("expected file.exists true")
	}
}

func Test_hgA_DownloadsRecheck_NotFound(t *testing.T) {
	gin.SetMode(gin.TestMode)
	store := hgAStore(t)
	s := streamer.NewForTesting()
	router := gin.New()
	router.POST("/api/downloads/:id/recheck", DownloadsRecheck(store, s))
	w := hgADo(router, "POST", "/api/downloads/999/recheck", nil)
	if w.Code != http.StatusNotFound {
		t.Fatalf("status=%d want 404", w.Code)
	}
}

func Test_hgA_DownloadsPause_NotFound(t *testing.T) {
	gin.SetMode(gin.TestMode)
	store := hgAStore(t)
	router := gin.New()
	router.PATCH("/api/downloads/:id/pause", DownloadsPause(store))
	w := hgADo(router, "PATCH", "/api/downloads/999/pause", nil)
	if w.Code != http.StatusNotFound {
		t.Fatalf("status=%d want 404", w.Code)
	}
}

func Test_hgA_DownloadsPause_Success(t *testing.T) {
	gin.SetMode(gin.TestMode)
	store := hgAStore(t)
	d, err := store.Create(downloads.Download{InfoHash: hgAValidHash, Magnet: MagnetPrefix + hgAValidHash, Name: "x"})
	if err != nil {
		t.Fatal(err)
	}
	router := gin.New()
	router.PATCH("/api/downloads/:id/pause", DownloadsPause(store))
	w := hgADo(router, "PATCH", "/api/downloads/"+itoa(d.ID)+"/pause", nil)
	if w.Code != http.StatusNoContent {
		t.Fatalf("status=%d want 204", w.Code)
	}
}

func Test_hgA_DownloadsResume_NotFound(t *testing.T) {
	gin.SetMode(gin.TestMode)
	store := hgAStore(t)
	router := gin.New()
	router.PATCH("/api/downloads/:id/resume", DownloadsResume(store))
	w := hgADo(router, "PATCH", "/api/downloads/999/resume", nil)
	if w.Code != http.StatusNotFound {
		t.Fatalf("status=%d want 404", w.Code)
	}
}

func Test_hgA_DownloadsBatchPause_Success(t *testing.T) {
	gin.SetMode(gin.TestMode)
	store := hgAStore(t)
	d, err := store.Create(downloads.Download{InfoHash: hgAValidHash, Magnet: MagnetPrefix + hgAValidHash, Name: "x"})
	if err != nil {
		t.Fatal(err)
	}
	router := gin.New()
	router.PATCH("/api/downloads/batch/pause", DownloadsBatchPause(store))
	body, _ := json.Marshal(map[string][]int{"ids": {d.ID}})
	w := hgADo(router, "PATCH", "/api/downloads/batch/pause", body)
	if w.Code != http.StatusOK {
		t.Fatalf("status=%d want 200", w.Code)
	}
}

func Test_hgA_DownloadsBatchResume_Success(t *testing.T) {
	gin.SetMode(gin.TestMode)
	store := hgAStore(t)
	d, err := store.Create(downloads.Download{InfoHash: hgAValidHash, Magnet: MagnetPrefix + hgAValidHash, Name: "x"})
	if err != nil {
		t.Fatal(err)
	}
	_ = store.SetStatus(0, d.ID, downloads.StatusPaused)
	router := gin.New()
	router.PATCH("/api/downloads/batch/resume", DownloadsBatchResume(store))
	body, _ := json.Marshal(map[string][]int{"ids": {d.ID}})
	w := hgADo(router, "PATCH", "/api/downloads/batch/resume", body)
	if w.Code != http.StatusOK {
		t.Fatalf("status=%d want 200", w.Code)
	}
}

func Test_hgA_DownloadsBatchDelete_Success(t *testing.T) {
	gin.SetMode(gin.TestMode)
	store := hgAStore(t)
	d, err := store.Create(downloads.Download{InfoHash: hgAValidHash, Magnet: MagnetPrefix + hgAValidHash, Name: "x"})
	if err != nil {
		t.Fatal(err)
	}
	router := gin.New()
	router.POST("/api/downloads/batch/delete", DownloadsBatchDelete(store, nil))
	body, _ := json.Marshal(map[string][]int{"ids": {d.ID, 99999}})
	w := hgADo(router, "POST", "/api/downloads/batch/delete", body)
	if w.Code != http.StatusOK {
		t.Fatalf("status=%d want 200", w.Code)
	}
	var resp struct {
		Deleted int   `json:"deleted"`
		Total   int   `json:"total"`
		Failed  []int `json:"failed"`
	}
	_ = json.Unmarshal(w.Body.Bytes(), &resp)
	// Idempotent delete: the missing id (99999) is not an error — it's already
	// gone, so it counts toward `deleted` and `failed` stays empty. `failed`
	// only carries IDs the store actually errored on.
	if resp.Deleted != 2 || resp.Total != 2 || len(resp.Failed) != 0 {
		t.Errorf("deleted=%d total=%d failed=%v want 2/2/[]", resp.Deleted, resp.Total, resp.Failed)
	}
}

func Test_hgA_DownloadsList_WithRow(t *testing.T) {
	gin.SetMode(gin.TestMode)
	store := hgAStore(t)
	srcDir := t.TempDir()
	// Completed download with FilePath OUTSIDE the download dir → Promoted=true.
	hgACompletedDownload(t, store, srcDir, "lib.mkv")

	router := gin.New()
	router.GET("/api/downloads", DownloadsList(store, nil, nil, nil, "/some/download/dir"))
	w := hgADo(router, "GET", "/api/downloads", nil)
	if w.Code != http.StatusOK {
		t.Fatalf("status=%d want 200", w.Code)
	}
	var list []downloads.Download
	_ = json.Unmarshal(w.Body.Bytes(), &list)
	if len(list) != 1 || !list[0].Promoted {
		t.Errorf("expected 1 promoted row, got %+v", list)
	}
}

// ----------------------------------------------------------------------------
// small local helpers (hgA-prefixed)
// ----------------------------------------------------------------------------

func itoa(i int) string {
	return strconv.Itoa(i)
}
