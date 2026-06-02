package handlers

import (
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/luizg/jackui/internal/config"
	"github.com/luizg/jackui/internal/jackett"
	"github.com/luizg/jackui/internal/streamer"
)

type configResponse struct {
	Port         int                      `json:"port"`
	Jackett      jackettConfigResponse    `json:"jackett"`
	Clients      []downloadClientResponse `json:"downloadClients"`
	EnvOverrides map[string]string        `json:"envOverrides,omitempty"`
}

type jackettConfigResponse struct {
	URL string `json:"url"`
	// APIKey is omitted from GET responses (never echo the secret back, same as
	// download-client passwords). On PUT, an empty APIKey means "keep current".
	APIKey string `json:"apiKey,omitempty"`
	// APIKeySet tells the UI a key is stored without revealing it, so it can
	// show a "configured — leave blank to keep" hint instead of an empty field.
	APIKeySet bool `json:"apiKeySet,omitempty"`
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
				URL: cfg.Jackett.URL,
				// Never echo the key back; just signal whether one is set.
				APIKeySet: cfg.Jackett.APIKey != "",
			},
			Clients:      clients,
			EnvOverrides: config.ActiveEnvOverrides(),
		})
	}
}

// UpdateConfig handles PUT /api/config
func UpdateConfig(cfg *config.Config, configPath string, jackettClient *jackett.Client, streamSrv *streamer.Streamer) gin.HandlerFunc {
	return func(c *gin.Context) {
		var req configUpdateRequest
		if err := c.ShouldBindJSON(&req); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "invalid request body"})
			return
		}

		applyConfigUpdates(cfg, &req)
		cfg.DownloadClients = mergeDownloadClients(cfg.DownloadClients, req.Clients)

		if err := cfg.Save(configPath); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to save config: " + err.Error()})
			return
		}

		// Update live Jackett client so searches use the new URL/key.
		if jackettClient != nil {
			jackettClient.URL = cfg.Jackett.URL
			jackettClient.APIKey = cfg.Jackett.APIKey
		}
		// Update the SSRF guard's trusted host so torrent fetches aren't blocked.
		if streamSrv != nil && req.Jackett.URL != "" {
			streamSrv.UpdateJackettHost(req.Jackett.URL)
		}

		c.JSON(http.StatusOK, gin.H{"message": "config saved successfully"})
	}
}

func applyConfigUpdates(cfg *config.Config, req *configUpdateRequest) {
	cfg.Port = req.Port
	cfg.Jackett.URL = req.Jackett.URL
	if req.Jackett.APIKey != "" {
		cfg.Jackett.APIKey = req.Jackett.APIKey
	}
}

func mergeDownloadClients(existing []config.DownloadClient, reqClients []downloadClientResponse) []config.DownloadClient {
	out := make([]config.DownloadClient, 0, len(reqClients))
	for _, dc := range reqClients {
		password := resolveClientPassword(dc.Password, dc.ID, existing)
		out = append(out, config.DownloadClient{
			ID:       dc.ID,
			Name:     dc.Name,
			Type:     dc.Type,
			URL:      dc.URL,
			Username: dc.Username,
			Password: password,
			Default:  dc.Default,
		})
	}
	return out
}

func resolveClientPassword(password, id string, existing []config.DownloadClient) string {
	if password != "" {
		return password
	}
	for _, e := range existing {
		if e.ID == id {
			return e.Password
		}
	}
	return ""
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
