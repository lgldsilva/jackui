package handlers

import (
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/lgldsilva/jackui/internal/config"
	"github.com/lgldsilva/jackui/internal/handlers/httpshared"
	"github.com/lgldsilva/jackui/internal/streamer"
)

// anacrolix defaults (v1.61.0) — used as placeholders in the UI when the field
// is 0 ("use the lib default"). Kept here so the UI doesn't have to guess.
// Mirror NewDefaultClientConfig + streamReadaheadDefault.
const (
	defReadaheadMB        = 32
	defMaxConnsPerTorrent = 50
	defHalfOpenConns      = 25
	defPeersHighWater     = 500
	defPieceHashers       = 2
)

type streamSettingsDefaults struct {
	ReadaheadMB        int `json:"readaheadMB"`
	MaxConnsPerTorrent int `json:"maxConnsPerTorrent"`
	HalfOpenConns      int `json:"halfOpenConns"`
	PeersHighWater     int `json:"peersHighWater"`
	PieceHashers       int `json:"pieceHashers"`
}

type streamSettingsBody struct {
	MaxDownloadRate    int64  `json:"maxDownloadRate"` // bytes/seg, 0=ilimitado
	MaxUploadRate      int64  `json:"maxUploadRate"`
	ReadaheadMB        int    `json:"readaheadMB"`
	StorageBackend     string `json:"storageBackend"`
	MaxConnsPerTorrent int    `json:"maxConnsPerTorrent"`
	HalfOpenConns      int    `json:"halfOpenConns"`
	PeersHighWater     int    `json:"peersHighWater"`
	PieceHashers       int    `json:"pieceHashers"`
	MaxCacheGB         int    `json:"maxCacheGB"`
	// SeedTrackers: substrings of announce URLs whose torrents keep
	// seeding after use (e.g. "amigos-share"). Applied live, no restart.
	SeedTrackers []string `json:"seedTrackers"`
	// HLSMediaRenditions wires the EXT-X-MEDIA renditions (audio/subtitle) into the
	// HLS master (Phase 2 M2b). Applied live (the handler reads it on the next play). ⚠ with ON,
	// the in-app audio selector regresses on hls.js (Chrome/Firefox) until the front
	// migrates to hls.audioTrack — native Safari uses its own menu.
	HLSMediaRenditions bool `json:"hlsMediaRenditions"`
}

type streamSettingsResponse struct {
	streamSettingsBody
	Defaults streamSettingsDefaults `json:"defaults"`
}

func currentStreamSettings(cfg *config.Config, s *streamer.Streamer) streamSettingsBody {
	st := cfg.Stream
	down, up := st.MaxDownloadRate, st.MaxUploadRate
	// Live rate limits are the source of truth (they may have been changed via the
	// legacy /stream/limits endpoint without going through the config).
	if s != nil {
		down, up = s.RateLimits()
	}
	return streamSettingsBody{
		MaxDownloadRate:    down,
		MaxUploadRate:      up,
		ReadaheadMB:        st.ReadaheadMB,
		StorageBackend:     st.StorageBackend,
		MaxConnsPerTorrent: st.MaxConnsPerTorrent,
		HalfOpenConns:      st.HalfOpenConns,
		PeersHighWater:     st.PeersHighWater,
		PieceHashers:       st.PieceHashers,
		MaxCacheGB:         st.MaxCacheGB,
		SeedTrackers:       st.SeedTrackers,
		HLSMediaRenditions: st.HLSMediaRenditions,
	}
}

func defaultStreamSettings() streamSettingsDefaults {
	return streamSettingsDefaults{
		ReadaheadMB:        defReadaheadMB,
		MaxConnsPerTorrent: defMaxConnsPerTorrent,
		HalfOpenConns:      defHalfOpenConns,
		PeersHighWater:     defPeersHighWater,
		PieceHashers:       defPieceHashers,
	}
}

// StreamGetSettings handles GET /api/stream/settings — current values + defaults.
func StreamGetSettings(cfg *config.Config, s *streamer.Streamer) gin.HandlerFunc {
	return func(c *gin.Context) {
		c.JSON(http.StatusOK, streamSettingsResponse{
			streamSettingsBody: currentStreamSettings(cfg, s),
			Defaults:           defaultStreamSettings(),
		})
	}
}

