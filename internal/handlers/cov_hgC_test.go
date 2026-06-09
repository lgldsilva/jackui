package handlers

import (
	"bytes"
	"encoding/json"
	"encoding/xml"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/lgldsilva/jackui/internal/config"
	"github.com/lgldsilva/jackui/internal/downloads"
	"github.com/lgldsilva/jackui/internal/history"
	"github.com/lgldsilva/jackui/internal/jackett"
	"github.com/lgldsilva/jackui/internal/local"
	"github.com/lgldsilva/jackui/internal/streamer"
)

// hgCListIndexersXML mirrors the XML shape Jackett returns for ?t=indexers so
// the fake server can advertise a single configured indexer.
type hgCListIndexersXML struct {
	XMLName  xml.Name `xml:"indexers"`
	Indexers []struct {
		ID         string `xml:"id,attr"`
		Configured string `xml:"configured,attr"`
		Title      string `xml:"title"`
		Language   string `xml:"language"`
		Type       string `xml:"type"`
	} `xml:"indexer"`
}

// hgCJackettServer returns an httptest server that answers the indexers list
// and a single search result, so SearchSSE can run its full live flow.
func hgCJackettServer(t *testing.T) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("t") == "indexers" {
			var resp hgCListIndexersXML
			resp.Indexers = append(resp.Indexers, struct {
				ID         string `xml:"id,attr"`
				Configured string `xml:"configured,attr"`
				Title      string `xml:"title"`
				Language   string `xml:"language"`
				Type       string `xml:"type"`
			}{ID: "idx1", Configured: "true", Title: "Indexer 1"})
			w.Header().Set("Content-Type", "application/xml")
			_ = xml.NewEncoder(w).Encode(resp)
			return
		}
		_, _ = w.Write([]byte(`{"Results":[{"Title":"Live Result","Tracker":"T1","Seeders":3,"Peers":5,"Size":1000,"InfoHash":"live123"}]}`))
	}))
	t.Cleanup(srv.Close)
	return srv
}

func Test_hgC_SearchSSE_FullFlow(t *testing.T) {
	gin.SetMode(gin.TestMode)
	srv := hgCJackettServer(t)
	client := jackett.New(srv.URL, "key")

	store, err := history.New(t.TempDir() + "/hist.db")
	if err != nil {
		t.Fatal(err)
	}
	// Seed one cached result so emitCachedResults runs its loop (>0 path).
	if err := store.Save("matrix", []jackett.Result{
		{Title: "Cached Matrix", Tracker: "T0", InfoHash: "cachedhash"},
	}, 0, false); err != nil {
		t.Fatal(err)
	}

	router := gin.New()
	router.GET("/api/search/stream", SearchSSE(client, store, nil, nil))

	req := httptest.NewRequest("GET", "/api/search/stream?q=matrix", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body: %s", w.Code, w.Body.String())
	}
	if ct := w.Header().Get("Content-Type"); !strings.HasPrefix(ct, "text/event-stream") {
		t.Errorf("Content-Type = %q, want text/event-stream", ct)
	}
	body := w.Body.String()
	if !strings.Contains(body, "event: done") {
		t.Errorf("missing done event; body: %s", body)
	}
	if !strings.Contains(body, "Cached Matrix") {
		t.Errorf("expected cached result emitted; body: %s", body)
	}
	if !strings.Contains(body, "Live Result") {
		t.Errorf("expected live result emitted; body: %s", body)
	}
}

func Test_hgC_SearchSSE_ListIndexersError(t *testing.T) {
	gin.SetMode(gin.TestMode)
	// Server always 500s → ListIndexers fails → StreamSearch returns error →
	// SearchSSE emits an `error` event (cachedCount==0 && live==0 path).
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()
	client := jackett.New(srv.URL, "key")

	router := gin.New()
	router.GET("/api/search/stream", SearchSSE(client, nil, nil, nil))

	req := httptest.NewRequest("GET", "/api/search/stream?q=anything", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (SSE already started); body: %s", w.Code, w.Body.String())
	}
	body := w.Body.String()
	if !strings.Contains(body, "event: error") {
		t.Errorf("expected error event; body: %s", body)
	}
	if !strings.Contains(body, "event: done") {
		t.Errorf("expected done event; body: %s", body)
	}
}

