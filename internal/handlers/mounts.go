package handlers

import (
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/lgldsilva/jackui/internal/config"
	"github.com/lgldsilva/jackui/internal/local"
)

// MountsGet handles GET /api/mounts (admin) — the full mount config, including
// AllowedUsers (which the public /local/mounts endpoint deliberately omits).
func MountsGet(cfg *config.Config) gin.HandlerFunc {
	return func(c *gin.Context) {
		mounts := cfg.External.Mounts
		if mounts == nil {
			mounts = []config.ExternalMount{}
		}
		c.JSON(http.StatusOK, mounts)
	}
}

// MountsUpdate handles PUT /api/mounts (admin) — replaces the mount list,
// persists it to the config file, and applies it live to the browser (no restart).
func MountsUpdate(cfg *config.Config, configPath string, browser *local.Browser) gin.HandlerFunc {
	return func(c *gin.Context) {
		var mounts []config.ExternalMount
		if err := c.ShouldBindJSON(&mounts); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "invalid request body"})
			return
		}
		if msg := validateMounts(mounts); msg != "" {
			c.JSON(http.StatusBadRequest, gin.H{"error": msg})
			return
		}
		// Snapshot → mutate → Save. On a Save failure the in-memory config is
		// ROLLED BACK so GET /api/mounts keeps reporting what is actually
		// persisted (and the browser is never updated with unsaved state).
		old := cfg.External.Mounts
		cfg.External.Mounts = mounts
		if err := cfg.Save(configPath); err != nil {
			cfg.External.Mounts = old
			c.JSON(http.StatusInternalServerError, gin.H{
				"error": "failed to save config: " + err.Error() +
					" — nada foi alterado; o config.yaml precisa ser gravável pelo uid do container (ajuste dono/permissão no host)",
			})
			return
		}
		if browser != nil {
			browser.SetMounts(mounts)
		}
		c.JSON(http.StatusOK, gin.H{"message": "mounts saved"})
	}
}

func validateMounts(mounts []config.ExternalMount) string {
	seen := make(map[string]bool, len(mounts))
	for _, m := range mounts {
		name := strings.TrimSpace(m.Name)
		if name == "" {
			return "todo mount precisa de um nome"
		}
		if strings.TrimSpace(m.Path) == "" {
			return "mount \"" + name + "\" precisa de um caminho"
		}
		if seen[name] {
			return "nome de mount duplicado: " + name
		}
		seen[name] = true
	}
	return ""
}
