package config

import (
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

type Config struct {
	Jackett struct {
		URL    string `yaml:"url"`
		APIKey string `yaml:"api_key"`
	} `yaml:"jackett"`
	DownloadClients []DownloadClient `yaml:"download_clients"`
	Port            int              `yaml:"port"`
	// DatabaseURL is the PostgreSQL DSN for the unified data store (the single
	// source of truth for all state). Env: JACKUI_DATABASE_URL (preferred) or
	// DATABASE_URL.
	DatabaseURL    string               `yaml:"database_url"`
	Stream         StreamConfig         `yaml:"stream"`
	Subtitles      SubtitlesConfig      `yaml:"subtitles"`
	Auth           AuthConfig           `yaml:"auth"`
	Notifications  NotificationsConfig  `yaml:"notifications"`
	TMDB           TMDBConfig           `yaml:"tmdb"`
	External       ExternalConfig       `yaml:"external"`
	AI             AIConfig             `yaml:"ai"`
	SMTP           SMTPConfig           `yaml:"smtp"`
	DownloadsQueue DownloadsQueueConfig `yaml:"downloads_queue"`
	// BaseURL is the public URL of the app (e.g. https://jackui.example.com),
	// used to build links in emails (reset/verify/invite). Falls back to the
	// request's Origin when empty.
	BaseURL string `yaml:"base_url"`
}

// SMTPConfig configures outbound email (password reset, email verification,
// invites). Empty Host disables email — the relevant flows then expose a
// copyable link to the admin instead of sending mail.
type SMTPConfig struct {
	Host     string `yaml:"host"`
	Port     int    `yaml:"port"`
	Username string `yaml:"username"`
	Password string `yaml:"password"`
	From     string `yaml:"from"` // From: address; defaults to Username when empty
}

// AIConfig declares an OpenAI-compatible LLM fallback chain used to turn a messy
// torrent name into a clean title before the TMDB lookup. Entirely optional:
// with no providers/chain (or enabled:false) the resolver falls back to TMDB's
// own regex title cleaning. Providers are keyed by name; the chain references
// them by `provider` and is walked in order until one returns a usable title.
type AIConfig struct {
	Enabled   bool                  `yaml:"enabled"`
	Providers map[string]AIProvider `yaml:"providers"`
	Chain     []AIChainSlot         `yaml:"chain"`
	// MaxCostPer1M caps which models the benchmark will TEST (and thus pay for),
	// in USD per 1M tokens (blended prompt+completion). 0 (default) = free models
	// only — paid models are never called, so the benchmark spends nothing. Raise
	// it to also test paid models up to that price; the composite score then ranks
	// by cost too (cheaper wins), so it's value-based, not a binary free/paid flag.
	MaxCostPer1M float64 `yaml:"max_cost_per_1m"`
	// ElectricityPricePerKWh and LocalPowerWatts price the ENERGY of LOCAL models —
	// they aren't truly free (the GPU draws power). When the price is > 0 the
	// benchmark estimates a local model's $/1M from its measured latency × tokens ×
	// power × tariff, so a slow/power-hungry local model ranks below a fast
	// cloud-free one. Price 0 (default) keeps local at cost 0. Watts defaults to 250.
	ElectricityPricePerKWh float64 `yaml:"electricity_price_per_kwh"`
	LocalPowerWatts        float64 `yaml:"local_power_watts"`
}

type AIProvider struct {
	BaseURL string `yaml:"base_url"` // OpenAI-compatible base, e.g. https://api.groq.com/openai/v1
	APIKey  string `yaml:"api_key"`  // bearer token; empty for keyless backends (ollama)
	// PreferredModels overrides the built-in pick order for this provider's default chain
	// slot (empty → the defaultProviderModels fallback). It only biases WHICH model this
	// provider offers pre-benchmark; the chain ORDER is always the benchmark's job.
	PreferredModels []string `yaml:"preferred_models,omitempty"`
	// FreeModels overrides the pinned free-tier id list for providers whose free tier
	// can't be discovered (Google — its /models has no pricing). Empty → the default.
	// Update this instead of the code when a provider ships/reprices a free model.
	FreeModels []string `yaml:"free_models,omitempty"`
	// RPM caps requests-per-minute to this provider so a burst benchmark doesn't trip a
	// free-tier rate limit and leave models "incomplete". 0 → the built-in default (see
	// defaultProviderModels; Google's free tier is ~30/min). Set high to disable throttling.
	RPM int `yaml:"rpm,omitempty"`
}

type AIChainSlot struct {
	ID       string `yaml:"id"`       // unique label (used by the benchmark + logs)
	Provider string `yaml:"provider"` // key into Providers
	Model    string `yaml:"model"`    // model id sent to /chat/completions
	// Disabled lets the benchmark (Fase 3) park a model without deleting it.
	Disabled bool `yaml:"disabled"`
}

// DownloadsQueueConfig tunes the background-download scheduler: how many run at
// once, when a no-seed download is bumped to the back of the queue, and the
// queue's anti-starvation aging. RotationEnabled gates the Phase-2 automatic
// source rotation (re-search Jackett when a source dries up).
type DownloadsQueueConfig struct {
	MaxActive         int  `yaml:"max_active"`          // GLOBAL ceiling: concurrent downloads across all users (streaming excluded); default 3
	PerUserMaxActive  int  `yaml:"per_user_max_active"` // per-user concurrent cap; 0 = no per-user limit (only the global ceiling applies); default 0
	StallThresholdMin int  `yaml:"stall_threshold_min"` // no-progress+no-seed minutes before a demote; default 30
	MaxStalls         int  `yaml:"max_stalls"`          // stalls before pausing the download; default 3 (0 = cycle forever)
	AgingStepMin      int  `yaml:"aging_step_min"`      // queue aging: minutes waited per +1 bonus; default 60
	AgingCap          int  `yaml:"aging_cap"`           // ceiling on the aging bonus; default 150
	RotationEnabled   bool `yaml:"rotation_enabled"`    // Phase 2: auto source rotation via Jackett; default false
	// AutoPromoteArr: when on, a completed download created via the Transmission
	// RPC (Sonarr/Radarr) is written straight into SharedDir/<category>/ — the
	// same "completed downloads" tree Transmission uses — so the *arr import it as
	// expected. UI downloads are unaffected. Needs Stream.SharedDir set. Default off.
	AutoPromoteArr bool `yaml:"auto_promote_arr"`
}

// ExternalConfig declares filesystem mounts the user wants browsable from
// the web UI — typical setups: bind-mount an external HDD or NAS share so
// the Local Files page lists what's already on disk.
type ExternalConfig struct {
	Mounts []ExternalMount `yaml:"mounts"`
	// MaxUploadMB caps a single local upload (anti disk-fill DoS). 0 = default.
	MaxUploadMB int `yaml:"max_upload_mb"`
	// LocalReadaheadMB is the read-ahead buffer used when serving/transcoding a
	// file from a local mount. On rclone/Drive mounts a larger aligned read-ahead
	// turns many tiny FUSE Range reads into a few big fetches, smoothing playback.
	// 0 → 16 MiB default. Distinct from Stream.ReadaheadMB (torrent-only).
	LocalReadaheadMB int `yaml:"local_readahead_mb"`
	// LocalCacheGB caps the dedicated cache that pre-fetches whole files from
	// slow mounts (rclone/Drive) to local disk for instant, seekable, EIO-proof
	// playback. LRU eviction keeps it under the cap. 0 → 50 GiB default.
	LocalCacheGB int `yaml:"local_cache_gb"`
}

type ExternalMount struct {
	Name         string   `yaml:"name" json:"name"`                  // Display name shown in the UI ("HD Externo", "NAS")
	Path         string   `yaml:"path" json:"path"`                  // Absolute path inside the container
	AllowedUsers []string `yaml:"allowed_users" json:"allowedUsers"` // Empty = visible to all; otherwise only these usernames
	UserSubpath  bool     `yaml:"user_subpath" json:"userSubpath"`   // When true, each user sees/writes only their own subdir
}

type NotificationsConfig struct {
	NtfyBaseURL       string `yaml:"ntfy_base_url"`      // default https://ntfy.sh
	NtfyDefaultTopic  string `yaml:"ntfy_default_topic"` // used when a watchlist has no override
	NtfyToken         string `yaml:"ntfy_token"`         // access token for protected/self-hosted topics (sent as Authorization: Bearer)
	WatchlistInterval int    `yaml:"watchlist_minutes"`  // poll interval in minutes (default 15)
}

type TMDBConfig struct {
	APIKey     string `yaml:"api_key"`      // empty disables enrichment (no posters)
	OMDbAPIKey string `yaml:"omdb_api_key"` // empty disables real IMDb ratings (falls back to TMDB vote)
}

type AuthConfig struct {
	Enabled       bool   `yaml:"enabled"`        // false = legacy no-auth mode (everything public)
	JWTSecret     string `yaml:"jwt_secret"`     // HS256 secret; REQUIRED (>=32 bytes) when auth enabled — boot fails otherwise
	AdminUsername string `yaml:"admin_username"` // bootstrap admin login
	AdminPassword string `yaml:"admin_password"` // bootstrap admin password (only used on first run)
	DBPath        string `yaml:"db_path"`        // auth DB (defaults to /data/auth.db)
}

type BandwidthSchedule struct {
	TimeRange       string `yaml:"time_range"`        // e.g. "08:00-18:00"
	MaxDownloadRate int64  `yaml:"max_download_rate"` // bytes/sec
	MaxUploadRate   int64  `yaml:"max_upload_rate"`   // bytes/sec
}

type StreamConfig struct {
	DataDir         string `yaml:"data_dir"`         // where torrent pieces are stored
	DownloadDir     string `yaml:"download_dir"`     // where completed downloads are moved (empty = stay in cache)
	SharedDir       string `yaml:"shared_dir"`       // shared library destination for "Promote" (empty = feature disabled)
	IdleMinutes     int    `yaml:"idle_minutes"`     // drop torrent after N min idle (files stay)
	MetadataSeconds int    `yaml:"metadata_seconds"` // metadata fetch timeout
	MaxCacheGB      int    `yaml:"max_cache_gb"`     // total cache size cap; 0 = unlimited
	// MaxConcurrentTransfers caps simultaneous file move/copy operations (post-
	// download move, Local-tab move); the rest queue FIFO. 0 = default (3). Higher
	// helps cloud/rclone destinations; lower (1-2) is better for a single HDD.
	MaxConcurrentTransfers int `yaml:"max_concurrent_transfers"`
	// TransferConcurrencyMode controla como as cópias de promote/move concorrem:
	//   "" / "auto" → detecta o disco DESTINO: serializa em HDD (evita seek
	//                 thrashing), paraleliza em SSD/NVMe. (default, recomendado)
	//   "serial"    → sempre uma cópia por vez (qualquer disco).
	//   "parallel"  → sempre em paralelo até MaxConcurrentTransfers (ignora a
	//                 detecção de HDD; útil p/ RAID/NVMe-cache onde seek não dói).
	// Lido AO VIVO a cada promote (UI/yaml alteram sem reiniciar).
	TransferConcurrencyMode string              `yaml:"transfer_concurrency_mode"`
	PromoteDirs             []PromoteDir        `yaml:"promote_dirs"` // additional promote destinations (name + path)
	BandwidthSchedules      []BandwidthSchedule `yaml:"bandwidth_schedules"`

	// ── Performance / hardware tuning (0/"" = usar default; aplicado no streamer) ──
	// Banda: caps de peer em bytes/seg; 0 = ilimitado. Aplicados AO VIVO via
	// Streamer.SetRateLimits (não exigem reinício).
	MaxDownloadRate int64 `yaml:"max_download_rate"`
	MaxUploadRate   int64 `yaml:"max_upload_rate"`
	// ReadaheadMB é o buffer de leitura à frente por sessão de streaming. 0 → 32.
	// Mais readahead = playback mais suave em rede/disco lento, porém mais RAM por
	// stream simultâneo. Aplicado ao vivo (vale no próximo play).
	ReadaheadMB int `yaml:"readahead_mb"`
	// StorageBackend escolhe como os pieces são abertos no disco: "file" (padrão,
	// grava direto) ou "mmap" (mapeia em memória via page cache; random-access/seek
	// mais rápido). Mudança exige REINÍCIO (o anacrolix lê isso na construção).
	StorageBackend string `yaml:"storage_backend"`
	// Tuning de peers/CPU — todos exigem REINÍCIO. 0 = default da lib anacrolix
	// (conns=50, half-open=25, peersHighWater=500, pieceHashers=2).
	MaxConnsPerTorrent int `yaml:"max_conns_per_torrent"`
	HalfOpenConns      int `yaml:"half_open_conns"`
	PeersHighWater     int `yaml:"peers_high_water"`
	PieceHashers       int `yaml:"piece_hashers"`
	// HLSVODMode controla o caminho de VOD finito (seekbar) no HLS transcodado:
	// "all" (padrão: VOD para todos, inclusive HLS nativo do Safari), "hlsjs"
	// (VOD apenas para clientes não-Safari — rollback se o Safari regredir),
	// "off" (só EVENT/live). Permite ajustar sem recompilar; rollback
	// instantâneo voltando para "hlsjs" ou "off".
	// Env: JACKUI_HLS_VOD_MODE. Aplicado ao vivo (vale na próxima sessão HLS).
	HLSVODMode string `yaml:"hls_vod_mode"`
	// SeedTrackers lista substrings/hostnames de trackers cujos torrents devem
	// CONTINUAR seedando após o uso (não são dropados pelo idle reaper nem pelo
	// fim do stream), em vez do comportamento padrão de dropar. Casado
	// case-insensitive contra as announce URLs do torrent (ex.: "amigos-share").
	// Vazio = ninguém é mantido (comportamento atual). Aplicado ao vivo via
	// Streamer.SetSeedTrackers. Env: JACKUI_SEED_TRACKERS (CSV).
	SeedTrackers []string `yaml:"seed_trackers"`
}

// StorageBackendFile/Mmap são os valores válidos de StreamConfig.StorageBackend.
const (
	StorageBackendFile = "file"
	StorageBackendMmap = "mmap"
)

type PromoteDir struct {
	Name string `yaml:"name"`
	Path string `yaml:"path"`
}

type SubtitlesConfig struct {
	OpenSubtitlesAPIKey   string `yaml:"opensubtitles_api_key"`
	OpenSubtitlesUsername string `yaml:"opensubtitles_username"`
	OpenSubtitlesPassword string `yaml:"opensubtitles_password"`
	CacheDir              string `yaml:"cache_dir"`
}

type DownloadClient struct {
	ID       string `yaml:"id"`
	Name     string `yaml:"name"`
	Type     string `yaml:"type"` // "qbittorrent" or "transmission"
	URL      string `yaml:"url"`
	Username string `yaml:"username"`
	Password string `yaml:"password"`
	Default  bool   `yaml:"default"`
}

func Load(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			cfg := defaultConfig()
			if saveErr := cfg.Save(path); saveErr != nil {
				return nil, fmt.Errorf("failed to create default config: %w", saveErr)
			}
			return cfg, nil
		}
		return nil, fmt.Errorf("failed to read config: %w", err)
	}

	var cfg Config
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("failed to parse config: %w", err)
	}

	if cfg.Port == 0 {
		cfg.Port = 8989
	}

	applyEnvOverrides(&cfg)

	return &cfg, nil
}

