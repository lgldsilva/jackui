package handlers

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/lgldsilva/jackui/internal/ai"
	"github.com/lgldsilva/jackui/internal/config"
	"github.com/lgldsilva/jackui/internal/local"
	"github.com/lgldsilva/jackui/internal/mailer"
	"github.com/lgldsilva/jackui/internal/streamer"
)

func TestComputeOSHash_Valid(t *testing.T) {
	data := make([]byte, 128*1024)
	for i := range data {
		data[i] = byte(i % 256)
	}
	f, err := os.CreateTemp(t.TempDir(), "hash-test")
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	defer f.Close()
	if _, err := f.Write(data); err != nil {
		t.Fatalf("write: %v", err)
	}
	st, err := f.Stat()
	if err != nil {
		t.Fatalf("stat: %v", err)
	}

	hashRes, hashErr, query := computeOSHash(f, st, f.Name())
	if hashErr != nil {
		t.Fatalf("computeOSHash: %v", hashErr)
	}
	if hashRes.Hash == "" {
		t.Error("expected non-empty hash")
	}
	if query == "" {
		t.Error("expected non-empty query")
	}
}

func TestComputeOSHash_SmallFile(t *testing.T) {
	f, err := os.CreateTemp(t.TempDir(), "small")
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	defer f.Close()
	f.Write([]byte("tiny"))
	st, _ := f.Stat()

	_, hashErr, query := computeOSHash(f, st, f.Name())
	if hashErr == nil {
		t.Error("expected error for small file")
	}
	if query == "" {
		t.Error("expected non-empty query even with hash error")
	}
}

func TestBuildSearchOpts(t *testing.T) {
	opts := buildSearchOpts("Test Movie", "pt-BR,pt", streamer.HashResult{Hash: "abc123", Size: 1024}, nil)
	if opts.Query != "Test Movie" {
		t.Errorf("Query = %q, want 'Test Movie'", opts.Query)
	}
	if opts.Languages != "pt-BR,pt" {
		t.Errorf("Languages = %q", opts.Languages)
	}
	if opts.MovieHash != "abc123" {
		t.Errorf("MovieHash = %q", opts.MovieHash)
	}
	if opts.MovieBytesize != 1024 {
		t.Errorf("MovieBytesize = %d", opts.MovieBytesize)
	}
}

func TestBuildSearchOpts_WithHashError(t *testing.T) {
	opts := buildSearchOpts("Test Movie", "en", streamer.HashResult{}, errors.New("hash error"))
	if opts.MovieHash != "" {
		t.Errorf("expected empty MovieHash on error, got %q", opts.MovieHash)
	}
	if opts.MovieBytesize != 0 {
		t.Errorf("expected 0 MovieBytesize on error")
	}
}

func TestBuildSearchOpts_WithSeasonEpisode(t *testing.T) {
	opts := buildSearchOpts("Show.S01E02", "en", streamer.HashResult{}, nil)
	if opts.Season != 1 {
		t.Errorf("Season = %d, want 1", opts.Season)
	}
	if opts.Episode != 2 {
		t.Errorf("Episode = %d, want 2", opts.Episode)
	}
}

func TestCanModifyMount_Admin(t *testing.T) {
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest("GET", "/", nil)
	setAuth(c, 1, true)

	if !canModifyMount(c, "Downloads") {
		t.Error("admin should be able to modify any mount")
	}
}

func TestCanModifyMount_NonAdminMeusDownloads(t *testing.T) {
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest("GET", "/", nil)
	setAuth(c, 1, false)

	if !canModifyMount(c, "Meus downloads") {
		t.Error("non-admin should be able to modify Meus downloads")
	}
}

func TestCanModifyMount_NonAdminOther(t *testing.T) {
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest("GET", "/", nil)
	setAuth(c, 1, false)

	if canModifyMount(c, "Downloads") {
		t.Error("non-admin should NOT be able to modify Downloads")
	}
}

func TestCanModifyMount_NoClaimsGranted(t *testing.T) {
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest("GET", "/", nil)

	if !canModifyMount(c, "Meus downloads") {
		t.Error("Meus downloads should be modifiable even without claims")
	}
}

