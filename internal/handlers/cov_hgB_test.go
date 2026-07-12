package handlers

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/lgldsilva/jackui/internal/config"
	"github.com/lgldsilva/jackui/internal/downloads"
	"github.com/lgldsilva/jackui/internal/local"
	"github.com/lgldsilva/jackui/internal/streamer"
	"github.com/lgldsilva/jackui/internal/transcode"
)

// ─── shared helpers (hgB-prefixed to avoid collisions) ──────────────────────

const hgBHexHash = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"

// hgBBrowser builds a local.Browser exposing a single mount "Test" rooted at a
// fresh temp dir (no AllowedUsers → anon access permitted, so no auth wiring
// needed for these handler paths).
func hgBBrowser(t *testing.T) (*local.Browser, string) {
	t.Helper()
	dir := t.TempDir()
	b := local.NewBrowser([]config.ExternalMount{{Name: "Test", Path: dir}})
	return b, dir
}

// hgBManager spins up a real HLSSessionManager backed by a temp dir. We never
// drive ffmpeg through it — only the Peek / not-found bookkeeping paths.
func hgBManager(t *testing.T) *transcode.HLSSessionManager {
	t.Helper()
	mgr, err := transcode.NewHLSManager(t.TempDir())
	if err != nil {
		t.Fatalf("NewHLSManager: %v", err)
	}
	return mgr
}

func hgBGET(t *testing.T, r *gin.Engine, url string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest("GET", url, nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	return w
}

// ─── hls.go ─────────────────────────────────────────────────────────────────

func Test_hgB_StreamHLSMaster_BadHash(t *testing.T) {
	gin.SetMode(gin.TestMode)
	s := streamer.NewForTesting()
	mgr := hgBManager(t)
	r := gin.New()
	r.GET("/m/:hash/:file", StreamHLSMaster(s, mgr, nil, nil))

	w := hgBGET(t, r, "/m/nothex/0")
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status=%d want 400; body=%s", w.Code, w.Body.String())
	}
}

func Test_hgB_StreamHLSMaster_BadFileIndex(t *testing.T) {
	gin.SetMode(gin.TestMode)
	s := streamer.NewForTesting()
	mgr := hgBManager(t)
	r := gin.New()
	r.GET("/m/:hash/:file", StreamHLSMaster(s, mgr, nil, nil))

	w := hgBGET(t, r, "/m/"+hgBHexHash+"/notanint")
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status=%d want 400; body=%s", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), errInvalidFileIndex) {
		t.Errorf("body should mention invalid file index; got %s", w.Body.String())
	}
}

func Test_hgB_StreamHLSSegment_BadHash(t *testing.T) {
	gin.SetMode(gin.TestMode)
	mgr := hgBManager(t)
	r := gin.New()
	r.GET("/s/:hash/:file/:seg", StreamHLSSegment(nil, mgr, nil))

	w := hgBGET(t, r, "/s/nothex/0/seg_00000.ts")
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status=%d want 400; body=%s", w.Code, w.Body.String())
	}
}

func Test_hgB_StreamHLSSegment_BadFileIndex(t *testing.T) {
	gin.SetMode(gin.TestMode)
	mgr := hgBManager(t)
	r := gin.New()
	r.GET("/s/:hash/:file/:seg", StreamHLSSegment(nil, mgr, nil))

	w := hgBGET(t, r, "/s/"+hgBHexHash+"/xx/seg_00000.ts")
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status=%d want 400; body=%s", w.Code, w.Body.String())
	}
}

// No active session for this key → resolveHLSSession / getSession (Peek) 404s.
func Test_hgB_StreamHLSSegment_SessionNotActive(t *testing.T) {
	gin.SetMode(gin.TestMode)
	mgr := hgBManager(t)
	r := gin.New()
	r.GET("/s/:hash/:file/:seg", StreamHLSSegment(nil, mgr, nil))

	w := hgBGET(t, r, "/s/"+hgBHexHash+"/0/seg_00000.ts")
	if w.Code != http.StatusNotFound {
		t.Fatalf("status=%d want 404; body=%s", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), "session not active") {
		t.Errorf("expected 'session not active' hint; got %s", w.Body.String())
	}
}

