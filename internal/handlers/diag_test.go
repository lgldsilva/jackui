package handlers

import (
	"bytes"
	"net/http"
	"net/http/httptest"
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