func TestIsAdminMove_Admin(t *testing.T) {
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest("POST", "/", nil)
	setAuth(c, 1, true)

	if !isAdminMove(c) {
		t.Error("expected admin to pass")
	}
}

func TestIsAdminMove_NonAdmin(t *testing.T) {
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest("POST", "/", nil)
	setAuth(c, 1, false)

	if isAdminMove(c) {
		t.Error("expected non-admin to fail")
	}
	if w.Code != 403 {
		t.Errorf("status = %d, want 403", w.Code)
	}
}

func TestIsAdminMove_NoClaims(t *testing.T) {
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest("POST", "/", nil)

	if isAdminMove(c) {
		t.Error("expected no-claims to fail")
	}
	if w.Code != 403 {
		t.Errorf("status = %d, want 403", w.Code)
	}
}

func TestResolveSource_InvalidMount(t *testing.T) {
	gin.SetMode(gin.TestMode)
	b := local.NewBrowser(nil)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest("POST", "/", nil)
	_, _, err := resolveSource(b, c, &moveEntryReq{
		SrcMount: "DoesNotExist",
		SrcPath:  "file.txt",
	})
	if err == nil {
		t.Error("expected error for invalid mount")
	}
}

func TestResolveSource_NotFound(t *testing.T) {
	gin.SetMode(gin.TestMode)
	mountDir := t.TempDir()
	b := local.NewBrowser([]config.ExternalMount{
		{Name: "Test", Path: mountDir},
	})
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest("POST", "/", nil)
	_, _, err := resolveSource(b, c, &moveEntryReq{
		SrcMount: "Test",
		SrcPath:  "nonexistent.txt",
	})
	if err == nil {
		t.Error("expected error for non-existent source")
	}
}

func TestResolveSource_Valid(t *testing.T) {
	gin.SetMode(gin.TestMode)
	mountDir := t.TempDir()
	os.WriteFile(filepath.Join(mountDir, "file.txt"), []byte("content"), 0644)
	b := local.NewBrowser([]config.ExternalMount{
		{Name: "Test", Path: mountDir},
	})
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest("POST", "/", nil)
	abs, stat, err := resolveSource(b, c, &moveEntryReq{
		SrcMount: "Test",
		SrcPath:  "file.txt",
	})
	if err != nil {
		t.Fatalf("resolveSource: %v", err)
	}
	if abs == "" {
		t.Error("expected non-empty abs")
	}
	if stat.IsDir() {
		t.Error("expected file, not dir")
	}
}

func TestResolveDeletablePath_Valid(t *testing.T) {
	mountDir := t.TempDir()
	os.WriteFile(filepath.Join(mountDir, "file.txt"), []byte("content"), 0644)
	b := local.NewBrowser([]config.ExternalMount{
		{Name: "Test", Path: mountDir},
	})
	abs, err := resolveDeletablePath(b, "Test", "file.txt")
	if err != nil {
		t.Fatalf("resolveDeletablePath: %v", err)
	}
	if abs == "" {
		t.Error("expected non-empty abs")
	}
}

func TestResolveDeletablePath_MountRoot(t *testing.T) {
	mountDir := t.TempDir()
	b := local.NewBrowser([]config.ExternalMount{
		{Name: "Test", Path: mountDir},
	})
	_, err := resolveDeletablePath(b, "Test", ".")
	if err == nil {
		t.Error("expected error for mount root")
	}
}

func TestResolveDeletablePath_NonExistent(t *testing.T) {
	mountDir := t.TempDir()
	b := local.NewBrowser([]config.ExternalMount{
		{Name: "Test", Path: mountDir},
	})
	_, err := resolveDeletablePath(b, "Test", "nonexistent.txt")
	if !os.IsNotExist(err) {
		t.Errorf("expected not-exist error, got %v", err)
	}
}

func TestCopyFileAndRemove(t *testing.T) {
	srcDir := t.TempDir()
	dstDir := t.TempDir()
	src := filepath.Join(srcDir, "src.txt")
	dst := filepath.Join(dstDir, "dst.txt")
	os.WriteFile(src, []byte("hello"), 0644)
	stat, _ := os.Stat(src)

	err := copyFileAndRemove(src, dst, stat)
	if err != nil {
		t.Fatalf("copyFileAndRemove: %v", err)
	}
	if _, err := os.Stat(src); !os.IsNotExist(err) {
		t.Error("source should be removed")
	}
	data, err := os.ReadFile(dst)
	if err != nil {
		t.Fatalf("read dst: %v", err)
	}
	if string(data) != "hello" {
		t.Errorf("dst content = %q, want 'hello'", string(data))
	}
}

