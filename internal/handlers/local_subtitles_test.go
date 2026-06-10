package handlers

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/lgldsilva/jackui/internal/config"
	"github.com/lgldsilva/jackui/internal/local"
)

func TestLocalProbe_NoMount(t *testing.T) {
	gin.SetMode(gin.TestMode)
	b := local.NewBrowser(nil)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest("GET", "/api/local/probe", nil)

	LocalProbe(b)(c)

	if w.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400; body: %s", w.Code, w.Body.String())
	}
}

func TestLocalProbe_NoPath(t *testing.T) {
	gin.SetMode(gin.TestMode)
	b := local.NewBrowser(nil)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest("GET", "/api/local/probe?mount=Test", nil)

	LocalProbe(b)(c)

	if w.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400; body: %s", w.Code, w.Body.String())
	}
}

func TestLocalProbe_UnknownMount(t *testing.T) {
	gin.SetMode(gin.TestMode)
	mountDir := t.TempDir()
	b := local.NewBrowser([]config.ExternalMount{
		{Name: "Real", Path: mountDir},
	})

	router := gin.New()
	router.GET("/api/local/probe", LocalProbe(b))

	req := httptest.NewRequest("GET", "/api/local/probe?mount=DoesNotExist&path=test.mp4", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusForbidden {
		t.Errorf("status = %d, want 403; body: %s", w.Code, w.Body.String())
	}
}

func TestLocalSidecars_NoMount(t *testing.T) {
	gin.SetMode(gin.TestMode)
	b := local.NewBrowser(nil)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest("GET", "/api/local/sidecars", nil)

	LocalSidecars(b)(c)

	if w.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400; body: %s", w.Code, w.Body.String())
	}
}

func TestLocalSidecarRead_NoName(t *testing.T) {
	gin.SetMode(gin.TestMode)
	b := local.NewBrowser(nil)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest("GET", "/api/local/sidecar?mount=Test&path=video.mp4", nil)

	LocalSidecarRead(b)(c)

	if w.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400; body: %s", w.Code, w.Body.String())
	}
}

func TestLocalSidecarRead_InvalidName(t *testing.T) {
	gin.SetMode(gin.TestMode)
	mountDir := t.TempDir()
	b := local.NewBrowser([]config.ExternalMount{
		{Name: "Test", Path: mountDir},
	})

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest("GET", "/api/local/sidecar?mount=Test&path=video.mp4&name=../etc/passwd", nil)

	LocalSidecarRead(b)(c)

	if w.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400; body: %s", w.Code, w.Body.String())
	}
}

func TestLocalSubtitlesAuto_NoMount(t *testing.T) {
	gin.SetMode(gin.TestMode)
	b := local.NewBrowser(nil)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest("GET", "/api/local/subtitles/auto", nil)

	LocalSubtitlesAuto(b, nil)(c)

	if w.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400; body: %s", w.Code, w.Body.String())
	}
}

func TestLocalSubtitleExtract_NoMount(t *testing.T) {
	gin.SetMode(gin.TestMode)
	b := local.NewBrowser(nil)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest("GET", "/api/local/subtrack", nil)

	LocalSubtitleExtract(b, nil)(c)

	if w.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400; body: %s", w.Code, w.Body.String())
	}
}

func TestLocalSubtitleExtract_NoTrack(t *testing.T) {
	gin.SetMode(gin.TestMode)
	b := local.NewBrowser(nil)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest("GET", "/api/local/subtrack?mount=Test&path=video.mp4", nil)

	LocalSubtitleExtract(b, nil)(c)

	if w.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400; body: %s", w.Code, w.Body.String())
	}
}

func TestLocalSubtitleExtract_InvalidTrack(t *testing.T) {
	gin.SetMode(gin.TestMode)
	mountDir := t.TempDir()
	videoFile := filepath.Join(mountDir, "video.mp4")
	os.WriteFile(videoFile, []byte("dummy"), 0644)
	b := local.NewBrowser([]config.ExternalMount{
		{Name: "Test", Path: mountDir},
	})

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest("GET", "/api/local/subtrack?mount=Test&path=video.mp4&track=abc", nil)

	LocalSubtitleExtract(b, nil)(c)

	if w.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400; body: %s", w.Code, w.Body.String())
	}
}

