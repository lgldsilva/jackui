package handlers

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/lgldsilva/jackui/internal/config"
	"github.com/lgldsilva/jackui/internal/local"
	"github.com/lgldsilva/jackui/internal/streamer"
)

func TestLocalMounts_Empty(t *testing.T) {
	gin.SetMode(gin.TestMode)
	b := local.NewBrowser(nil)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest("GET", "/api/local/mounts", nil)

	LocalMounts(b)(c)

	if w.Code != http.StatusOK {
		t.Errorf("status = %d, want 200; body: %s", w.Code, w.Body.String())
	}
	var mounts []local.Mount
	json.Unmarshal(w.Body.Bytes(), &mounts)
	if len(mounts) != 0 {
		t.Errorf("expected empty mounts, got %d", len(mounts))
	}
}

func TestLocalMounts_WithMounts(t *testing.T) {
	gin.SetMode(gin.TestMode)
	b := local.NewBrowser([]config.ExternalMount{
		{Name: "Videos", Path: "/tmp"},
	})
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest("GET", "/api/local/mounts", nil)

	LocalMounts(b)(c)

	if w.Code != http.StatusOK {
		t.Errorf("status = %d, want 200; body: %s", w.Code, w.Body.String())
	}
	var mounts []local.Mount
	json.Unmarshal(w.Body.Bytes(), &mounts)
	if len(mounts) != 1 {
		t.Errorf("expected 1 mount, got %d", len(mounts))
	}
	if mounts[0].Name != "Videos" {
		t.Errorf("mount name = %q, want 'Videos'", mounts[0].Name)
	}
}

func TestLocalList_NoMount(t *testing.T) {
	gin.SetMode(gin.TestMode)
	b := local.NewBrowser(nil)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest("GET", "/api/local/list", nil)

	LocalList(b)(c)

	if w.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400; body: %s", w.Code, w.Body.String())
	}
}

func TestLocalList_UnknownMount(t *testing.T) {
	gin.SetMode(gin.TestMode)
	mountDir := t.TempDir()
	b := local.NewBrowser([]config.ExternalMount{
		{Name: "Real", Path: mountDir},
	})

	router := gin.New()
	router.GET("/api/local/list", LocalList(b))

	req := httptest.NewRequest("GET", "/api/local/list?mount=DoesNotExist", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusForbidden {
		t.Errorf("status = %d, want 403; body: %s", w.Code, w.Body.String())
	}
}

func TestLocalList_WithRealDir(t *testing.T) {
	gin.SetMode(gin.TestMode)
	mountDir := t.TempDir()
	os.WriteFile(filepath.Join(mountDir, "test.txt"), []byte("content"), 0644)
	os.Mkdir(filepath.Join(mountDir, "subdir"), 0755)
	b := local.NewBrowser([]config.ExternalMount{
		{Name: "Test", Path: mountDir},
	})

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest("GET", "/api/local/list?mount=Test", nil)

	LocalList(b)(c)

	if w.Code != http.StatusOK {
		t.Errorf("status = %d, want 200; body: %s", w.Code, w.Body.String())
	}
	var entries []local.Entry
	json.Unmarshal(w.Body.Bytes(), &entries)
	if len(entries) != 2 {
		t.Errorf("expected 2 entries, got %d", len(entries))
	}
}

func TestLocalList_NonExistentPath(t *testing.T) {
	gin.SetMode(gin.TestMode)
	mountDir := t.TempDir()
	b := local.NewBrowser([]config.ExternalMount{
		{Name: "Test", Path: mountDir},
	})

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest("GET", "/api/local/list?mount=Test&path=nonexistent", nil)

	LocalList(b)(c)

	if w.Code != http.StatusNotFound {
		t.Errorf("status = %d, want 404; body: %s", w.Code, w.Body.String())
	}
}

func TestLocalFile_NoParams(t *testing.T) {
	gin.SetMode(gin.TestMode)
	b := local.NewBrowser(nil)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest("GET", "/api/local/file", nil)

	LocalFile(b)(c)

	if w.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400; body: %s", w.Code, w.Body.String())
	}
}

func TestLocalFile_MissingPath(t *testing.T) {
	gin.SetMode(gin.TestMode)
	b := local.NewBrowser(nil)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest("GET", "/api/local/file?mount=Test", nil)

	LocalFile(b)(c)

	if w.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400; body: %s", w.Code, w.Body.String())
	}
}