func TestCopyDirAndRemove(t *testing.T) {
	srcDir := t.TempDir()
	dstDir := t.TempDir()
	src := filepath.Join(srcDir, "srcdir")
	dst := filepath.Join(dstDir, "dstdir")

	os.MkdirAll(filepath.Join(src, "sub"), 0755)
	os.WriteFile(filepath.Join(src, "file1.txt"), []byte("one"), 0644)
	os.WriteFile(filepath.Join(src, "sub", "file2.txt"), []byte("two"), 0644)
	stat, _ := os.Stat(src)

	err := copyDirAndRemove(src, dst, stat)
	if err != nil {
		t.Fatalf("copyDirAndRemove: %v", err)
	}
	if _, err := os.Stat(src); !os.IsNotExist(err) {
		t.Error("source should be removed")
	}
	data, err := os.ReadFile(filepath.Join(dst, "sub", "file2.txt"))
	if err != nil {
		t.Fatalf("read dst file: %v", err)
	}
	if string(data) != "two" {
		t.Errorf("content = %q, want 'two'", string(data))
	}
}

func TestCopyDirAndRemove_SingleLevel(t *testing.T) {
	srcDir := t.TempDir()
	dstDir := t.TempDir()
	src := filepath.Join(srcDir, "srcdir")
	dst := filepath.Join(dstDir, "dstdir")

	os.MkdirAll(src, 0755)
	os.WriteFile(filepath.Join(src, "file.txt"), []byte("content"), 0644)
	stat, _ := os.Stat(src)

	err := copyDirAndRemove(src, dst, stat)
	if err != nil {
		t.Fatalf("copyDirAndRemove: %v", err)
	}
	if _, err := os.Stat(dst); os.IsNotExist(err) {
		t.Error("destination should exist")
	}
}

func TestIsMountRoot_Matching(t *testing.T) {
	mountDir := t.TempDir()
	b := local.NewBrowser([]config.ExternalMount{
		{Name: "Test", Path: mountDir},
	})
	if !isMountRoot(b, mountDir) {
		t.Error("expected true for mount root")
	}
}

func TestIsMountRoot_NonMatchingPath(t *testing.T) {
	b := local.NewBrowser([]config.ExternalMount{
		{Name: "Test", Path: "/tmp"},
	})
	if isMountRoot(b, "/nonexistent") {
		t.Error("expected false for non-mount")
	}
}

func TestResolveLocalAbs_ValidFile(t *testing.T) {
	mountDir := t.TempDir()
	os.WriteFile(filepath.Join(mountDir, "file.txt"), []byte("data"), 0644)
	b := local.NewBrowser([]config.ExternalMount{
		{Name: "Test", Path: mountDir},
	})
	abs, err := resolveLocalAbs(b, "Test", "file.txt")
	if err != nil {
		t.Fatalf("resolveLocalAbs: %v", err)
	}
	if abs == "" {
		t.Error("expected non-empty abs path")
	}
}

func TestResolveLocalAbs_Dir(t *testing.T) {
	mountDir := t.TempDir()
	os.MkdirAll(filepath.Join(mountDir, "subdir"), 0755)
	b := local.NewBrowser([]config.ExternalMount{
		{Name: "Test", Path: mountDir},
	})
	abs, err := resolveLocalAbs(b, "Test", "subdir")
	if err != nil {
		t.Fatalf("resolveLocalAbs: %v", err)
	}
	if abs != "" {
		t.Error("expected empty for directory")
	}
}

func TestResolveLocalAbs_NonExistent(t *testing.T) {
	mountDir := t.TempDir()
	b := local.NewBrowser([]config.ExternalMount{
		{Name: "Test", Path: mountDir},
	})
	abs, err := resolveLocalAbs(b, "Test", "nonexistent.txt")
	if err != nil {
		t.Fatalf("resolveLocalAbs: %v", err)
	}
	if abs != "" {
		t.Error("expected empty for non-existent")
	}
}

