package config

import (
	"fmt"
	"os"
	"strconv"
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
	DBPath          string           `yaml:"db_path"`
	Stream          StreamConfig     `yaml:"stream"`
	Subtitles       SubtitlesConfig  `yaml:"subtitles"`
	Auth            AuthConfig       `yaml:"auth"`
	Notifications   NotificationsConfig `yaml:"notifications"`
	TMDB            TMDBConfig          `yaml:"tmdb"`
	External        ExternalConfig      `yaml:"external"`
	AI              AIConfig            `yaml:"ai"`
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

// ExternalConfig declares filesystem mounts the user wants browsable from
// the web UI — typical setups: bind-mount an external HDD or NAS share so
// the Local Files page lists what's already on disk. Per-user is NOT
// enforced here; admin curates the mount list in config.yaml.
type ExternalConfig struct {
	Mounts []ExternalMount `yaml:"mounts"`
}

type ExternalMount struct {
	Name string `yaml:"name"` // Display name shown in the UI ("HD Externo", "NAS")
	Path string `yaml:"path"` // Absolute path inside the container
}

type NotificationsConfig struct {
	NtfyBaseURL      string `yaml:"ntfy_base_url"`       // default https://ntfy.sh
	NtfyDefaultTopic string `yaml:"ntfy_default_topic"`  // used when a watchlist has no override
	WatchlistInterval int   `yaml:"watchlist_minutes"`   // poll interval in minutes (default 15)
}

type TMDBConfig struct {
	APIKey string `yaml:"api_key"` // empty disables enrichment (no posters)
}

type AuthConfig struct {
	Enabled       bool   `yaml:"enabled"`         // false = legacy no-auth mode (everything public)
	JWTSecret     string `yaml:"jwt_secret"`      // HS256 secret (auto-generated if empty + persisted)
	AdminUsername string `yaml:"admin_username"`  // bootstrap admin login
	AdminPassword string `yaml:"admin_password"`  // bootstrap admin password (only used on first run)
	DBPath        string `yaml:"db_path"`         // auth DB (defaults to /data/auth.db)
}

type StreamConfig struct {
	DataDir         string `yaml:"data_dir"`         // where torrent pieces are stored
	IdleMinutes     int    `yaml:"idle_minutes"`     // drop torrent after N min idle (files stay)
	MetadataSeconds int    `yaml:"metadata_seconds"` // metadata fetch timeout
	MaxCacheGB      int    `yaml:"max_cache_gb"`     // total cache size cap; 0 = unlimited
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
	if v := os.Getenv("JACKETT_URL"); v != "" {
		cfg.Jackett.URL = v
	}
	if v := os.Getenv("JACKETT_API_KEY"); v != "" {
		cfg.Jackett.APIKey = v
	}
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
	if v := os.Getenv("JACKUI_STREAM_MAX_GB"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n >= 0 {
			cfg.Stream.MaxCacheGB = n
		}
	}
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
	if v := os.Getenv("JACKUI_NTFY_TOPIC"); v != "" {
		cfg.Notifications.NtfyDefaultTopic = v
	}
	if v := os.Getenv("JACKUI_NTFY_URL"); v != "" {
		cfg.Notifications.NtfyBaseURL = v
	}
	if v := os.Getenv("TMDB_API_KEY"); v != "" {
		cfg.TMDB.APIKey = v
	}

	applyAIEnv(cfg)

	// Sensible defaults if not set anywhere
	if cfg.Stream.IdleMinutes == 0 {
		cfg.Stream.IdleMinutes = 30
	}
	if cfg.Stream.MetadataSeconds == 0 {
		cfg.Stream.MetadataSeconds = 60
	}
}

// applyAIEnv fills provider credentials from the standard env vars and, when no
// explicit chain is configured, auto-seeds a sensible default from whatever
// providers have credentials. This lets the same env vars the box already
// exports (GROQ_API_KEY / OPENROUTER_API_KEY / OLLAMA_BASE_URL) light up the AI
// title-identification without hand-editing config.yaml.
func applyAIEnv(cfg *Config) {
	if cfg.AI.Providers == nil {
		cfg.AI.Providers = map[string]AIProvider{}
	}
	setKey := func(name, baseURL, key string) {
		p := cfg.AI.Providers[name]
		if p.BaseURL == "" {
			p.BaseURL = baseURL
		}
		if key != "" {
			p.APIKey = key
		}
		cfg.AI.Providers[name] = p
	}
	if v := os.Getenv("GROQ_API_KEY"); v != "" {
		setKey("groq", "https://api.groq.com/openai/v1", v)
	}
	if v := os.Getenv("OPENROUTER_API_KEY"); v != "" {
		setKey("openrouter", "https://openrouter.ai/api/v1", v)
	}
	if v := os.Getenv("OLLAMA_BASE_URL"); v != "" {
		// Ollama exposes the OpenAI-compatible API under /v1.
		base := v
		if !strings.HasSuffix(base, "/v1") {
			base = strings.TrimRight(base, "/") + "/v1"
		}
		setKey("ollama", base, "")
	}
	if v := os.Getenv("JACKUI_AI_ENABLED"); v == "0" || v == "false" {
		cfg.AI.Enabled = false
		return
	}

	// Auto-seed a default chain only when the user hasn't configured one.
	if len(cfg.AI.Chain) == 0 {
		var chain []AIChainSlot
		if cfg.AI.Providers["groq"].APIKey != "" {
			chain = append(chain, AIChainSlot{ID: "groq-8b", Provider: "groq", Model: "llama-3.1-8b-instant"})
		}
		if cfg.AI.Providers["openrouter"].APIKey != "" {
			chain = append(chain, AIChainSlot{ID: "openrouter-nemotron", Provider: "openrouter", Model: "nvidia/nemotron-nano-9b-v2:free"})
		}
		if cfg.AI.Providers["ollama"].BaseURL != "" {
			chain = append(chain, AIChainSlot{ID: "ollama-qwen", Provider: "ollama", Model: "qwen2.5:7b"})
		}
		if len(chain) > 0 {
			cfg.AI.Chain = chain
			cfg.AI.Enabled = true
		}
	} else if !cfg.AI.Enabled {
		// Explicit chain present → enable unless the user turned it off above.
		cfg.AI.Enabled = true
	}
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