// resolveHLSSession returns nil (404) for an unknown key — exercised directly.
func Test_hgB_ResolveHLSSession_NotFound(t *testing.T) {
	gin.SetMode(gin.TestMode)
	mgr := hgBManager(t)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest("GET", "/", nil)

	h, err := parseHash(hgBHexHash)
	if err != nil {
		t.Fatalf("parseHash: %v", err)
	}
	if sess := resolveHLSSession(c, nil, mgr, nil, h, 0, "seg_00000.ts"); sess != nil {
		t.Fatal("expected nil session for unknown key")
	}
	if w.Code != http.StatusNotFound {
		t.Errorf("status=%d want 404", w.Code)
	}
}

// Quando a sessão sumiu (drop/reap), o handler RESSUSCITA-A a partir do arquivo
// completo em vez de 404 — evitando o burst de 404 que o Safari (VOD) dispara
// percorrendo a playlist inteira. Precisa de encoder (skip sem ffmpeg).
func Test_hgB_ResolveHLSSession_RespawnsFromCompletedFile(t *testing.T) {
	gin.SetMode(gin.TestMode)
	if _, err := transcode.Probe(context.Background(), true); err != nil || transcode.Cached() == nil {
		t.Skip("caps de transcode indisponíveis (sem ffmpeg?); respawn precisa do encoder")
	}
	mgr := hgBManager(t)
	store := newDownloadsStore(t)
	dir := t.TempDir()
	file := filepath.Join(dir, "movie.mkv")
	if err := os.WriteFile(file, []byte("payload-bytes"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	d, err := store.Create(downloads.Download{UserID: 1, InfoHash: hgBHexHash, FileIndex: 0, Magnet: "m", Name: "x", FilePath: file})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	store.SetStatus(1, d.ID, downloads.StatusCompleted)
	t.Cleanup(func() { mgr.Close(hgBHexHash + "-0") })

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest("GET", "/", nil)
	h, _ := parseHash(hgBHexHash)
	// Sem sessão ativa, mas com streamer + fonte completa → respawn (não 404).
	sess := resolveHLSSession(c, streamer.NewForTesting(), mgr, store, h, 0, "seg_00000.ts")
	if sess == nil {
		t.Fatal("esperava respawn da sessão a partir do arquivo completo, veio nil")
	}
}

// Respawn com fonte disponível mas encoder indisponível (caps não probadas) →
// startHLSSession falha e o handler retorna nil (404). Cobre o ramo de erro do
// respawn sem depender de ffmpeg.
func Test_hgB_ResolveHLSSession_RespawnEncoderUnavailable(t *testing.T) {
	gin.SetMode(gin.TestMode)
	transcode.ResetCachedForTesting() // força GetOrStart a falhar ("caps not probed")
	mgr := hgBManager(t)
	store := newDownloadsStore(t)
	dir := t.TempDir()
	file := filepath.Join(dir, "movie.mkv")
	if err := os.WriteFile(file, []byte("payload-bytes"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	d, err := store.Create(downloads.Download{UserID: 1, InfoHash: hgBHexHash, FileIndex: 0, Magnet: "m", Name: "x", FilePath: file})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	store.SetStatus(1, d.ID, downloads.StatusCompleted)

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest("GET", "/", nil)
	h, _ := parseHash(hgBHexHash)
	if sess := resolveHLSSession(c, streamer.NewForTesting(), mgr, store, h, 0, "seg_00000.ts"); sess != nil {
		t.Fatal("sem caps de encoder o respawn deve falhar (nil)")
	}
}

// resolveTranscodeSource prefers a completed file on disk over the streamer.
func Test_hgB_ResolveTranscodeSource_CompletedFile(t *testing.T) {
	gin.SetMode(gin.TestMode)
	store := newDownloadsStore(t)
	// Write a real completed file and register it.
	dir := t.TempDir()
	file := filepath.Join(dir, "movie.mkv")
	if err := os.WriteFile(file, []byte("payload-bytes"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	d, err := store.Create(downloads.Download{UserID: 1, InfoHash: hgBHexHash, FileIndex: 0, Magnet: "m", Name: "x", FilePath: file})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	store.SetStatus(1, d.ID, downloads.StatusCompleted)

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest("GET", "/", nil)
	h, _ := parseHash(hgBHexHash)
	hc := &hlsCtx{c: c, s: streamer.NewForTesting(), store: store, h: h, fileIdx: 0}

	src, size, complete := resolveTranscodeSource(hc)
	if src == nil {
		t.Fatal("expected a source reader for the completed file")
	}
	defer func() { _ = src.Close() }()
	if size != int64(len("payload-bytes")) {
		t.Errorf("size=%d want %d", size, len("payload-bytes"))
	}
	// A completed file on disk is a complete/seekable source → forces VOD.
	if !complete {
		t.Error("completed-file source must report complete=true (forces VOD)")
	}
}

// serveHLSPlaylist's VOD branch synthesises a finite playlist via buildVODPlaylist.
func Test_hgB_BuildVODPlaylist_ZeroDuration(t *testing.T) {
	pl := string(buildVODPlaylist(0, "", false))
	if !strings.Contains(pl, "#EXT-X-ENDLIST") {
		t.Error("zero-duration playlist must still be finite")
	}
	// ceil(0/4) clamps to at least 1 segment.
	if got := strings.Count(pl, "seg_"); got != 1 {
		t.Errorf("expected 1 segment for 0s, got %d\n%s", got, pl)
	}
}

// httpshared.EnsureVODSegment must no-op silently for an unparseable segment name.
func Test_hgB_EnsureVODSegment_BadName(t *testing.T) {
	// A nil-ish session would panic on IsVOD; instead validate the parse guard
	// indirectly: ParseSegIndex rejects junk, which is the gate httpshared.EnsureVODSegment
	// relies on before touching the session.
	if _, ok := transcode.ParseSegIndex("not-a-segment"); ok {
		t.Error("expected ParseSegIndex to reject junk name")
	}
}

// ─── local_play.go ──────────────────────────────────────────────────────────

// ─── local_subtitles.go ─────────────────────────────────────────────────────

// ─── stream.go ──────────────────────────────────────────────────────────────

func Test_hgB_StreamAdd_MissingMagnet(t *testing.T) {
	gin.SetMode(gin.TestMode)
	s := streamer.NewForTesting()
	r := gin.New()
	r.POST("/add", StreamAdd(s, nil))
	req := httptest.NewRequest("POST", "/add", strings.NewReader(`{"magnet":""}`))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status=%d want 400; body=%s", w.Code, w.Body.String())
	}
}

func Test_hgB_StreamAdd_InvalidJSON(t *testing.T) {
	gin.SetMode(gin.TestMode)
	s := streamer.NewForTesting()
	r := gin.New()
	r.POST("/add", StreamAdd(s, nil))
	req := httptest.NewRequest("POST", "/add", strings.NewReader(`not json`))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status=%d want 400; body=%s", w.Code, w.Body.String())
	}
}

func Test_hgB_StreamPlaylistM3U_BadHash(t *testing.T) {
	gin.SetMode(gin.TestMode)
	s := streamer.NewForTesting()
	r := gin.New()
	r.GET("/pl/:hash/:file", StreamPlaylistM3U(s))
	w := hgBGET(t, r, "/pl/nothex/0")
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status=%d want 400; body=%s", w.Code, w.Body.String())
	}
}

func Test_hgB_StreamPlaylistM3U_BadFileIndex(t *testing.T) {
	gin.SetMode(gin.TestMode)
	s := streamer.NewForTesting()
	r := gin.New()
	r.GET("/pl/:hash/:file", StreamPlaylistM3U(s))
	w := hgBGET(t, r, "/pl/"+hgBHexHash+"/zz")
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status=%d want 400; body=%s", w.Code, w.Body.String())
	}
}

func Test_hgB_StreamSetFilePriority_BadHash(t *testing.T) {
	gin.SetMode(gin.TestMode)
	s := streamer.NewForTesting()
	r := gin.New()
	r.POST("/p/:hash/files/:idx/priority", StreamSetFilePriority(s))
	req := httptest.NewRequest("POST", "/p/nothex/files/0/priority", strings.NewReader(`{"priority":"high"}`))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status=%d want 400; body=%s", w.Code, w.Body.String())
	}
}

func Test_hgB_StreamSetFilePriority_BadIdx(t *testing.T) {
	gin.SetMode(gin.TestMode)
	s := streamer.NewForTesting()
	r := gin.New()
	r.POST("/p/:hash/files/:idx/priority", StreamSetFilePriority(s))
	req := httptest.NewRequest("POST", "/p/"+hgBHexHash+"/files/zz/priority", strings.NewReader(`{"priority":"high"}`))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status=%d want 400; body=%s", w.Code, w.Body.String())
	}
}