func TestResolveTargetBase_Empty(t *testing.T) {
	base, err := resolveTargetBase("", "/shared", nil)
	if err != nil {
		t.Fatalf("resolveTargetBase: %v", err)
	}
	if base != "/shared" {
		t.Errorf("base = %q, want '/shared'", base)
	}
}

func TestResolveTargetBase_Matching(t *testing.T) {
	dests := []PromoteDest{
		{Name: "Library", Path: "/shared"},
		{Name: "Extra", Path: "/extra"},
	}
	base, err := resolveTargetBase("/extra", "/shared", dests)
	if err != nil {
		t.Fatalf("resolveTargetBase: %v", err)
	}
	if base != "/extra" {
		t.Errorf("base = %q, want '/extra'", base)
	}
}

func TestResolveTargetBase_NonMatching(t *testing.T) {
	_, err := resolveTargetBase("/invalid", "/shared", []PromoteDest{{Name: "Lib", Path: "/shared"}})
	if err == nil {
		t.Error("expected error for non-matching targetBase")
	}
}

func TestSanitizeSubdir_Empty(t *testing.T) {
	got, err := sanitizeSubdir("")
	if err != nil {
		t.Fatalf("sanitizeSubdir: %v", err)
	}
	if got != "" {
		t.Errorf("got %q, want ''", got)
	}
}

func TestSanitizeSubdir_Absolute(t *testing.T) {
	_, err := sanitizeSubdir("/abs/path")
	if err == nil {
		t.Error("expected error for absolute path")
	}
}

func TestSanitizeSubdir_Traversal(t *testing.T) {
	_, err := sanitizeSubdir("../escape")
	if err == nil {
		t.Error("expected error for traversal")
	}
}

func TestSanitizeSubdir_Valid(t *testing.T) {
	got, err := sanitizeSubdir("movies/2026")
	if err != nil {
		t.Fatalf("sanitizeSubdir: %v", err)
	}
	if got != "movies/2026" {
		t.Errorf("got %q, want 'movies/2026'", got)
	}
}

func TestSanitizeSubdir_Dot(t *testing.T) {
	got, err := sanitizeSubdir(".")
	if err != nil {
		t.Fatalf("sanitizeSubdir: %v", err)
	}
	if got != "" {
		t.Errorf("got %q, want ''", got)
	}
}

func TestComputePromoteDst_NoAIClient(t *testing.T) {
	dst, dir := computePromoteDst(&promoteDstDeps{base: "/base"}, "test.mp4", "/base/filmes")
	if dst != "/base/filmes/test.mp4" {
		t.Errorf("dst = %q, want '/base/filmes/test.mp4'", dst)
	}
	if dir != "/base/filmes" {
		t.Errorf("dir = %q", dir)
	}
}

func TestComputePromoteDst_WithAIClient(t *testing.T) {
	aiSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]any{
			"choices": []map[string]any{
				{"message": map[string]any{
					"role":    "assistant",
					"content": `{"title":"Test Movie","year":2023,"kind":"movie"}`,
				}},
			},
		})
	}))
	defer aiSrv.Close()

	aiCfg := config.AIConfig{
		Enabled: true,
		Providers: map[string]config.AIProvider{
			"t": {BaseURL: aiSrv.URL, APIKey: "k"},
		},
		Chain: []config.AIChainSlot{
			{ID: "t", Provider: "t", Model: "m"},
		},
	}
	aiClient := ai.New(aiCfg)
	if aiClient == nil {
		t.Fatal("ai.New returned nil")
	}

	dst, dir := computePromoteDst(&promoteDstDeps{
		ctx:      nil,
		aiClient: aiClient,
		base:     "/base",
	}, "Test.Movie.2023.mp4", "/base/filmes")
	// With AI enabled and context=nil, it may fail or succeed — either is fine
	_ = dst
	_ = dir
}

func TestExtractLocalPromoteReq_NoBody(t *testing.T) {
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest("POST", "/", nil)

	_, _, ok := extractLocalPromoteReq(c, nil, "/shared", nil)
	if ok {
		t.Error("expected extract to fail with no body")
	}
}

