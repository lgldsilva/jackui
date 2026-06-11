// Package ai is a small OpenAI-compatible LLM client with a configurable
// fallback chain, used to turn a messy torrent name into a clean title before
// the TMDB lookup. It's deliberately narrow: one task (title identification),
// no tool-calling loop. The chain is walked in order, a per-slot circuit breaker
// skips known-down providers, and the first usable answer wins.
package ai

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strconv"
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
	if isFreeModel(provider, model) {
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

// ─── OpenAI-compatible /chat/completions ─────────────────────────────────────

type chatReq struct {
	Model           string        `json:"model"`
	Messages        []chatMessage `json:"messages"`
	Temperature     float64       `json:"temperature"`
	MaxTokens       int           `json:"max_tokens"`
	ResponseFormat  *respFormat   `json:"response_format,omitempty"`
	ReasoningEffort string        `json:"reasoning_effort,omitempty"`
}

// maxOutputTokens caps the reply. It has to be generous because reasoning models
// (gpt-oss, o-series) spend this same budget on chain-of-thought tokens BEFORE
// emitting the JSON — with only 200 they ran out mid-reasoning and Groq returned
// 400 "max completion tokens reached before generating a valid document". The cap
// doesn't slow models that finish early (the JSON we want is ~40 tokens); it just
// stops a reasoner from erroring. See https://console.groq.com/docs/reasoning.
const maxOutputTokens = 1024

// respFormat forces JSON output on providers that support it (Groq/OpenRouter/
// Ollama OpenAI-compat). Set only for title identification, not the plain-text
// music query.
type respFormat struct {
	Type string `json:"type"`
}

type chatMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type chatResp struct {
	Choices []struct {
		Message chatMessage `json:"message"`
	} `json:"choices"`
	Usage struct {
		TotalTokens int `json:"total_tokens"`
	} `json:"usage"`
	Error *struct {
		Message string `json:"message"`
		Code    string `json:"code"`
	} `json:"error"`
}

var errRateLimited = errors.New("ai: rate limited")
var errModelNotFound = errors.New("ai: model not found")
var errInsufficientBalance = errors.New("ai: saldo insuficiente")

// errBadOutput marks "the model responded but produced no usable answer" — a
// genuine quality failure of THIS model on THIS input (HTTP 400 json_validate_failed,
// or content we couldn't parse), as opposed to a transient/infra problem (rate
// limit, 5xx, network). The benchmark scores a bad output as a 0-accuracy case
// instead of silently skipping it, so a model that fails some cases can't show a
// clean 100% next to a failure reason.
var errBadOutput = errors.New("ai: model produced no usable output")

// rateLimitError is a 429 carrying the vendor's Retry-After hint (0 if none). It
// unwraps to errRateLimited so existing errors.Is checks still match; the benchmark
// reads RetryAfter to back off the exact reset window and retry, so a transiently
// throttled model still gets a COMPLETE score instead of 100% over a few cases.
type rateLimitError struct {
	slotID     string
	RetryAfter time.Duration
}

func (e *rateLimitError) Error() string { return "ai: rate limited: " + e.slotID }
func (e *rateLimitError) Unwrap() error { return errRateLimited }

// parseRetryAfter reads a Retry-After header (delay in seconds, possibly
// fractional — Groq sends e.g. "2" or "1.5"). HTTP-date form and junk yield 0.
func parseRetryAfter(v string) time.Duration {
	v = strings.TrimSpace(v)
	if v == "" {
		return 0
	}
	if secs, err := strconv.ParseFloat(v, 64); err == nil && secs > 0 {
		return time.Duration(secs * float64(time.Second))
	}
	return 0
}

// isFreeModel reports whether a model costs nothing to call: it's on a free-tier
// provider (Groq's free tier, local Ollama), or its id carries a free marker
// (:free / -free). The single source of truth for "safe to benchmark/adopt without
// spending" — used by discovery, FreeOnly, and adoption.
func isFreeModel(provider, model string) bool {
	return freeTierProviders[provider] ||
		strings.HasSuffix(model, ":free") || strings.HasSuffix(model, "-free")
}

// FreeOnly drops paid models (a metered provider without a free marker) so the
// benchmark never spends credits on them — this also prunes paid leftovers a
// pre-filter run had adopted into the chain (e.g. a Zen "big-pickle").
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

// retryAfterOf pulls the vendor's Retry-After out of a rate-limit error (0 if the
// error isn't a rateLimitError or carried no hint).
func retryAfterOf(err error) time.Duration {
	var rl *rateLimitError
	if errors.As(err, &rl) {
		return rl.RetryAfter
	}
	return 0
}

// noteChainFailure routes a chain-walk error to the right recovery:
//   - model gone (vendor removed it) → self-heal the provider in the background.
//   - rate limit (429) → park the WHOLE provider (shared free quota) for the
//     vendor's Retry-After (capped), so we don't hammer every model on a dead key.
//   - anything else → trip just this model's breaker.
func (c *Client) noteChainFailure(s Slot, err error) {
	switch {
	case errors.Is(err, errModelNotFound):
		go c.healProvider(s.Provider)
	case errors.Is(err, errRateLimited):
		c.breaker.recordRateLimit(s.Provider, retryAfterOf(err))
	default:
		c.breaker.recordFailure(s.ID)
	}
}

// looksPaymentError checks if a failed response is due to insufficient balance
// (paid model with no credits) vs a genuine error. These should be recorded
// as "pago — sem saldo" rather than a hard failure, so the benchmark knows the
// model exists but couldn't be tested.
func looksPaymentError(status int, body string) bool {
	if status == http.StatusPaymentRequired || status == http.StatusForbidden {
		return true
	}
	b := strings.ToLower(body)
	for _, p := range []string{
		"insufficient", "insufficient_quota", "quota exceeded", "quota_exceeded",
		"payment required", "payment_required", "insufficient balance",
		"exceeded your current quota", "rate limit exceeded", "billing",
		"insufficient_credits", "not enough credits", "credit limit",
		"user_rate_limit_exceeded", "forbidden",
	} {
		if strings.Contains(b, p) {
			return true
		}
	}
	return false
}

// looksModelNotFound maps the "this model doesn't exist" responses across the
// vendors we use — each phrases it differently:
//   - Groq:       404, code "model_not_found", "... does not exist"
//   - OpenRouter: 400/404, "is not a valid model", "No endpoints found for model"
//   - Ollama:     404, "model '...' not found" / "try pulling it"
func looksModelNotFound(status int, body string) bool {
	if status == http.StatusNotFound {
		return true
	}
	b := strings.ToLower(body)
	for _, p := range []string{
		"model_not_found", "does not exist", "is not a valid model",
		"not a valid model id", "no endpoints found for model", "model not found",
		"no such model", "try pulling", "decommissioned", "has been deprecated",
	} {
		if strings.Contains(b, p) {
			return true
		}
	}
	return false
}

// httpResponseError classifies a /chat/completions response status into one of
// the error sentinels (rate limit, model-not-found, no-balance, bad-output) or a
// generic error. Returns nil for a 2xx status. Kept out of chat() so the request
// path stays simple (and under the cognitive-complexity gate).
func httpResponseError(s Slot, status int, raw string, retryAfter time.Duration) error {
	if status >= 200 && status < 300 {
		return nil
	}
	if status == http.StatusTooManyRequests {
		return &rateLimitError{slotID: s.ID, RetryAfter: retryAfter}
	}
	if looksModelNotFound(status, raw) {
		return fmt.Errorf("%w: %s/%s", errModelNotFound, s.Provider, s.Model)
	}
	if looksPaymentError(status, raw) {
		return fmt.Errorf("%w: %s/%s — sem saldo", errInsufficientBalance, s.Provider, s.Model)
	}
	// A 400 is the vendor rejecting the model's own output (e.g. Groq's
	// json_validate_failed) — a quality failure of this model, not a transient
	// infra error like a 5xx. Mark it so the benchmark scores it as a 0.
	if status == http.StatusBadRequest {
		return fmt.Errorf("%w: %s returned 400: %s", errBadOutput, s.ID, strings.TrimSpace(raw))
	}
	return fmt.Errorf("ai: %s returned %d: %s", s.ID, status, strings.TrimSpace(raw))
}

func (c *Client) chat(ctx context.Context, s Slot, system, user string, jsonMode bool) (content string, latency time.Duration, tokens int, err error) {
	reqBody := chatReq{
		Model:       s.Model,
		Temperature: 0, // deterministic — we want the same title every time
		MaxTokens:   maxOutputTokens,
		Messages: []chatMessage{
			{Role: "system", Content: system},
			{Role: "user", Content: user},
		},
	}
	if jsonMode {
		reqBody.ResponseFormat = &respFormat{Type: "json_object"}
	}
	// gpt-oss are reasoning models: cap reasoning to "low" so the tiny JSON output
	// is reached fast (keeps latency down) and the token budget isn't burned on
	// chain-of-thought. Scoped to gpt-oss by name — Groq/OpenRouter honor it, and
	// we deliberately don't send it to other families (e.g. Qwen3 uses a different
	// reasoning knob, so a blanket value would be wrong there).
	if strings.Contains(s.Model, "gpt-oss") {
		reqBody.ReasoningEffort = "low"
	}
	body, _ := json.Marshal(reqBody)
	req, reqErr := http.NewRequestWithContext(ctx, http.MethodPost, s.BaseURL+"/chat/completions", bytes.NewReader(body))
	if reqErr != nil {
		return "", 0, 0, reqErr
	}
	req.Header.Set("Content-Type", "application/json")
	if s.apiKey != "" {
		req.Header.Set("Authorization", "Bearer "+s.apiKey)
	}

	start := time.Now()
	resp, doErr := c.http.Do(req)
	latency = time.Since(start)
	if doErr != nil {
		return "", latency, 0, doErr
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))

	if e := httpResponseError(s, resp.StatusCode, string(raw), parseRetryAfter(resp.Header.Get("Retry-After"))); e != nil {
		return "", latency, 0, e
	}

	var cr chatResp
	if e := json.Unmarshal(raw, &cr); e != nil {
		return "", latency, 0, fmt.Errorf("ai: %s bad json: %w", s.ID, e)
	}
	if cr.Error != nil && cr.Error.Message != "" {
		return "", latency, 0, fmt.Errorf("ai: %s error: %s", s.ID, cr.Error.Message)
	}
	if len(cr.Choices) == 0 {
		return "", latency, 0, fmt.Errorf("ai: %s returned no choices", s.ID)
	}
	return cr.Choices[0].Message.Content, latency, cr.Usage.TotalTokens, nil
}