func TestLocalFile_UnknownMount(t *testing.T) {
	gin.SetMode(gin.TestMode)
	b := local.NewBrowser([]config.ExternalMount{
		{Name: "Test", Path: "/tmp"},
	})

	router := gin.New()
	router.GET("/api/local/file", LocalFile(b))

	req := httptest.NewRequest("GET", "/api/local/file?mount=DoesNotExist&path=test.mp4", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusForbidden {
		t.Errorf("status = %d, want 403; body: %s", w.Code, w.Body.String())
	}
}

func TestLocalFile_NotFound(t *testing.T) {
	gin.SetMode(gin.TestMode)
	mountDir := t.TempDir()
	b := local.NewBrowser([]config.ExternalMount{
		{Name: "Test", Path: mountDir},
	})
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest("GET", "/api/local/file?mount=Test&path=nonexistent.mp4", nil)

	LocalFile(b)(c)

	if w.Code != http.StatusNotFound {
		t.Errorf("status = %d, want 404; body: %s", w.Code, w.Body.String())
	}
}

func TestLocalFile_IsDir(t *testing.T) {
	gin.SetMode(gin.TestMode)
	mountDir := t.TempDir()
	os.Mkdir(filepath.Join(mountDir, "subdir"), 0755)
	b := local.NewBrowser([]config.ExternalMount{
		{Name: "Test", Path: mountDir},
	})
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest("GET", "/api/local/file?mount=Test&path=subdir", nil)

	LocalFile(b)(c)

	if w.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400; body: %s", w.Code, w.Body.String())
	}
}

func TestLocalFile_ServesFile(t *testing.T) {
	gin.SetMode(gin.TestMode)
	mountDir := t.TempDir()
	content := []byte("hello world")
	os.WriteFile(filepath.Join(mountDir, "test.txt"), content, 0644)
	b := local.NewBrowser([]config.ExternalMount{
		{Name: "Test", Path: mountDir},
	})
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest("GET", "/api/local/file?mount=Test&path=test.txt", nil)

	LocalFile(b)(c)

	if w.Code != http.StatusOK {
		t.Errorf("status = %d, want 200; body: %s", w.Code, w.Body.String())
	}
	if !bytes.Equal(w.Body.Bytes(), content) {
		t.Errorf("body = %q, want %q", w.Body.Bytes(), content)
	}
}

func TestLocalWalk_NoMount(t *testing.T) {
	gin.SetMode(gin.TestMode)
	b := local.NewBrowser(nil)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest("GET", "/api/local/walk", nil)

	LocalWalk(b)(c)

	if w.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400; body: %s", w.Code, w.Body.String())
	}
}

func TestLocalWalk_NoPath(t *testing.T) {
	gin.SetMode(gin.TestMode)
	b := local.NewBrowser(nil)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest("GET", "/api/local/walk?mount=Test", nil)

	LocalWalk(b)(c)

	if w.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400; body: %s", w.Code, w.Body.String())
	}
}

func TestLocalWalk_NonExistentPath(t *testing.T) {
	gin.SetMode(gin.TestMode)
	mountDir := t.TempDir()
	b := local.NewBrowser([]config.ExternalMount{
		{Name: "Test", Path: mountDir},
	})
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest("GET", "/api/local/walk?mount=Test&path=nonexistent", nil)

	LocalWalk(b)(c)

	if w.Code != http.StatusNotFound {
		t.Errorf("status = %d, want 404; body: %s", w.Code, w.Body.String())
	}
}

func TestLocalWalk_Valid(t *testing.T) {
	gin.SetMode(gin.TestMode)
	mountDir := t.TempDir()
	os.WriteFile(filepath.Join(mountDir, "file1.mp4"), []byte("vid"), 0644)
	os.WriteFile(filepath.Join(mountDir, "file2.txt"), []byte("text"), 0644)
	b := local.NewBrowser([]config.ExternalMount{
		{Name: "Test", Path: mountDir},
	})
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest("GET", "/api/local/walk?mount=Test&path=.", nil)

	LocalWalk(b)(c)

	if w.Code != http.StatusOK {
		t.Errorf("status = %d, want 200; body: %s", w.Code, w.Body.String())
	}
	var resp map[string]interface{}
	json.Unmarshal(w.Body.Bytes(), &resp)
	total, _ := resp["total"].(float64)
	if int(total) != 2 {
		t.Errorf("total = %d, want 2", int(total))
	}
}

