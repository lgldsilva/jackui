package handlers

import (
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/luizg/jackui/internal/config"
	"github.com/luizg/jackui/internal/jackett"
)

type configResponse struct {
	Port    int                      `json:"port"`
	Jackett jackettConfigResponse    `json:"jackett"`
	Clients []downloadClientResponse `json:"downloadClients"`
}

type jackettConfigResponse struct {
	URL    string `json:"url"`
	APIKey string `json:"apiKey"`
}

type downloadClientResponse struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	Type     string `json:"type"`
	URL      string `json:"url"`
	Username string `json:"username"`
	Password string `json:"password,omitempty"`
	Default  bool   `json:"default"`
}

type configUpdateRequest struct {
	Port    int                      `json:"port"`
	Jackett jackettConfigResponse    `json:"jackett"`
	Clients []downloadClientResponse `json:"downloadClients"`
}

// GetConfig handles GET /api/config
func GetConfig(cfg *config.Config, configPath string) gin.HandlerFunc {
	return func(c *gin.Context) {
		clients := make([]downloadClientResponse, 0, len(cfg.DownloadClients))
		for _, dc := range cfg.DownloadClients {
			clients = append(clients, downloadClientResponse{
				ID:       dc.ID,
				Name:     dc.Name,
				Type:     dc.Type,
				URL:      dc.URL,
				Username: dc.Username,
				// Omit password in response for security
				Default: dc.Default,
			})
		}

		c.JSON(http.StatusOK, configResponse{
			Port: cfg.Port,
			Jackett: jackettConfigResponse{
				URL:    cfg.Jackett.URL,
				APIKey: cfg.Jackett.APIKey,
			},
			Clients: clients,
		})
	}
}

// UpdateConfig handles PUT /api/config
func UpdateConfig(cfg *config.Config, configPath string) gin.HandlerFunc {
	return func(c *gin.Context) {
		var req configUpdateRequest
		if err := c.ShouldBindJSON(&req); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "invalid request body"})
			return
		}

		// Update config
		cfg.Port = req.Port
		cfg.Jackett.URL = req.Jackett.URL
		cfg.Jackett.APIKey = req.Jackett.APIKey

		newClients := make([]config.DownloadClient, 0, len(req.Clients))
		for _, dc := range req.Clients {
			// Preserve existing password if not provided
			password := dc.Password
			if password == "" {
				for _, existing := range cfg.DownloadClients {
					if existing.ID == dc.ID {
						password = existing.Password
						break
					}
				}
			}

			newClients = append(newClients, config.DownloadClient{
				ID:       dc.ID,
				Name:     dc.Name,
				Type:     dc.Type,
				URL:      dc.URL,
				Username: dc.Username,
				Password: password,
				Default:  dc.Default,
			})
		}
		cfg.DownloadClients = newClients

		if err := cfg.Save(configPath); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to save config: " + err.Error()})
			return
		}

		c.JSON(http.StatusOK, gin.H{"message": "config saved successfully"})
	}
}

// TestJackett handles POST /api/config/test
func TestJackett(cfg *config.Config) gin.HandlerFunc {
	return func(c *gin.Context) {
		client := jackett.New(cfg.Jackett.URL, cfg.Jackett.APIKey)
		if err := client.TestConnection(); err != nil {
			c.JSON(http.StatusBadGateway, gin.H{"error": err.Error(), "success": false})
			return
		}
		c.JSON(http.StatusOK, gin.H{"message": "connection successful", "success": true})
	}
}
