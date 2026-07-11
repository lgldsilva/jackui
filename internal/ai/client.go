// Package ai is a small OpenAI-compatible LLM client with a configurable
// fallback chain, used to turn a messy torrent name into a clean title before
// the TMDB lookup. It's deliberately narrow: one task (title identification),
// no tool-calling loop. The chain is walked in order, a per-slot circuit breaker
// skips known-down providers, and the first usable answer wins.
package ai

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/lgldsilva/jackui/internal/config"
)

// Slot is one resolved chain entry — a provider's base URL + key bound to a
// specific model id.
type Slot struct {
	ID       string
	Provider string
	Model    string
	BaseURL  string
	apiKey   string
	Free     bool // true when the model is free (CostPer1M == 0)
	Local    bool // true for a model served by the LOCAL Ollama GPU (see localModel)
	// CostPer1M is the blended (prompt+completion)/2 price in USD per 1M tokens.
	// 0 = free; -1 = UNKNOWN (a metered provider that doesn't expose pricing, e.g.
	// OpenCode Zen) — those are excluded from the benchmark so we never call a
	// model we can't price. Discovery fills it from /models; resolveSlot can only
	// tell free (0) from unknown (-1) for chain models (no pricing data there).
	CostPer1M float64
}

// localModel reports whether a slot runs on the LOCAL Ollama — a model served from
// this machine's own GPU. True only for the ollama provider WITHOUT the "-cloud"
// suffix: those models share a single GPU and Ollama serves one at a time, so the
// benchmark must run them strictly sequentially (concurrent calls exceed Ollama's
// connection slots and thrash models in/out of VRAM). Ollama *cloud* models (the
// "-cloud" suffix) run on remote infra and parallelize fine, like any other vendor.
func localModel(provider, model string) bool {
	return provider == "ollama" && !strings.HasSuffix(model, "-cloud")
}

// TitleResult is what IdentifyTitle extracts from a raw torrent name.
type TitleResult struct {
	Title string `json:"title"`
	Year  int    `json:"year"`
	Kind  string `json:"kind"` // "movie" | "tv" | "unknown"
}

// Query returns the title (plus year when known) formatted for a TMDB search.
func (r *TitleResult) Query() string {
	if r == nil || r.Title == "" {
		return ""
	}
	if r.Year > 0 {
		return fmt.Sprintf("%s %d", r.Title, r.Year)
	}
	return r.Title
}

type Client struct {
	mu        sync.RWMutex // guards slots (self-heal mutates while requests read)
	slots     []Slot
	providers map[string]config.AIProvider // kept so ApplyChain can resolve new slots
	breaker   *breaker
	http      *http.Client
	healing   sync.Map // provider -> in-flight, dedupes self-heal
	// cost is the runtime-tunable cost config (ceiling + energy tariff/watts),
	// swapped atomically so the Settings UI can update it live without a restart.
	cost atomic.Pointer[CostConfig]
	// googleFree is the effective free-tier id list for the "google" provider (config
	// override or built-in default), cached so isFreeModel can gate Gemini's per-model
	// free tier — which its /models can't reveal. See config.DefaultFreeModels.
	googleFree []string
	// limiter paces calls per provider to respect free-tier requests/min caps (nil when
	// no provider is throttled). Set in New from each provider's effective RPM.
	limiter *providerLimiter
}

// CostConfig holds the knobs that drive the value-based score: the benchmark cost
// ceiling, the electricity tariff, and the GPU power draw used to price local
// models' energy. See SetCostConfig / CostConfig().
type CostConfig struct {
	MaxCostPer1M float64 `json:"maxCostPer1M"` // ceiling for testing paid models ($/1M); 0 = free only
	KWhPrice     float64 `json:"kwhPrice"`     // electricity tariff ($/kWh); 0 = local stays free
	LocalWatts   float64 `json:"localWatts"`   // GPU power draw under load (W)
}

// CostConfig returns the live cost config (never nil after New).
func (c *Client) CostConfig() CostConfig {
	if cc := c.cost.Load(); cc != nil {
		return *cc
	}
	return CostConfig{}
}

