package config

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
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
	DownloadClients []DownloadClient     `yaml:"download_clients"`
	Port            int                  `yaml:"port"`
	DBPath          string               `yaml:"db_path"`
	Stream          StreamConfig         `yaml:"stream"`
	Subtitles       SubtitlesConfig      `yaml:"subtitles"`
	Auth            AuthConfig           `yaml:"auth"`
	Notifications   NotificationsConfig  `yaml:"notifications"`
	TMDB            TMDBConfig           `yaml:"tmdb"`
	External        ExternalConfig       `yaml:"external"`
	AI              AIConfig             `yaml:"ai"`
	SMTP            SMTPConfig           `yaml:"smtp"`
	DownloadsQueue  DownloadsQueueConfig `yaml:"downloads_queue"`
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
	JWTSecret     string `yaml:"jwt_secret"`     // HS256 secret (auto-generated if empty + persisted)
	AdminUsername string `yaml:"admin_username"` // bootstrap admin login
	AdminPassword string `yaml:"admin_password"` // bootstrap admin password (only used on first run)
	DBPath        string `yaml:"db_path"`        // auth DB (defaults to /data/auth.db)
}

type StreamConfig struct {
	DataDir         string       `yaml:"data_dir"`         // where torrent pieces are stored
	DownloadDir     string       `yaml:"download_dir"`     // where completed downloads are moved (empty = stay in cache)
	SharedDir       string       `yaml:"shared_dir"`       // shared library destination for "Promote" (empty = feature disabled)
	StateDir        string       `yaml:"state_dir"`        // where SQLite stores live (favorites, library, etc.); empty = DataDir
	IdleMinutes     int          `yaml:"idle_minutes"`     // drop torrent after N min idle (files stay)
	MetadataSeconds int          `yaml:"metadata_seconds"` // metadata fetch timeout
	MaxCacheGB      int          `yaml:"max_cache_gb"`     // total cache size cap; 0 = unlimited
	PromoteDirs     []PromoteDir `yaml:"promote_dirs"`     // additional promote destinations (name + path)

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
	applyJackettEnv(cfg)
	applyStreamEnv(cfg)
	applyAuthEnv(cfg)
	applyNotificationsEnv(cfg)
	applyTMDBEnv(cfg)
	applyExternalMountsEnv(cfg)
	applySMTPEnv(cfg)
	applyAIEnv(cfg)
	applyDownloadsQueueEnv(cfg)

	if v := os.Getenv("JACKUI_MAX_UPLOAD_MB"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			cfg.External.MaxUploadMB = n
		}
	}
	if v := os.Getenv("JACKUI_LOCAL_READAHEAD_MB"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			cfg.External.LocalReadaheadMB = n
		}
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
	"TMDB_API_KEY": true, "OMDB_API_KEY": true, "JACKUI_NTFY_TOKEN": true,
}

func ActiveEnvOverrides() map[string]string {
	keys := []string{
		"JACKETT_URL", "JACKETT_API_KEY",
		"JACKUI_PORT", "JACKUI_DB_PATH",
		"JACKUI_STREAM_DIR", "JACKUI_DOWNLOAD_DIR",
		"JACKUI_STATE_DIR", "JACKUI_SHARED_DIR",
		"JACKUI_STREAM_MAX_GB",
		"JACKUI_AUTH_ENABLED", "JACKUI_ADMIN_PASSWORD", "JACKUI_ADMIN_USERNAME", "JACKUI_JWT_SECRET",
		"JACKUI_NTFY_TOPIC", "JACKUI_NTFY_URL", "JACKUI_NTFY_TOKEN",
		"TMDB_API_KEY", "OMDB_API_KEY",
		"JACKUI_SMTP_HOST", "JACKUI_SMTP_PORT", "JACKUI_SMTP_USER", "JACKUI_SMTP_PASS", "JACKUI_SMTP_FROM",
		"JACKUI_BASE_URL",
		"JACKUI_EXTERNAL_MOUNTS",
		"JACKUI_AI_ENABLED", "GROQ_API_KEY", "OPENROUTER_API_KEY", "OLLAMA_BASE_URL", "JACKUI_AI_MAX_COST_PER_1M", "JACKUI_AI_KWH_PRICE", "JACKUI_AI_LOCAL_WATTS",
		"JACKUI_MAX_UPLOAD_MB", "JACKUI_LOCAL_READAHEAD_MB",
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
	if v := os.Getenv("JACKUI_DB_PATH"); v != "" {
		cfg.DBPath = v
	}
	if v := os.Getenv("JACKUI_STREAM_DIR"); v != "" {
		cfg.Stream.DataDir = v
	}
	if v := os.Getenv("JACKUI_DOWNLOAD_DIR"); v != "" {
		cfg.Stream.DownloadDir = v
	}
	if v := os.Getenv("JACKUI_STATE_DIR"); v != "" {
		cfg.Stream.StateDir = v
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

func applyEnvInt64MB(target *int64, name string) {
	if n, ok := envInt(name); ok && n >= 0 {
		*target = int64(n) * 1024 * 1024
	}
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

func applyExternalMountsEnv(cfg *Config) {
	v := os.Getenv("JACKUI_EXTERNAL_MOUNTS")
	if v == "" {
		return
	}
	seen := map[string]bool{}
	for _, m := range cfg.External.Mounts {
		seen[m.Path] = true
	}
	for _, spec := range strings.Split(v, ",") {
		spec = strings.TrimSpace(spec)
		i := strings.Index(spec, ":")
		if i <= 0 || i == len(spec)-1 {
			continue
		}
		name, rest := strings.TrimSpace(spec[:i]), strings.TrimSpace(spec[i+1:])
		// Optional trailing ":usersubpath" flag turns the mount into per-user
		// private subdirs (mount/{username}/...). Backward compatible: specs
		// without the flag keep the shared-root behavior.
		userSubpath := false
		if j := strings.LastIndex(rest, ":"); j >= 0 {
			if flag := strings.ToLower(strings.TrimSpace(rest[j+1:])); flag == "usersubpath" || flag == "subpath" {
				userSubpath = true
				rest = strings.TrimSpace(rest[:j])
			}
		}
		path := rest
		if name == "" || path == "" || seen[path] {
			continue
		}
		cfg.External.Mounts = append(cfg.External.Mounts, ExternalMount{Name: name, Path: path, UserSubpath: userSubpath})
		seen[path] = true
	}
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
	var chain []AIChainSlot
	chain = cfg.appendOpenCodeSlot(chain)
	chain = cfg.appendGroqSlot(chain)
	chain = cfg.appendOpenRouterSlot(chain)
	chain = cfg.appendOllamaSlots(chain)
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
	m := pickModel(models, "deepseek-v4-flash-free")
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
	m := pickModel(models, "llama-3.1-8b-instant", "llama-3.3-70b-versatile", "mixtral-8x7b-32768")
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
	m := pickModel(models, "meta-llama/llama-3.3-70b-instruct:free")
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
	if m := pickModel(models, "qwen2.5:7b", "llama3.2:3b", "llama3.1:8b", "mistral:7b"); m != "" {
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