func (c *Config) Save(path string) error {
	data, err := yaml.Marshal(c)
	if err != nil {
		return fmt.Errorf("failed to marshal config: %w", err)
	}

	if err := os.WriteFile(path, data, 0644); err != nil {
		return fmt.Errorf("failed to write config: %w", err)
	}

	return nil
}

// applyEnvOverrides sobrescreve valores do YAML com variáveis de ambiente quando definidas.
// JACKUI_PORT, JACKETT_URL, JACKETT_API_KEY são os mais comuns em Docker.
func applyEnvOverrides(cfg *Config) {
	applyDatabaseEnv(cfg)
	applyJackettEnv(cfg)
	applyStreamEnv(cfg)
	applyAuthEnv(cfg)
	applyNotificationsEnv(cfg)
	applyTMDBEnv(cfg)
	applyExternalMountsEnv(cfg)
	applySMTPEnv(cfg)
	applyAIEnv(cfg)
	applyDownloadsQueueEnv(cfg)

	applyEnvPositiveInt(&cfg.External.MaxUploadMB, "JACKUI_MAX_UPLOAD_MB")
	applyEnvPositiveInt(&cfg.External.LocalReadaheadMB, "JACKUI_LOCAL_READAHEAD_MB")
	applyEnvPositiveInt(&cfg.External.LocalCacheGB, "JACKUI_LOCAL_CACHE_GB")
	if v := os.Getenv("JACKUI_HLS_VOD_MODE"); v != "" {
		cfg.Stream.HLSVODMode = v
	}
	if cfg.Stream.HLSVODMode == "" {
		// Default VOD for ALL clients, Safari/iOS native HLS included. The #61
		// Safari stall root cause (the MPEG-TS muxer's ~1.4s initial_offset)
		// was fixed at the muxer (-muxdelay/-muxpreload 0; guarded by
		// TestEncodeSpecZeroesPTSBothModes), and the native path shares the
		// same session/playlist infra the stable hls.js path already uses.
		// "hlsjs" is the rollback if Safari regresses; "off" disables VOD.
		// Unknown-duration sources still fall back to EVENT (see shouldVOD).
		cfg.Stream.HLSVODMode = "all"
	}
	if cfg.External.MaxUploadMB <= 0 {
		cfg.External.MaxUploadMB = 65536 // 64 GiB
	}
	if cfg.Stream.IdleMinutes == 0 {
		cfg.Stream.IdleMinutes = 30
	}
	if cfg.Stream.MetadataSeconds == 0 {
		cfg.Stream.MetadataSeconds = 60
	}
}