func Test_hgC_EmitCachedResults_WithStore(t *testing.T) {
	gin.SetMode(gin.TestMode)
	store, err := history.New(t.TempDir() + "/hist.db")
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Save("dune", []jackett.Result{
		{Title: "Dune A", InfoHash: "h1"},
		{Title: "Dune B", InfoHash: ""},
	}, 0, false); err != nil {
		t.Fatal(err)
	}

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest("GET", "/", nil)
	setSSEHeaders(c)

	enricher := buildEnricher(nil, nil, 0, false)
	seen, count := emitCachedResults(c, store, "dune", 0, false, nil, enricher)
	if count != 2 {
		t.Errorf("count = %d, want 2", count)
	}
	// Only the result with a non-empty InfoHash is recorded in seen.
	if !seen["h1"] {
		t.Errorf("seen missing h1: %v", seen)
	}
	if len(seen) != 1 {
		t.Errorf("len(seen) = %d, want 1", len(seen))
	}
}

func Test_hgC_EmitCachedResults_SkipsWhenIndexersScoped(t *testing.T) {
	gin.SetMode(gin.TestMode)
	store, err := history.New(t.TempDir() + "/hist.db")
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Save("dune", []jackett.Result{
		{Title: "Dune A", InfoHash: "h1"},
		{Title: "Dune B", InfoHash: "h2"},
	}, 0, false); err != nil {
		t.Fatal(err)
	}

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest("GET", "/", nil)
	setSSEHeaders(c)

	enricher := buildEnricher(nil, nil, 0, false)
	// Scoped to specific indexers → cache phase is skipped (cached rows carry no
	// indexer id, so emitting them would leak other providers). Live search
	// handles the scoped query instead.
	seen, count := emitCachedResults(c, store, "dune", 0, false, []string{"knaben"}, enricher)
	if count != 0 {
		t.Errorf("count = %d, want 0 (cache skipped when indexers scoped)", count)
	}
	if len(seen) != 0 {
		t.Errorf("len(seen) = %d, want 0", len(seen))
	}
}

func Test_hgC_DropTorrentFromStreamer_NilStreamer(t *testing.T) {
	// Must be a no-op (no panic) when the streamer is nil.
	dropTorrentFromStreamer(nil, downloads.Download{InfoHash: "abc", Name: "x"})
}

func Test_hgC_DropTorrentFromStreamer_InvalidHashNoName(t *testing.T) {
	s := streamer.NewForTesting()
	// Invalid (non-hex / wrong length) hash skips Drop; empty name returns early
	// before touching favorites.
	dropTorrentFromStreamer(s, downloads.Download{InfoHash: "not-a-real-hash", Name: ""})
}

func Test_hgC_DropTorrentFromStreamer_WithNameAndFavorites(t *testing.T) {
	s := streamer.NewForTesting()
	favs, err := streamer.NewFavorites(t.TempDir() + "/favs.db")
	if err != nil {
		t.Fatal(err)
	}
	defer favs.Close()
	if err := favs.Add("My Torrent", "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", "magnet:?xt=x", "", 1); err != nil {
		t.Fatal(err)
	}
	s.SetFavorites(favs)

	d := downloads.Download{
		UserID:   1,
		InfoHash: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		Name:     "My Torrent",
	}
	dropTorrentFromStreamer(s, d)

	if favs.IsFavorite("My Torrent") {
		t.Error("expected favorite to be removed after drop")
	}
}

