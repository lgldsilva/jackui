package config

import (
	"encoding/json"
	"net/http"
	"strings"
	"time"
)

// Descoberta/seleção de modelos de IA (fetchModels/pickModel/match*) — extraído de config.go.
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
