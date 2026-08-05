package local

import (
	"bytes"
	"context"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"

	"github.com/lgldsilva/jackui/internal/auth"
	"github.com/lgldsilva/jackui/internal/config"
	lb "github.com/lgldsilva/jackui/internal/local"
	"github.com/lgldsilva/jackui/internal/streamer"
	"github.com/lgldsilva/jackui/internal/transfer"
)

// invokeLocalQuery runs a query-string-driven local handler as an admin.
func invokeLocalQuery(t *testing.T, h gin.HandlerFunc, target string) *httptest.ResponseRecorder {
	t.Helper()
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodGet, target, nil)
	c.Set("auth.claims", &auth.Claims{UserID: 1, Username: "admin", Role: auth.RoleAdmin})
	h(c)
	return w
}

// invokeLocalUpload posts one multipart file as an admin.
func invokeLocalUpload(t *testing.T, h gin.HandlerFunc, target, filename string, content []byte) *httptest.ResponseRecorder {
	t.Helper()
	body := &bytes.Buffer{}
	writer := multipart.NewWriter(body)
	part, err := writer.CreateFormFile("file", filename)
	if err != nil {
		t.Fatalf("CreateFormFile: %v", err)
	}
	if _, err := part.Write(content); err != nil {
		t.Fatalf("part.Write: %v", err)
	}
	if err := writer.Close(); err != nil {
		t.Fatalf("writer.Close: %v", err)
	}
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodPost, target, body)
	c.Request.Header.Set("Content-Type", writer.FormDataContentType())
	c.Set("auth.claims", &auth.Claims{UserID: 1, Username: "admin", Role: auth.RoleAdmin})
	h(c)
	return w
}

func newTestBrowser(mount string, path string) *lb.Browser {
	return lb.NewBrowser([]config.ExternalMount{{Name: mount, Path: path}})
}

func TestLocalUploadMountRequired(t *testing.T) {
	gin.SetMode(gin.TestMode)
	b := newTestBrowser("M", t.TempDir())
	w := invokeMove(t, LocalUpload(b, 0), http.MethodPost, "/api/local/upload", "")
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusBadRequest)
	}
}

func TestLocalUploadInvalidFilename(t *testing.T) {
	gin.SetMode(gin.TestMode)
	b := newTestBrowser("M", t.TempDir())
	w := invokeLocalUpload(t, LocalUpload(b, 0), "/api/local/upload?mount=M", "/", []byte("x"))
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d; body=%s", w.Code, http.StatusBadRequest, w.Body.String())
	}
}

func TestLocalUploadTooLarge(t *testing.T) {
	gin.SetMode(gin.TestMode)
	// validateUpload is exercised directly (without the handler's
	// MaxBytesReader, which would fail FormFile first) to hit the friendly
	// size-ceiling rejection.
	body := &bytes.Buffer{}
	writer := multipart.NewWriter(body)
	part, err := writer.CreateFormFile("file", "a.mkv")
	if err != nil {
		t.Fatalf("CreateFormFile: %v", err)
	}
	if _, err := part.Write([]byte("0123456789")); err != nil {
		t.Fatalf("part.Write: %v", err)
	}
	if err := writer.Close(); err != nil {
		t.Fatalf("writer.Close: %v", err)
	}
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodPost, "/api/local/upload?mount=M", body)
	c.Request.Header.Set("Content-Type", writer.FormDataContentType())
	if _, _, ok := validateUpload(c, 4); ok {
		t.Fatal("oversized file must be rejected")
	}
	if w.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("status = %d, want %d; body=%s", w.Code, http.StatusRequestEntityTooLarge, w.Body.String())
	}
}

