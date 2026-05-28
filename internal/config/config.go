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
	DownloadClients []DownloadClient    `yaml:"download_clients"`
	Port            int                 `yaml:"port"`
	DBPath          string              `yaml:"db_path"`
	Stream          StreamConfig        `yaml:"stream"`
	Subtitles       SubtitlesConfig     `yaml:"subtitles"`
	Auth            AuthConfig          `yaml:"auth"`
	Notifications   NotificationsConfig `yaml:"notifications"`
	TMDB            TMDBConfig          `yaml:"tmdb"`
	External        ExternalConfig      `yaml:"external"`
	AI              AIConfig            `yaml:"ai"`
	SMTP            SMTPConfig          `yaml:"smtp"`
	// BaseURL is the public URL of the app (e.g. https://jackui.raspberrypi.lan),
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
// the Local Files page lists what's already on disk.
type ExternalConfig struct {
	Mounts []ExternalMount `yaml:"mounts"`
}

type ExternalMount struct {
	Name         string   `yaml:"name"`         // Display name shown in the UI ("HD Externo", "NAS")
	Path         string   `yaml:"path"`         // Absolute path inside the container
	AllowedUsers []string `yaml:"allowed_users"` // Empty = visible to all; otherwise only these usernames
}

type NotificationsConfig struct {
	NtfyBaseURL       string `yaml:"ntfy_base_url"`      // default https://ntfy.sh
	NtfyDefaultTopic  string `yaml:"ntfy_default_topic"` // used when a watchlist has no override
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
}

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
	if v := os.Getenv("OMDB_API_KEY"); v != "" {
		cfg.TMDB.OMDbAPIKey = v
	}
	// JACKUI_EXTERNAL_MOUNTS lets the deploy declare browsable mounts without
	// editing config.yaml — format: "Name:/abs/path,Other:/abs/path2". Merged
	// into external.mounts, skipping paths already present.
	if v := os.Getenv("JACKUI_EXTERNAL_MOUNTS"); v != "" {
		seen := map[string]bool{}
		for _, m := range cfg.External.Mounts {
			seen[m.Path] = true
		}
		for _, spec := range strings.Split(v, ",") {
			spec = strings.TrimSpace(spec)
			i := strings.Index(spec, ":")
			if i <= 0 || i == len(spec)-1 {
				continue // need "Name:/path"
			}
			name, path := strings.TrimSpace(spec[:i]), strings.TrimSpace(spec[i+1:])
			if name == "" || path == "" || seen[path] {
				continue
			}
			cfg.External.Mounts = append(cfg.External.Mounts, ExternalMount{Name: name, Path: path})
			seen[path] = true
		}
	}

	// SMTP + base URL — credentials live in env (gitignored), not committed yaml.
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
		// Local Ollama, OpenAI-compatible under /v1. Behind the VPN, use the LAN
		// IP (e.g. http://192.168.0.100:11434) — the `.lan` hostname won't resolve
		// through the VPN DNS.
		base := v
		if !strings.HasSuffix(base, "/v1") {
			base = strings.TrimRight(base, "/") + "/v1"
		}
		setKey("ollama", base, "")
	}
	// Ollama Cloud models are reached THROUGH the local Ollama (which already has
	// the cloud API key configured) — they're just model names with a "-cloud"
	// suffix on the same endpoint. So no separate provider/key is needed here.
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
			// A stronger free model that reliably follows the JSON format (the
			// nano model often replied without JSON).
			chain = append(chain, AIChainSlot{ID: "openrouter-llama70b", Provider: "openrouter", Model: "meta-llama/llama-3.3-70b-instruct:free"})
		}
		if cfg.AI.Providers["ollama"].BaseURL != "" {
			// Local model + a cloud model (both via the local Ollama endpoint; the
			// "-cloud" suffix proxies to Ollama Cloud using its configured key).
			chain = append(chain,
				AIChainSlot{ID: "ollama-qwen", Provider: "ollama", Model: "qwen2.5:7b"},
				AIChainSlot{ID: "ollama-cloud-gptoss", Provider: "ollama", Model: "gpt-oss:120b-cloud"},
			)
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
