package handlers

import (
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/lgldsilva/jackui/internal/config"
	"github.com/lgldsilva/jackui/internal/streamer"
)

// Defaults do anacrolix (v1.61.0) — usados como placeholders na UI quando o
// campo está em 0 ("usar default da lib"). Mantidos aqui para a UI não precisar
// adivinhar. Espelham NewDefaultClientConfig + streamReadaheadDefault.
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
	// SeedTrackers: substrings de announce URLs cujos torrents continuam
	// seedando após o uso (ex.: "jackui"). Aplicado ao vivo, sem reinício.
	SeedTrackers []string `json:"seedTrackers"`
	// HLSMediaRenditions liga as renditions EXT-X-MEDIA (áudio/legenda) no master
	// HLS (Phase 2 M2b). Aplicado ao vivo (o handler lê no próximo play). ⚠ com ON,
	// o seletor de áudio in-app regride no hls.js (Chrome/Firefox) até o front
	// migrar pra hls.audioTrack — Safari nativo usa o menu próprio.
	HLSMediaRenditions bool `json:"hlsMediaRenditions"`
}

type streamSettingsResponse struct {
	streamSettingsBody
	Defaults streamSettingsDefaults `json:"defaults"`
}

func currentStreamSettings(cfg *config.Config, s *streamer.Streamer) streamSettingsBody {
	st := cfg.Stream
	down, up := st.MaxDownloadRate, st.MaxUploadRate
	// Rate limits ao vivo são a fonte da verdade (podem ter sido mudados via o
	// endpoint legado /stream/limits sem passar pela config).
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

// StreamGetSettings handles GET /api/stream/settings — valores atuais + defaults.
func StreamGetSettings(cfg *config.Config, s *streamer.Streamer) gin.HandlerFunc {
	return func(c *gin.Context) {
		c.JSON(http.StatusOK, streamSettingsResponse{
			streamSettingsBody: currentStreamSettings(cfg, s),
			Defaults:           defaultStreamSettings(),
		})
	}
}

// validateStreamSettings devolve uma mensagem de erro (vazia = ok). Rejeita
// negativos e backend fora de {file,mmap}.
func validateStreamSettings(b *streamSettingsBody) string {
	if b.MaxDownloadRate < 0 || b.MaxUploadRate < 0 {
		return "rate limits devem ser >= 0 (0 = ilimitado)"
	}
	negInt := b.ReadaheadMB < 0 || b.MaxConnsPerTorrent < 0 || b.HalfOpenConns < 0 ||
		b.PeersHighWater < 0 || b.PieceHashers < 0 || b.MaxCacheGB < 0
	if negInt {
		return "valores numéricos devem ser >= 0"
	}
	if b.StorageBackend != config.StorageBackendFile && b.StorageBackend != config.StorageBackendMmap {
		return "storageBackend inválido (use \"file\" ou \"mmap\")"
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

// streamRestartRequired diz se a mudança exige reiniciar o processo: campos lidos
// só na construção do client anacrolix (storage/conns/peers/hashers) ou o cache
// cap (s.cfg é copiado no boot). Compara o pedido com a config corrente.
func streamRestartRequired(old config.StreamConfig, b *streamSettingsBody) bool {
	return b.StorageBackend != old.StorageBackend ||
		b.MaxConnsPerTorrent != old.MaxConnsPerTorrent ||
		b.HalfOpenConns != old.HalfOpenConns ||
		b.PeersHighWater != old.PeersHighWater ||
		b.PieceHashers != old.PieceHashers ||
		b.MaxCacheGB != old.MaxCacheGB
}

// StreamUpdateSettings handles PUT /api/stream/settings (AdminOnly). Valida,
// persiste na config.yaml, aplica AO VIVO o que dá (rate limits + readahead) e
// devolve {restartRequired} para o que só vale após reiniciar.
func StreamUpdateSettings(cfg *config.Config, configPath string, s *streamer.Streamer) gin.HandlerFunc {
	return func(c *gin.Context) {
		var b streamSettingsBody
		if err := c.ShouldBindJSON(&b); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "invalid request body"})
			return
		}
		if msg := validateStreamSettings(&b); msg != "" {
			c.JSON(http.StatusBadRequest, gin.H{"error": msg})
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
			c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to save config: " + err.Error()})
			return
		}

		// Aplica ao vivo o que não exige reinício.
		if s != nil {
			s.SetRateLimits(b.MaxDownloadRate, b.MaxUploadRate)
			s.SetStreamReadahead(b.ReadaheadMB)
			s.SetSeedTrackers(cfg.Stream.SeedTrackers)
		}

		c.JSON(http.StatusOK, gin.H{"message": "settings saved", "restartRequired": restart})
	}
}