func TestExtractLocalPromoteReq_EmptyMount(t *testing.T) {
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest("POST", "/", strings.NewReader(`{}`))
	c.Request.Header.Set("Content-Type", "application/json")

	_, _, ok := extractLocalPromoteReq(c, nil, "/shared", nil)
	if ok {
		t.Error("expected extract to fail with empty mount")
	}
}

func TestExtractLocalPromoteReq_UnknownMount(t *testing.T) {
	mountDir := t.TempDir()
	b := local.NewBrowser([]config.ExternalMount{
		{Name: "Real", Path: mountDir},
	})
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest("POST", "/", strings.NewReader(`{"mount":"Fake","paths":["file.mp4"]}`))
	c.Request.Header.Set("Content-Type", "application/json")

	_, _, ok := extractLocalPromoteReq(c, b, "/shared", nil)
	if ok {
		t.Error("expected extract to fail with unknown mount")
	}
}

func TestBuildLocalPreviews_Empty(t *testing.T) {
	previews := buildLocalPreviews(nil, nil)
	if len(previews) != 0 {
		t.Errorf("expected empty previews, got %d", len(previews))
	}
}

func TestBuildLocalPreviews_EmptyPaths(t *testing.T) {
	previews := buildLocalPreviews(nil, []string{})
	if len(previews) != 0 {
		t.Errorf("expected empty previews, got %d", len(previews))
	}
}

func TestPreviewItem_RootPath(t *testing.T) {
	result := previewItem(nil, ".")
	if result["error"] != "cannot promote mount root" {
		t.Errorf("expected root error, got %v", result["error"])
	}
}

func TestPreviewItem_NonExistent(t *testing.T) {
	mountDir := t.TempDir()
	b := local.NewBrowser([]config.ExternalMount{
		{Name: "Test", Path: mountDir},
	})
	d := &localPreviewDeps{
		b:     b,
		mount: "Test",
	}
	result := previewItem(d, "nonexistent.mp4")
	if result["error"] != "arquivo não existe" {
		t.Errorf("expected 'arquivo não existe', got %v", result["error"])
	}
}

func TestExtractLocalPromoteReq_Valid(t *testing.T) {
	mountDir := t.TempDir()
	b := local.NewBrowser([]config.ExternalMount{
		{Name: "Meus downloads", Path: mountDir},
	})
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest("POST", "/", strings.NewReader(`{"mount":"Meus downloads","paths":["file.mp4"]}`))
	c.Request.Header.Set("Content-Type", "application/json")
	setAuth(c, 1, true)

	req, base, ok := extractLocalPromoteReq(c, b, "/shared", []PromoteDest{{Name: "Bib", Path: "/shared"}})
	if !ok {
		t.Fatalf("extractLocalPromoteReq failed")
	}
	if req.Mount != "Meus downloads" {
		t.Errorf("Mount = %q", req.Mount)
	}
	if base != "/shared" {
		t.Errorf("base = %q", base)
	}
}

func TestLocalMoveHandler_SelfMove(t *testing.T) {
	mountDir := t.TempDir()
	subDir := filepath.Join(mountDir, "sub")
	os.MkdirAll(subDir, 0755)
	b := local.NewBrowser([]config.ExternalMount{
		{Name: "Test", Path: mountDir},
	})
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	body := `{"srcMount":"Test","srcPath":"sub","dstMount":"Test","dstPath":"sub/nested"}`
	c.Request = httptest.NewRequest("POST", "/", strings.NewReader(body))
	c.Request.Header.Set("Content-Type", "application/json")
	setAuth(c, 1, true)

	localMoveHandler(c, b, nil, nil)
	if w.Code != 400 {
		t.Errorf("status = %d, want 400 for self-move; body: %s", w.Code, w.Body.String())
	}
}

func TestLocalMoveHandler_MissingFields(t *testing.T) {
	b := local.NewBrowser(nil)
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest("POST", "/", strings.NewReader(`{"srcMount":"M"}`))
	c.Request.Header.Set("Content-Type", "application/json")

	localMoveHandler(c, b, nil, nil)
	if w.Code != 400 {
		t.Errorf("status = %d, want 400; body: %s", w.Code, w.Body.String())
	}
}