// applyDatabaseEnv resolves the PostgreSQL DSN from the environment.
// JACKUI_DATABASE_URL takes precedence over the conventional DATABASE_URL; when
// neither is set but the JACKUI_PG_* parts are, a DSN is assembled from them.
func applyDatabaseEnv(cfg *Config) {
	if v := os.Getenv("JACKUI_DATABASE_URL"); v != "" {
		cfg.DatabaseURL = v
		return
	}
	if v := os.Getenv("DATABASE_URL"); v != "" {
		cfg.DatabaseURL = v
		return
	}
	host := os.Getenv("JACKUI_PG_HOST")
	if host == "" {
		return
	}
	port := os.Getenv("JACKUI_PG_PORT")
	if port == "" {
		port = "5432"
	}
	user := os.Getenv("JACKUI_PG_USER")
	if user == "" {
		user = "jackui"
	}
	dbname := os.Getenv("JACKUI_PG_DB")
	if dbname == "" {
		dbname = "jackui"
	}
	sslmode := os.Getenv("JACKUI_PG_SSLMODE")
	if sslmode == "" {
		sslmode = "disable"
	}
	u := url.URL{
		Scheme:   "postgres",
		User:     url.UserPassword(user, os.Getenv("JACKUI_PG_PASSWORD")),
		Host:     net.JoinHostPort(host, port),
		Path:     "/" + dbname,
		RawQuery: "sslmode=" + sslmode,
	}
	cfg.DatabaseURL = u.String()
}