func TestLocalWalk_MediaOnly(t *testing.T) {
	gin.SetMode(gin.TestMode)
	mountDir := t.TempDir()
	os.WriteFile(filepath.Join(mountDir, "movie.mp4"), []byte("vid"), 0644)
	os.WriteFile(filepath.Join(mountDir, "notes.txt"), []byte("text"), 0644)
	b := local.NewBrowser([]config.ExternalMount{
		{Name: "Test", Path: mountDir},
	})
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest("GET", "/api/local/walk?mount=Test&path=.&media_only=true", nil)

	LocalWalk(b)(c)

	if w.Code != http.StatusOK {
		t.Errorf("status = %d, want 200; body: %s", w.Code, w.Body.String())
	}
	var resp map[string]interface{}
	json.Unmarshal(w.Body.Bytes(), &resp)
	total, _ := resp["total"].(float64)
	if int(total) != 1 {
		t.Errorf("total = %d, want 1 (only mp4); body: %s", int(total), w.Body.String())
	}
}

func TestLocalTranscode_NoMount(t *testing.T) {
	gin.SetMode(gin.TestMode)
	b := local.NewBrowser(nil)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest("GET", "/api/local/transcode", nil)

	LocalTranscode(b)(c)

	if w.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400; body: %s", w.Code, w.Body.String())
	}
}

func TestLocalTranscode_NoPath(t *testing.T) {
	gin.SetMode(gin.TestMode)
	b := local.NewBrowser(nil)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest("GET", "/api/local/transcode?mount=Test", nil)

	LocalTranscode(b)(c)

	if w.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400; body: %s", w.Code, w.Body.String())
	}
}

func TestLocalTranscode_NotFound(t *testing.T) {
	gin.SetMode(gin.TestMode)
	mountDir := t.TempDir()
	b := local.NewBrowser([]config.ExternalMount{
		{Name: "Test", Path: mountDir},
	})
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest("GET", "/api/local/transcode?mount=Test&path=nonexistent.mp4", nil)

	LocalTranscode(b)(c)

	if w.Code != http.StatusNotFound {
		t.Errorf("status = %d, want 404; body: %s", w.Code, w.Body.String())
	}
}

func TestLocalThumb_NoMount(t *testing.T) {
	gin.SetMode(gin.TestMode)
	b := local.NewBrowser(nil)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest("GET", "/api/local/thumb", nil)

	LocalThumb(b)(c)

	if w.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400; body: %s", w.Code, w.Body.String())
	}
}

func TestLocalThumb_NonVideoExt(t *testing.T) {
	gin.SetMode(gin.TestMode)
	mountDir := t.TempDir()
	os.WriteFile(filepath.Join(mountDir, "doc.txt"), []byte("text"), 0644)
	b := local.NewBrowser([]config.ExternalMount{
		{Name: "Test", Path: mountDir},
	})

	router := gin.New()
	router.GET("/api/local/thumb", LocalThumb(b))

	req := httptest.NewRequest("GET", "/api/local/thumb?mount=Test&path=doc.txt", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusNoContent {
		t.Errorf("status = %d, want 204; body: %s", w.Code, w.Body.String())
	}
}

func TestLocalThumb_NotFound(t *testing.T) {
	gin.SetMode(gin.TestMode)
	mountDir := t.TempDir()
	b := local.NewBrowser([]config.ExternalMount{
		{Name: "Test", Path: mountDir},
	})

	router := gin.New()
	router.GET("/api/local/thumb", LocalThumb(b))

	req := httptest.NewRequest("GET", "/api/local/thumb?mount=Test&path=nonexistent.mp4", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusNotFound {
		t.Errorf("status = %d, want 404; body: %s", w.Code, w.Body.String())
	}
}

func TestLocalMoveEntry_NoBody(t *testing.T) {
	gin.SetMode(gin.TestMode)
	b := local.NewBrowser(nil)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest("POST", "/api/local/move", nil)

	LocalMoveEntry(b)(c)

	if w.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400; body: %s", w.Code, w.Body.String())
	}
}

func TestLocalMoveEntry_MissingFields(t *testing.T) {
	gin.SetMode(gin.TestMode)
	b := local.NewBrowser(nil)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest("POST", "/api/local/move", bytes.NewReader([]byte(`{}`)))
	c.Request.Header.Set("Content-Type", "application/json")

	LocalMoveEntry(b)(c)

	if w.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400; body: %s", w.Code, w.Body.String())
	}
}

