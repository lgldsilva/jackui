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
	slots   []Slot
	breaker *breaker
	http    *http.Client
}

// New builds a Client from config. Returns nil when AI is disabled or no usable
// chain slot resolves — callers treat nil as "no AI, use the regex fallback".
func New(cfg config.AIConfig) *Client {
	if !cfg.Enabled {
		return nil
	}
	var slots []Slot
	for _, s := range cfg.Chain {
		if s.Disabled {
			continue
		}
		p, ok := cfg.Providers[s.Provider]
		if !ok || p.BaseURL == "" {
			continue
		}
		slots = append(slots, Slot{
			ID:       s.ID,
			Provider: s.Provider,
			Model:    s.Model,
			BaseURL:  strings.TrimRight(p.BaseURL, "/"),
			apiKey:   p.APIKey,
		})
	}
	if len(slots) == 0 {
		return nil
	}
	return &Client{
		slots:   slots,
		breaker: newBreaker(),
		http:    &http.Client{Timeout: 20 * time.Second},
	}
}

// Slots returns a copy of the resolved chain (for the benchmark + status UI).
func (c *Client) Slots() []Slot {
	out := make([]Slot, len(c.slots))
	copy(out, c.slots)
	return out
}

const identifySystem = `You extract the canonical movie or TV show title from a raw torrent/release name.
Strip resolution, codec, release group, language and season/episode tags.
Reply with ONLY a JSON object, no prose, no code fences:
{"title": "<clean title>", "year": <release year or 0>, "kind": "movie" | "tv" | "unknown"}`

// IdentifyTitle walks the chain until a slot returns a usable title. Returns the
// result and the slot id that produced it. A nil result with nil error means no
// slot could parse a title (caller falls back to regex cleaning).
func (c *Client) IdentifyTitle(ctx context.Context, rawName string) (*TitleResult, string, error) {
	var lastErr error
	for _, s := range c.slots {
		if !c.breaker.available(s.ID) {
			continue
		}
		res, _, err := c.identifyWithSlot(ctx, s, rawName)
		if err != nil {
			lastErr = err
			c.breaker.recordFailure(s.ID, isRateLimit(err))
			continue
		}
		c.breaker.recordSuccess(s.ID)
		if res != nil && res.Title != "" {
			return res, s.ID, nil
		}
	}
	return nil, "", lastErr
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
	content, latency, err := c.chat(ctx, s, identifySystem, rawName)
	if err != nil {
		return nil, latency, err
	}
	res, perr := parseTitleJSON(content)
	if perr != nil {
		return nil, latency, perr
	}
	return res, latency, nil
}

// ─── OpenAI-compatible /chat/completions ─────────────────────────────────────

type chatReq struct {
	Model       string        `json:"model"`
	Messages    []chatMessage `json:"messages"`
	Temperature float64       `json:"temperature"`
	MaxTokens   int           `json:"max_tokens"`
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

func isRateLimit(err error) bool { return errors.Is(err, errRateLimited) }

func (c *Client) chat(ctx context.Context, s Slot, system, user string) (string, time.Duration, error) {
	body, _ := json.Marshal(chatReq{
		Model:       s.Model,
		Temperature: 0, // deterministic — we want the same title every time
		MaxTokens:   200,
		Messages: []chatMessage{
			{Role: "system", Content: system},
			{Role: "user", Content: user},
		},
	})
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

	if resp.StatusCode == http.StatusTooManyRequests {
		return "", latency, fmt.Errorf("%w: %s", errRateLimited, s.ID)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", latency, fmt.Errorf("ai: %s returned %d: %s", s.ID, resp.StatusCode, strings.TrimSpace(string(raw)))
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

// parseTitleJSON pulls the JSON object out of a model reply that may be wrapped
// in prose or ```json fences, then validates it has a title.
func parseTitleJSON(content string) (*TitleResult, error) {
	start := strings.IndexByte(content, '{')
	end := strings.LastIndexByte(content, '}')
	if start < 0 || end <= start {
		return nil, fmt.Errorf("ai: no json object in reply")
	}
	var res TitleResult
	if err := json.Unmarshal([]byte(content[start:end+1]), &res); err != nil {
		return nil, fmt.Errorf("ai: parse title json: %w", err)
	}
	res.Title = strings.TrimSpace(res.Title)
	if res.Kind == "" {
		res.Kind = "unknown"
	}
	return &res, nil
}
