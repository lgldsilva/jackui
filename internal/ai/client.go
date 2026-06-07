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
	"strings"
	"sync"
	"time"

	"github.com/luizg/jackui/internal/config"
)

// Slot is one resolved chain entry — a provider's base URL + key bound to a
// specific model id.
type Slot struct {
	ID       string
	Provider string
	Model    string
	BaseURL  string
	apiKey   string
	Free     bool // true when the model is free (no billing cost)
	Local    bool // true for a model served by the LOCAL Ollama GPU (see localModel)
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
	return Slot{ID: id, Provider: provider, Model: model, BaseURL: strings.TrimRight(p.BaseURL, "/"), apiKey: p.APIKey, Local: localModel(provider, model)}, true
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

const identifySystem = `You extract the canonical movie or TV show title from a raw torrent/release name.
Strip resolution, codec, release group, language and season/episode tags.
Reply with ONLY a JSON object, no prose, no code fences:
{"title": "<clean title>", "year": <release year or 0>, "kind": "movie" | "tv" | "unknown"}`

// IdentifyTitle walks the chain until a slot returns a usable title. Returns the
// result and the slot id that produced it. A nil result with nil error means no
// slot could parse a title (caller falls back to regex cleaning).
func (c *Client) IdentifyTitle(ctx context.Context, rawName string) (*TitleResult, string, error) {
	var lastErr error
	for _, s := range c.slotList() {
		if !c.breaker.available(s.ID) {
			continue
		}
		res, _, err := c.identifyWithSlot(ctx, s, rawName)
		if err != nil {
			lastErr = err
			// A model that no longer exists (vendor removed/renamed it) means the
			// chain is stale — self-heal that provider in the background (cheap
			// discovery, no scoring). Otherwise just back off via the breaker.
			if errors.Is(err, errModelNotFound) {
				go c.healProvider(s.Provider)
			} else {
				c.breaker.recordFailure(s.ID, isRateLimit(err))
			}
			continue
		}
		c.breaker.recordSuccess(s.ID)
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
		if !c.breaker.available(s.ID) {
			continue
		}
		content, _, err := c.chat(ctx, s, musicSystem, rawName, false)
		if err != nil {
			if errors.Is(err, errModelNotFound) {
				go c.healProvider(s.Provider)
			} else {
				c.breaker.recordFailure(s.ID, isRateLimit(err))
			}
			continue
		}
		c.breaker.recordSuccess(s.ID)
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
	content, latency, err := c.chat(ctx, s, identifySystem, rawName, true)
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
func (c *Client) metadataWithSlot(ctx context.Context, s Slot, rawName string) (*RenameMetadata, time.Duration, error) {
	content, latency, err := c.chat(ctx, s, renameSystem, rawName, true)
	if err != nil {
		return nil, latency, err
	}
	res, perr := parseRenameJSON(content)
	if perr != nil {
		return nil, latency, fmt.Errorf("%w: %v", errBadOutput, perr)
	}
	return res, latency, nil
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

func isRateLimit(err error) bool { return errors.Is(err, errRateLimited) }

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
func httpResponseError(s Slot, status int, raw string) error {
	if status >= 200 && status < 300 {
		return nil
	}
	if status == http.StatusTooManyRequests {
		return fmt.Errorf("%w: %s", errRateLimited, s.ID)
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

func (c *Client) chat(ctx context.Context, s Slot, system, user string, jsonMode bool) (string, time.Duration, error) {
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
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, s.BaseURL+"/chat/completions", bytes.NewReader(body))
	if err != nil {
		return "", 0, err
	}
	req.Header.Set("Content-Type", "application/json")
	if s.apiKey != "" {
		req.Header.Set("Authorization", "Bearer "+s.apiKey)
	}

	start := time.Now()
	resp, err := c.http.Do(req)
	latency := time.Since(start)
	if err != nil {
		return "", latency, err
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))

	if err := httpResponseError(s, resp.StatusCode, string(raw)); err != nil {
		return "", latency, err
	}

	var cr chatResp
	if err := json.Unmarshal(raw, &cr); err != nil {
		return "", latency, fmt.Errorf("ai: %s bad json: %w", s.ID, err)
	}
	if cr.Error != nil && cr.Error.Message != "" {
		return "", latency, fmt.Errorf("ai: %s error: %s", s.ID, cr.Error.Message)
	}
	if len(cr.Choices) == 0 {
		return "", latency, fmt.Errorf("ai: %s returned no choices", s.ID)
	}
	return cr.Choices[0].Message.Content, latency, nil
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

const renameSystem = `You analyze raw media filenames and extract metadata for organized file naming.
Content can be a mainstream movie, TV show episode, or adult scene.

Extract the following fields as JSON:
- "title": The best descriptive title preserving all meaningful info.
  - Mainstream movie/show: use the canonical title (e.g. "Breaking Bad").
  - Adult scene with pattern "studio.YY.MM.DD.model.name.scene.description.xxx":
    produce "Studio - Model Name - Scene Description" — keep the model name and scene
    description, never collapse to just the studio/brand name.
  - Do NOT strip descriptive parts. If in doubt, keep more detail.
- "year": Release year (integer, 0 if unknown). Date tokens like "26.03.29" mean 2026.
- "kind": "movie" or "tv".
- "season": Season number (integer, only for tv, else 0).
- "episode": Episode number (integer, only for tv, else 0).
- "episode_title": Episode title only if explicitly present in filename, otherwise "".

Reply with ONLY the raw JSON object, no prose, no code fences.`

type RenameMetadata struct {
	Title        string `json:"title"`
	Year         int    `json:"year"`
	Kind         string `json:"kind"` // "movie" | "tv"
	Season       int    `json:"season"`
	Episode      int    `json:"episode"`
	EpisodeTitle string `json:"episode_title"`
}

func (c *Client) ExtractRenameMetadata(ctx context.Context, rawName string) (*RenameMetadata, string, error) {
	if c == nil {
		return nil, "", errors.New("ai client not initialized")
	}
	var lastErr error
	for _, s := range c.slotList() {
		if !c.breaker.available(s.ID) {
			continue
		}
		content, _, err := c.chat(ctx, s, renameSystem, rawName, true)
		if err != nil {
			lastErr = err
			if errors.Is(err, errModelNotFound) {
				go c.healProvider(s.Provider)
			} else {
				c.breaker.recordFailure(s.ID, isRateLimit(err))
			}
			continue
		}
		c.breaker.recordSuccess(s.ID)
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