func Test_hgB_StreamSetFilePriority_BadBody(t *testing.T) {
	gin.SetMode(gin.TestMode)
	s := streamer.NewForTesting()
	r := gin.New()
	r.POST("/p/:hash/files/:idx/priority", StreamSetFilePriority(s))
	req := httptest.NewRequest("POST", "/p/"+hgBHexHash+"/files/0/priority", strings.NewReader(`not json`))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status=%d want 400; body=%s", w.Code, w.Body.String())
	}
}

// Inactive torrent: SetFilePriority returns an error → handler maps to 400/404.
func Test_hgB_StreamSetFilePriority_NotActive(t *testing.T) {
	gin.SetMode(gin.TestMode)
	s := streamer.NewForTesting()
	r := gin.New()
	r.POST("/p/:hash/files/:idx/priority", StreamSetFilePriority(s))
	req := httptest.NewRequest("POST", "/p/"+hgBHexHash+"/files/0/priority", strings.NewReader(`{"priority":"high"}`))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusBadRequest && w.Code != http.StatusNotFound {
		t.Fatalf("status=%d want 400/404; body=%s", w.Code, w.Body.String())
	}
}

// StreamFile serves directly from the completed-download store when present.
func Test_hgB_StreamFile_FromCompletedStore(t *testing.T) {
	gin.SetMode(gin.TestMode)
	store := newDownloadsStore(t)
	dir := t.TempDir()
	file := filepath.Join(dir, "done.mp4")
	if err := os.WriteFile(file, []byte("completed-bytes"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	d, err := store.Create(downloads.Download{UserID: 1, InfoHash: hgBHexHash, FileIndex: 0, Magnet: "m", Name: "x", FilePath: file})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	store.SetStatus(1, d.ID, downloads.StatusCompleted)

	s := streamer.NewForTesting()
	r := gin.New()
	r.GET("/f/:hash/:file", StreamFile(s, store))
	w := hgBGET(t, r, "/f/"+hgBHexHash+"/0")
	if w.Code != http.StatusOK {
		t.Fatalf("status=%d want 200; body=%s", w.Code, w.Body.String())
	}
	if w.Body.String() != "completed-bytes" {
		t.Errorf("body=%q want completed-bytes", w.Body.String())
	}
}

// StreamFile with no completed entry and inactive torrent → 404 from streamer.
func Test_hgB_StreamFile_NotFound(t *testing.T) {
	gin.SetMode(gin.TestMode)
	store := newDownloadsStore(t)
	s := streamer.NewForTesting()
	r := gin.New()
	r.GET("/f/:hash/:file", StreamFile(s, store))
	w := hgBGET(t, r, "/f/"+hgBHexHash+"/0")
	if w.Code != http.StatusNotFound {
		t.Fatalf("status=%d want 404; body=%s", w.Code, w.Body.String())
	}
}

func Test_hgB_StreamFile_BadFileIndex(t *testing.T) {
	gin.SetMode(gin.TestMode)
	s := streamer.NewForTesting()
	r := gin.New()
	r.GET("/f/:hash/:file", StreamFile(s, nil))
	w := hgBGET(t, r, "/f/"+hgBHexHash+"/zz")
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status=%d want 400; body=%s", w.Code, w.Body.String())
	}
}

// serveFromCompletedStore returns false when the recorded path no longer exists
// on disk (stat fails), forcing the streamer fallback.
func Test_hgB_ServeFromCompletedStore_MissingFile(t *testing.T) {
	gin.SetMode(gin.TestMode)
	store := newDownloadsStore(t)
	d, err := store.Create(downloads.Download{UserID: 1, InfoHash: hgBHexHash, FileIndex: 0, Magnet: "m", Name: "x", FilePath: "/no/such/path.mkv"})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	store.SetStatus(1, d.ID, downloads.StatusCompleted)

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest("GET", "/", nil)
	h, _ := parseHash(hgBHexHash)
	if served := serveFromCompletedStore(c, store, streamer.NewForTesting(), h, 0); served {
		t.Fatal("expected false when completed path is gone")
	}
}