func TestLocalMoveEntryDestTraversal(t *testing.T) {
	gin.SetMode(gin.TestMode)
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "a.txt"), []byte("x"))
	b := newTestBrowser("M", dir)
	handler := LocalMoveEntry(b, nil, streamer.NewForTesting(), transfer.New())
	w := invokeMove(t, handler, http.MethodPost, "/api/local/move", `{"srcMount":"M","srcPath":"a.txt","dstMount":"M","dstPath":"../evil"}`)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d; body=%s", w.Code, http.StatusBadRequest, w.Body.String())
	}
}

func TestLocalMoveEntryDestDirCreateFails(t *testing.T) {
	gin.SetMode(gin.TestMode)
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "a.txt"), []byte("x"))
	writeFile(t, filepath.Join(dir, "blocker"), []byte("x")) // regular file where a dir is needed
	b := newTestBrowser("M", dir)
	handler := LocalMoveEntry(b, nil, streamer.NewForTesting(), transfer.New())
	w := invokeMove(t, handler, http.MethodPost, "/api/local/move", `{"srcMount":"M","srcPath":"a.txt","dstMount":"M","dstPath":"blocker/sub"}`)
	if w.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want %d; body=%s", w.Code, http.StatusInternalServerError, w.Body.String())
	}
}

func TestLocalRenamePathTraversal(t *testing.T) {
	gin.SetMode(gin.TestMode)
	b := newTestBrowser("M", t.TempDir())
	handler := LocalRename(b, nil, streamer.NewForTesting())
	w := invokeMove(t, handler, http.MethodPost, "/api/local/rename", `{"mount":"M","path":"../x","newName":"y"}`)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d; body=%s", w.Code, http.StatusBadRequest, w.Body.String())
	}
}

func TestLocalRenameMoveFails(t *testing.T) {
	gin.SetMode(gin.TestMode)
	if os.Geteuid() == 0 {
		t.Skip("root ignores permission bits")
	}
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "a.txt"), []byte("x"))
	if err := os.Chmod(dir, 0o555); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(dir, 0o755) })
	b := newTestBrowser("M", dir)
	handler := LocalRename(b, nil, streamer.NewForTesting())
	w := invokeMove(t, handler, http.MethodPost, "/api/local/rename", `{"mount":"M","path":"a.txt","newName":"b.txt"}`)
	if w.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want %d; body=%s", w.Code, http.StatusInternalServerError, w.Body.String())
	}
}

func TestLocalProbeTraversal(t *testing.T) {
	gin.SetMode(gin.TestMode)
	b := newTestBrowser("M", t.TempDir())
	w := invokeLocalQuery(t, LocalProbe(b), "/api/local/probe?mount=M&path=../x")
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusBadRequest)
	}
}

func TestLocalProbeFFProbeError(t *testing.T) {
	gin.SetMode(gin.TestMode)
	if _, err := os.Stat("/usr/bin/ffprobe"); err != nil {
		t.Skip("ffprobe not installed")
	}
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "v.mkv"), []byte("not a video"))
	// An already-cancelled request context makes the ffprobe exec fail with no
	// stdout — the only shape runFFprobe reports as an error (ffprobe exits 0,
	// or 1 with an empty-JSON stdout, for plain unreadable/invalid files).
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodGet, "/api/local/probe", nil).WithContext(ctx)
	if _, ok := runLocalFFProbe(c, filepath.Join(dir, "v.mkv")); ok {
		t.Fatal("probe with cancelled context must fail")
	}
	if w.Code != http.StatusBadGateway {
		t.Fatalf("status = %d, want %d; body=%s", w.Code, http.StatusBadGateway, w.Body.String())
	}
}

func TestLocalSidecarsTraversal(t *testing.T) {
	gin.SetMode(gin.TestMode)
	b := newTestBrowser("M", t.TempDir())
	w := invokeLocalQuery(t, LocalSidecars(b), "/api/local/sidecars?mount=M&path=../x")
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusBadRequest)
	}
}

