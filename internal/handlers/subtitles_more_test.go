package handlers

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
)

func BenchmarkSubtitlesSearch(b *testing.B) {
	gin.SetMode(gin.TestMode)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		w := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(w)
		c.Request = httptest.NewRequest("GET", "/api/subtitles/search?q=test", nil)
		SubtitlesSearch(nil)(c)
	}
}

func TestSubtitlesSearch_ValidQueryNoClient(t *testing.T) {
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest("GET", "/api/subtitles/search?q=Inception", nil)

	SubtitlesSearch(nil)(c)

	if w.Code != http.StatusBadGateway {
		t.Errorf("status = %d, want 502; body: %s", w.Code, w.Body.String())
	}
}

func TestSubtitlesSearch_WithSeasonEpisode(t *testing.T) {
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest("GET", "/api/subtitles/search?q=Breaking+Bad&season=1&episode=1", nil)

	SubtitlesSearch(nil)(c)

	if w.Code != http.StatusBadGateway {
		t.Errorf("status = %d, want 502; body: %s", w.Code, w.Body.String())
	}
}

func TestSubtitlesDownload_ValidFileIDNoClient(t *testing.T) {
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Params = []gin.Param{{Key: "fileId", Value: "test123"}}
	c.Request = httptest.NewRequest("GET", "/api/subtitles/download/test123", nil)

	SubtitlesDownload(nil)(c)

	if w.Code != http.StatusBadGateway {
		t.Errorf("status = %d, want 502; body: %s", w.Code, w.Body.String())
	}
}
