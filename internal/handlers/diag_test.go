package handlers

import (
	"bytes"
	"log"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
)

func TestClientLog_ValidPayload(t *testing.T) {
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest("POST", "/api/log", bytes.NewReader([]byte(`{"level":"info","tag":"player","msg":"test message"}`)))
	c.Request.Header.Set("Content-Type", "application/json")

	ClientLog()(c)

	if w.Code != http.StatusNoContent && w.Code != http.StatusOK {
		t.Errorf("status = %d, want 204", w.Code)
	}
}

func TestClientLog_InvalidBody(t *testing.T) {
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest("POST", "/api/log", bytes.NewReader([]byte(`not json`)))
	c.Request.Header.Set("Content-Type", "application/json")

	ClientLog()(c)

	if w.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", w.Code)
	}
}

func TestClientLog_EmptyBody(t *testing.T) {
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest("POST", "/api/log", bytes.NewReader([]byte(`{}`)))
	c.Request.Header.Set("Content-Type", "application/json")

	ClientLog()(c)

	if w.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", w.Code)
	}
}

func TestClientLog_MissingRequiredFields(t *testing.T) {
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest("POST", "/api/log", bytes.NewReader([]byte(`{"tag":"player"}`)))
	c.Request.Header.Set("Content-Type", "application/json")

	ClientLog()(c)

	if w.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", w.Code)
	}
}

func TestClientLog_WithData(t *testing.T) {
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest("POST", "/api/log", bytes.NewReader([]byte(`{"level":"error","msg":"playback failed","data":{"code":3}}`)))
	c.Request.Header.Set("Content-Type", "application/json")

	ClientLog()(c)

	if w.Code != http.StatusNoContent && w.Code != http.StatusOK {
		t.Errorf("status = %d, want 204", w.Code)
	}
}

func TestClientLog_OversizedBody(t *testing.T) {
	gin.SetMode(gin.TestMode)
	bigBody := make([]byte, 20*1024)
	for i := range bigBody {
		bigBody[i] = 'A'
	}
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest("POST", "/api/log", bytes.NewReader(bigBody))
	c.Request.Header.Set("Content-Type", "application/json")

	ClientLog()(c)

	if w.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400 (oversized body rejected)", w.Code)
	}
}

// Stream URLs reported by the player carry ?token=<JWT>; the handler must
// redact them before they reach the server log (docker logs).
func TestClientLog_RedactsTokens(t *testing.T) {
	gin.SetMode(gin.TestMode)
	var buf bytes.Buffer
	prev := log.Writer()
	log.SetOutput(&buf)
	defer log.SetOutput(prev)

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	body := `{"level":"info","tag":"player","msg":"loadstart","data":{"src":"/api/stream/abc/0?token=eyJSECRET.PAYLOAD.SIG","currentSrc":"http://x/hls/index.m3u8?token=OTHERSECRET&x=1"}}`
	c.Request = httptest.NewRequest("POST", "/api/diag/log", bytes.NewReader([]byte(body)))
	c.Request.Header.Set("Content-Type", "application/json")

	ClientLog()(c)

	out := buf.String()
	if strings.Contains(out, "SECRET") {
		t.Errorf("token leaked into the log: %s", out)
	}
	if !strings.Contains(out, "token=REDACTED") {
		t.Errorf("expected token=REDACTED in log, got: %s", out)
	}
}
