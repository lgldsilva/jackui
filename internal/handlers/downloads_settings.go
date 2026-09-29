package handlers

import (
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/lgldsilva/jackui/internal/config"
	"github.com/lgldsilva/jackui/internal/handlers/httpshared"
)

// downloadsQueueBody is the wire shape for GET/PUT /api/downloads/settings.
type downloadsQueueBody struct {
	MaxActive           int  `json:"maxActive"`
	PerUserMaxActive    int  `json:"perUserMaxActive"`
	MaxConcurrentVerify int  `json:"maxConcurrentVerify"` // disk-bound piece rechecks; independent of maxActive
	StallThresholdMin   int  `json:"stallThresholdMin"`
	MaxStalls           int  `json:"maxStalls"`
	AgingStepMin        int  `json:"agingStepMin"`
	AgingCap            int  `json:"agingCap"`
	RotationEnabled     bool `json:"rotationEnabled"`
	AutoPromoteArr      bool `json:"autoPromoteArr"`
	// TransferConcurrencyMode: "auto" (default) | "serial" | "parallel".
	TransferConcurrencyMode string `json:"transferConcurrencyMode"`
}

func currentDownloadsQueue(cfg *config.Config) downloadsQueueBody {
	q := cfg.DownloadsQueue
	mcv := q.MaxConcurrentVerify
	if mcv < 1 {
		mcv = 1
	}
	return downloadsQueueBody{
		MaxActive:               q.MaxActive,
		PerUserMaxActive:        q.PerUserMaxActive,
		MaxConcurrentVerify:     mcv,
		StallThresholdMin:       q.StallThresholdMin,
		MaxStalls:               q.MaxStalls,
		AgingStepMin:            q.AgingStepMin,
		AgingCap:                q.AgingCap,
		RotationEnabled:         q.RotationEnabled,
		AutoPromoteArr:          q.AutoPromoteArr,
		TransferConcurrencyMode: transferModeOrAuto(cfg.Stream.TransferConcurrencyMode),
	}
}

// transferModeOrAuto normalizes the empty value (default) to "auto" in the
// response, so the UI always shows a selected option.
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
		return "maxActive must be >= 1"
	}
	if b.PerUserMaxActive < 0 {
		return "perUserMaxActive must be >= 0 (0 = no per-user limit)"
	}
	if b.MaxConcurrentVerify < 1 {
		return "maxConcurrentVerify must be >= 1"
	}
	if b.StallThresholdMin < 1 {
		return "stallThresholdMin must be >= 1"
	}
	if b.MaxStalls < 1 {
		return "maxStalls must be >= 1"
	}
	if b.AgingStepMin < 0 || b.AgingCap < 0 {
		return "aging values must be >= 0"
	}
	switch b.TransferConcurrencyMode {
	case "", transferModeAuto, transferModeSerial, transferModeParallel:
	default:
		return "transferConcurrencyMode must be auto, serial or parallel"
	}
	return ""
}

// DownloadsUpdateSettings handles PUT /api/downloads/settings (AdminOnly). The
// worker reads these live each tick, so everything applies without a restart.
// setVerifyConcurrency applies maxConcurrentVerify to the streamer (may be nil).
func DownloadsUpdateSettings(cfg *config.Config, configPath string, setVerifyConcurrency func(int)) gin.HandlerFunc {
	return func(c *gin.Context) {
		var b downloadsQueueBody
		if err := c.ShouldBindJSON(&b); err != nil {
			httpshared.RespondErrorMessage(c, http.StatusBadRequest, "invalid request body")
			return
		}
		// Omitted field (0) → disk-safe default; keeps older clients working.
		if b.MaxConcurrentVerify == 0 {
			b.MaxConcurrentVerify = 1
		}
		if msg := validateDownloadsQueue(&b); msg != "" {
			httpshared.RespondErrorMessage(c, http.StatusBadRequest, msg)
			return
		}
		cfg.DownloadsQueue.MaxActive = b.MaxActive
		cfg.DownloadsQueue.PerUserMaxActive = b.PerUserMaxActive
		cfg.DownloadsQueue.MaxConcurrentVerify = b.MaxConcurrentVerify
		cfg.DownloadsQueue.StallThresholdMin = b.StallThresholdMin
		cfg.DownloadsQueue.MaxStalls = b.MaxStalls
		cfg.DownloadsQueue.AgingStepMin = b.AgingStepMin
		cfg.DownloadsQueue.AgingCap = b.AgingCap
		cfg.DownloadsQueue.RotationEnabled = b.RotationEnabled
		cfg.DownloadsQueue.AutoPromoteArr = b.AutoPromoteArr
		// "auto" is the default; persist empty to keep the yaml clean.
		if b.TransferConcurrencyMode == transferModeAuto {
			cfg.Stream.TransferConcurrencyMode = ""
		} else {
			cfg.Stream.TransferConcurrencyMode = b.TransferConcurrencyMode
		}
		if setVerifyConcurrency != nil {
			setVerifyConcurrency(b.MaxConcurrentVerify)
		}

		if err := cfg.Save(configPath); err != nil {
			httpshared.RespondErrorMessage(c, http.StatusInternalServerError, "failed to save config: "+err.Error())
			return
		}
		// All queue settings are read live by the worker → no restart needed.
		c.JSON(http.StatusOK, gin.H{"message": "settings saved", "restartRequired": false})
	}
}