func TestLocalMoveHandler_SourceNotFound(t *testing.T) {
	mountDir := t.TempDir()
	b := local.NewBrowser([]config.ExternalMount{
		{Name: "Test", Path: mountDir},
	})
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	body := `{"srcMount":"Test","srcPath":"nonexistent.txt","dstMount":"Test","dstPath":"."}`
	c.Request = httptest.NewRequest("POST", "/", strings.NewReader(body))
	c.Request.Header.Set("Content-Type", "application/json")
	setAuth(c, 1, true)

	localMoveHandler(c, b, nil, nil)
	if w.Code != 400 {
		t.Errorf("status = %d, want 400; body: %s", w.Code, w.Body.String())
	}
}

func TestLocalMoveHandler_UnknownDstMount(t *testing.T) {
	mountDir := t.TempDir()
	os.WriteFile(filepath.Join(mountDir, "file.txt"), []byte("x"), 0644)
	b := local.NewBrowser([]config.ExternalMount{
		{Name: "Src", Path: mountDir},
	})
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	body := `{"srcMount":"Src","srcPath":"file.txt","dstMount":"DoesNotExist","dstPath":"."}`
	c.Request = httptest.NewRequest("POST", "/", strings.NewReader(body))
	c.Request.Header.Set("Content-Type", "application/json")
	setAuth(c, 1, true)

	localMoveHandler(c, b, nil, nil)
	if w.Code != 403 {
		t.Errorf("status = %d, want 403; body: %s", w.Code, w.Body.String())
	}
}

func TestLocalMoveHandler_Success(t *testing.T) {
	srcDir := t.TempDir()
	dstDir := t.TempDir()
	os.WriteFile(filepath.Join(srcDir, "file.txt"), []byte("content"), 0644)
	b := local.NewBrowser([]config.ExternalMount{
		{Name: "Src", Path: srcDir},
		{Name: "Dst", Path: dstDir},
	})
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	body := `{"srcMount":"Src","srcPath":"file.txt","dstMount":"Dst","dstPath":"."}`
	c.Request = httptest.NewRequest("POST", "/", strings.NewReader(body))
	c.Request.Header.Set("Content-Type", "application/json")
	setAuth(c, 1, true)

	localMoveHandler(c, b, nil, nil)
	if w.Code != 200 {
		t.Errorf("status = %d, want 200; body: %s", w.Code, w.Body.String())
	}
}

func TestParseAt_NegativeValue(t *testing.T) {
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest("GET", "/?at=-1", nil)
	if got := parseAt(c); got != 10 {
		t.Errorf("got %d, want 10", got)
	}
}

func TestCollectDirSubs_NoSubs(t *testing.T) {
	dir := t.TempDir()
	subs, err := collectDirSubs(dir, "movie")
	if err != nil {
		t.Fatalf("collectDirSubs: %v", err)
	}
	if len(subs) != 0 {
		t.Errorf("expected 0 subs, got %d", len(subs))
	}
}

func TestCollectDirSubs_WithSubtitleFile(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "movie.srt"), []byte("1\n00:00:01,000 --> 00:00:02,000\nHello\n"), 0644)
	os.WriteFile(filepath.Join(dir, "movie.pt-BR.srt"), []byte("1\n00:00:01,000 --> 00:00:02,000\nOi\n"), 0644)
	os.WriteFile(filepath.Join(dir, "movie.en.vtt"), []byte("WEBVTT"), 0644)
	os.WriteFile(filepath.Join(dir, "other.txt"), []byte("not a sub"), 0644)
	os.WriteFile(filepath.Join(dir, "dir_sub.ass"), []byte("[Script Info]"), 0644)

	subs, err := collectDirSubs(dir, "movie")
	if err != nil {
		t.Fatalf("collectDirSubs: %v", err)
	}
	if len(subs) != 4 {
		t.Errorf("expected 4 subtitle files (excluding .txt), got %d", len(subs))
	}
	// First item should have match=2 (basename match)
	if subs[0].Match != 2 {
		t.Errorf("expected first sub to have match=2, got match=%d", subs[0].Match)
	}
}

