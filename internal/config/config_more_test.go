package config

import (
	"testing"
)

func TestApplyJackettEnv(t *testing.T) {
	cfg := &Config{}
	t.Setenv("JACKETT_URL", "http://jackett:9117")
	t.Setenv("JACKETT_API_KEY", "testkey123")

	applyJackettEnv(cfg)

	if cfg.Jackett.URL != "http://jackett:9117" {
		t.Fatalf("Jackett.URL = %q", cfg.Jackett.URL)
	}
	if cfg.Jackett.APIKey != "testkey123" {
		t.Fatalf("Jackett.APIKey = %q", cfg.Jackett.APIKey)
	}
}

func TestApplyEnvOverrides_LocalReadaheadMB(t *testing.T) {
	cfg := &Config{}
	t.Setenv("JACKUI_LOCAL_READAHEAD_MB", "48")
	applyEnvOverrides(cfg)
	if cfg.External.LocalReadaheadMB != 48 {
		t.Fatalf("LocalReadaheadMB = %d, want 48", cfg.External.LocalReadaheadMB)
	}
}

func TestApplyEnvOverrides_LocalReadaheadMB_InvalidIgnored(t *testing.T) {
	cfg := &Config{}
	t.Setenv("JACKUI_LOCAL_READAHEAD_MB", "nope")
	applyEnvOverrides(cfg)
	if cfg.External.LocalReadaheadMB != 0 {
		t.Fatalf("LocalReadaheadMB = %d, want 0 (invalid ignored)", cfg.External.LocalReadaheadMB)
	}
}

func TestApplyEnvOverrides_HLSVODMode(t *testing.T) {
	cfg := &Config{}
	t.Setenv("JACKUI_HLS_VOD_MODE", "all")
	applyEnvOverrides(cfg)
	if cfg.Stream.HLSVODMode != "all" {
		t.Fatalf("HLSVODMode = %q, want all", cfg.Stream.HLSVODMode)
	}
}

func TestApplyEnvOverrides_HLSVODMode_DefaultsToHlsjs(t *testing.T) {
	cfg := &Config{}
	t.Setenv("JACKUI_HLS_VOD_MODE", "")
	applyEnvOverrides(cfg)
	if cfg.Stream.HLSVODMode != "hlsjs" {
		t.Fatalf("HLSVODMode default = %q, want hlsjs (seekbar on for hls.js)", cfg.Stream.HLSVODMode)
	}
}

func TestApplyStreamEnv(t *testing.T) {
	cfg := &Config{}

	t.Setenv("JACKUI_PORT", "9999")
	t.Setenv("JACKUI_DB_PATH", "/custom/db")
	t.Setenv("JACKUI_STREAM_DIR", "/custom/stream")
	t.Setenv("JACKUI_DOWNLOAD_DIR", "/custom/dl")
	t.Setenv("JACKUI_STATE_DIR", "/custom/state")
	t.Setenv("JACKUI_SHARED_DIR", "/custom/shared")
	t.Setenv("JACKUI_STREAM_MAX_GB", "50")

	applyStreamEnv(cfg)

	if cfg.Port != 9999 {
		t.Fatalf("Port = %d, want 9999", cfg.Port)
	}
	if cfg.DBPath != "/custom/db" {
		t.Fatalf("DBPath = %q", cfg.DBPath)
	}
	if cfg.Stream.DataDir != "/custom/stream" {
		t.Fatalf("DataDir = %q", cfg.Stream.DataDir)
	}
	if cfg.Stream.DownloadDir != "/custom/dl" {
		t.Fatalf("DownloadDir = %q", cfg.Stream.DownloadDir)
	}
	if cfg.Stream.StateDir != "/custom/state" {
		t.Fatalf("StateDir = %q", cfg.Stream.StateDir)
	}
	if cfg.Stream.SharedDir != "/custom/shared" {
		t.Fatalf("SharedDir = %q", cfg.Stream.SharedDir)
	}
	if cfg.Stream.MaxCacheGB != 50 {
		t.Fatalf("MaxCacheGB = %d, want 50", cfg.Stream.MaxCacheGB)
	}
}

