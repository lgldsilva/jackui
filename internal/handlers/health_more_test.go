package handlers

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/lgldsilva/jackui/internal/history"
	"github.com/lgldsilva/jackui/internal/jackett"
)

func TestHealth_Handler(t *testing.T) {
	gin.SetMode(gin.TestMode)
	_ = t.TempDir()

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest("GET", "/api/health", nil)

	Health(nil)(c)

	if w.Code != http.StatusOK {
		t.Errorf("status = %d, want 200; body: %s", w.Code, w.Body.String())
	}
}

func TestStatus_Handler(t *testing.T) {
	gin.SetMode(gin.TestMode)
	store, err := history.New(t.TempDir() + "/status.db")
	if err != nil {
		t.Fatal(err)
	}
	jackettClient := jackett.New("http://jackett:9117", "")

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest("GET", "/api/status", nil)

	Status(jackettClient, store)(c)

	if w.Code != http.StatusOK && w.Code != http.StatusServiceUnavailable {
		t.Errorf("status = %d, want 200 or 503; body: %s", w.Code, w.Body.String())
	}
}