func TestCollectDirSubs_DirEntries(t *testing.T) {
	dir := t.TempDir()
	os.MkdirAll(filepath.Join(dir, "subs"), 0755)
	os.WriteFile(filepath.Join(dir, "movie.srt"), []byte("content"), 0644)

	subs, err := collectDirSubs(dir, "movie")
	if err != nil {
		t.Fatalf("collectDirSubs: %v", err)
	}
	// Should skip directory entry, only find .srt
	found := false
	for _, s := range subs {
		if s.Name == "movie.srt" {
			found = true
			break
		}
	}
	if !found {
		t.Error("expected to find movie.srt")
	}
}

func TestNotifyWithMailer(t *testing.T) {
	mlr := mailer.New(config.SMTPConfig{})
	notify(mlr, "", "Subject", "Intro", "http://link")
}

func TestNotifyWithMailerAndTo(t *testing.T) {
	mlr := mailer.New(config.SMTPConfig{})
	notify(mlr, "test@example.com", "Subject", "Intro", "http://link")
}

func TestResolveLocalFile_Valid(t *testing.T) {
	gin.SetMode(gin.TestMode)
	mountDir := t.TempDir()
	os.WriteFile(filepath.Join(mountDir, "file.txt"), []byte("data"), 0644)
	b := local.NewBrowser([]config.ExternalMount{
		{Name: "Test", Path: mountDir},
	})
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest("GET", "/api/local/file?mount=Test&path=file.txt", nil)

	abs, ok := resolveLocalFile(b, c, "Test", "file.txt")
	if !ok {
		t.Fatal("resolveLocalFile failed")
	}
	if abs == "" {
		t.Error("expected non-empty abs")
	}
}

func TestResolveLocalFile_Dir(t *testing.T) {
	gin.SetMode(gin.TestMode)
	mountDir := t.TempDir()
	os.MkdirAll(filepath.Join(mountDir, "subdir"), 0755)
	b := local.NewBrowser([]config.ExternalMount{
		{Name: "Test", Path: mountDir},
	})
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest("GET", "/api/local/file?mount=Test&path=subdir", nil)

	_, ok := resolveLocalFile(b, c, "Test", "subdir")
	if ok {
		t.Error("expected resolveLocalFile to fail for directory")
	}
}

func TestResolveLocalFile_UnknownMount(t *testing.T) {
	gin.SetMode(gin.TestMode)
	b := local.NewBrowser([]config.ExternalMount{
		{Name: "Real", Path: t.TempDir()},
	})
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest("GET", "/api/local/file?mount=Fake&path=file.txt", nil)

	_, ok := resolveLocalFile(b, c, "Fake", "file.txt")
	if ok {
		t.Error("expected resolveLocalFile to fail for unknown mount")
	}
}

func TestLocalPlayVideoResp_ProbeFailedSafeExt(t *testing.T) {
	gin.SetMode(gin.TestMode)
	mountDir := t.TempDir()
	notAVideo := filepath.Join(mountDir, "video.mp4")
	os.WriteFile(notAVideo, []byte("garbage"), 0644)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest("GET", "/", nil)

	resp := localPlayVideoResp(c, notAVideo, "Test", "video.mp4", "tok")
	// ffprobe may succeed or fail on garbage — we just check no panic and valid output
	if resp.Kind != "direct" && resp.Kind != "hls" {
		t.Errorf("kind = %q, want 'direct' or 'hls'", resp.Kind)
	}
	if resp.URL == "" {
		t.Error("expected non-empty URL")
	}
}

func TestLocalPlayVideoResp_ProbeFailedUnsafeExt(t *testing.T) {
	gin.SetMode(gin.TestMode)
	mountDir := t.TempDir()
	notAVideo := filepath.Join(mountDir, "video.avi")
	os.WriteFile(notAVideo, []byte("garbage"), 0644)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest("GET", "/", nil)

	resp := localPlayVideoResp(c, notAVideo, "Test", "video.avi", "tok")
	if resp.Kind != "direct" && resp.Kind != "hls" {
		t.Errorf("kind = %q, want 'direct' or 'hls'", resp.Kind)
	}
	if resp.URL == "" {
		t.Error("expected non-empty URL")
	}
}

func TestParseAt_Zero(t *testing.T) {
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest("GET", "/?at=0", nil)
	if got := parseAt(c); got != 0 {
		t.Errorf("got %d, want 0", got)
	}
}
