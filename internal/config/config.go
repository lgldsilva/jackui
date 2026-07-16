package config

import (
	"fmt"
	"os"
	"strings"

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
	Enabled       bool   `yaml:"enabled"`        // env default ON; explicit JACKUI_AUTH_ENABLED=0 + escape for dev/LAN
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
	// HLSMediaRenditions liga as renditions EXT-X-MEDIA do master HLS (Phase 2
	// M2b): faixas de áudio alternativas como TYPE=AUDIO (a/:track) e legendas de
	// texto como TYPE=SUBTITLES (sub/:track, WebVTT). DEFAULT false (dark launch):
	// o frontend precisa migrar pra hls.audioTrack (troca seamless, sem reload) e
	// ser validado no Chrome/Safari antes de ligar — com false o master mantém o
	// comportamento M2a (só STREAM-INF multi-resolução + ?audio via reload).
	// Env: JACKUI_HLS_MEDIA_RENDITIONS (1/true).
	HLSMediaRenditions bool `yaml:"hls_media_renditions"`
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
	// #nosec G304 -- path validado por Browser.ResolvePath (guarda traversal/symlink) ou derivado de hash/config interna
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

	// #nosec G306 -- arquivo de midia/cache; 0644 intencional p/ leitura
	if err := os.WriteFile(path, data, 0644); err != nil {
		return fmt.Errorf("failed to write config: %w", err)
	}

	return nil
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