// applyDownloadsQueueEnv reads the queue-scheduler knobs from the environment
// and applies sane defaults for any unset value (so the worker always sees a
// usable config even with no YAML/env at all).
func applyDownloadsQueueEnv(cfg *Config) {
	applyEnvInt(&cfg.DownloadsQueue.MaxActive, "JACKUI_DL_MAX_ACTIVE")
	applyEnvInt(&cfg.DownloadsQueue.PerUserMaxActive, "JACKUI_DL_PER_USER_MAX")
	applyEnvInt(&cfg.DownloadsQueue.StallThresholdMin, "JACKUI_DL_STALL_MIN")
	applyEnvInt(&cfg.DownloadsQueue.MaxStalls, "JACKUI_DL_MAX_STALLS")
	applyEnvInt(&cfg.DownloadsQueue.AgingStepMin, "JACKUI_DL_AGING_STEP_MIN")
	applyEnvInt(&cfg.DownloadsQueue.AgingCap, "JACKUI_DL_AGING_CAP")
	if v := os.Getenv("JACKUI_DL_ROTATION"); v == "1" || strings.EqualFold(v, "true") {
		cfg.DownloadsQueue.RotationEnabled = true
	}
	if v := os.Getenv("JACKUI_DL_AUTO_PROMOTE_ARR"); v == "1" || strings.EqualFold(v, "true") {
		cfg.DownloadsQueue.AutoPromoteArr = true
	}
	if cfg.DownloadsQueue.MaxActive <= 0 {
		cfg.DownloadsQueue.MaxActive = 3
	}
	if cfg.DownloadsQueue.PerUserMaxActive < 0 {
		cfg.DownloadsQueue.PerUserMaxActive = 0 // 0 = no per-user limit
	}
	if cfg.DownloadsQueue.StallThresholdMin <= 0 {
		cfg.DownloadsQueue.StallThresholdMin = 30
	}
	if cfg.DownloadsQueue.MaxStalls <= 0 {
		cfg.DownloadsQueue.MaxStalls = 3 // user chose "pause after N stalls"
	}
	if cfg.DownloadsQueue.AgingStepMin <= 0 {
		cfg.DownloadsQueue.AgingStepMin = 60
	}
	if cfg.DownloadsQueue.AgingCap <= 0 {
		cfg.DownloadsQueue.AgingCap = 150
	}
}

// ActiveEnvOverrides returns which env vars are set and override the YAML config.
// Used by the frontend to show "managed by environment" badges.
// maskedEnvKeys are env overrides that hold secrets: their presence is reported
// to the admin UI (so it can show "set via env") but the value is masked, never
// echoed back in GET /api/config.
var maskedEnvKeys = map[string]bool{
	"JACKETT_API_KEY": true, "JACKUI_ADMIN_PASSWORD": true, "JACKUI_JWT_SECRET": true,
	"JACKUI_SMTP_PASS": true, "GROQ_API_KEY": true, "OPENROUTER_API_KEY": true,
	"OPENCODE_API_KEY": true, "GEMINI_API_KEY": true,
	"TMDB_API_KEY": true, "OMDB_API_KEY": true, "JACKUI_NTFY_TOKEN": true,
	// DSNs carry the DB password.
	"JACKUI_DATABASE_URL": true, "DATABASE_URL": true, "JACKUI_PG_PASSWORD": true,
}

func ActiveEnvOverrides() map[string]string {
	keys := []string{
		"JACKETT_URL", "JACKETT_API_KEY",
		"JACKUI_DATABASE_URL", "DATABASE_URL",
		"JACKUI_PG_HOST", "JACKUI_PG_PORT", "JACKUI_PG_USER", "JACKUI_PG_PASSWORD", "JACKUI_PG_DB", "JACKUI_PG_SSLMODE",
		"JACKUI_PORT",
		"JACKUI_STREAM_DIR", "JACKUI_DOWNLOAD_DIR",
		"JACKUI_SHARED_DIR",
		"JACKUI_STREAM_MAX_GB",
		"JACKUI_AUTH_ENABLED", "JACKUI_ADMIN_PASSWORD", "JACKUI_ADMIN_USERNAME", "JACKUI_JWT_SECRET",
		"JACKUI_NTFY_TOPIC", "JACKUI_NTFY_URL", "JACKUI_NTFY_TOKEN",
		"TMDB_API_KEY", "OMDB_API_KEY",
		"JACKUI_SMTP_HOST", "JACKUI_SMTP_PORT", "JACKUI_SMTP_USER", "JACKUI_SMTP_PASS", "JACKUI_SMTP_FROM",
		"JACKUI_BASE_URL",
		"JACKUI_EXTERNAL_MOUNTS",
		"JACKUI_AI_ENABLED", "GROQ_API_KEY", "OPENROUTER_API_KEY", "OPENCODE_API_KEY", "GEMINI_API_KEY", "OLLAMA_BASE_URL", "JACKUI_AI_MAX_COST_PER_1M", "JACKUI_AI_KWH_PRICE", "JACKUI_AI_LOCAL_WATTS",
		"JACKUI_MAX_UPLOAD_MB", "JACKUI_LOCAL_READAHEAD_MB", "JACKUI_LOCAL_CACHE_GB", "JACKUI_HLS_VOD_MODE", "JACKUI_MAX_GPU_TRANSCODES",
		"JACKUI_DL_MAX_ACTIVE", "JACKUI_DL_PER_USER_MAX", "JACKUI_DL_STALL_MIN", "JACKUI_DL_MAX_STALLS",
		"JACKUI_DL_AGING_STEP_MIN", "JACKUI_DL_AGING_CAP", "JACKUI_DL_ROTATION",
	}
	out := make(map[string]string, len(keys))
	for _, k := range keys {
		if v, ok := os.LookupEnv(k); ok {
			if maskedEnvKeys[k] {
				v = "••••••"
			}
			out[k] = v
		}
	}
	return out
}