func Test_hgC_ThumbCachePath_StableForSameInputs(t *testing.T) {
	dir := t.TempDir()
	f := filepath.Join(dir, "vid.mp4")
	if err := os.WriteFile(f, []byte("data"), 0o644); err != nil {
		t.Fatal(err)
	}
	cacheDir := filepath.Join(dir, "cache")
	p1 := thumbCachePath(cacheDir, f, 10)
	p2 := thumbCachePath(cacheDir, f, 10)
	if p1 != p2 {
		t.Errorf("path not stable: %q vs %q", p1, p2)
	}
	if !strings.HasSuffix(p1, ".jpg") {
		t.Errorf("path = %q, want .jpg suffix", p1)
	}
	if filepath.Dir(p1) != cacheDir {
		t.Errorf("dir = %q, want %q", filepath.Dir(p1), cacheDir)
	}
	// Different timestamp arg => different cache key.
	if thumbCachePath(cacheDir, f, 99) == p1 {
		t.Error("expected different key for different `at`")
	}
}

func Test_hgC_CopyFileAndRemove(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "src.bin")
	dst := filepath.Join(dir, "sub", "dst.bin")
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(src, []byte("hello world"), 0o644); err != nil {
		t.Fatal(err)
	}
	stat, err := os.Stat(src)
	if err != nil {
		t.Fatal(err)
	}
	if err := copyFileAndRemove(src, dst, stat); err != nil {
		t.Fatalf("copyFileAndRemove: %v", err)
	}
	if _, err := os.Stat(src); !os.IsNotExist(err) {
		t.Error("src should be removed after copy")
	}
	got, err := os.ReadFile(dst)
	if err != nil {
		t.Fatalf("read dst: %v", err)
	}
	if string(got) != "hello world" {
		t.Errorf("dst content = %q", string(got))
	}
}

func Test_hgC_CopyFileAndRemove_SrcMissing(t *testing.T) {
	dir := t.TempDir()
	stat := hgCFakeFileInfo{}
	if err := copyFileAndRemove(filepath.Join(dir, "nope"), filepath.Join(dir, "out"), stat); err == nil {
		t.Error("expected error opening missing source")
	}
}

// hgCFakeFileInfo satisfies os.FileInfo enough for copyFileAndRemove's Mode()
// call on the error path (the file open fails before mode matters).
type hgCFakeFileInfo struct{ os.FileInfo }

func (hgCFakeFileInfo) Mode() os.FileMode { return 0o644 }
func (hgCFakeFileInfo) IsDir() bool       { return false }

func Test_hgC_CopyDirAndRemove(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "srcdir")
	if err := os.MkdirAll(filepath.Join(src, "nested"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(src, "a.txt"), []byte("A"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(src, "nested", "b.txt"), []byte("B"), 0o644); err != nil {
		t.Fatal(err)
	}
	stat, err := os.Stat(src)
	if err != nil {
		t.Fatal(err)
	}
	dst := filepath.Join(dir, "dstdir")
	if err := copyDirAndRemove(src, dst, stat); err != nil {
		t.Fatalf("copyDirAndRemove: %v", err)
	}
	if _, err := os.Stat(src); !os.IsNotExist(err) {
		t.Error("src dir should be removed")
	}
	if got, _ := os.ReadFile(filepath.Join(dst, "a.txt")); string(got) != "A" {
		t.Errorf("a.txt = %q, want A", string(got))
	}
	if got, _ := os.ReadFile(filepath.Join(dst, "nested", "b.txt")); string(got) != "B" {
		t.Errorf("nested/b.txt = %q, want B", string(got))
	}
}

func Test_hgC_MovePath_Rename(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "m.txt")
	dst := filepath.Join(dir, "m2.txt")
	if err := os.WriteFile(src, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	stat, _ := os.Stat(src)
	if err := movePath(src, dst, stat); err != nil {
		t.Fatalf("movePath: %v", err)
	}
	if _, err := os.Stat(dst); err != nil {
		t.Errorf("dst missing after move: %v", err)
	}
}

func Test_hgC_LocalMove_Success(t *testing.T) {
	gin.SetMode(gin.TestMode)
	srcDir := t.TempDir()
	dstDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(srcDir, "file.txt"), []byte("payload"), 0o644); err != nil {
		t.Fatal(err)
	}
	b := local.NewBrowser([]config.ExternalMount{
		{Name: "Src", Path: srcDir},
		{Name: "Dst", Path: dstDir},
	})

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest("POST", "/api/local/move",
		bytes.NewReader([]byte(`{"srcMount":"Src","srcPath":"file.txt","dstMount":"Dst","dstPath":""}`)))
	c.Request.Header.Set("Content-Type", "application/json")
	setAuth(c, 1, true)

	LocalMoveEntry(b)(c)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body: %s", w.Code, w.Body.String())
	}
	if _, err := os.Stat(filepath.Join(dstDir, "file.txt")); err != nil {
		t.Errorf("moved file not at destination: %v", err)
	}
	if _, err := os.Stat(filepath.Join(srcDir, "file.txt")); !os.IsNotExist(err) {
		t.Error("source file should be gone after move")
	}
}

