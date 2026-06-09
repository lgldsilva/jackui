package downloader

import (
	"testing"

	"github.com/lgldsilva/jackui/internal/config"
)

func TestNew(t *testing.T) {
	t.Run("qbittorrent", func(t *testing.T) {
		assertNewClient(t, config.DownloadClient{Type: "qbittorrent", URL: "http://localhost:8080"})
	})
	t.Run("transmission", func(t *testing.T) {
		assertNewClient(t, config.DownloadClient{Type: "transmission", URL: "http://localhost:9091"})
	})
	t.Run("unknown type", func(t *testing.T) {
		assertNewClientError(t, config.DownloadClient{Type: "unknown"})
	})
	t.Run("empty type", func(t *testing.T) {
		assertNewClientError(t, config.DownloadClient{Type: ""})
	})
}

func assertNewClient(t *testing.T, dc config.DownloadClient) {
	t.Helper()
	client, err := New(dc)
	if err != nil {
		t.Fatalf("New(%+v): %v", dc, err)
	}
	if client == nil {
		t.Fatal("expected non-nil client")
	}
	if client.Type() != dc.Type {
		t.Errorf("Type() = %q, want %q", client.Type(), dc.Type)
	}
}

func assertNewClientError(t *testing.T, dc config.DownloadClient) {
	t.Helper()
	_, err := New(dc)
	if err == nil {
		t.Fatalf("New(%+v): expected error", dc)
	}
}

func TestClientName(t *testing.T) {
	client, err := New(config.DownloadClient{Type: "qbittorrent", Name: "My QBit", URL: "http://localhost:8080"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if name := client.Name(); name != "My QBit" {
		t.Errorf("Name() = %q, want %q", name, "My QBit")
	}
}
