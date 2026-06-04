package config

import "testing"

func TestApplyDownloadsQueueEnv_Defaults(t *testing.T) {
	cfg := &Config{}
	applyDownloadsQueueEnv(cfg)
	q := cfg.DownloadsQueue
	if q.MaxActive != 3 || q.StallThresholdMin != 30 || q.MaxStalls != 3 || q.AgingStepMin != 60 || q.AgingCap != 150 {
		t.Fatalf("unexpected defaults: %+v", q)
	}
	if q.PerUserMaxActive != 0 {
		t.Errorf("per-user limit should default to 0 (unlimited), got %d", q.PerUserMaxActive)
	}
	if q.RotationEnabled {
		t.Error("rotation should default to disabled")
	}
}

func TestApplyDownloadsQueueEnv_PerUserMax(t *testing.T) {
	t.Setenv("JACKUI_DL_PER_USER_MAX", "2")
	cfg := &Config{}
	applyDownloadsQueueEnv(cfg)
	if cfg.DownloadsQueue.PerUserMaxActive != 2 {
		t.Fatalf("expected per-user max 2 from env, got %d", cfg.DownloadsQueue.PerUserMaxActive)
	}
}

func TestApplyDownloadsQueueEnv_NegativePerUserClampsToZero(t *testing.T) {
	t.Setenv("JACKUI_DL_PER_USER_MAX", "") // ensure env doesn't override the YAML value
	cfg := &Config{DownloadsQueue: DownloadsQueueConfig{PerUserMaxActive: -5}}
	applyDownloadsQueueEnv(cfg)
	if cfg.DownloadsQueue.PerUserMaxActive != 0 {
		t.Errorf("negative per-user max should clamp to 0, got %d", cfg.DownloadsQueue.PerUserMaxActive)
	}
}

func TestApplyDownloadsQueueEnv_Overrides(t *testing.T) {
	t.Setenv("JACKUI_DL_MAX_ACTIVE", "5")
	t.Setenv("JACKUI_DL_STALL_MIN", "15")
	t.Setenv("JACKUI_DL_MAX_STALLS", "2")
	t.Setenv("JACKUI_DL_AGING_STEP_MIN", "30")
	t.Setenv("JACKUI_DL_AGING_CAP", "100")
	t.Setenv("JACKUI_DL_ROTATION", "true")

	cfg := &Config{}
	applyDownloadsQueueEnv(cfg)
	q := cfg.DownloadsQueue
	if q.MaxActive != 5 || q.StallThresholdMin != 15 || q.MaxStalls != 2 || q.AgingStepMin != 30 || q.AgingCap != 100 {
		t.Fatalf("env overrides not applied: %+v", q)
	}
	if !q.RotationEnabled {
		t.Error("rotation should be enabled via env")
	}
}

func TestActiveEnvOverrides_ReportsQueueKeys(t *testing.T) {
	t.Setenv("JACKUI_DL_MAX_ACTIVE", "4")
	out := ActiveEnvOverrides()
	if out["JACKUI_DL_MAX_ACTIVE"] != "4" {
		t.Fatalf("expected queue env key reported, got %q", out["JACKUI_DL_MAX_ACTIVE"])
	}
}