func TestLocalMoveEntry_NotAdmin(t *testing.T) {
	gin.SetMode(gin.TestMode)
	mountDir := t.TempDir()
	b := local.NewBrowser([]config.ExternalMount{
		{Name: "Src", Path: mountDir},
		{Name: "Dst", Path: t.TempDir()},
	})
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest("POST", "/api/local/move", bytes.NewReader([]byte(`{"srcMount":"Src","srcPath":"test.txt","dstMount":"Dst"}`)))
	c.Request.Header.Set("Content-Type", "application/json")

	LocalMoveEntry(b)(c)

	if w.Code != http.StatusForbidden {
		t.Errorf("status = %d, want 403 (not admin); body: %s", w.Code, w.Body.String())
	}
}

func TestLocalMoveEntry_Admin_SourceNotFound(t *testing.T) {
	gin.SetMode(gin.TestMode)
	mountDir := t.TempDir()
	b := local.NewBrowser([]config.ExternalMount{
		{Name: "Src", Path: mountDir},
	})
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest("POST", "/api/local/move", bytes.NewReader([]byte(`{"srcMount":"Src","srcPath":"nonexistent.txt","dstMount":"Src"}`)))
	c.Request.Header.Set("Content-Type", "application/json")
	setAuth(c, 1, true)

	LocalMoveEntry(b)(c)

	if w.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400; body: %s", w.Code, w.Body.String())
	}
}

func TestLocalPlay_NoMount(t *testing.T) {
	gin.SetMode(gin.TestMode)
	b := local.NewBrowser(nil)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest("GET", "/api/local/play", nil)

	LocalPlay(b)(c)

	if w.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400; body: %s", w.Code, w.Body.String())
	}
}

func TestLocalPlay_NoPath(t *testing.T) {
	gin.SetMode(gin.TestMode)
	b := local.NewBrowser(nil)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest("GET", "/api/local/play?mount=Test", nil)

	LocalPlay(b)(c)

	if w.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400; body: %s", w.Code, w.Body.String())
	}
}

func TestLocalPlay_UnknownMount(t *testing.T) {
	gin.SetMode(gin.TestMode)
	mountDir := t.TempDir()
	b := local.NewBrowser([]config.ExternalMount{
		{Name: "Test", Path: mountDir},
	})

	router := gin.New()
	router.GET("/api/local/play", LocalPlay(b))

	req := httptest.NewRequest("GET", "/api/local/play?mount=DoesNotExist&path=test.mp4", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusForbidden {
		t.Errorf("status = %d, want 403; body: %s", w.Code, w.Body.String())
	}
}

func TestLocalPlay_FileNotFound(t *testing.T) {
	gin.SetMode(gin.TestMode)
	mountDir := t.TempDir()
	b := local.NewBrowser([]config.ExternalMount{
		{Name: "Test", Path: mountDir},
	})
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest("GET", "/api/local/play?mount=Test&path=nonexistent.mp4", nil)

	LocalPlay(b)(c)

	if w.Code != http.StatusNotFound {
		t.Errorf("status = %d, want 404; body: %s", w.Code, w.Body.String())
	}
}

func TestLocalPlay_Dir(t *testing.T) {
	gin.SetMode(gin.TestMode)
	mountDir := t.TempDir()
	os.Mkdir(filepath.Join(mountDir, "subdir"), 0755)
	b := local.NewBrowser([]config.ExternalMount{
		{Name: "Test", Path: mountDir},
	})
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest("GET", "/api/local/play?mount=Test&path=subdir", nil)

	LocalPlay(b)(c)

	if w.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400; body: %s", w.Code, w.Body.String())
	}
}

func TestLocalPlay_AudioFile(t *testing.T) {
	gin.SetMode(gin.TestMode)
	mountDir := t.TempDir()
	os.WriteFile(filepath.Join(mountDir, "song.mp3"), []byte("audio"), 0644)
	b := local.NewBrowser([]config.ExternalMount{
		{Name: "Test", Path: mountDir},
	})
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest("GET", "/api/local/play?mount=Test&path=song.mp3", nil)

	LocalPlay(b)(c)

	if w.Code != http.StatusOK {
		t.Errorf("status = %d, want 200; body: %s", w.Code, w.Body.String())
	}
	var resp LocalPlayResp
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatal("invalid JSON:", err)
	}
	if resp.Kind != "direct" {
		t.Errorf("kind = %q, want 'direct'", resp.Kind)
	}
	if resp.URL == "" {
		t.Error("expected non-empty URL")
	}
}