// parseTitleJSON pulls the JSON object out of a model reply (possibly wrapped in
// prose or ```json fences). When a weaker free model ignores the JSON format and
// replies with just the title text, fall back to using that line as the title —
// better a usable title than a hard failure.
func parseTitleJSON(content string) (*TitleResult, error) {
	start := strings.IndexByte(content, '{')
	end := strings.LastIndexByte(content, '}')
	if start >= 0 && end > start {
		var res TitleResult
		if err := json.Unmarshal([]byte(content[start:end+1]), &res); err == nil {
			res.Title = strings.TrimSpace(res.Title)
			if res.Title != "" {
				if res.Kind == "" {
					res.Kind = "unknown"
				}
				return &res, nil
			}
		}
	}
	// Fallback: take the first non-empty line, stripped of quotes/markdown. Only
	// accept short, title-like text (reject multi-sentence prose).
	for _, line := range strings.Split(content, "\n") {
		t := strings.Trim(strings.TrimSpace(line), "`\"' #*-")
		if t != "" && len(t) <= 80 && !strings.Contains(t, ". ") {
			return &TitleResult{Title: t, Kind: "unknown"}, nil
		}
	}
	return nil, fmt.Errorf("ai: no usable title in reply")
}

// renameSystem is the production extraction prompt: it drives the AI rename
// feature AND the title cleaning before TMDB, and is exactly what the benchmark
// scores (metadataWithSlot). The few-shot examples must NEVER reuse a raw from
// DefaultBenchmarkCases — a model could copy the answer straight from its own
// prompt and inflate the benchmark (guarded by TestDefaultCasesNotInPrompts).
const renameSystem = `You extract structured metadata from raw torrent/release filenames (movies, TV episodes, season packs, anime, documentaries, live events, music, adult scenes) for organized file naming.

Reply with ONLY one raw JSON object, no prose, no code fences:
{"title": "", "year": 0, "kind": "movie" or "tv", "season": 0, "episode": 0, "episode_title": ""}

Field rules:
- "title": the clean canonical title.
  - Strip ALL technical noise: resolution (720p/1080p/2160p/4K/UHD), source (BluRay/REMUX/WEB-DL/WEBRip/HDTV/AMZN/NF/HULU/HMAX/CR), codec (x264/x265/H.264/HEVC/AV1/XviD/10bit), audio (DDP5.1/DD+/DTS/Atmos/AAC/FLAC/320kbps), HDR/DV/HDR10+, edition tags (REPACK/PROPER/EXTENDED/UNRATED/REMASTERED/Director's Cut/Final Cut/Special Edition/COMPLETE/PPV), language/dub tags (DUAL/MULTI/DUBLADO/LEGENDADO/NACIONAL/FRENCH/GERMAN/SPANISH/KOREAN/JAPANESE), bracketed ids/checksums, file extensions, leading website tags ("www.Site.com - ", "[ Site.xx ]") and the trailing release group.
  - Replace dots/underscores with spaces; restore natural capitalization; KEEP the title's own punctuation, accents and language exactly — never translate ("Divertida Mente 2" stays "Divertida Mente 2").
  - KEEP numbers that belong to the title: "Blade Runner 2049", "Wonder Woman 1984", "1917", "2012", "UFC 300".
  - TV/anime: the title is the SERIES name only — never include SxxEyy, episode numbers, "Season N" or "COMPLETE" in it.
  - Anime: use the romanized title as written in the filename.
  - Music: "Artist - Album" when both are present, else just the artist.
  - Live events (UFC/F1/WWE): keep the event name, number and bout/session ("UFC 299 O'Malley vs Vera 2").
  - Adult scene "studio.YY.MM.DD.performer.name.scene.description.XXX": produce "Studio - Performer Name - Scene Description" — keep the performer and scene description, never collapse to just the studio. If in doubt, keep more detail.
- "year": the RELEASE year (integer, 0 if unknown). It is the standalone 4-digit year next to the quality tags, NOT a number that is part of the title. Adult date tokens like "24.03.15" mean 2024.
- "kind": "tv" for series/anime episodes and season packs, else "movie".
- "season"/"episode": integers, only for tv (else 0). "S03E07" or "3x07" → season 3, episode 7. A season pack ("S01", "Season 1", "S01.COMPLETE") → season set, episode 0. Anime with absolute numbering ("[Group] Title - 05") → episode 5, season 0 unless explicit.
- "episode_title": only when explicitly present in the filename, else "".

Examples:
The.Dark.Knight.2008.1080p.BluRay.x264-REFiNED → {"title":"The Dark Knight","year":2008,"kind":"movie","season":0,"episode":0,"episode_title":""}
Class.of.1999.1990.720p.BluRay.x264-SADPANDA → {"title":"Class of 1999","year":1990,"kind":"movie","season":0,"episode":0,"episode_title":""}
The.Sopranos.S02E04.Commendatori.720p.HDTV.x264 → {"title":"The Sopranos","year":0,"kind":"tv","season":2,"episode":4,"episode_title":"Commendatori"}
True.Detective.S01.COMPLETE.1080p.BluRay.x264-DEMAND → {"title":"True Detective","year":0,"kind":"tv","season":1,"episode":0,"episode_title":""}
[SubsPlease] Yofukashi no Uta - 03 (1080p) [1A2B3C4D].mkv → {"title":"Yofukashi no Uta","year":0,"kind":"tv","season":0,"episode":3,"episode_title":""}
Central.do.Brasil.1998.NACIONAL.1080p.BluRay.x264-TROPiX → {"title":"Central do Brasil","year":1998,"kind":"movie","season":0,"episode":0,"episode_title":""}
www.TamilMV.re - Mission.Impossible.Fallout.2018.1080p.WEB-DL → {"title":"Mission Impossible Fallout","year":2018,"kind":"movie","season":0,"episode":0,"episode_title":""}
Daft.Punk.Random.Access.Memories.2013.FLAC.24bit → {"title":"Daft Punk - Random Access Memories","year":2013,"kind":"movie","season":0,"episode":0,"episode_title":""}
Vixen.23.05.20.Eva.Elfie.Sun.Kissed.XXX.1080p.MP4-WRB → {"title":"Vixen - Eva Elfie - Sun Kissed","year":2023,"kind":"movie","season":0,"episode":0,"episode_title":""}`