func TestApplyStreamEnv_InvalidPort(t *testing.T) {
	cfg := &Config{}
	t.Setenv("JACKUI_PORT", "invalid")

	applyStreamEnv(cfg)

	if cfg.Port != 0 {
		t.Fatalf("expected Port to stay 0, got %d", cfg.Port)
	}
}

func TestApplyAuthEnv(t *testing.T) {
	cfg := &Config{}

	t.Setenv("JACKUI_AUTH_ENABLED", "1")
	t.Setenv("JACKUI_ADMIN_PASSWORD", "adminpass")
	t.Setenv("JACKUI_ADMIN_USERNAME", "myadmin")
	t.Setenv("JACKUI_JWT_SECRET", "myjwtsecret")

	applyAuthEnv(cfg)

	if !cfg.Auth.Enabled {
		t.Fatal("expected auth enabled")
	}
	if cfg.Auth.AdminPassword != "adminpass" {
		t.Fatalf("AdminPassword = %q", cfg.Auth.AdminPassword)
	}
	if cfg.Auth.AdminUsername != "myadmin" {
		t.Fatalf("AdminUsername = %q", cfg.Auth.AdminUsername)
	}
	if cfg.Auth.JWTSecret != "myjwtsecret" {
		t.Fatalf("JWTSecret = %q", cfg.Auth.JWTSecret)
	}
}

func TestApplyAuthEnv_EnabledTrue(t *testing.T) {
	cfg := &Config{}
	t.Setenv("JACKUI_AUTH_ENABLED", "true")
	applyAuthEnv(cfg)
	if !cfg.Auth.Enabled {
		t.Fatal("expected auth enabled when JACKUI_AUTH_ENABLED=true")
	}
}

func TestApplyNotificationsEnv(t *testing.T) {
	cfg := &Config{}
	t.Setenv("JACKUI_NTFY_TOPIC", "mytopic")
	t.Setenv("JACKUI_NTFY_URL", "https://ntfy.example.com")

	applyNotificationsEnv(cfg)

	if cfg.Notifications.NtfyDefaultTopic != "mytopic" {
		t.Fatalf("NtfyDefaultTopic = %q", cfg.Notifications.NtfyDefaultTopic)
	}
	if cfg.Notifications.NtfyBaseURL != "https://ntfy.example.com" {
		t.Fatalf("NtfyBaseURL = %q", cfg.Notifications.NtfyBaseURL)
	}
}

func TestApplyTMDBEnv(t *testing.T) {
	cfg := &Config{}
	t.Setenv("TMDB_API_KEY", "tmdbkey")
	t.Setenv("OMDB_API_KEY", "omdbkey")

	applyTMDBEnv(cfg)

	if cfg.TMDB.APIKey != "tmdbkey" {
		t.Fatalf("TMDB.APIKey = %q", cfg.TMDB.APIKey)
	}
	if cfg.TMDB.OMDbAPIKey != "omdbkey" {
		t.Fatalf("OMDbAPIKey = %q", cfg.TMDB.OMDbAPIKey)
	}
}

func TestApplySMTPEnv(t *testing.T) {
	cfg := &Config{}
	t.Setenv("JACKUI_SMTP_HOST", "smtp.example.com")
	t.Setenv("JACKUI_SMTP_PORT", "587")
	t.Setenv("JACKUI_SMTP_USER", "user")
	t.Setenv("JACKUI_SMTP_PASS", "pass")
	t.Setenv("JACKUI_SMTP_FROM", "from@example.com")
	t.Setenv("JACKUI_BASE_URL", "https://jackui.example.com")

	applySMTPEnv(cfg)

	if cfg.SMTP.Host != "smtp.example.com" {
		t.Fatalf("SMTP.Host = %q", cfg.SMTP.Host)
	}
	if cfg.SMTP.Port != 587 {
		t.Fatalf("SMTP.Port = %d", cfg.SMTP.Port)
	}
	if cfg.SMTP.Username != "user" {
		t.Fatalf("SMTP.Username = %q", cfg.SMTP.Username)
	}
	if cfg.SMTP.Password != "pass" {
		t.Fatalf("SMTP.Password = %q", cfg.SMTP.Password)
	}
	if cfg.SMTP.From != "from@example.com" {
		t.Fatalf("SMTP.From = %q", cfg.SMTP.From)
	}
	if cfg.BaseURL != "https://jackui.example.com" {
		t.Fatalf("BaseURL = %q", cfg.BaseURL)
	}
}