func TestLocalPlay_VideoFile_FallbackToHLS(t *testing.T) {
	gin.SetMode(gin.TestMode)
	mountDir := t.TempDir()
	os.WriteFile(filepath.Join(mountDir, "movie.mkv"), []byte("not a real video, probe will fail"), 0644)
	b := local.NewBrowser([]config.ExternalMount{
		{Name: "Test", Path: mountDir},
	})
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest("GET", "/api/local/play?mount=Test&path=movie.mkv", nil)

	LocalPlay(b)(c)

	if w.Code != http.StatusOK {
		t.Errorf("status = %d, want 200 (probe fails, so HLS via safe ext?); body: %s", w.Code, w.Body.String())
	}
	var resp LocalPlayResp
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatal("invalid JSON:", err)
	}
	if resp.Kind != "hls" {
		t.Errorf("kind = %q, want 'hls' for MKV; body: %s", resp.Kind, w.Body.String())
	}
}

func TestLocalHLSMaster_NoMount(t *testing.T) {
	gin.SetMode(gin.TestMode)
	s := streamer.NewForTesting()
	b := local.NewBrowser(nil)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest("GET", "/api/local/hls/index.m3u8", nil)

	LocalHLSMaster(b, nil)(c)

	if w.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400; body: %s", w.Code, w.Body.String())
	}
	_ = s
}

func TestLocalHLSSegment_NoMount(t *testing.T) {
	gin.SetMode(gin.TestMode)
	b := local.NewBrowser(nil)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest("GET", "/api/local/hls/seg", nil)

	LocalHLSSegment(b, nil)(c)

	if w.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400; body: %s", w.Code, w.Body.String())
	}
}

func TestLocalHLSSegment_NoSeg(t *testing.T) {
	gin.SetMode(gin.TestMode)
	b := local.NewBrowser(nil)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest("GET", "/api/local/hls/seg?mount=Test&path=video.mp4", nil)

	LocalHLSSegment(b, nil)(c)

	if w.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400; body: %s", w.Code, w.Body.String())
	}
}

func TestLocalPromote_NoSharedDir(t *testing.T) {
	gin.SetMode(gin.TestMode)
	b := local.NewBrowser(nil)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest("POST", "/api/local/promote", bytes.NewReader([]byte(`{"mount":"Test","path":"video.mp4"}`)))
	c.Request.Header.Set("Content-Type", "application/json")

	LocalPromote(b, nil, nil, "", nil)(c)

	if w.Code != http.StatusConflict {
		t.Errorf("status = %d, want 409; body: %s", w.Code, w.Body.String())
	}
}

func TestLocalPromotePreview_NoSharedDir(t *testing.T) {
	gin.SetMode(gin.TestMode)
	b := local.NewBrowser(nil)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest("POST", "/api/local/promote/preview", bytes.NewReader([]byte(`{"mount":"Test","path":"video.mp4"}`)))
	c.Request.Header.Set("Content-Type", "application/json")

	LocalPromotePreview(b, nil, nil, "", nil)(c)

	if w.Code != http.StatusConflict {
		t.Errorf("status = %d, want 409; body: %s", w.Code, w.Body.String())
	}
}

func TestLocalPromote_NoBody(t *testing.T) {
	gin.SetMode(gin.TestMode)
	b := local.NewBrowser(nil)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest("POST", "/api/local/promote", nil)

	LocalPromote(b, nil, nil, "/shared", nil)(c)

	if w.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400; body: %s", w.Code, w.Body.String())
	}
}

func TestIsAudioByExt(t *testing.T) {
	tests := []struct {
		path string
		want bool
	}{
		{"song.mp3", true},
		{"audio.m4a", true},
		{"track.aac", true},
		{"music.flac", true},
		{"voice.ogg", true},
		{"recording.wav", true},
		{"podcast.opus", true},
		{"video.mp4", false},
		{"movie.mkv", false},
		{"document.pdf", false},
	}
	for _, tc := range tests {
		t.Run(tc.path, func(t *testing.T) {
			got := isAudioByExt(tc.path)
			if got != tc.want {
				t.Errorf("isAudioByExt(%q) = %v, want %v", tc.path, got, tc.want)
			}
		})
	}
}

func TestUserFromCtx_NoClaims(t *testing.T) {
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest("GET", "/", nil)

	got := userFromCtx(c)
	if got != "" {
		t.Errorf("expected empty string, got %q", got)
	}
}