func applyJackettEnv(cfg *Config) {
	if v := os.Getenv("JACKETT_URL"); v != "" {
		cfg.Jackett.URL = v
	}
	if v := os.Getenv("JACKETT_API_KEY"); v != "" {
		cfg.Jackett.APIKey = v
	}
}

func applyStreamEnv(cfg *Config) {
	if v := os.Getenv("JACKUI_PORT"); v != "" {
		if p, err := strconv.Atoi(v); err == nil && p > 0 {
			cfg.Port = p
		}
	}
	if v := os.Getenv("JACKUI_STREAM_DIR"); v != "" {
		cfg.Stream.DataDir = v
	}
	if v := os.Getenv("JACKUI_DOWNLOAD_DIR"); v != "" {
		cfg.Stream.DownloadDir = v
	}
	if v := os.Getenv("JACKUI_SHARED_DIR"); v != "" {
		cfg.Stream.SharedDir = v
	}
	if v := os.Getenv("JACKUI_STREAM_MAX_GB"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n >= 0 {
			cfg.Stream.MaxCacheGB = n
		}
	}
	applyStreamPerfEnv(cfg)
}

// applyStreamPerfEnv lê os knobs de performance/hardware do ambiente e sanitiza
// o backend de storage. Rates vêm em MB/s no env (mais legível) e são convertidos
// para bytes/seg na config.
func applyStreamPerfEnv(cfg *Config) {
	applyEnvInt64MB(&cfg.Stream.MaxDownloadRate, "JACKUI_STREAM_DOWN_MBPS")
	applyEnvInt64MB(&cfg.Stream.MaxUploadRate, "JACKUI_STREAM_UP_MBPS")
	applyEnvInt(&cfg.Stream.ReadaheadMB, "JACKUI_READAHEAD_MB")
	if v := os.Getenv("JACKUI_STORAGE_BACKEND"); v != "" {
		cfg.Stream.StorageBackend = v
	}
	applyEnvInt(&cfg.Stream.MaxConnsPerTorrent, "JACKUI_MAX_CONNS")
	applyEnvInt(&cfg.Stream.HalfOpenConns, "JACKUI_HALF_OPEN")
	applyEnvInt(&cfg.Stream.PeersHighWater, "JACKUI_PEERS_HIGH")
	applyEnvInt(&cfg.Stream.PieceHashers, "JACKUI_PIECE_HASHERS")
	if v := os.Getenv("JACKUI_SEED_TRACKERS"); v != "" {
		cfg.Stream.SeedTrackers = splitCSV(v)
	}

	// Sanitiza: qualquer valor fora de {file,mmap} (incl. vazio) vira "file".
	if cfg.Stream.StorageBackend != StorageBackendMmap {
		cfg.Stream.StorageBackend = StorageBackendFile
	}
}

func applyEnvInt(target *int, name string) {
	if n, ok := envInt(name); ok && n >= 0 {
		*target = n
	}
}

// applyEnvPositiveInt sets *target from a strictly-positive env int. A 0,
// negative or non-numeric value is ignored (keeps the current value/default).
func applyEnvPositiveInt(target *int, name string) {
	if n, ok := envInt(name); ok && n > 0 {
		*target = n
	}
}

func applyEnvInt64MB(target *int64, name string) {
	if n, ok := envInt(name); ok && n >= 0 {
		*target = int64(n) * 1024 * 1024
	}
}

// splitCSV divide uma lista separada por vírgula, ignorando espaços e entradas
// vazias. Devolve nil quando não sobra nenhum item.
func splitCSV(v string) []string {
	var out []string
	for _, part := range strings.Split(v, ",") {
		if s := strings.TrimSpace(part); s != "" {
			out = append(out, s)
		}
	}
	return out
}

// envInt lê uma env var inteira. Retorna (0,false) se ausente ou inválida.
func envInt(name string) (int, bool) {
	v := os.Getenv(name)
	if v == "" {
		return 0, false
	}
	n, err := strconv.Atoi(v)
	if err != nil {
		return 0, false
	}
	return n, true
}

func applyAuthEnv(cfg *Config) {
	if v := os.Getenv("JACKUI_AUTH_ENABLED"); v == "1" || v == "true" {
		cfg.Auth.Enabled = true
	}
	if v := os.Getenv("JACKUI_ADMIN_PASSWORD"); v != "" {
		cfg.Auth.AdminPassword = v
	}
	if v := os.Getenv("JACKUI_ADMIN_USERNAME"); v != "" {
		cfg.Auth.AdminUsername = v
	}
	if v := os.Getenv("JACKUI_JWT_SECRET"); v != "" {
		cfg.Auth.JWTSecret = v
	}
}

func applyNotificationsEnv(cfg *Config) {
	if v := os.Getenv("JACKUI_NTFY_TOPIC"); v != "" {
		cfg.Notifications.NtfyDefaultTopic = v
	}
	if v := os.Getenv("JACKUI_NTFY_URL"); v != "" {
		cfg.Notifications.NtfyBaseURL = v
	}
	if v := os.Getenv("JACKUI_NTFY_TOKEN"); v != "" {
		cfg.Notifications.NtfyToken = v
	}
}

func applyTMDBEnv(cfg *Config) {
	if v := os.Getenv("TMDB_API_KEY"); v != "" {
		cfg.TMDB.APIKey = v
	}
	if v := os.Getenv("OMDB_API_KEY"); v != "" {
		cfg.TMDB.OMDbAPIKey = v
	}
}

// applyExternalMountsEnv merges JACKUI_EXTERNAL_MOUNTS specs into the YAML
// mounts. Dedupe keys are NORMALIZED (path: Clean+TrimSpace; name: lowercase
// trimmed) so the env can never spawn a twin of an admin-saved mount — e.g.
// env "Downloads:/downloads/" vs saved "/downloads" with allowed_users. A twin
// would show up unrestricted to everyone and break the next PUT /api/mounts
// with "nome de mount duplicado". The saved (possibly restricted) entry wins.
func applyExternalMountsEnv(cfg *Config) {
	v := os.Getenv("JACKUI_EXTERNAL_MOUNTS")
	if v == "" {
		return
	}
	seenPath, seenName := mountDedupeKeys(cfg.External.Mounts)
	for _, spec := range strings.Split(v, ",") {
		m, ok := parseMountSpec(spec)
		if !ok {
			continue
		}
		pathKey, nameKey := mountPathKey(m.Path), mountNameKey(m.Name)
		if seenPath[pathKey] || seenName[nameKey] {
			continue
		}
		cfg.External.Mounts = append(cfg.External.Mounts, m)
		seenPath[pathKey] = true
		seenName[nameKey] = true
	}
}

