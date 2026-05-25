package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoad_CreatesDefaultWhenMissing(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")

	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("expected no error, got: %v", err)
	}

	if cfg.Port != 8989 {
		t.Errorf("Port = %d, want 8989", cfg.Port)
	}
	if cfg.Jackett.URL != "http://localhost:9117" {
		t.Errorf("Jackett.URL = %q, want http://localhost:9117", cfg.Jackett.URL)
	}
	if cfg.Jackett.APIKey == "" {
		t.Error("expected non-empty default API key placeholder")
	}
	if len(cfg.DownloadClients) == 0 {
		t.Error("expected at least one default download client")
	}

	if _, err := os.Stat(path); os.IsNotExist(err) {
		t.Error("expected config file to be written to disk")
	}
}

func TestLoad_ValidYAML(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	content := `
port: 7777
jackett:
  url: http://jackett:9117
  api_key: secret123
download_clients:
  - id: qbit
    name: My qBit
    type: qbittorrent
    url: http://localhost:8080
    username: admin
    password: pass
    default: true
  - id: trans
    name: Transmission
    type: transmission
    url: http://localhost:9091
    default: false
`
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}

	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if cfg.Port != 7777 {
		t.Errorf("Port = %d, want 7777", cfg.Port)
	}
	if cfg.Jackett.URL != "http://jackett:9117" {
		t.Errorf("Jackett.URL = %q", cfg.Jackett.URL)
	}
	if cfg.Jackett.APIKey != "secret123" {
		t.Errorf("Jackett.APIKey = %q, want secret123", cfg.Jackett.APIKey)
	}
	if len(cfg.DownloadClients) != 2 {
		t.Fatalf("len(DownloadClients) = %d, want 2", len(cfg.DownloadClients))
	}

	dc := cfg.DownloadClients[0]
	if dc.ID != "qbit" || dc.Name != "My qBit" || dc.Type != "qbittorrent" {
		t.Errorf("client[0] fields mismatch: %+v", dc)
	}
	if !dc.Default {
		t.Error("expected client[0].Default = true")
	}
	if cfg.DownloadClients[1].Default {
		t.Error("expected client[1].Default = false")
	}
}

func TestLoad_DefaultPort_WhenZero(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	content := "jackett:\n  url: http://localhost:9117\n  api_key: key\n"
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}

	cfg, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Port != 8989 {
		t.Errorf("Port = %d, want default 8989", cfg.Port)
	}
}

func TestLoad_InvalidYAML(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(path, []byte(":::invalid: yaml: :::"), 0644); err != nil {
		t.Fatal(err)
	}

	_, err := Load(path)
	if err == nil {
		t.Error("expected error for invalid YAML, got nil")
	}
}

func TestSave_Roundtrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")

	original := &Config{Port: 9999}
	original.Jackett.URL = "http://myhost:9117"
	original.Jackett.APIKey = "mykey"
	original.DownloadClients = []DownloadClient{
		{ID: "t1", Name: "Transmission", Type: "transmission", URL: "http://t:9091", Default: false},
		{ID: "q1", Name: "qBit", Type: "qbittorrent", URL: "http://q:8080", Username: "admin", Password: "pass", Default: true},
	}

	if err := original.Save(path); err != nil {
		t.Fatalf("Save failed: %v", err)
	}

	loaded, err := Load(path)
	if err != nil {
		t.Fatalf("Load after Save failed: %v", err)
	}

	if loaded.Port != original.Port {
		t.Errorf("Port: want %d got %d", original.Port, loaded.Port)
	}
	if loaded.Jackett.URL != original.Jackett.URL {
		t.Errorf("Jackett.URL: want %q got %q", original.Jackett.URL, loaded.Jackett.URL)
	}
	if loaded.Jackett.APIKey != original.Jackett.APIKey {
		t.Errorf("Jackett.APIKey mismatch")
	}
	if len(loaded.DownloadClients) != 2 {
		t.Fatalf("len(DownloadClients) = %d, want 2", len(loaded.DownloadClients))
	}
	if loaded.DownloadClients[0].ID != "t1" || loaded.DownloadClients[1].ID != "q1" {
		t.Errorf("client IDs mismatch: %v", loaded.DownloadClients)
	}
	if loaded.DownloadClients[1].Password != "pass" {
		t.Error("password not preserved in roundtrip")
	}
}