// SetCostConfig swaps in new cost knobs live (watts falls back to a default).
func (c *Client) SetCostConfig(cc CostConfig) {
	cc.LocalWatts = localWattsOrDefault(cc.LocalWatts)
	c.cost.Store(&cc)
}

// Providers returns the list of configured provider names (e.g. "ollama", "groq").
func (c *Client) Providers() []string {
	if c == nil {
		return nil
	}
	c.mu.RLock()
	defer c.mu.RUnlock()
	var out []string
	for name := range c.providers {
		out = append(out, name)
	}
	sort.Strings(out) // stable order so the UI dropdown doesn't shuffle between reloads
	return out
}

// slotList returns a snapshot of the live chain (safe to iterate without holding
// the lock while making slow network calls).
func (c *Client) slotList() []Slot {
	c.mu.RLock()
	defer c.mu.RUnlock()
	out := make([]Slot, len(c.slots))
	copy(out, c.slots)
	return out
}

// New builds a Client from config. Returns nil when AI is disabled or no usable
// chain slot resolves — callers treat nil as "no AI, use the regex fallback".
func New(cfg config.AIConfig) *Client {
	if !cfg.Enabled {
		return nil
	}
	c := &Client{
		providers: cfg.Providers,
		breaker:   newBreaker(),
		// Generous backstop only — real per-call limits come from the ctx the
		// caller passes (resolve ~25s, benchmark ~90s, warmup ~120s for cold
		// local models loading into VRAM).
		http: &http.Client{Timeout: 130 * time.Second},
	}
	// Effective Google free-tier list: provider override, else the built-in default.
	// Cached before resolveSlot (which calls isFreeModel) runs.
	if p, ok := cfg.Providers["google"]; ok && len(p.FreeModels) > 0 {
		c.googleFree = p.FreeModels
	} else {
		c.googleFree = config.DefaultFreeModels("google")
	}
	// Per-provider requests/min caps (config override, else built-in default) → limiter.
	rpm := map[string]int{}
	for name, p := range cfg.Providers {
		if r := config.EffectiveRPM(name, p.RPM); r > 0 {
			rpm[name] = r
		}
	}
	c.limiter = newProviderLimiter(rpm)
	c.SetCostConfig(CostConfig{MaxCostPer1M: cfg.MaxCostPer1M, KWhPrice: cfg.ElectricityPricePerKWh, LocalWatts: cfg.LocalPowerWatts})
	for _, s := range cfg.Chain {
		if s.Disabled {
			continue
		}
		if slot, ok := c.resolveSlot(s.ID, s.Provider, s.Model); ok {
			c.slots = append(c.slots, slot)
		}
	}
	if len(c.slots) == 0 {
		return nil
	}
	return c
}

// resolveSlot binds a provider+model to a usable Slot (base URL + key). Returns
// ok=false when the provider is unknown / has no base URL.
func (c *Client) resolveSlot(id, provider, model string) (Slot, bool) {
	p, ok := c.providers[provider]
	if !ok || p.BaseURL == "" {
		return Slot{}, false
	}
	if id == "" {
		id = provider + ":" + model
	}
	// No pricing data for a chain model — we can only tell free (0) from unknown
	// (-1, a paid model we can't price → excluded from the benchmark).
	cost := -1.0
	if c.isFreeModel(provider, model) {
		cost = 0
	}
	return Slot{ID: id, Provider: provider, Model: model, BaseURL: strings.TrimRight(p.BaseURL, "/"), apiKey: p.APIKey, Free: cost == 0, Local: localModel(provider, model), CostPer1M: cost}, true
}

// ApplyChain replaces the live chain with the given (provider, model) defs in
// order — used to adopt a benchmark ranking as the working chain (best first,
// free local models retained as low-ranked fallbacks). Unresolvable defs are
// skipped; an empty result leaves the chain unchanged.
func (c *Client) ApplyChain(defs []config.AIChainSlot) {
	var next []Slot
	for _, d := range defs {
		if slot, ok := c.resolveSlot(d.ID, d.Provider, d.Model); ok {
			next = append(next, slot)
		}
	}
	if len(next) > 0 {
		c.mu.Lock()
		c.slots = next
		c.mu.Unlock()
	}
}