func Test_hgC_LocalMove_SelfMove(t *testing.T) {
	gin.SetMode(gin.TestMode)
	mountDir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(mountDir, "folder"), 0o755); err != nil {
		t.Fatal(err)
	}
	b := local.NewBrowser([]config.ExternalMount{
		{Name: "M", Path: mountDir},
	})

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	// Moving "folder" into "folder" => self-move guard rejects with 400.
	c.Request = httptest.NewRequest("POST", "/api/local/move",
		bytes.NewReader([]byte(`{"srcMount":"M","srcPath":"folder","dstMount":"M","dstPath":"folder"}`)))
	c.Request.Header.Set("Content-Type", "application/json")
	setAuth(c, 1, true)

	LocalMoveEntry(b)(c)

	if w.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400 (self-move); body: %s", w.Code, w.Body.String())
	}
}

func Test_hgC_LocalMove_CollisionRefused(t *testing.T) {
	gin.SetMode(gin.TestMode)
	srcDir := t.TempDir()
	dstDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(srcDir, "file.txt"), []byte("new"), 0o644); err != nil {
		t.Fatal(err)
	}
	// Destination already has a file with the same name → must NOT be clobbered.
	if err := os.WriteFile(filepath.Join(dstDir, "file.txt"), []byte("existing"), 0o644); err != nil {
		t.Fatal(err)
	}
	b := local.NewBrowser([]config.ExternalMount{
		{Name: "Src", Path: srcDir},
		{Name: "Dst", Path: dstDir},
	})

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest("POST", "/api/local/move",
		bytes.NewReader([]byte(`{"srcMount":"Src","srcPath":"file.txt","dstMount":"Dst","dstPath":""}`)))
	c.Request.Header.Set("Content-Type", "application/json")
	setAuth(c, 1, true)

	LocalMoveEntry(b)(c)

	if w.Code != http.StatusConflict {
		t.Fatalf("status = %d, want 409 (collision); body: %s", w.Code, w.Body.String())
	}
	// Destination file must be untouched; source must still be there.
	if data, _ := os.ReadFile(filepath.Join(dstDir, "file.txt")); string(data) != "existing" {
		t.Errorf("destination overwritten: %q", data)
	}
	if _, err := os.Stat(filepath.Join(srcDir, "file.txt")); err != nil {
		t.Errorf("source should be intact after refused move: %v", err)
	}
}

func Test_hgC_CopyFileAndRemove_PreservesMtime(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "a.txt")
	dst := filepath.Join(dir, "b.txt")
	if err := os.WriteFile(src, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	old := time.Now().Add(-72 * time.Hour).Truncate(time.Second)
	if err := os.Chtimes(src, old, old); err != nil {
		t.Fatal(err)
	}
	stat, _ := os.Stat(src)
	if err := copyFileAndRemove(src, dst, stat); err != nil {
		t.Fatalf("copyFileAndRemove: %v", err)
	}
	st, err := os.Stat(dst)
	if err != nil {
		t.Fatal(err)
	}
	if !st.ModTime().Truncate(time.Second).Equal(old) {
		t.Errorf("mtime not preserved: got %v, want %v", st.ModTime(), old)
	}
}