func TestLocalSidecarsMissingDir(t *testing.T) {
	gin.SetMode(gin.TestMode)
	b := newTestBrowser("M", t.TempDir())
	w := invokeLocalQuery(t, LocalSidecars(b), "/api/local/sidecars?mount=M&path=nodir/v.mkv")
	if w.Code != http.StatusBadGateway {
		t.Fatalf("status = %d, want %d; body=%s", w.Code, http.StatusBadGateway, w.Body.String())
	}
}

func TestLocalSidecarReadTraversal(t *testing.T) {
	gin.SetMode(gin.TestMode)
	b := newTestBrowser("M", t.TempDir())
	w := invokeLocalQuery(t, LocalSidecarRead(b), "/api/local/sidecar?mount=M&path=../x&name=a.srt")
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusBadRequest)
	}
}

func TestLocalSubtitlesAutoTraversal(t *testing.T) {
	gin.SetMode(gin.TestMode)
	b := newTestBrowser("M", t.TempDir())
	w := invokeLocalQuery(t, LocalSubtitlesAuto(b, nil), "/api/local/subtitles/auto?mount=M&path=../x")
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusBadRequest)
	}
}

func TestLocalSubtitlesAutoMissingFile(t *testing.T) {
	gin.SetMode(gin.TestMode)
	b := newTestBrowser("M", t.TempDir())
	w := invokeLocalQuery(t, LocalSubtitlesAuto(b, nil), "/api/local/subtitles/auto?mount=M&path=nope.mkv")
	if w.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want %d; body=%s", w.Code, http.StatusNotFound, w.Body.String())
	}
}

func TestLocalFileTraversal(t *testing.T) {
	gin.SetMode(gin.TestMode)
	b := newTestBrowser("M", t.TempDir())
	w := invokeLocalQuery(t, LocalFile(b, nil, nil), "/api/local/file?mount=M&path=../x")
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusBadRequest)
	}
}

func TestLocalTranscodeTraversal(t *testing.T) {
	gin.SetMode(gin.TestMode)
	b := newTestBrowser("M", t.TempDir())
	w := invokeLocalQuery(t, LocalTranscode(b), "/api/local/transcode?mount=M&path=../x")
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusBadRequest)
	}
}

func TestLocalTranscodeError(t *testing.T) {
	gin.SetMode(gin.TestMode)
	if _, err := os.Stat("/usr/bin/ffmpeg"); err != nil {
		t.Skip("ffmpeg not installed")
	}
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "v.mkv"), []byte("not a video"))
	b := newTestBrowser("M", dir)
	w := invokeLocalQuery(t, LocalTranscode(b), "/api/local/transcode?mount=M&path=v.mkv")
	if w.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want %d; body=%s", w.Code, http.StatusInternalServerError, w.Body.String())
	}
}

func TestLocalWalkTraversal(t *testing.T) {
	gin.SetMode(gin.TestMode)
	b := newTestBrowser("M", t.TempDir())
	w := invokeLocalQuery(t, LocalWalk(b, streamer.NewForTesting()), "/api/local/walk?mount=M&path=../x")
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d; body=%s", w.Code, http.StatusBadRequest, w.Body.String())
	}
}

func newPromoteDeps(b *lb.Browser, sharedDir string) LocalPromoteDeps {
	return LocalPromoteDeps{Browser: b, SharedDir: sharedDir, Streamer: streamer.NewForTesting()}
}

func TestLocalPromoteBadTargetSubdir(t *testing.T) {
	gin.SetMode(gin.TestMode)
	b := newTestBrowser("M", t.TempDir())
	handler := LocalPromote(newPromoteDeps(b, t.TempDir()))
	w := invokeMove(t, handler, http.MethodPost, "/api/local/promote", `{"mount":"M","path":"a.txt","targetSubdir":"../x"}`)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d; body=%s", w.Code, http.StatusBadRequest, w.Body.String())
	}
}

