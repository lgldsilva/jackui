package handlers

import (
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
)

func TestClassifyForBrowser_SafeContainer(t *testing.T) {
	direct, reason := classifyForBrowser(localProbe{
		Container:  "mp4",
		VideoCodec: "h264",
		AudioCodec: "aac",
	})
	if !direct {
		t.Errorf("expected direct play, got reason=%q", reason)
	}
	if reason != "" {
		t.Errorf("expected empty reason, got %q", reason)
	}
}

func TestClassifyForBrowser_UnsafeContainer(t *testing.T) {
	direct, reason := classifyForBrowser(localProbe{
		Container:  "matroska",
		VideoCodec: "h264",
		AudioCodec: "aac",
	})
	if direct {
		t.Error("expected HLS for matroska")
	}
	if reason != "container=matroska" {
		t.Errorf("reason = %q, want 'container=matroska'", reason)
	}
}

func TestClassifyForBrowser_UnsafeVideoCodec(t *testing.T) {
	direct, reason := classifyForBrowser(localProbe{
		Container:  "mp4",
		VideoCodec: "hevc",
		AudioCodec: "aac",
	})
	if direct {
		t.Error("expected HLS for hevc")
	}
	if reason != "vcodec=hevc" {
		t.Errorf("reason = %q, want 'vcodec=hevc'", reason)
	}
}

func TestClassifyForBrowser_UnsafeAudioCodec(t *testing.T) {
	direct, reason := classifyForBrowser(localProbe{
		Container:  "mp4",
		VideoCodec: "h264",
		AudioCodec: "ac3",
	})
	if direct {
		t.Error("expected HLS for ac3")
	}
	if reason != "acodec=ac3" {
		t.Errorf("reason = %q, want 'acodec=ac3'", reason)
	}
}

func TestClassifyForBrowser_AllSafeContainers(t *testing.T) {
	for container := range browserSafeContainers {
		direct, _ := classifyForBrowser(localProbe{
			Container:  container,
			VideoCodec: "h264",
			AudioCodec: "aac",
		})
		if !direct {
			t.Errorf("expected direct for container=%s", container)
		}
	}
}

func TestClassifyForBrowser_EmptyCodecs(t *testing.T) {
	direct, reason := classifyForBrowser(localProbe{
		Container:  "mp4",
		VideoCodec: "",
		AudioCodec: "",
	})
	if !direct {
		t.Errorf("expected direct with empty codecs, got reason=%q", reason)
	}
}

func TestClassifyForBrowser_EmptyContainer(t *testing.T) {
	direct, reason := classifyForBrowser(localProbe{
		Container:  "",
		VideoCodec: "h264",
		AudioCodec: "aac",
	})
	if direct {
		t.Error("expected HLS for empty container")
	}
	if reason != "container=" {
		t.Errorf("reason = %q, want 'container='", reason)
	}
}

func TestLocalPlayVideoResp_ProbeResult(t *testing.T) {
	gin.SetMode(gin.TestMode)
	mountDir := t.TempDir()
	os.WriteFile(filepath.Join(mountDir, "video.mp4"), []byte("garbage not a real video"), 0644)

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest("GET", "/api/local/play?mount=Test&path=video.mp4", nil)

	abs := filepath.Join(mountDir, "video.mp4")
	resp := localPlayVideoResp(c, abs, "Test", "video.mp4", "")

	// ffprobe may succeed or fail - we just verify we get a valid response
	// without panic. Both "direct" and "hls" are valid depending on ffprobe.
	if resp.Kind != "direct" && resp.Kind != "hls" {
		t.Errorf("kind = %q, want 'direct' or 'hls'", resp.Kind)
	}
	if resp.URL == "" {
		t.Error("expected non-empty URL")
	}
}

func TestAppendTokenToURL_NoQueryString(t *testing.T) {
	got := appendTokenToURL("mytoken", "/api/local/file")
	if !strings.HasSuffix(got, "?token=mytoken") {
		t.Errorf("got %q, want '/api/local/file?token=mytoken'", got)
	}
}

func TestValidSegName_InvalidPath(t *testing.T) {
	if validSegName("../seg.ts") {
		t.Error("expected false for path traversal")
	}
}

func TestValidSegName_InvalidSlash(t *testing.T) {
	if validSegName("dir/seg.ts") {
		t.Error("expected false for slash in name")
	}
}

func TestValidSegName_DoubleDot(t *testing.T) {
	if validSegName("seg..ts") {
		t.Error("expected false for double dot")
	}
}

func TestLocalSessionKey_Deterministic(t *testing.T) {
	key1 := localSessionKey("Mount", "path/to/file.mp4")
	key2 := localSessionKey("Mount", "path/to/file.mp4")
	if key1 != key2 {
		t.Error("expected same key for same input")
	}
	if !strings.HasPrefix(key1, "local-") {
		t.Errorf("expected key to start with 'local-', got %q", key1)
	}
}

func TestLocalSessionKey_DifferentInputs(t *testing.T) {
	key1 := localSessionKey("Mount1", "file1.mp4")
	key2 := localSessionKey("Mount2", "file2.mp4")
	if key1 == key2 {
		t.Error("expected different keys for different inputs")
	}
}
