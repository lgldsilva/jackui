package config

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

// Regression: Load applied the env overrides and Save marshalled that merged
// struct, so secrets that only ever lived in the environment were written to
// config.yaml — on the very first boot and again on every settings save.
func TestSave_DoesNotPersistEnvSourcedValues(t *testing.T) {
	secrets := map[string]string{
		"JACKETT_API_KEY":     "env-jackett-key",
		"JACKUI_JWT_SECRET":   "env-jwt-secret-0123456789abcdef0123456789",
		"JACKUI_DATABASE_URL": "postgres://u:env-db-pass@db/jackui",
		"JACKUI_SMTP_PASS":    "env-smtp-pass",
		"TMDB_API_KEY":        "env-tmdb-key",
		"JACKUI_NTFY_TOKEN":   "env-ntfy-token",
	}
	for k, v := range secrets {
		t.Setenv(k, v)
	}
	path := filepath.Join(t.TempDir(), "config.yaml")

	// First boot: the file is created from the defaults.
	cfg, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Jackett.APIKey != "env-jackett-key" || cfg.Auth.JWTSecret != secrets["JACKUI_JWT_SECRET"] {
		t.Fatalf("env overrides must still be effective in memory: %+v", cfg.Jackett)
	}
	assertNoSecrets(t, path, secrets)

	// A settings save (any handler) must not leak them either, and must keep
	// what the admin changed.
	cfg.Update(func(c *Config) { c.DownloadsQueue.MaxActive = 7 })
	if err := cfg.Save(path); err != nil {
		t.Fatal(err)
	}
	assertNoSecrets(t, path, secrets)

	for k := range secrets {
		t.Setenv(k, "")
	}
	reloaded, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if reloaded.DownloadsQueue.MaxActive != 7 {
		t.Errorf("runtime change not persisted: max_active = %d", reloaded.DownloadsQueue.MaxActive)
	}
	if reloaded.Jackett.APIKey != "YOUR_API_KEY_HERE" {
		t.Errorf("file value was replaced by the env one: %q", reloaded.Jackett.APIKey)
	}
}

func assertNoSecrets(t *testing.T, path string, secrets map[string]string) {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	for env, v := range secrets {
		if strings.Contains(string(data), v) {
			t.Errorf("config.yaml contains the value of %s", env)
		}
	}
}

// Regression: Save used os.WriteFile on the live path (truncate, then write),
// so a concurrent reader — or a crash mid-write — could observe an empty or
// half-written config.yaml.
func TestSave_ReaderNeverSeesPartialFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	cfg := &Config{}
	for i := 0; i < 8000; i++ {
		cfg.External.Mounts = append(cfg.External.Mounts, ExternalMount{Name: "mount", Path: "/some/long/path/to/make/the/file/big"})
	}
	if err := cfg.Save(path); err != nil {
		t.Fatal(err)
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}

	done := make(chan struct{})
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		defer close(done)
		for i := 0; i < 12; i++ {
			if err := cfg.Save(path); err != nil {
				t.Errorf("save: %v", err)
				return
			}
		}
	}()

	for reading := true; reading; {
		select {
		case <-done:
			reading = false
		default:
		}
		got, err := os.ReadFile(path)
		if err != nil {
			t.Errorf("read: %v", err)
			break
		}
		if !bytes.Equal(got, want) {
			t.Errorf("reader saw a partial file: %d of %d bytes", len(got), len(want))
			break
		}
	}
	wg.Wait()
}