func TestResolveLocalFileStat_Valid(t *testing.T) {
	gin.SetMode(gin.TestMode)
	mountDir := t.TempDir()
	content := []byte("hello")
	os.WriteFile(filepath.Join(mountDir, "test.txt"), content, 0644)
	b := local.NewBrowser([]config.ExternalMount{
		{Name: "Test", Path: mountDir},
	})

	abs, stat, err := resolveLocalFileStat(b, "Test", "test.txt")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if abs == "" {
		t.Fatal("expected non-empty abs path")
	}
	if stat.IsDir() {
		t.Error("expected file, got dir")
	}
}

func TestResolveLocalFileStat_NotFound(t *testing.T) {
	gin.SetMode(gin.TestMode)
	mountDir := t.TempDir()
	b := local.NewBrowser([]config.ExternalMount{
		{Name: "Test", Path: mountDir},
	})

	_, _, err := resolveLocalFileStat(b, "Test", "nonexistent.txt")
	if err == nil {
		t.Error("expected error for non-existent file")
	}
}

func TestResolveLocalFileStat_IsDir(t *testing.T) {
	gin.SetMode(gin.TestMode)
	mountDir := t.TempDir()
	os.Mkdir(filepath.Join(mountDir, "subdir"), 0755)
	b := local.NewBrowser([]config.ExternalMount{
		{Name: "Test", Path: mountDir},
	})

	_, _, err := resolveLocalFileStat(b, "Test", "subdir")
	if err == nil {
		t.Error("expected error for directory")
	}
}

func TestResolveDest_WithPath(t *testing.T) {
	gin.SetMode(gin.TestMode)
	mountDir := t.TempDir()
	b := local.NewBrowser([]config.ExternalMount{
		{Name: "Test", Path: mountDir},
	})

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest("POST", "/", nil)

	dst, err := resolveDest(b, c, &moveEntryReq{
		SrcMount: "Test",
		SrcPath:  "source.txt",
		DstMount: "Test",
		DstPath:  "targetdir",
	}, filepath.Join(mountDir, "source.txt"))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	expected := filepath.Join(mountDir, "targetdir", "source.txt")
	if dst != expected {
		t.Errorf("expected %s, got %s", expected, dst)
	}
}

func TestResolveDest_EmptyDstPath(t *testing.T) {
	gin.SetMode(gin.TestMode)
	mountDir := t.TempDir()
	b := local.NewBrowser([]config.ExternalMount{
		{Name: "Test", Path: mountDir},
	})

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest("POST", "/", nil)

	dst, err := resolveDest(b, c, &moveEntryReq{
		SrcMount: "Test",
		SrcPath:  "source.txt",
		DstMount: "Test",
		DstPath:  "",
	}, filepath.Join(mountDir, "source.txt"))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if filepath.Base(dst) != "source.txt" {
		t.Errorf("expected source.txt, got %s", filepath.Base(dst))
	}
}

func TestWriteSSE(t *testing.T) {
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest("GET", "/", nil)

	writeSSE(c, "testevent", map[string]string{"key": "value"})

	body := w.Body.String()
	if !strings.Contains(body, "event: testevent") {
		t.Errorf("expected SSE event, got: %s", body)
	}
	if !strings.Contains(body, `"key":"value"`) {
		t.Errorf("expected JSON data, got: %s", body)
	}
}

func TestIsMountRoot_WithMatchingMount(t *testing.T) {
	mountDir := t.TempDir()
	b := local.NewBrowser([]config.ExternalMount{
		{Name: "Test", Path: mountDir},
	})

	if !isMountRoot(b, mountDir) {
		t.Error("expected true for mount root path")
	}
}

func TestIsMountRoot_NonMatching(t *testing.T) {
	mountDir := t.TempDir()
	b := local.NewBrowser([]config.ExternalMount{
		{Name: "Test", Path: mountDir},
	})

	if isMountRoot(b, "/nonexistent") {
		t.Error("expected false for non-mount path")
	}
}

func TestBaseURL_Configured(t *testing.T) {
	if got := baseURL(nil, "https://example.com"); got != "https://example.com" {
		t.Errorf("got %q, want 'https://example.com'", got)
	}
}

func TestBaseURL_OriginHeader(t *testing.T) {
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest("GET", "/", nil)
	c.Request.Header.Set("Origin", "https://custom.origin")

	got := baseURL(c, "")
	if got != "https://custom.origin" {
		t.Errorf("got %q, want 'https://custom.origin'", got)
	}
}

func TestBaseURL_Fallback(t *testing.T) {
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest("GET", "/", nil)

	got := baseURL(c, "")
	if !strings.HasPrefix(got, "http") {
		t.Errorf("expected http URL, got %q", got)
	}
}
