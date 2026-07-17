package config

import (
	"net"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// Overrides por variável de ambiente (apply*Env) — extraído de config.go.
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
	if v := os.Getenv("JACKUI_HLS_MEDIA_RENDITIONS"); v == "1" || v == "true" {
		cfg.Stream.HLSMediaRenditions = true
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
		"JACKUI_AUTH_ENABLED", "JACKUI_ALLOW_INSECURE_AUTH", "JACKUI_ADMIN_PASSWORD", "JACKUI_ADMIN_USERNAME", "JACKUI_JWT_SECRET",
		"JACKUI_NTFY_TOPIC", "JACKUI_NTFY_URL", "JACKUI_NTFY_TOKEN",
		"TMDB_API_KEY", "OMDB_API_KEY",
		"JACKUI_SMTP_HOST", "JACKUI_SMTP_PORT", "JACKUI_SMTP_USER", "JACKUI_SMTP_PASS", "JACKUI_SMTP_FROM",
		"JACKUI_BASE_URL",
		"JACKUI_EXTERNAL_MOUNTS",
		"JACKUI_AI_ENABLED", "GROQ_API_KEY", "OPENROUTER_API_KEY", "OPENCODE_API_KEY", "GEMINI_API_KEY", "OLLAMA_BASE_URL", "JACKUI_AI_MAX_COST_PER_1M", "JACKUI_AI_KWH_PRICE", "JACKUI_AI_LOCAL_WATTS",
		"JACKUI_MAX_UPLOAD_MB", "JACKUI_LOCAL_READAHEAD_MB", "JACKUI_LOCAL_CACHE_GB", "JACKUI_HLS_VOD_MODE", "JACKUI_HLS_MEDIA_RENDITIONS", "JACKUI_MAX_GPU_TRANSCODES",
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
	switch os.Getenv("JACKUI_AUTH_ENABLED") {
	case "0", "false":
		cfg.Auth.Enabled = false
	default:
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
	// #nosec G304 -- path validado por Browser.ResolvePath (guarda traversal/symlink) ou derivado de hash/config interna
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
