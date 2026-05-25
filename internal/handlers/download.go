package handlers

import (
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/luizg/jackui/internal/config"
	"github.com/luizg/jackui/internal/downloader"
)

type downloadRequest struct {
	ClientID   string `json:"clientId"`
	MagnetURI  string `json:"magnetUri"`
	TorrentURL string `json:"torrentUrl"`
	SavePath   string `json:"savePath"`
}

type clientResponse struct {
	ID      string `json:"id"`
	Name    string `json:"name"`
	Type    string `json:"type"`
	Default bool   `json:"default"`
}

// Download handles POST /api/download
func Download(cfg *config.Config) gin.HandlerFunc {
	return func(c *gin.Context) {
		var req downloadRequest
		if err := c.ShouldBindJSON(&req); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "invalid request body"})
			return
		}

		if req.MagnetURI == "" && req.TorrentURL == "" {
			c.JSON(http.StatusBadRequest, gin.H{"error": "magnetUri or torrentUrl is required"})
			return
		}

		// Find the download client
		var selectedClient *config.DownloadClient
		if req.ClientID == "" {
			// Use default client
			for i := range cfg.DownloadClients {
				if cfg.DownloadClients[i].Default {
					selectedClient = &cfg.DownloadClients[i]
					break
				}
			}
			if selectedClient == nil && len(cfg.DownloadClients) > 0 {
				selectedClient = &cfg.DownloadClients[0]
			}
		} else {
			for i := range cfg.DownloadClients {
				if cfg.DownloadClients[i].ID == req.ClientID {
					selectedClient = &cfg.DownloadClients[i]
					break
				}
			}
		}

		if selectedClient == nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "no download client found"})
			return
		}

		client, err := downloader.New(*selectedClient)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}

		if req.MagnetURI != "" {
			if err := client.AddMagnet(req.MagnetURI, req.SavePath); err != nil {
				c.JSON(http.StatusBadGateway, gin.H{"error": err.Error()})
				return
			}
		} else {
			if err := client.AddTorrentURL(req.TorrentURL, req.SavePath); err != nil {
				c.JSON(http.StatusBadGateway, gin.H{"error": err.Error()})
				return
			}
		}

		c.JSON(http.StatusOK, gin.H{"message": "torrent added successfully"})
	}
}

// GetClients handles GET /api/clients
func GetClients(cfg *config.Config) gin.HandlerFunc {
	return func(c *gin.Context) {
		clients := make([]clientResponse, 0, len(cfg.DownloadClients))
		for _, dc := range cfg.DownloadClients {
			clients = append(clients, clientResponse{
				ID:      dc.ID,
				Name:    dc.Name,
				Type:    dc.Type,
				Default: dc.Default,
			})
		}
		c.JSON(http.StatusOK, clients)
	}
}
