package handlers

import (
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/luizg/jackui/internal/config"
)

// downloadsQueueBody is the wire shape for GET/PUT /api/downloads/settings.
type downloadsQueueBody struct {
	MaxActive         int  `json:"maxActive"`
	StallThresholdMin int  `json:"stallThresholdMin"`
	MaxStalls         int  `json:"maxStalls"`
	AgingStepMin      int  `json:"agingStepMin"`
	AgingCap          int  `json:"agingCap"`
	RotationEnabled   bool `json:"rotationEnabled"`
}

func currentDownloadsQueue(cfg *config.Config) downloadsQueueBody {
	q := cfg.DownloadsQueue
	return downloadsQueueBody{
		MaxActive:         q.MaxActive,
		StallThresholdMin: q.StallThresholdMin,
		MaxStalls:         q.MaxStalls,
		AgingStepMin:      q.AgingStepMin,
		AgingCap:          q.AgingCap,
		RotationEnabled:   q.RotationEnabled,
	}
}

// DownloadsGetSettings handles GET /api/downloads/settings — current queue knobs.
func DownloadsGetSettings(cfg *config.Config) gin.HandlerFunc {
	return func(c *gin.Context) {
		c.JSON(http.StatusOK, currentDownloadsQueue(cfg))
	}
}

func validateDownloadsQueue(b *downloadsQueueBody) string {
	if b.MaxActive < 1 {
		return "maxActive deve ser >= 1"
	}
	if b.StallThresholdMin < 1 {
		return "stallThresholdMin deve ser >= 1"
	}
	if b.MaxStalls < 1 {
		return "maxStalls deve ser >= 1"
	}
	if b.AgingStepMin < 0 || b.AgingCap < 0 {
		return "valores de aging devem ser >= 0"
	}
	return ""
}

// DownloadsUpdateSettings handles PUT /api/downloads/settings (AdminOnly). The
// worker reads these live each tick, so everything applies without a restart.
func DownloadsUpdateSettings(cfg *config.Config, configPath string) gin.HandlerFunc {
	return func(c *gin.Context) {
		var b downloadsQueueBody
		if err := c.ShouldBindJSON(&b); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "invalid request body"})
			return
		}
		if msg := validateDownloadsQueue(&b); msg != "" {
			c.JSON(http.StatusBadRequest, gin.H{"error": msg})
			return
		}
		cfg.DownloadsQueue.MaxActive = b.MaxActive
		cfg.DownloadsQueue.StallThresholdMin = b.StallThresholdMin
		cfg.DownloadsQueue.MaxStalls = b.MaxStalls
		cfg.DownloadsQueue.AgingStepMin = b.AgingStepMin
		cfg.DownloadsQueue.AgingCap = b.AgingCap
		cfg.DownloadsQueue.RotationEnabled = b.RotationEnabled

		if err := cfg.Save(configPath); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to save config: " + err.Error()})
			return
		}
		// All queue settings are read live by the worker → no restart needed.
		c.JSON(http.StatusOK, gin.H{"message": "settings saved", "restartRequired": false})
	}
}