// mountPathKey normalizes a mount path for dedupe ("/downloads/" == "/downloads").
func mountPathKey(p string) string {
	return filepath.Clean(strings.TrimSpace(p))
}

// mountNameKey normalizes a mount name for case-insensitive dedupe.
func mountNameKey(n string) string {
	return strings.ToLower(strings.TrimSpace(n))
}

// mountDedupeKeys indexes existing mounts by normalized path AND name.
func mountDedupeKeys(mounts []ExternalMount) (paths, names map[string]bool) {
	paths, names = map[string]bool{}, map[string]bool{}
	for _, m := range mounts {
		paths[mountPathKey(m.Path)] = true
		names[mountNameKey(m.Name)] = true
	}
	return paths, names
}

// parseMountSpec parses one "Name:/path[:usersubpath]" item from
// JACKUI_EXTERNAL_MOUNTS. The optional trailing ":usersubpath" flag turns the
// mount into per-user private subdirs (mount/{username}/...). Backward
// compatible: specs without the flag keep the shared-root behavior.
func parseMountSpec(spec string) (ExternalMount, bool) {
	spec = strings.TrimSpace(spec)
	i := strings.Index(spec, ":")
	if i <= 0 || i == len(spec)-1 {
		return ExternalMount{}, false
	}
	name, rest := strings.TrimSpace(spec[:i]), strings.TrimSpace(spec[i+1:])
	userSubpath := false
	if j := strings.LastIndex(rest, ":"); j >= 0 {
		if flag := strings.ToLower(strings.TrimSpace(rest[j+1:])); flag == "usersubpath" || flag == "subpath" {
			userSubpath = true
			rest = strings.TrimSpace(rest[:j])
		}
	}
	if name == "" || rest == "" {
		return ExternalMount{}, false
	}
	return ExternalMount{Name: name, Path: rest, UserSubpath: userSubpath}, true
}

// CheckWritable reports whether path can be opened for writing — used at boot
// to warn early that Settings/Mounts changes won't persist (typical cause: the
// host file is owned by root while the container runs as uid 1000).
func CheckWritable(path string) error {
	f, err := os.OpenFile(path, os.O_WRONLY, 0)
	if err != nil {
		return err
	}
	return f.Close()
}

func applySMTPEnv(cfg *Config) {
	if v := os.Getenv("JACKUI_SMTP_HOST"); v != "" {
		cfg.SMTP.Host = v
	}
	if v := os.Getenv("JACKUI_SMTP_PORT"); v != "" {
		if p, err := strconv.Atoi(v); err == nil {
			cfg.SMTP.Port = p
		}
	}
	if v := os.Getenv("JACKUI_SMTP_USER"); v != "" {
		cfg.SMTP.Username = v
	}
	if v := os.Getenv("JACKUI_SMTP_PASS"); v != "" {
		cfg.SMTP.Password = v
	}
	if v := os.Getenv("JACKUI_SMTP_FROM"); v != "" {
		cfg.SMTP.From = v
	}
	if v := os.Getenv("JACKUI_BASE_URL"); v != "" {
		cfg.BaseURL = v
	}
}

// applyAIEnv fills provider credentials from the standard env vars and, when no
// explicit chain is configured, auto-seeds a sensible default from whatever
// providers have credentials. This lets the same env vars the box already
// exports (GROQ_API_KEY / OPENROUTER_API_KEY / OLLAMA_BASE_URL) light up the AI
// title-identification without hand-editing config.yaml.
//
// Model discovery is dynamic: buildDefaultChain queries each provider's
// /v1/models endpoint and picks the best free/fast model available, so no
// model names are hardcoded and it adapts as providers add/rename models.
func applyAIEnv(cfg *Config) {
	if cfg.AI.Providers == nil {
		cfg.AI.Providers = map[string]AIProvider{}
	}
	applyAIProviderEnv(cfg, "GROQ_API_KEY", "groq", "https://api.groq.com/openai/v1")
	applyAIProviderEnv(cfg, "OPENROUTER_API_KEY", "openrouter", "https://openrouter.ai/api/v1")
	applyAIProviderEnv(cfg, "OPENCODE_API_KEY", "opencode", "https://opencode.ai/zen/v1")
	// Google Gemini via its OpenAI-compatible endpoint. The free tier (flash / flash-lite:
	// 1500 req/day, 1M TPM) is more than enough for title identification, and the model is
	// strong at structured JSON extraction — so it goes at the TOP of the default chain.
	applyAIProviderEnv(cfg, "GEMINI_API_KEY", "google", "https://generativelanguage.googleapis.com/v1beta/openai/")
	applyOllamaEnv(cfg)

	if v := os.Getenv("JACKUI_AI_MAX_COST_PER_1M"); v != "" {
		if f, err := strconv.ParseFloat(v, 64); err == nil && f >= 0 {
			cfg.AI.MaxCostPer1M = f
		}
	}
	if v := os.Getenv("JACKUI_AI_KWH_PRICE"); v != "" {
		if f, err := strconv.ParseFloat(v, 64); err == nil && f >= 0 {
			cfg.AI.ElectricityPricePerKWh = f
		}
	}
	if v := os.Getenv("JACKUI_AI_LOCAL_WATTS"); v != "" {
		if f, err := strconv.ParseFloat(v, 64); err == nil && f > 0 {
			cfg.AI.LocalPowerWatts = f
		}
	}

	if v := os.Getenv("JACKUI_AI_ENABLED"); v == "0" || v == "false" {
		cfg.AI.Enabled = false
		return
	}
	autoSeedChain(cfg)
}

func applyAIProviderEnv(cfg *Config, envName, providerName, defaultBaseURL string) {
	v := os.Getenv(envName)
	if v == "" {
		return
	}
	p := cfg.AI.Providers[providerName]
	if p.BaseURL == "" {
		p.BaseURL = defaultBaseURL
	}
	p.APIKey = v
	cfg.AI.Providers[providerName] = p
}