// Slots returns a copy of the resolved chain (for the benchmark + status UI).
func (c *Client) Slots() []Slot { return c.slotList() }

const identifySystem = `You extract the canonical movie or TV series title from a raw torrent/release name.

Rules:
- Strip technical noise: resolution (720p/1080p/2160p/4K/UHD), source (BluRay/REMUX/WEB-DL/WEBRip/HDTV/AMZN/NF), codec (x264/x265/H.264/HEVC/AV1/10bit), audio (DDP5.1/DTS/Atmos/AAC), HDR/DV, edition tags (REPACK/PROPER/EXTENDED/COMPLETE/Director's Cut), language/dub tags (DUAL/MULTI/DUBLADO/LEGENDADO/NACIONAL/FRENCH/GERMAN), season/episode markers, bracketed ids/groups, file extensions, leading site tags ("www.Site.com - ") and the trailing release group.
- Dots/underscores become spaces. Keep the title's own language, accents and punctuation — never translate.
- KEEP numbers that are part of the title ("Blade Runner 2049", "Wonder Woman 1984", "1917"); "year" is the standalone RELEASE year next to the quality tags, 0 if unknown.
- TV/anime: return only the series name, without SxxEyy or episode numbers. Anime: use the romanized title as written.

Reply with ONLY a JSON object, no prose, no code fences:
{"title": "<clean title>", "year": <release year or 0>, "kind": "movie" | "tv" | "unknown"}`

// IdentifyTitle walks the chain until a slot returns a usable title. Returns the
// result and the slot id that produced it. A nil result with nil error means no
// slot could parse a title (caller falls back to regex cleaning).
func (c *Client) IdentifyTitle(ctx context.Context, rawName string) (*TitleResult, string, error) {
	var lastErr error
	for _, s := range c.slotList() {
		if !c.breaker.available(s.Provider, s.ID) {
			continue
		}
		res, _, err := c.identifyWithSlot(ctx, s, rawName)
		if err != nil {
			lastErr = err
			c.noteChainFailure(s, err)
			continue
		}
		c.breaker.recordSuccess(s.Provider, s.ID)
		if res != nil && res.Title != "" {
			return res, s.ID, nil
		}
	}
	return nil, "", lastErr
}

const musicSystem = `You turn a raw music torrent/release name into a concise query to find the ALBUM COVER on an image search.
Output ONLY the query text (no quotes, no prose), ideally "<artist> <album>" — drop years, formats (FLAC/MP3/320), scene tags and bracketed noise. If you can't tell, return the cleaned name.`

// MusicQuery asks the chain to build a cover-art search query from a messy music
// release name (e.g. "Disturbed - Discography 2000-2019 [FLAC]" → "Disturbed").
// Walks the chain like IdentifyTitle; returns "" if nothing usable came back.
func (c *Client) MusicQuery(ctx context.Context, rawName string) string {
	for _, s := range c.slotList() {
		if !c.breaker.available(s.Provider, s.ID) {
			continue
		}
		content, _, _, err := c.chat(ctx, s, musicSystem, rawName, false)
		if err != nil {
			c.noteChainFailure(s, err)
			continue
		}
		c.breaker.recordSuccess(s.Provider, s.ID)
		// Plain text reply (no JSON) — take the first non-empty line, strip quotes.
		q := strings.TrimSpace(content)
		if i := strings.IndexByte(q, '\n'); i >= 0 {
			q = strings.TrimSpace(q[:i])
		}
		q = strings.Trim(q, `"'`)
		if q != "" {
			return q
		}
	}
	return ""
}

// IdentifyWithSlot runs a single named slot, bypassing the breaker. Used by the
// benchmark to measure each model independently.
func (c *Client) IdentifyWithSlot(ctx context.Context, slotID, rawName string) (*TitleResult, time.Duration, error) {
	for _, s := range c.slots {
		if s.ID == slotID {
			return c.identifyWithSlot(ctx, s, rawName)
		}
	}
	return nil, 0, fmt.Errorf("ai: slot %q not found", slotID)
}

