package downloader

import (
	"testing"

	"github.com/luizg/jackui/internal/config"
)

func TestNew(t *testing.T) {
	tests := []struct {
		name    string
		dc      config.DownloadClient
		wantErr bool
	}{
		{"qbittorrent", config.DownloadClient{Type: "qbittorrent", URL: "http://localhost:8080"}, false},
		{"transmission", config.DownloadClient{Type: "transmission", URL: "http://localhost:9091"}, false},
		{"unknown type", config.DownloadClient{Type: "unknown"}, true},
		{"empty type", config.DownloadClient{Type: ""}, true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			client, err := New(tc.dc)
			if tc.wantErr {
				if err == nil {
					t.Fatal("expected error")
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if client == nil {
				t.Fatal("expected non-nil client")
			}
			if client.Type() != tc.dc.Type {
				t.Errorf("Type() = %q, want %q", client.Type(), tc.dc.Type)
			}
		})
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