func TestApplySMTPEnv_InvalidPort(t *testing.T) {
	cfg := &Config{}
	t.Setenv("JACKUI_SMTP_PORT", "not-a-number")
	applySMTPEnv(cfg)
	if cfg.SMTP.Port != 0 {
		t.Fatalf("expected Port 0, got %d", cfg.SMTP.Port)
	}
}

func TestApplyAIEnv_GroqKey(t *testing.T) {
	cfg := &Config{}
	t.Setenv("GROQ_API_KEY", "groqkey123")
	applyAIEnv(cfg)
	if cfg.AI.Providers["groq"].APIKey != "groqkey123" {
		t.Fatalf("groq APIKey not set")
	}
}

func TestApplyAIEnv_OpenRouterKey(t *testing.T) {
	cfg := &Config{}
	t.Setenv("OPENROUTER_API_KEY", "orkey123")
	applyAIEnv(cfg)
	if cfg.AI.Providers["openrouter"].APIKey != "orkey123" {
		t.Fatalf("openrouter APIKey not set")
	}
}

func TestApplyAIEnv_Disabled(t *testing.T) {
	cfg := &Config{}
	t.Setenv("GROQ_API_KEY", "key")
	t.Setenv("JACKUI_AI_ENABLED", "0")
	applyAIEnv(cfg)
	if cfg.AI.Enabled {
		t.Fatal("expected AI disabled")
	}
}

func TestApplyOllamaEnv(t *testing.T) {
	cfg := &Config{}
	t.Setenv("OLLAMA_BASE_URL", "http://ollama:11434")
	applyAIEnv(cfg)
	p := cfg.AI.Providers["ollama"]
	if p.BaseURL != "http://ollama:11434/v1" {
		t.Fatalf("ollama BaseURL = %q", p.BaseURL)
	}
}

func TestApplyOllamaEnv_AlreadyV1(t *testing.T) {
	cfg := &Config{}
	t.Setenv("OLLAMA_BASE_URL", "http://ollama:11434/v1")
	applyAIEnv(cfg)
	p := cfg.AI.Providers["ollama"]
	if p.BaseURL != "http://ollama:11434/v1" {
		t.Fatalf("ollama BaseURL = %q", p.BaseURL)
	}
}

func TestApplyOllamaEnv_Empty(t *testing.T) {
	cfg := &Config{}
	applyAIEnv(cfg)
	p := cfg.AI.Providers["ollama"]
	if p.BaseURL != "" {
		t.Fatalf("expected empty ollama baseURL, got %q", p.BaseURL)
	}
}

func TestPickModel_Preferred(t *testing.T) {
	m := pickModel([]string{"llama-3.1-8b-instant", "mixtral-8x7b-32768"}, "llama-3.1-8b-instant")
	if m != "llama-3.1-8b-instant" {
		t.Fatalf("got %q", m)
	}
}

func TestPickModel_FreeFallback(t *testing.T) {
	m := pickModel([]string{"model-free", "other"}, "nonexistent")
	if m != "model-free" {
		t.Fatalf("expected free model, got %q", m)
	}
}

func TestPickModel_CheapFallback(t *testing.T) {
	m := pickModel([]string{"some-flash-model", "other"}, "nonexistent")
	if m != "some-flash-model" {
		t.Fatalf("expected flash model, got %q", m)
	}
}

