package handlers

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/lgldsilva/jackui/internal/handlers/httpshared"
)

func TestTranscodeCapabilities_ReturnsJSON(t *testing.T) {
	gin.SetMode(gin.TestMode)

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest("GET", "/api/transcode/capabilities", nil)

	TranscodeCapabilities(c)

	if w.Code != http.StatusOK {
		t.Errorf("status = %d, want 200; body: %s", w.Code, w.Body.String())
	}
	var resp interface{}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("expected valid JSON, got error: %v, body: %s", err, w.Body.String())
	}
}

func TestParseIntOr(t *testing.T) {
	tests := []struct {
		s    string
		def  int
		want int
	}{
		{"", 5, 5},
		{"42", 0, 42},
		{"invalid", 99, 99},
		{"0", 100, 0},
		{"-1", 10, -1},
	}
	for _, tt := range tests {
		got := httpshared.ParseIntOr(tt.s, tt.def)
		if got != tt.want {
			t.Errorf("httpshared.ParseIntOr(%q, %d) = %d, want %d", tt.s, tt.def, got, tt.want)
		}
	}
}

func TestTranscodeCapabilities_RefreshFlag(t *testing.T) {
	gin.SetMode(gin.TestMode)

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest("GET", "/api/transcode/capabilities?refresh=1", nil)

	TranscodeCapabilities(c)

	if w.Code != http.StatusOK {
		t.Errorf("status = %d, want 200; body: %s", w.Code, w.Body.String())
	}
}

func TestTranscodeActive_NoManager(t *testing.T) {
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest("GET", "/api/transcode/active", nil)

	TranscodeActive(nil)(c)

	if w.Code != http.StatusOK {
		t.Errorf("status = %d, want 200", w.Code)
	}
	var resp map[string]interface{}
	json.Unmarshal(w.Body.Bytes(), &resp)
	if _, ok := resp["sessions"]; !ok {
		t.Error("expected sessions field")
	}
	if _, ok := resp["gpu"]; !ok {
		t.Error("expected gpu field")
	}
}

func TestTranscodeKill_NilManager(t *testing.T) {
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest("DELETE", "/api/transcode/active/somekey", nil)

	TranscodeKill(nil)(c)

	if w.Code != http.StatusNotFound {
		t.Errorf("status = %d, want 404", w.Code)
	}
}

func TestTranscodeKill_EmptyKey(t *testing.T) {
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Params = []gin.Param{{Key: "key", Value: ""}}
	c.Request = httptest.NewRequest("DELETE", "/api/transcode/active/", nil)

	TranscodeKill(nil)(c)

	if w.Code != http.StatusNotFound {
		t.Errorf("status = %d, want 404", w.Code)
	}
}