func (c *Client) identifyWithSlot(ctx context.Context, s Slot, rawName string) (*TitleResult, time.Duration, error) {
	content, latency, _, err := c.chat(ctx, s, identifySystem, rawName, true)
	if err != nil {
		return nil, latency, err
	}
	res, perr := parseTitleJSON(content)
	if perr != nil {
		return nil, latency, fmt.Errorf("%w: %v", errBadOutput, perr)
	}
	return res, latency, nil
}

// metadataWithSlot runs one slot through the FULL rename prompt (title + year +
// kind + season + episode), timed, bypassing the breaker. The benchmark scores
// this richer extraction — not the title-only path — so accuracy reflects the
// actual rename task (séries with the right season/episode), which is what the
// "Renomear e Organizar via IA" feature depends on.
func (c *Client) metadataWithSlot(ctx context.Context, s Slot, rawName string) (*RenameMetadata, time.Duration, int, error) {
	content, latency, tokens, err := c.chat(ctx, s, renameSystem, rawName, true)
	if err != nil {
		return nil, latency, tokens, err
	}
	res, perr := parseRenameJSON(content)
	if perr != nil {
		return nil, latency, tokens, fmt.Errorf("%w: %v", errBadOutput, perr)
	}
	return res, latency, tokens, nil
}

// errBadOutput marks "the model responded but produced no usable answer" — a
// genuine quality failure of THIS model on THIS input (HTTP 400 json_validate_failed,
// or content we couldn't parse), as opposed to a transient/infra problem (rate
// limit, 5xx, network). The benchmark scores a bad output as a 0-accuracy case
// instead of silently skipping it, so a model that fails some cases can't show a
// clean 100% next to a failure reason.
var errBadOutput = errors.New("ai: model produced no usable output")

// isFreeModel reports whether a model costs nothing to call: it's on a free-tier
// provider (Groq's free tier, local Ollama), or its id carries a free marker
// (:free / -free). The single source of truth for "safe to benchmark/adopt without
// spending" — used by discovery, FreeOnly, and adoption.
func (c *Client) isFreeModel(provider, model string) bool {
	if freeTierProviders[provider] {
		return true
	}
	// Google's free tier can't be discovered (no pricing in /models, not in the id) —
	// gate against the effective free-id list (config override or default), so anything
	// not on it (pro, gemini-3.5-flash) is treated as paid.
	if provider == "google" {
		return config.IsFreeGoogleModel(model, c.googleFree)
	}
	return strings.HasSuffix(model, ":free") || strings.HasSuffix(model, "-free")
}

// AffordableSlots drops models the benchmark isn't allowed to PAY to test: those
// whose cost exceeds maxCostPer1M, and those with UNKNOWN cost (-1 — a metered
// provider with no pricing, never call it). With the default ceiling of 0 this is
// exactly "free only" (so we never spend); raising the ceiling lets cheap paid
// models in. This also prunes paid leftovers a pre-filter run adopted (e.g. Zen
// "big-pickle", which is unknown-cost).
func (c *Client) AffordableSlots(slots []Slot) []Slot {
	ceiling := c.CostConfig().MaxCostPer1M
	out := make([]Slot, 0, len(slots))
	for _, s := range slots {
		if s.CostPer1M >= 0 && s.CostPer1M <= ceiling {
			out = append(out, s)
		}
	}
	return out
}

// localWattsOrDefault falls back to a mid-range GPU-under-load figure when unset.
func localWattsOrDefault(w float64) float64 {
	if w <= 0 {
		return 250
	}
	return w
}

// localEnergyCostPer1M estimates a LOCAL model's energy cost in USD per 1M tokens
// from the benchmark's measured total latency and token count: energy(kWh) =
// power × time, cost = energy × tariff, scaled to 1M tokens. Returns 0 when the
// tariff is unset (local stays free until configured) or there's nothing measured.
func (c *Client) localEnergyCostPer1M(totalLatency time.Duration, totalTokens int) float64 {
	cc := c.CostConfig()
	if cc.KWhPrice <= 0 || totalTokens <= 0 || totalLatency <= 0 {
		return 0
	}
	powerKW := localWattsOrDefault(cc.LocalWatts) / 1000.0
	return powerKW * cc.KWhPrice * (totalLatency.Hours() / float64(totalTokens) * 1_000_000)
}