func applyOllamaEnv(cfg *Config) {
	v := os.Getenv("OLLAMA_BASE_URL")
	if v == "" {
		return
	}
	if !strings.HasSuffix(v, "/v1") {
		v = strings.TrimRight(v, "/") + "/v1"
	}
	p := cfg.AI.Providers["ollama"]
	if p.BaseURL == "" {
		p.BaseURL = v
	}
	cfg.AI.Providers["ollama"] = p
}

func autoSeedChain(cfg *Config) {
	if len(cfg.AI.Chain) == 0 {
		chain := cfg.buildDefaultChain()
		if len(chain) > 0 {
			cfg.AI.Chain = chain
			cfg.AI.Enabled = true
		}
	} else if !cfg.AI.Enabled {
		cfg.AI.Enabled = true
	}
}

func (cfg *Config) buildDefaultChain() []AIChainSlot {
	// This order is a BOOTSTRAP only, NOT a quality ranking — it just lists whatever has
	// credentials. No provider is privileged by position: the benchmark scores every model
	// and AdoptBenchmark reorders the live chain best-first (by composite), so the SCORE
	// decides who runs first, never a hardcoded pick here. (Measured: Groq's llama-3.1-8b
	// beats Gemini on this task by speed at equal accuracy — but that comes out of the
	// benchmark, it isn't wired in.)
	var chain []AIChainSlot
	chain = cfg.appendOpenCodeSlot(chain)
	chain = cfg.appendGroqSlot(chain)
	chain = cfg.appendOpenRouterSlot(chain)
	chain = cfg.appendGoogleSlot(chain)
	chain = cfg.appendOllamaSlots(chain)
	return chain
}

// appendGoogleSlot adds a FREE Gemini model as an available chain slot (its position is
// bootstrap only — the benchmark ranks it; see buildDefaultChain). The pick is dynamic
// within the free set — pickFreeGoogleModel intersects what /models actually serves with
// freeGoogleModels — so it adapts to the account's catalog and NEVER selects a paid model
// (Google's free tier isn't discoverable; see freeGoogleModels). Adds nothing when
// discovery fails or no free model is served (same graceful no-op as the other providers);
// the chain walk + breaker then fall through to the next provider.
func (cfg *Config) appendGoogleSlot(chain []AIChainSlot) []AIChainSlot {
	p, ok := cfg.AI.Providers["google"]
	if !ok || p.APIKey == "" {
		return chain
	}
	if m := pickFreeGoogleModel(fetchModels(p.BaseURL, p.APIKey), cfg.freeModels("google")); m != "" {
		chain = append(chain, AIChainSlot{ID: "google-" + m, Provider: "google", Model: m})
	}
	return chain
}

func (cfg *Config) appendOpenCodeSlot(chain []AIChainSlot) []AIChainSlot {
	p, ok := cfg.AI.Providers["opencode"]
	if !ok || p.APIKey == "" {
		return chain
	}
	models := fetchModels(p.BaseURL, p.APIKey)
	// Prefer a free Zen model; fall through to matchFreeModel (any "-free"). Never
	// default to a paid frontier model (e.g. "big-pickle") — Zen bills credits per
	// call and the default slot is also what the benchmark runs.
	m := pickModel(models, cfg.preferredModels("opencode")...)
	if m != "" {
		chain = append(chain, AIChainSlot{ID: "zen-" + m, Provider: "opencode", Model: m})
	}
	return chain
}

func (cfg *Config) appendGroqSlot(chain []AIChainSlot) []AIChainSlot {
	p, ok := cfg.AI.Providers["groq"]
	if !ok || p.APIKey == "" {
		return chain
	}
	models := fetchModels(p.BaseURL, p.APIKey)
	m := pickModel(models, cfg.preferredModels("groq")...)
	if m != "" {
		chain = append(chain, AIChainSlot{ID: "groq-" + m, Provider: "groq", Model: m})
	}
	return chain
}

func (cfg *Config) appendOpenRouterSlot(chain []AIChainSlot) []AIChainSlot {
	p, ok := cfg.AI.Providers["openrouter"]
	if !ok || p.APIKey == "" {
		return chain
	}
	models := fetchModels(p.BaseURL, p.APIKey)
	m := pickModel(models, cfg.preferredModels("openrouter")...)
	if m != "" {
		chain = append(chain, AIChainSlot{ID: "or-" + m, Provider: "openrouter", Model: m})
	}
	return chain
}

func (cfg *Config) appendOllamaSlots(chain []AIChainSlot) []AIChainSlot {
	p, ok := cfg.AI.Providers["ollama"]
	if !ok || p.BaseURL == "" {
		return chain
	}
	models := fetchModels(p.BaseURL, "")
	// Default prefers models good at instruction-following + JSON on an 8GB Pascal GPU
	// (measured: llama3.1:8b best all-round, gemma3:4b fast+accurate) and avoids reasoning
	// models (qwen3/deepseek-r1) whose <think> burns the maxOutputTokens budget before the
	// JSON. Overridable via the ollama provider's preferred_models. See defaultProviderModels.
	if m := pickModel(models, cfg.preferredModels("ollama")...); m != "" {
		chain = append(chain, AIChainSlot{ID: "ollama-" + m, Provider: "ollama", Model: m})
	}
	for _, m := range models {
		if strings.HasSuffix(m, "-cloud") {
			chain = append(chain, AIChainSlot{ID: "ollama-cloud-" + m, Provider: "ollama", Model: m})
			break
		}
	}
	return chain
}

func defaultConfig() *Config {
	cfg := &Config{}
	cfg.Port = 8989
	cfg.Jackett.URL = "http://localhost:9117"
	cfg.Jackett.APIKey = "YOUR_API_KEY_HERE"
	cfg.DownloadClients = []DownloadClient{
		{
			ID:       "qbit-local",
			Name:     "qBittorrent Local",
			Type:     "qbittorrent",
			URL:      "http://localhost:8080",
			Username: "admin",
			Password: "adminadmin",
			Default:  true,
		},
	}
	return cfg
}