type RenameMetadata struct {
	Title        string `json:"title"`
	Year         int    `json:"year"`
	Kind         string `json:"kind"` // "movie" | "tv"
	Season       int    `json:"season"`
	Episode      int    `json:"episode"`
	EpisodeTitle string `json:"episode_title"`
}

func (c *Client) ExtractRenameMetadata(ctx context.Context, rawName string) (*RenameMetadata, string, error) {
	return c.ExtractRenameMetadataWithContext(ctx, rawName, "")
}

// ExtractRenameMetadataWithContext is ExtractRenameMetadata plus an optional
// taxonomy hint. The USER message stays the bare rawName (so title extraction
// is unaffected and the benchmark/parser behaviour is identical); the hint is
// appended to the SYSTEM prompt as an extra instruction telling the model which
// destination category folders already exist and to prefer reusing one. An
// empty hint is the exact legacy call.
func (c *Client) ExtractRenameMetadataWithContext(ctx context.Context, rawName, taxonomyHint string) (*RenameMetadata, string, error) {
	if c == nil {
		return nil, "", errors.New("ai client not initialized")
	}
	system := renameSystem
	if taxonomyHint != "" {
		system = renameSystem + "\n\n" + taxonomyHint
	}
	var lastErr error
	for _, s := range c.slotList() {
		if !c.breaker.available(s.Provider, s.ID) {
			continue
		}
		content, _, _, err := c.chat(ctx, s, system, rawName, true)
		if err != nil {
			lastErr = err
			c.noteChainFailure(s, err)
			continue
		}
		c.breaker.recordSuccess(s.Provider, s.ID)
		res, perr := parseRenameJSON(content)
		if perr == nil && res != nil && res.Title != "" {
			return res, s.ID, nil
		}
	}
	return nil, "", lastErr
}

func parseRenameJSON(content string) (*RenameMetadata, error) {
	start := strings.IndexByte(content, '{')
	end := strings.LastIndexByte(content, '}')
	if start >= 0 && end > start {
		var res RenameMetadata
		if err := json.Unmarshal([]byte(content[start:end+1]), &res); err == nil {
			res.Title = strings.TrimSpace(res.Title)
			if res.Title != "" {
				if res.Kind != "movie" && res.Kind != "tv" {
					res.Kind = "movie" // fallback
				}
				return &res, nil
			}
		}
	}
	// Fallback to simple parse using the generic parseTitleJSON fallback
	tr, err := parseTitleJSON(content)
	if err == nil && tr != nil {
		return &RenameMetadata{
			Title: tr.Title,
			Year:  tr.Year,
			Kind:  tr.Kind,
		}, nil
	}
	return nil, fmt.Errorf("ai: no usable rename metadata in reply")
}