func TestPickModel_NonEmbedding(t *testing.T) {
	m := pickModel([]string{"text-embedding-ada-002", "gpt-3.5-turbo"}, "nonexistent")
	if m != "gpt-3.5-turbo" {
		t.Fatalf("expected non-embedding model, got %q", m)
	}
}

func TestPickModel_First(t *testing.T) {
	m := pickModel([]string{"only-model"}, "nonexistent")
	if m != "only-model" {
		t.Fatalf("got %q", m)
	}
}

func TestPickModel_Empty(t *testing.T) {
	m := pickModel(nil, "nonexistent")
	if m != "" {
		t.Fatalf("expected empty, got %q", m)
	}
}

func TestMatchFreeModel(t *testing.T) {
	if m := matchFreeModel([]string{"test-free", "other"}); m != "test-free" {
		t.Fatalf("got %q", m)
	}
	if m := matchFreeModel([]string{"no-suffix"}); m != "" {
		t.Fatalf("expected empty, got %q", m)
	}
}

func TestMatchCheapModel(t *testing.T) {
	if m := matchCheapModel([]string{"model-flash", "other"}); m != "model-flash" {
		t.Fatalf("got %q", m)
	}
	if m := matchCheapModel([]string{"model-MINI", "other"}); m != "model-MINI" {
		t.Fatalf("got %q", m)
	}
	if m := matchCheapModel([]string{"nano-model", "other"}); m != "nano-model" {
		t.Fatalf("got %q", m)
	}
	if m := matchCheapModel([]string{"no-match"}); m != "" {
		t.Fatalf("expected empty, got %q", m)
	}
}

func TestMatchNonEmbedding(t *testing.T) {
	if m := matchNonEmbedding([]string{"text-embedding-ada-002", "gpt-4"}); m != "gpt-4" {
		t.Fatalf("got %q", m)
	}
	if m := matchNonEmbedding([]string{"text-embedding-ada-002"}); m != "" {
		t.Fatalf("expected empty, got %q", m)
	}
	if m := matchNonEmbedding(nil); m != "" {
		t.Fatalf("expected empty, got %q", m)
	}
}

func TestDefaultConfig(t *testing.T) {
	cfg := defaultConfig()
	if cfg.Port != 8989 {
		t.Fatalf("Port = %d", cfg.Port)
	}
	if cfg.Jackett.URL != "http://localhost:9117" {
		t.Fatalf("Jackett.URL = %q", cfg.Jackett.URL)
	}
	if len(cfg.DownloadClients) != 1 {
		t.Fatalf("expected 1 download client, got %d", len(cfg.DownloadClients))
	}
}

func TestApplyEnvOverrides_DefaultIdleAndMetadata(t *testing.T) {
	cfg := &Config{}
	applyEnvOverrides(cfg)
	if cfg.Stream.IdleMinutes != 30 {
		t.Fatalf("IdleMinutes = %d", cfg.Stream.IdleMinutes)
	}
	if cfg.Stream.MetadataSeconds != 60 {
		t.Fatalf("MetadataSeconds = %d", cfg.Stream.MetadataSeconds)
	}
}

func TestAutoSeedChain_NoProviders(t *testing.T) {
	cfg := &Config{}
	autoSeedChain(cfg)
	if cfg.AI.Enabled {
		t.Fatal("expected AI not enabled without providers")
	}
}

func TestAutoSeedChain_ExistingChainDisabled(t *testing.T) {
	cfg := &Config{}
	cfg.AI.Chain = []AIChainSlot{{ID: "test", Provider: "groq", Model: "llama"}}
	cfg.AI.Enabled = false
	autoSeedChain(cfg)
	if !cfg.AI.Enabled {
		t.Fatal("expected AI to be enabled when chain exists")
	}
	if len(cfg.AI.Chain) != 1 {
		t.Fatalf("expected chain to be preserved")
	}
}

func TestBuildDefaultChain_NoProviders(t *testing.T) {
	cfg := &Config{}
	chain := cfg.buildDefaultChain()
	if len(chain) != 0 {
		t.Fatalf("expected empty chain, got %d", len(chain))
	}
}