func TestLocalPromoteNoPaths(t *testing.T) {
	gin.SetMode(gin.TestMode)
	b := newTestBrowser("M", t.TempDir())
	handler := LocalPromote(newPromoteDeps(b, t.TempDir()))
	w := invokeMove(t, handler, http.MethodPost, "/api/local/promote", `{"mount":"M"}`)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d; body=%s", w.Code, http.StatusBadRequest, w.Body.String())
	}
}

func TestLocalPromoteBadTargetBase(t *testing.T) {
	gin.SetMode(gin.TestMode)
	b := newTestBrowser("M", t.TempDir())
	handler := LocalPromote(newPromoteDeps(b, t.TempDir()))
	w := invokeMove(t, handler, http.MethodPost, "/api/local/promote", `{"mount":"M","path":"a.txt","targetBase":"/nope"}`)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d; body=%s", w.Code, http.StatusBadRequest, w.Body.String())
	}
}

func TestLocalPromoteItemErrors(t *testing.T) {
	gin.SetMode(gin.TestMode)
	dir := t.TempDir()
	b := newTestBrowser("M", dir)
	handler := LocalPromote(newPromoteDeps(b, t.TempDir()))
	// "." is the mount root, "/abs" is rejected by ResolvePath, "missing.txt"
	// does not exist — every item fails, so the batch is 422 with per-item errors.
	w := invokeMove(t, handler, http.MethodPost, "/api/local/promote", `{"mount":"M","paths":[".","/abs","missing.txt"]}`)
	if w.Code != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d, want %d; body=%s", w.Code, http.StatusUnprocessableEntity, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), "cannot promote mount root") {
		t.Fatalf("mount root error missing from body: %s", w.Body.String())
	}
}

func TestLocalPromoteDestIsFile(t *testing.T) {
	gin.SetMode(gin.TestMode)
	mountDir := t.TempDir()
	writeFile(t, filepath.Join(mountDir, "a.txt"), []byte("x"))
	sharedFile := filepath.Join(t.TempDir(), "not-a-dir")
	writeFile(t, sharedFile, []byte("x"))
	b := newTestBrowser("M", mountDir)
	handler := LocalPromote(newPromoteDeps(b, sharedFile))
	w := invokeMove(t, handler, http.MethodPost, "/api/local/promote", `{"mount":"M","path":"a.txt"}`)
	if w.Code != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d, want %d; body=%s", w.Code, http.StatusUnprocessableEntity, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), "criar destino") {
		t.Fatalf("mkdir error missing from body: %s", w.Body.String())
	}
}

func TestLocalPromoteMoveFails(t *testing.T) {
	gin.SetMode(gin.TestMode)
	if os.Geteuid() == 0 {
		t.Skip("root ignores permission bits")
	}
	mountDir := t.TempDir()
	writeFile(t, filepath.Join(mountDir, "a.txt"), []byte("x"))
	sharedDir := t.TempDir()
	if err := os.Chmod(sharedDir, 0o555); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(sharedDir, 0o755) })
	b := newTestBrowser("M", mountDir)
	handler := LocalPromote(newPromoteDeps(b, sharedDir))
	w := invokeMove(t, handler, http.MethodPost, "/api/local/promote", `{"mount":"M","path":"a.txt"}`)
	if w.Code != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d, want %d; body=%s", w.Code, http.StatusUnprocessableEntity, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), "mover arquivo") {
		t.Fatalf("move error missing from body: %s", w.Body.String())
	}
}

func TestLocalPromotePreviewResolveError(t *testing.T) {
	gin.SetMode(gin.TestMode)
	b := newTestBrowser("M", t.TempDir())
	handler := LocalPromotePreview(b, nil, nil, t.TempDir(), nil, streamer.NewForTesting())
	w := invokeMove(t, handler, http.MethodPost, "/api/local/promote/preview", `{"mount":"M","paths":["/abs"]}`)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d; body=%s", w.Code, http.StatusOK, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), "error") {
		t.Fatalf("per-item error missing from body: %s", w.Body.String())
	}
}