// fetchModels queries an OpenAI-compatible /v1/models endpoint and returns
// the list of model IDs. Returns nil on any failure (timeout, network error,
// non-200) so callers fall back to defaults transparently.
func fetchModels(baseURL, apiKey string) []string {
	client := &http.Client{Timeout: 5 * time.Second}
	url := strings.TrimRight(baseURL, "/") + "/models"
	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return nil
	}
	if apiKey != "" {
		req.Header.Set("Authorization", "Bearer "+apiKey)
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != 200 {
		return nil
	}
	var result struct {
		Data []struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil
	}
	out := make([]string, 0, len(result.Data))
	for _, m := range result.Data {
		if m.ID != "" {
			out = append(out, m.ID)
		}
	}
	return out
}

// pickModel picks the best model for title-renaming from a list. Preference:
// 1. Free models with "free" suffix
// 2. Fast/cheap models (flash, mini, nano, small)
// 3. The first available model
func pickModel(models []string, preferred ...string) string {
	if m := matchPreferred(models, preferred); m != "" {
		return m
	}
	if m := matchFreeModel(models); m != "" {
		return m
	}
	if m := matchCheapModel(models); m != "" {
		return m
	}
	if m := matchNonEmbedding(models); m != "" {
		return m
	}
	if len(models) > 0 {
		return models[0]
	}
	return ""
}

func matchPreferred(models, preferred []string) string {
	for _, p := range preferred {
		for _, m := range models {
			if m == p {
				return p
			}
		}
	}
	return ""
}

func matchFreeModel(models []string) string {
	for _, m := range models {
		if strings.HasSuffix(m, "-free") {
			return m
		}
	}
	return ""
}

func matchCheapModel(models []string) string {
	for _, m := range models {
		low := strings.ToLower(m)
		if strings.Contains(low, "flash") || strings.Contains(low, "mini") || strings.Contains(low, "nano") {
			return m
		}
	}
	return ""
}

func matchNonEmbedding(models []string) string {
	for _, m := range models {
		if !strings.Contains(strings.ToLower(m), "embedding") {
			return m
		}
	}
	return ""
}

// defaultProviderModels holds the built-in model-selection defaults per provider AS DATA
// (not string literals scattered through the append*Slot funcs). `preferred` is the pick
// order for a provider's default chain slot; `free` is the pinned free-tier id list for
// providers whose free tier can't be discovered. A provider's config (PreferredModels /
// FreeModels) overrides either; these are only the fallback. Editing model choices lives
// here (or in config.yaml), one place — the chain ORDER is still the benchmark's job.
//
// Google's `free` needs pinning because its OpenAI /models exposes no pricing (OpenRouter's
// does) and the id doesn't reveal the tier — gemini-2.5-flash is free but gemini-3.5-flash
// is PAID. Anything absent is treated as paid, so we never pick/benchmark a costly model.
var defaultProviderModels = map[string]struct {
	preferred []string
	free      []string
	rpm       int // default requests/min cap (0 = unthrottled); set for free tiers that burst-limit
}{
	"opencode":   {preferred: []string{"deepseek-v4-flash-free"}},
	"groq":       {preferred: []string{"llama-3.1-8b-instant", "llama-3.3-70b-versatile", "mixtral-8x7b-32768"}},
	"openrouter": {preferred: []string{"meta-llama/llama-3.3-70b-instruct:free"}},
	"ollama":     {preferred: []string{"llama3.1:8b", "gemma3:4b", "llama3.2:3b", "mistral:7b"}},
	// Google's free tier is ~30 req/min (flash-lite); throttle so a burst benchmark
	// completes instead of tripping 429s and marking Gemini incomplete.
	"google": {free: []string{"gemini-2.5-flash-lite", "gemini-2.5-flash", "gemini-2.0-flash"}, rpm: 30},
}

// DefaultFreeModels exposes the built-in free-id fallback for a provider (used by the ai
// package when a provider's config sets no FreeModels override).
func DefaultFreeModels(provider string) []string { return defaultProviderModels[provider].free }

// EffectiveRPM returns the requests/min cap for a provider: the config override (configRPM)
// when > 0, else the built-in default (0 = unthrottled). Used by the ai client to pace calls.
func EffectiveRPM(provider string, configRPM int) int {
	if configRPM > 0 {
		return configRPM
	}
	return defaultProviderModels[provider].rpm
}

// preferredModels / freeModels return the effective list for a provider: the config
// override when set, else the built-in default.
func (cfg *Config) preferredModels(provider string) []string {
	if p, ok := cfg.AI.Providers[provider]; ok && len(p.PreferredModels) > 0 {
		return p.PreferredModels
	}
	return defaultProviderModels[provider].preferred
}

func (cfg *Config) freeModels(provider string) []string {
	if p, ok := cfg.AI.Providers[provider]; ok && len(p.FreeModels) > 0 {
		return p.FreeModels
	}
	return defaultProviderModels[provider].free
}

// normalizeGoogleModel strips Google's "models/" id prefix. Its OpenAI-compat /models
// endpoint lists ids as "models/gemini-2.5-flash", but the chat endpoint accepts the bare
// "gemini-2.5-flash" too — so we compare on the bare form to match freeGoogleModels
// (otherwise discovery/seeding would never recognize a free Gemini and skip it entirely).
func normalizeGoogleModel(id string) string {
	return strings.TrimPrefix(id, "models/")
}

// IsFreeGoogleModel reports whether a Google model id is on the free tier, given the
// effective free list (exact match on the prefix-normalized id, so a paid look-alike like
// gemini-3.5-flash is never treated as free). Pure — callers pass the config-or-default list.
func IsFreeGoogleModel(model string, free []string) bool {
	model = normalizeGoogleModel(model)
	for _, id := range free {
		if normalizeGoogleModel(id) == model {
			return true
		}
	}
	return false
}

// pickFreeGoogleModel returns the most-preferred FREE Gemini model the account actually
// serves (intersect discovered with the free list, comparing on the prefix-normalized id).
// Dynamic within the free set; never returns a paid model. Returns the free-list id (bare,
// which the chat endpoint accepts). Empty when discovery failed or no free model is served.
func pickFreeGoogleModel(discovered, free []string) string {
	have := make(map[string]bool, len(discovered))
	for _, m := range discovered {
		have[normalizeGoogleModel(m)] = true
	}
	for _, id := range free {
		if have[normalizeGoogleModel(id)] {
			return id
		}
	}
	return ""
}
