package handlers

import (
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/lgldsilva/jackui/internal/config"
	"github.com/lgldsilva/jackui/internal/downloader"
	"github.com/lgldsilva/jackui/internal/handlers/httpshared"
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

func parseDownloadRequest(c *gin.Context) (*downloadRequest, bool) {
	var req downloadRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		httpshared.RespondErrorMessage(c, http.StatusBadRequest, "invalid request body")
		return nil, false
	}
	if req.MagnetURI == "" && req.TorrentURL == "" {
		httpshared.RespondErrorMessage(c, http.StatusBadRequest, "magnetUri or torrentUrl is required")
		return nil, false
	}
	return &req, true
}

func resolveDownloadClient(cfg *config.Config, clientID string) *config.DownloadClient {
	if clientID == "" {
		for i := range cfg.DownloadClients {
			if cfg.DownloadClients[i].Default {
				return &cfg.DownloadClients[i]
			}
		}
		if len(cfg.DownloadClients) > 0 {
			return &cfg.DownloadClients[0]
		}
		return nil
	}
	for i := range cfg.DownloadClients {
		if cfg.DownloadClients[i].ID == clientID {
			return &cfg.DownloadClients[i]
		}
	}
	return nil
}

func addToDownloadClient(client downloader.Client, req *downloadRequest) error {
	if req.MagnetURI != "" {
		return client.AddMagnet(req.MagnetURI, req.SavePath)
	}
	return client.AddTorrentURL(req.TorrentURL, req.SavePath)
}

// Download handles POST /api/download
func Download(cfg *config.Config) gin.HandlerFunc {
	return func(c *gin.Context) {
		req, ok := parseDownloadRequest(c)
		if !ok {
			return
		}

		selectedClient := resolveDownloadClient(cfg, req.ClientID)
		if selectedClient == nil {
			httpshared.RespondErrorMessage(c, http.StatusBadRequest, "no download client found")
			return
		}

		client, err := downloader.New(*selectedClient)
		if err != nil {
			httpshared.RespondError(c, http.StatusInternalServerError, err)
			return
		}

		if err := addToDownloadClient(client, req); err != nil {
			httpshared.RespondError(c, http.StatusBadGateway, err)
			return
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
