package downloader

import (
	"fmt"

	"github.com/luizg/jackui/internal/config"
)

// Client defines the interface for download clients
type Client interface {
	AddMagnet(magnetURI string, savePath string) error
	AddTorrentURL(url string, savePath string) error
	Name() string
	Type() string
}

// New creates a new download client based on the configuration
func New(dc config.DownloadClient) (Client, error) {
	switch dc.Type {
	case "qbittorrent":
		return NewQBittorrent(dc), nil
	case "transmission":
		return NewTransmission(dc), nil
	default:
		return nil, fmt.Errorf("unknown download client type: %s", dc.Type)
	}
}
