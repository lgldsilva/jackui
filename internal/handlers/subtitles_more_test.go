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

func TestParseFFProbeStreams_InvalidJSON(t *testing.T) {
	_, err := parseFFProbeStreams([]byte(`not json`))
	if err == nil {
		t.Error("expected error for invalid JSON")
	}
}

func TestParseFFProbeStreams_Empty(t *testing.T) {
	result, err := parseFFProbeStreams([]byte(`{"streams":[],"format":{}}`))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(result.Audio) != 0 {
		t.Errorf("expected 0 audio tracks, got %d", len(result.Audio))
	}
	if len(result.Subtitles) != 0 {
		t.Errorf("expected 0 subtitle tracks, got %d", len(result.Subtitles))
	}
}

func TestParseFFProbeStreams_WithStreams(t *testing.T) {
	json := `{
		"streams": [
			{"index": 0, "codec_type": "video", "codec_name": "h264"},
			{"index": 1, "codec_type": "audio", "codec_name": "aac", "channels": 2, "tags": {"language": "eng"}},
			{"index": 2, "codec_type": "subtitle", "codec_name": "subrip", "tags": {"language": "por"}, "disposition": {"default": 1, "forced": 0}}
		],
		"format": {"duration": "120.5"}
	}`
	result, err := parseFFProbeStreams([]byte(json))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(result.Audio) != 1 {
		t.Fatalf("expected 1 audio track, got %d", len(result.Audio))
	}
	if result.Audio[0].Codec != "aac" {
		t.Errorf("audio codec = %q, want 'aac'", result.Audio[0].Codec)
	}
	if result.Audio[0].Language != "eng" {
		t.Errorf("audio language = %q, want 'eng'", result.Audio[0].Language)
	}
	if len(result.Subtitles) != 1 {
		t.Fatalf("expected 1 subtitle track, got %d", len(result.Subtitles))
	}
	if !result.Subtitles[0].Default {
		t.Error("subtitle should be default")
	}
	if result.DurationSec != 120.5 {
		t.Errorf("duration = %f, want 120.5", result.DurationSec)
	}
}

func TestParseFFProbeStreams_ImageSubs(t *testing.T) {
	json := `{
		"streams": [
			{"index": 0, "codec_type": "subtitle", "codec_name": "hdmv_pgs_subtitle"},
			{"index": 1, "codec_type": "subtitle", "codec_name": "dvd_subtitle"},
			{"index": 2, "codec_type": "subtitle", "codec_name": "subrip"}
		],
		"format": {}
	}`
	result, err := parseFFProbeStreams([]byte(json))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(result.Subtitles) != 3 {
		t.Fatalf("expected 3 subtitle tracks, got %d", len(result.Subtitles))
	}
	if !result.Subtitles[0].Image {
		t.Error("hdmv_pgs_subtitle should be image-based")
	}
	if !result.Subtitles[1].Image {
		t.Error("dvd_subtitle should be image-based")
	}
	if result.Subtitles[2].Image {
		t.Error("subrip should NOT be image-based")
	}
}

func TestParseFFProbeStreams_NoAudioSubtitleSlices(t *testing.T) {
	json := `{
		"streams": [
			{"index": 0, "codec_type": "data", "codec_name": "bin"}
		],
		"format": {}
	}`
	result, err := parseFFProbeStreams([]byte(json))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Audio == nil {
		t.Error("expected non-nil empty audio slice")
	}
	if result.Subtitles == nil {
		t.Error("expected non-nil empty subtitles slice")
	}
}

func TestParseFFProbeStreams_DurationParse(t *testing.T) {
	json := `{
		"streams": [],
		"format": {"duration": "invalid"}
	}`
	result, err := parseFFProbeStreams([]byte(json))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.DurationSec != 0 {
		t.Errorf("duration = %f, want 0", result.DurationSec)
	}
}

func TestCollectDirSubs_NoDir(t *testing.T) {
	_, err := collectDirSubs("/nonexistent/dir", "movie")
	if err == nil {
		t.Error("expected error for nonexistent dir")
	}
}