func Test_hgC_LocalPromotePreview_NoSharedDir(t *testing.T) {
	gin.SetMode(gin.TestMode)
	b := local.NewBrowser(nil)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest("POST", "/api/local/promote/preview", nil)

	LocalPromotePreview(b, nil, nil, "", nil)(c)

	if w.Code != http.StatusConflict {
		t.Errorf("status = %d, want 409 (shared dir not configured); body: %s", w.Code, w.Body.String())
	}
}

func Test_hgC_LocalPromotePreview_MountRoot(t *testing.T) {
	gin.SetMode(gin.TestMode)
	mountDir := t.TempDir()
	sharedDir := t.TempDir()
	b := local.NewBrowser([]config.ExternalMount{
		{Name: "Meus downloads", Path: mountDir},
	})

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest("POST", "/api/local/promote/preview",
		bytes.NewReader([]byte(`{"mount":"Meus downloads","path":"."}`)))
	c.Request.Header.Set("Content-Type", "application/json")
	setAuth(c, 1, true)

	LocalPromotePreview(b, nil, nil, sharedDir, nil)(c)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body: %s", w.Code, w.Body.String())
	}
	var resp struct {
		Previews []map[string]any `json:"previews"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	if len(resp.Previews) != 1 {
		t.Fatalf("previews = %d, want 1", len(resp.Previews))
	}
	if resp.Previews[0]["error"] == nil {
		t.Errorf("expected mount-root error in preview, got %v", resp.Previews[0])
	}
}

func Test_hgC_PreviewItem_FileMissing(t *testing.T) {
	gin.SetMode(gin.TestMode)
	mountDir := t.TempDir()
	b := local.NewBrowser([]config.ExternalMount{
		{Name: "M", Path: mountDir},
	})
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest("GET", "/", nil)

	d := &localPreviewDeps{c: c, b: b, mount: "M"}
	got := previewItem(d, "ghost.mp4")
	if got["error"] == nil {
		t.Errorf("expected error for missing file, got %v", got)
	}
}

func Test_hgC_BuildLocalPreviews_Empty(t *testing.T) {
	got := buildLocalPreviews(&localPreviewDeps{}, nil)
	if got == nil || len(got) != 0 {
		t.Errorf("buildLocalPreviews(nil) = %v, want empty non-nil slice", got)
	}
}

func Test_hgC_LocalThumb_TraversalBadRequest(t *testing.T) {
	gin.SetMode(gin.TestMode)
	mountDir := t.TempDir()
	b := local.NewBrowser([]config.ExternalMount{
		{Name: "Test", Path: mountDir},
	})
	router := gin.New()
	router.GET("/api/local/thumb", LocalThumb(b))

	// Video ext passes the early 204 guard, then ResolvePath rejects the
	// traversal => 400 from the abs-resolution error path.
	req := httptest.NewRequest("GET", "/api/local/thumb?mount=Test&path=../escape.mp4", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400; body: %s", w.Code, w.Body.String())
	}
}

func Test_hgC_LocalThumb_VideoNotFound204(t *testing.T) {
	gin.SetMode(gin.TestMode)
	mountDir := t.TempDir()
	b := local.NewBrowser([]config.ExternalMount{
		{Name: "Test", Path: mountDir},
	})
	router := gin.New()
	router.GET("/api/local/thumb", LocalThumb(b))

	// Valid video ext but the file does not exist => resolveLocalAbs returns
	// "" => 404.
	req := httptest.NewRequest("GET", "/api/local/thumb?mount=Test&path=missing.mp4", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusNotFound {
		t.Errorf("status = %d, want 404; body: %s", w.Code, w.Body.String())
	}
}
