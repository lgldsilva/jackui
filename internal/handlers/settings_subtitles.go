package handlers

import (
	"fmt"
	"net/http"
	"regexp"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/lgldsilva/jackui/internal/config"
	"github.com/lgldsilva/jackui/internal/handlers/httpshared"
)

// subtitlesSettingsBody is the wire shape for GET/POST /api/settings/subtitles.
// Deliberately explicit: config.SubtitlesConfig itself carries the
// OpenSubtitles credentials and must never be serialized to the web.
type subtitlesSettingsBody struct {
	DefaultLanguages string `json:"default_languages"`
	AutoFetch        bool   `json:"auto_fetch"`
	BurnDefault      bool   `json:"burn_default"`
}

// maxSubtitlesLanguages caps the comma-separated language list.
const maxSubtitlesLanguages = 5

// subtitlesLanguagePattern matches ONE language code: a 2-3 letter primary tag
// with optional "-"-joined subtags (e.g. "pt", "pt-BR", "zh-Hans"). Spaces are
// never valid inside a code.
var subtitlesLanguagePattern = regexp.MustCompile(`^[A-Za-z]{2,3}(-[A-Za-z0-9]{2,8})*$`)

// currentSubtitlesSettings projects the live config into the wire shape,
// applying the same fallbacks as config loading so a hand-built Config still
// answers with effective values (mirrors currentDownloadsQueue).
func currentSubtitlesSettings(cfg *config.Config) subtitlesSettingsBody {
	b := subtitlesSettingsBody{
		DefaultLanguages: cfg.Subtitles.DefaultLanguages,
		AutoFetch:        cfg.Subtitles.AutoFetch,
		BurnDefault:      cfg.Subtitles.BurnDefault,
	}
	if b.DefaultLanguages == "" {
		b.DefaultLanguages = config.DefaultSubtitlesLanguages
	}
	return b
}

// subtitlesSearchLanguages resolves the default language list for subtitle
// searches: the saved config value when set, else DefaultSubtitlesLanguages.
// Read per request, so a settings save takes effect on the next search.
func subtitlesSearchLanguages(cfg *config.Config) string {
	if cfg != nil && cfg.Subtitles.DefaultLanguages != "" {
		return cfg.Subtitles.DefaultLanguages
	}
	return config.DefaultSubtitlesLanguages
}

// normalizeSubtitlesLanguages validates and normalizes a comma-separated
// language list: entries are trimmed around commas, empty entries rejected,
// spaces inside codes rejected via the pattern, casing kept as sent
// ("pt-BR,pt" stays "pt-BR,pt"). Returns the normalized list or a
// default_languages field error message.
func normalizeSubtitlesLanguages(raw string) (string, string) {
	if strings.TrimSpace(raw) == "" {
		return "", "default_languages is required"
	}
	entries := strings.Split(raw, ",")
	if len(entries) > maxSubtitlesLanguages {
		return "", fmt.Sprintf("default_languages accepts at most %d language codes", maxSubtitlesLanguages)
	}
	normalized := make([]string, 0, len(entries))
	for _, entry := range entries {
		entry = strings.TrimSpace(entry)
		if entry == "" {
			return "", "default_languages contains an empty language code"
		}
		if !subtitlesLanguagePattern.MatchString(entry) {
			return "", fmt.Sprintf("default_languages contains an invalid language code: %q", entry)
		}
		normalized = append(normalized, entry)
	}
	return strings.Join(normalized, ","), ""
}

// SubtitlesGetSettings handles GET /api/settings/subtitles — the subtitle
// defaults the web consumes.
func SubtitlesGetSettings(cfg *config.Config) gin.HandlerFunc {
	return func(c *gin.Context) {
		c.JSON(http.StatusOK, currentSubtitlesSettings(cfg))
	}
}

// SubtitlesUpdateSettings handles POST /api/settings/subtitles (AdminOnly).
// Persists to the config file via the same Save mechanism as the other
// settings routes. Nothing reads these values at construction time — the
// searches/workers pick them up live, so no restart is ever required.
func SubtitlesUpdateSettings(cfg *config.Config, configPath string) gin.HandlerFunc {
	return func(c *gin.Context) {
		var b subtitlesSettingsBody
		if err := c.ShouldBindJSON(&b); err != nil {
			httpshared.RespondErrorMessage(c, http.StatusBadRequest, "invalid request body")
			return
		}
		normalized, errMsg := normalizeSubtitlesLanguages(b.DefaultLanguages)
		if errMsg != "" {
			httpshared.RespondErrorMessage(c, http.StatusBadRequest, errMsg)
			return
		}
		cfg.Subtitles.DefaultLanguages = normalized
		cfg.Subtitles.AutoFetch = b.AutoFetch
		cfg.Subtitles.BurnDefault = b.BurnDefault

		if err := cfg.Save(configPath); err != nil {
			httpshared.RespondErrorMessage(c, http.StatusInternalServerError, "failed to save config: "+err.Error())
			return
		}
		// Echo the normalized saved values so the UI shows exactly what stuck.
		c.JSON(http.StatusOK, gin.H{
			"message":           "settings saved",
			"restartRequired":   false,
			"default_languages": cfg.Subtitles.DefaultLanguages,
			"auto_fetch":        cfg.Subtitles.AutoFetch,
			"burn_default":      cfg.Subtitles.BurnDefault,
		})
	}
}
