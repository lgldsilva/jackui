package handlers

import (
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"

	"github.com/lgldsilva/jackui/internal/config"
)

func queueConfigForTest() *config.Config {
	return &config.Config{
		DownloadsQueue: config.DownloadsQueueConfig{
			MaxActive:           3,
			PerUserMaxActive:    1,
			MaxConcurrentVerify: 2,
			StallThresholdMin:   5,
			MaxStalls:           3,
			AgingStepMin:        10,
			AgingCap:            60,
			RotationEnabled:     true,
			AutoPromoteArr:      true,
		},
		Stream: config.StreamConfig{
			TransferConcurrencyMode: "serial",
		},
	}
}

func TestCurrentDownloadsQueue(t *testing.T) {
	cfg := queueConfigForTest()
	b := currentDownloadsQueue(cfg)
	if b.MaxActive != 3 || b.MaxConcurrentVerify != 2 || b.TransferConcurrencyMode != "serial" {
		t.Errorf("currentDownloadsQueue = %+v", b)
	}

	cfg.DownloadsQueue.MaxConcurrentVerify = 0
	b = currentDownloadsQueue(cfg)
	if b.MaxConcurrentVerify != 1 {
		t.Errorf("zero verify concurrency = %d, want 1", b.MaxConcurrentVerify)
	}
}

func TestTransferModeOrAuto(t *testing.T) {
	if got := transferModeOrAuto(""); got != transferModeAuto {
		t.Errorf("empty = %q, want auto", got)
	}
	if got := transferModeOrAuto("parallel"); got != "parallel" {
		t.Errorf("parallel = %q", got)
	}
}

func TestDownloadsGetSettings_NewCode(t *testing.T) {
	gin.SetMode(gin.TestMode)
	cfg := queueConfigForTest()
	w := invokeCoverageHandler(t, DownloadsGetSettings(cfg), http.MethodGet, "/api/downloads/settings", "")
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}
	if !strings.Contains(w.Body.String(), "maxActive") {
		t.Errorf("expected maxActive in body, got %s", w.Body.String())
	}
}

func TestValidateDownloadsQueue(t *testing.T) {
	valid := downloadsQueueBody{MaxActive: 1, MaxConcurrentVerify: 1, StallThresholdMin: 1, MaxStalls: 1}
	if msg := validateDownloadsQueue(&valid); msg != "" {
		t.Errorf("valid body rejected: %s", msg)
	}

	invalid := downloadsQueueBody{MaxActive: 0, MaxConcurrentVerify: 1, StallThresholdMin: 1, MaxStalls: 1}
	if msg := validateDownloadsQueue(&invalid); msg == "" {
		t.Error("zero maxActive must be rejected")
	}

	invalid = downloadsQueueBody{MaxActive: 1, MaxConcurrentVerify: 0, StallThresholdMin: 1, MaxStalls: 1}
	if msg := validateDownloadsQueue(&invalid); msg == "" {
		t.Error("zero maxConcurrentVerify must be rejected")
	}

	invalid = downloadsQueueBody{MaxActive: 1, MaxConcurrentVerify: 1, StallThresholdMin: 0, MaxStalls: 1}
	if msg := validateDownloadsQueue(&invalid); msg == "" {
		t.Error("zero stallThresholdMin must be rejected")
	}

	invalid = downloadsQueueBody{MaxActive: 1, MaxConcurrentVerify: 1, StallThresholdMin: 1, MaxStalls: 0}
	if msg := validateDownloadsQueue(&invalid); msg == "" {
		t.Error("zero maxStalls must be rejected")
	}

	invalid = downloadsQueueBody{MaxActive: 1, MaxConcurrentVerify: 1, StallThresholdMin: 1, MaxStalls: 1, TransferConcurrencyMode: "bogus"}
	if msg := validateDownloadsQueue(&invalid); msg == "" {
		t.Error("bogus transfer mode must be rejected")
	}
}

func TestDownloadsUpdateSettings_ValidationError_NewCode(t *testing.T) {
	gin.SetMode(gin.TestMode)
	cfg := queueConfigForTest()
	dir := t.TempDir()
	configPath := filepath.Join(dir, "config.yaml")

	called := 0
	setVerify := func(n int) { called++ }

	w := invokeCoverageHandler(t, DownloadsUpdateSettings(cfg, configPath, setVerify), http.MethodPut, "/api/downloads/settings", `{"maxActive":0,"maxConcurrentVerify":1,"stallThresholdMin":1,"maxStalls":1}`)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", w.Code)
	}
	if called != 0 {
		t.Fatal("setVerifyConcurrency must not be called on validation error")
	}
}

func TestDownloadsUpdateSettings_Success_NewCode(t *testing.T) {
	gin.SetMode(gin.TestMode)
	cfg := queueConfigForTest()
	dir := t.TempDir()
	configPath := filepath.Join(dir, "config.yaml")

	called := 0
	setVerify := func(n int) { called++ }

	body := `{"maxActive":5,"perUserMaxActive":2,"maxConcurrentVerify":3,"stallThresholdMin":10,"maxStalls":5,"agingStepMin":15,"agingCap":90,"rotationEnabled":false,"autoPromoteArr":false,"transferConcurrencyMode":"parallel"}`
	w := invokeCoverageHandler(t, DownloadsUpdateSettings(cfg, configPath, setVerify), http.MethodPut, "/api/downloads/settings", body)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", w.Code, w.Body.String())
	}
	if cfg.DownloadsQueue.MaxActive != 5 || cfg.DownloadsQueue.MaxConcurrentVerify != 3 {
		t.Errorf("config not updated: %+v", cfg.DownloadsQueue)
	}
	if cfg.Stream.TransferConcurrencyMode != "parallel" {
		t.Errorf("transfer mode = %q, want parallel", cfg.Stream.TransferConcurrencyMode)
	}
	if called != 1 {
		t.Fatalf("setVerifyConcurrency called %d times, want 1", called)
	}
	if _, err := os.Stat(configPath); os.IsNotExist(err) {
		t.Error("config file must be written")
	}
}