// validateStreamSettings returns an error message (empty = ok). Rejects
// negatives and a backend outside {file,mmap}.
func validateStreamSettings(b *streamSettingsBody) string {
	if b.MaxDownloadRate < 0 || b.MaxUploadRate < 0 {
		return "rate limits must be >= 0 (0 = unlimited)"
	}
	negInt := b.ReadaheadMB < 0 || b.MaxConnsPerTorrent < 0 || b.HalfOpenConns < 0 ||
		b.PeersHighWater < 0 || b.PieceHashers < 0 || b.MaxCacheGB < 0
	if negInt {
		return "numeric values must be >= 0"
	}
	if b.StorageBackend != config.StorageBackendFile && b.StorageBackend != config.StorageBackendMmap {
		return "invalid storageBackend (use \"file\" or \"mmap\")"
	}
	return ""
}

// cleanSeedTrackers trims entries and drops empties so a stray blank line in the
// UI textarea doesn't persist as an empty (match-everything) tracker substring.
func cleanSeedTrackers(in []string) []string {
	var out []string
	for _, t := range in {
		if s := strings.TrimSpace(t); s != "" {
			out = append(out, s)
		}
	}
	return out
}

// streamRestartRequired tells whether the change requires a process restart: fields
// read only at anacrolix client construction (storage/conns/peers/hashers) or the cache
// cap (s.cfg is copied at boot). Compares the request against the current config.
func streamRestartRequired(old config.StreamConfig, b *streamSettingsBody) bool {
	return b.StorageBackend != old.StorageBackend ||
		b.MaxConnsPerTorrent != old.MaxConnsPerTorrent ||
		b.HalfOpenConns != old.HalfOpenConns ||
		b.PeersHighWater != old.PeersHighWater ||
		b.PieceHashers != old.PieceHashers ||
		b.MaxCacheGB != old.MaxCacheGB
}

// StreamUpdateSettings handles PUT /api/stream/settings (AdminOnly). Validates,
// persists to config.yaml, applies live whatever it can (rate limits + readahead) and
// returns {restartRequired} for what only takes effect after a restart.
func StreamUpdateSettings(cfg *config.Config, configPath string, s *streamer.Streamer) gin.HandlerFunc {
	return func(c *gin.Context) {
		var b streamSettingsBody
		if err := c.ShouldBindJSON(&b); err != nil {
			httpshared.RespondErrorMessage(c, http.StatusBadRequest, "invalid request body")
			return
		}
		if msg := validateStreamSettings(&b); msg != "" {
			httpshared.RespondErrorMessage(c, http.StatusBadRequest, msg)
			return
		}

		restart := streamRestartRequired(cfg.Stream, &b)

		cfg.Stream.MaxDownloadRate = b.MaxDownloadRate
		cfg.Stream.MaxUploadRate = b.MaxUploadRate
		cfg.Stream.ReadaheadMB = b.ReadaheadMB
		cfg.Stream.StorageBackend = b.StorageBackend
		cfg.Stream.MaxConnsPerTorrent = b.MaxConnsPerTorrent
		cfg.Stream.HalfOpenConns = b.HalfOpenConns
		cfg.Stream.PeersHighWater = b.PeersHighWater
		cfg.Stream.PieceHashers = b.PieceHashers
		cfg.Stream.MaxCacheGB = b.MaxCacheGB
		cfg.Stream.SeedTrackers = cleanSeedTrackers(b.SeedTrackers)
		cfg.Stream.HLSMediaRenditions = b.HLSMediaRenditions

		if err := cfg.Save(configPath); err != nil {
			httpshared.RespondErrorMessage(c, http.StatusInternalServerError, "failed to save config: "+err.Error())
			return
		}

		// Applies live whatever does not require a restart.
		if s != nil {
			s.SetRateLimits(b.MaxDownloadRate, b.MaxUploadRate)
			s.SetStreamReadahead(b.ReadaheadMB)
			s.SetSeedTrackers(cfg.Stream.SeedTrackers)
		}

		c.JSON(http.StatusOK, gin.H{"message": "settings saved", "restartRequired": restart})
	}
}
