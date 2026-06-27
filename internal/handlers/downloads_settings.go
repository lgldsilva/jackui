package handlers

import (
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/lgldsilva/jackui/internal/config"
)

// downloadsQueueBody is the wire shape for GET/PUT /api/downloads/settings.
type downloadsQueueBody struct {
	MaxActive         int  `json:"maxActive"`
	PerUserMaxActive  int  `json:"perUserMaxActive"`
	StallThresholdMin int  `json:"stallThresholdMin"`
	MaxStalls         int  `json:"maxStalls"`
	AgingStepMin      int  `json:"agingStepMin"`
	AgingCap          int  `json:"agingCap"`
	RotationEnabled   bool `json:"rotationEnabled"`
	AutoPromoteArr    bool `json:"autoPromoteArr"`
	// TransferConcurrencyMode: "auto" (default) | "serial" | "parallel".
	TransferConcurrencyMode string `json:"transferConcurrencyMode"`
}

func currentDownloadsQueue(cfg *config.Config) downloadsQueueBody {
	q := cfg.DownloadsQueue
	return downloadsQueueBody{
		MaxActive:               q.MaxActive,
		PerUserMaxActive:        q.PerUserMaxActive,
		StallThresholdMin:       q.StallThresholdMin,
		MaxStalls:               q.MaxStalls,
		AgingStepMin:            q.AgingStepMin,
		AgingCap:                q.AgingCap,
		RotationEnabled:         q.RotationEnabled,
		AutoPromoteArr:          q.AutoPromoteArr,
		TransferConcurrencyMode: transferModeOrAuto(cfg.Stream.TransferConcurrencyMode),
	}
}

// transferModeOrAuto normaliza o valor vazio (default) para "auto" na resposta,
// pra UI sempre mostrar uma opção selecionada.
func transferModeOrAuto(m string) string {
	if m == "" {
		return transferModeAuto
	}
	return m
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
	if b.PerUserMaxActive < 0 {
		return "perUserMaxActive deve ser >= 0 (0 = sem limite por usuário)"
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
	switch b.TransferConcurrencyMode {
	case "", transferModeAuto, transferModeSerial, transferModeParallel:
	default:
		return "transferConcurrencyMode deve ser auto, serial ou parallel"
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
		cfg.DownloadsQueue.PerUserMaxActive = b.PerUserMaxActive
		cfg.DownloadsQueue.StallThresholdMin = b.StallThresholdMin
		cfg.DownloadsQueue.MaxStalls = b.MaxStalls
		cfg.DownloadsQueue.AgingStepMin = b.AgingStepMin
		cfg.DownloadsQueue.AgingCap = b.AgingCap
		cfg.DownloadsQueue.RotationEnabled = b.RotationEnabled
		cfg.DownloadsQueue.AutoPromoteArr = b.AutoPromoteArr
		// "auto" é o default; persiste vazio pra manter o yaml limpo.
		if b.TransferConcurrencyMode == transferModeAuto {
			cfg.Stream.TransferConcurrencyMode = ""
		} else {
			cfg.Stream.TransferConcurrencyMode = b.TransferConcurrencyMode
		}

		if err := cfg.Save(configPath); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to save config: " + err.Error()})
			return
		}
		// All queue settings are read live by the worker → no restart needed.
		c.JSON(http.StatusOK, gin.H{"message": "settings saved", "restartRequired": false})
	}
}