func TestLocalProbe_WithRealFile(t *testing.T) {
	gin.SetMode(gin.TestMode)
	mountDir := t.TempDir()
	videoFile := filepath.Join(mountDir, "test.mp4")
	os.WriteFile(videoFile, []byte("not a real video"), 0644)
	b := local.NewBrowser([]config.ExternalMount{
		{Name: "Videos", Path: mountDir},
	})

	router := gin.New()
	router.GET("/api/local/probe", LocalProbe(b))

	req := httptest.NewRequest("GET", "/api/local/probe?mount=Videos&path=test.mp4", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	// ffprobe may succeed with an empty probe or fail — either is fine
	// as long as it doesn't panic
	if w.Code != http.StatusOK && w.Code != http.StatusBadGateway {
		t.Errorf("status = %d, want 200 or 502; body: %s", w.Code, w.Body.String())
	}
}

func TestLocalSidecarRead_SRTFile(t *testing.T) {
	gin.SetMode(gin.TestMode)
	mountDir := t.TempDir()
	subFile := filepath.Join(mountDir, "sub.srt")
	srtContent := "1\n00:00:01,000 --> 00:00:02,000\nHello\n"
	os.WriteFile(subFile, []byte(srtContent), 0644)
	videoFile := filepath.Join(mountDir, "video.mp4")
	os.WriteFile(videoFile, []byte("dummy"), 0644)
	b := local.NewBrowser([]config.ExternalMount{
		{Name: "Videos", Path: mountDir},
	})

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest("GET", "/api/local/sidecar?mount=Videos&path=video.mp4&name=sub.srt", nil)

	LocalSidecarRead(b)(c)

	if w.Code != http.StatusOK {
		t.Errorf("status = %d, want 200; body: %s", w.Code, w.Body.String())
	}
	ct := w.Header().Get("Content-Type")
	if ct != MIMEVTT {
		t.Errorf("Content-Type = %q, want %q", ct, MIMEVTT)
	}
	if !bytes.Contains(w.Body.Bytes(), []byte("WEBVTT")) {
		t.Error("expected WEBVTT header in converted content")
	}
}

func TestLocalSidecars_WithRealFile(t *testing.T) {
	gin.SetMode(gin.TestMode)
	mountDir := t.TempDir()
	subFile := filepath.Join(mountDir, "movie.srt")
	os.WriteFile(subFile, []byte("1\n00:00:01,000 --> 00:00:02,000\nHello\n"), 0644)
	videoFile := filepath.Join(mountDir, "movie.mp4")
	os.WriteFile(videoFile, []byte("dummy"), 0644)
	b := local.NewBrowser([]config.ExternalMount{
		{Name: "Videos", Path: mountDir},
	})

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest("GET", "/api/local/sidecars?mount=Videos&path=movie.mp4", nil)

	LocalSidecars(b)(c)

	if w.Code != http.StatusOK {
		t.Errorf("status = %d, want 200; body: %s", w.Code, w.Body.String())
	}
}

func TestLocalSidecarRead_NoSubFile(t *testing.T) {
	gin.SetMode(gin.TestMode)
	mountDir := t.TempDir()
	videoFile := filepath.Join(mountDir, "video.mp4")
	os.WriteFile(videoFile, []byte("dummy"), 0644)
	b := local.NewBrowser([]config.ExternalMount{
		{Name: "Videos", Path: mountDir},
	})

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest("GET", "/api/local/sidecar?mount=Videos&path=video.mp4&name=nonexistent.srt", nil)

	LocalSidecarRead(b)(c)

	if w.Code != http.StatusNotFound {
		t.Errorf("status = %d, want 404; body: %s", w.Code, w.Body.String())
	}
}

func TestLocalSidecarRead_UnsupportedFormat(t *testing.T) {
	gin.SetMode(gin.TestMode)
	mountDir := t.TempDir()
	subFile := filepath.Join(mountDir, "sub.txt")
	os.WriteFile(subFile, []byte("whatever"), 0644)
	videoFile := filepath.Join(mountDir, "video.mp4")
	os.WriteFile(videoFile, []byte("dummy"), 0644)
	b := local.NewBrowser([]config.ExternalMount{
		{Name: "Videos", Path: mountDir},
	})

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest("GET", "/api/local/sidecar?mount=Videos&path=video.mp4&name=sub.txt", nil)

	LocalSidecarRead(b)(c)

	if w.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400; body: %s", w.Code, w.Body.String())
	}
}

func TestLocalSubtitlesAuto_NoFile(t *testing.T) {
	gin.SetMode(gin.TestMode)
	mountDir := t.TempDir()
	b := local.NewBrowser([]config.ExternalMount{
		{Name: "Test", Path: mountDir},
	})
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest("GET", "/api/local/subtitles/auto?mount=Test&path=nonexistent.mp4", nil)

	LocalSubtitlesAuto(b, nil)(c)

	if w.Code != http.StatusNotFound {
		t.Errorf("status = %d, want 404; body: %s", w.Code, w.Body.String())
	}
}

func TestDetectLangFromName(t *testing.T) {
	tests := []struct {
		name string
		want string
	}{
		{"movie.pt-BR.srt", "pt-BR"},
		{"movie.pt_br.srt", "pt-BR"},
		{"movie.pob.srt", "pt-BR"},
		{"movie.ptb.srt", "pt-BR"},
		{"movie.pt-pt.srt", "pt-PT"},
		{"movie.pt_pt.srt", "pt-PT"},
		{"movie.pt.srt", "pt"},
		{"movie.por.srt", "pt"},
		{"movie.portuguese.srt", "pt"},
		{"movie.en.srt", "en"},
		{"movie.eng.srt", "en"},
		{"movie.english.srt", "en"},
		{"movie.es.srt", "es"},
		{"movie.spa.srt", "es"},
		{"movie.spanish.srt", "es"},
		{"movie.fr.srt", "fr"},
		{"movie.fra.srt", "fr"},
		{"movie.french.srt", "fr"},
		{"movie.de.srt", ""},
		{"movie.ja.srt", ""},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := detectLangFromName(tc.name)
			if got != tc.want {
				t.Errorf("detectLangFromName(%q) = %q, want %q", tc.name, got, tc.want)
			}
		})
	}
}
