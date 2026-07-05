package ai

import (
	"bytes"
	"context"
	"encoding/json"
	"log"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/lgldsilva/jackui/internal/config"
)

// Descoberta de modelos/provedores (DiscoverModels/listModels/healProvider…) — extraído de benchmark.go.
// DiscoverOllamaModels queries the local Ollama (/api/tags) for models it serves and
// returns a Slot per model that isn't already in the chain — so the benchmark can test
// EVERY available model, not just the one wired into the chain. This includes Ollama
// CLOUD models (the "-cloud" suffix): recent Ollama registers them locally and lists
// them in /api/tags, so they're discovered here too (ollamaCanComplete keeps them —
// /api/show for a remote model doesn't report capabilities, and on doubt we keep). They
// resolve as Free (the ollama provider is free-tier) but non-Local (see localModel), so
// the benchmark parallelizes them instead of serializing on the single GPU.
func (c *Client) DiscoverOllamaModels(ctx context.Context) []Slot {
	// Find the ollama provider's base URL from the chain.
	var base, key string
	for _, s := range c.slots {
		if s.Provider == "ollama" {
			base, key = s.BaseURL, s.apiKey
			break
		}
	}
	if base == "" {
		return nil
	}
	existing := map[string]bool{}
	for _, s := range c.slots {
		existing[s.Provider+"|"+s.Model] = true
	}
	// /api/tags lives at the server root, not under the OpenAI-compat /v1.
	tagsURL := strings.TrimSuffix(strings.TrimRight(base, "/"), "/v1") + "/api/tags"
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, tagsURL, nil)
	resp, err := c.http.Do(req)
	if err != nil {
		return nil
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil
	}
	var tags struct {
		Models []struct {
			Name string `json:"name"`
		} `json:"models"`
	}
	if json.NewDecoder(resp.Body).Decode(&tags) != nil {
		return nil
	}
	root := strings.TrimSuffix(strings.TrimRight(base, "/"), "/v1")
	var out []Slot
	for _, m := range tags.Models {
		if m.Name == "" || existing["ollama|"+m.Name] {
			continue
		}
		// Skip models that can't do text completion (e.g. embedding models like
		// nomic-embed-text) — they can never extract a title and would just clutter
		// the benchmark with permanent 0% failures. Decided by Ollama's OWN
		// capability metadata, not a name heuristic. On any doubt (old Ollama, error)
		// we keep the model rather than over-filter.
		if !ollamaCanComplete(ctx, c.http, root, m.Name) {
			continue
		}
		existing["ollama|"+m.Name] = true
		out = append(out, Slot{ID: "ollama:" + m.Name, Provider: "ollama", Model: m.Name, BaseURL: base, apiKey: key, Free: true, Local: localModel("ollama", m.Name)})
	}
	return out
}

// ollamaCanComplete asks Ollama (/api/show) whether a model supports text
// "completion". Returns true on any uncertainty (call/parse error, or capabilities
// not reported by an older Ollama) so we never drop a usable model — it returns
// false ONLY when Ollama EXPLICITLY lists capabilities without "completion" (e.g.
// an embedding-only model like nomic-embed-text → ["embedding"]).
func ollamaCanComplete(ctx context.Context, hc *http.Client, root, model string) bool {
	body, _ := json.Marshal(map[string]string{"model": model})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, root+"/api/show", bytes.NewReader(body))
	if err != nil {
		return true
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := hc.Do(req)
	if err != nil {
		return true
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return true
	}
	var show struct {
		Capabilities []string `json:"capabilities"`
	}
	if json.NewDecoder(resp.Body).Decode(&show) != nil || len(show.Capabilities) == 0 {
		return true // old Ollama / no metadata → don't filter
	}
	for _, capability := range show.Capabilities {
		if capability == "completion" {
			return true
		}
	}
	return false
}

// freeTierProviders bill nothing per token — local Ollama, and Groq's
// rate-limited free tier. The benchmark may discover their WHOLE catalog. Every
// other provider is treated as metered/paid (OpenRouter, OpenCode Zen, and any
// unknown provider — paid by default): discovery there is limited to models we can
// positively tell are free, so benchmarking costly frontier models can't quietly
// burn credits/quota. See discoverViaModelsAPI.
var freeTierProviders = map[string]bool{"groq": true, "ollama": true}

// DiscoverModels lists candidate models across ALL providers so the benchmark
// can test every available model, not just one per provider from the chain:
//   - ollama:      every locally-installed model (/api/tags)
//   - free tier:   the whole /v1/models catalog (Groq) — nothing is billed
//   - metered:     ONLY models we can prove are free (a :free/-free id, or an
//     explicit 0 price). Paid frontier models on OpenRouter / OpenCode Zen are
//     ignored so the benchmark can't burn credits. (Zen returns no pricing at
//     all, so the suffix is the only signal there.)
//
// Skips models already in the chain. Caps at 100 per provider so the benchmark
// doesn't take hours on OpenRouter's 300+ model catalog.
func (c *Client) DiscoverModels(ctx context.Context) []Slot {
	existing := map[string]bool{}
	for _, s := range c.slots {
		existing[s.Provider+"|"+s.Model] = true
	}
	var out []Slot
	out = append(out, c.DiscoverOllamaModels(ctx)...)
	for _, s := range out {
		existing[s.Provider+"|"+s.Model] = true
	}
	for name, p := range c.providers {
		if name == "ollama" || p.BaseURL == "" {
			continue // ollama handled above; skip provider-less
		}
		out = append(out, c.discoverViaModelsAPI(ctx, name, p, 100, existing)...)
	}
	return out
}

// DiscoverModelsForProvider lists candidate models for a specific provider.
func (c *Client) DiscoverModelsForProvider(ctx context.Context, provider string) []Slot {
	existing := map[string]bool{}
	for _, s := range c.slots {
		existing[s.Provider+"|"+s.Model] = true
	}
	var out []Slot
	if provider == "ollama" {
		out = append(out, c.DiscoverOllamaModels(ctx)...)
	} else {
		p, ok := c.providers[provider]
		if ok && p.BaseURL != "" {
			out = append(out, c.discoverViaModelsAPI(ctx, provider, p, 100, existing)...)
		}
	}
	return out
}

// modelCostPer1M turns OpenAI-style per-TOKEN pricing (strings, USD) into a
// blended (prompt+completion)/2 price per 1M tokens. known=false when neither
// field is numeric — e.g. OpenCode Zen, which returns no pricing at all.
func modelCostPer1M(promptStr, completionStr string) (cost float64, known bool) {
	p, perr := strconv.ParseFloat(strings.TrimSpace(promptStr), 64)
	cmp, cerr := strconv.ParseFloat(strings.TrimSpace(completionStr), 64)
	if perr != nil && cerr != nil {
		return 0, false
	}
	return (p + cmp) / 2 * 1_000_000, true
}

// discoverViaModelsAPI hits the OpenAI-compatible GET /models on a provider.
func (c *Client) discoverViaModelsAPI(ctx context.Context, name string, p config.AIProvider, capN int, existing map[string]bool) []Slot {
	ceiling := c.CostConfig().MaxCostPer1M
	base := strings.TrimRight(p.BaseURL, "/")
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, base+"/models", nil)
	if p.APIKey != "" {
		req.Header.Set("Authorization", "Bearer "+p.APIKey)
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return nil
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil
	}
	var body struct {
		Data []struct {
			ID      string `json:"id"`
			Pricing struct {
				Prompt     string `json:"prompt"`
				Completion string `json:"completion"`
			} `json:"pricing"`
		} `json:"data"`
	}
	if json.NewDecoder(resp.Body).Decode(&body) != nil {
		return nil
	}
	var out []Slot
	for _, m := range body.Data {
		if m.ID == "" || existing[name+"|"+m.ID] || len(out) >= capN {
			continue
		}
		// Cost in USD per 1M tokens. Free-tier providers and :free/-free ids are 0;
		// a numeric price is blended; absent pricing (OpenCode Zen) is UNKNOWN.
		var cost float64
		known := true
		if c.isFreeModel(name, m.ID) {
			cost = 0
		} else {
			cost, known = modelCostPer1M(m.Pricing.Prompt, m.Pricing.Completion)
		}
		// Only discover models we can PAY for: known cost within the ceiling (0 =
		// free only, the default). Unknown-cost models (no pricing) are never
		// auto-tested — that's what keeps Zen's costly frontier models out.
		if !known || cost > ceiling {
			continue
		}
		existing[name+"|"+m.ID] = true
		out = append(out, Slot{ID: name + ":" + m.ID, Provider: name, Model: m.ID, BaseURL: base, apiKey: p.APIKey, Free: cost == 0, CostPer1M: cost})
	}
	return out
}

// listModels returns the model ids currently advertised by a provider (Ollama:
// /api/tags; others: OpenAI-compatible /models). Used by self-heal to verify a
// failing model really is gone and to pick a replacement. Empty on any error.
func (c *Client) listModels(ctx context.Context, provider string, p config.AIProvider) []string {
	base := strings.TrimRight(p.BaseURL, "/")
	if provider == "ollama" {
		return c.listOllamaModels(ctx, base)
	}
	return c.listOpenAIModels(ctx, base, p.APIKey)
}

func (c *Client) listOllamaModels(ctx context.Context, base string) []string {
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, strings.TrimSuffix(base, "/v1")+"/api/tags", nil)
	resp, err := c.http.Do(req)
	if err != nil || resp.StatusCode != http.StatusOK {
		if resp != nil {
			resp.Body.Close()
		}
		return nil
	}
	defer resp.Body.Close()
	var tags struct {
		Models []struct {
			Name string `json:"name"`
		} `json:"models"`
	}
	if json.NewDecoder(resp.Body).Decode(&tags) != nil {
		return nil
	}
	var out []string
	for _, m := range tags.Models {
		if m.Name != "" {
			out = append(out, m.Name)
		}
	}
	return out
}

func (c *Client) listOpenAIModels(ctx context.Context, base, apiKey string) []string {
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, base+"/models", nil)
	if apiKey != "" {
		req.Header.Set("Authorization", "Bearer "+apiKey)
	}
	resp, err := c.http.Do(req)
	if err != nil || resp.StatusCode != http.StatusOK {
		if resp != nil {
			resp.Body.Close()
		}
		return nil
	}
	defer resp.Body.Close()
	var body struct {
		Data []struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	if json.NewDecoder(resp.Body).Decode(&body) != nil {
		return nil
	}
	var out []string
	for _, m := range body.Data {
		if m.ID != "" {
			out = append(out, m.ID)
		}
	}
	return out
}

// healProvider self-heals a provider whose chain model returned "doesn't exist":
// it re-lists the provider's models, drops any chain slot whose model is gone,
// and — if that left the provider unrepresented — adds a still-valid model
// (OpenRouter prefers a free one) so the chain keeps a working slot. Cheap
// (one /models or /api/tags call, no scoring), deduped per provider, runtime-only
// (a manual benchmark re-optimizes + persists).
func (c *Client) healProvider(provider string) {
	if _, busy := c.healing.LoadOrStore(provider, true); busy {
		return
	}
	defer c.healing.Delete(provider)
	p, ok := c.providers[provider]
	if !ok || p.BaseURL == "" {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	ids := c.listModels(ctx, provider, p)
	if len(ids) == 0 {
		return
	}
	avail := toSet(ids)

	c.mu.Lock()
	defer c.mu.Unlock()

	kept, dropped, providerHas := filterSlotsByAvail(c.slots, provider, avail)
	if !dropped {
		return
	}
	if !providerHas {
		kept = append(kept, c.pickReplacementSlot(provider, ids, p))
	}
	c.slots = kept
}

func toSet(ids []string) map[string]bool {
	avail := map[string]bool{}
	for _, id := range ids {
		avail[id] = true
	}
	return avail
}

func filterSlotsByAvail(slots []Slot, provider string, avail map[string]bool) ([]Slot, bool, bool) {
	var kept []Slot
	dropped := false
	providerHas := false
	for _, s := range slots {
		if s.Provider == provider && !avail[s.Model] {
			dropped = true
			log.Printf("ai: self-heal — %s model %q no longer exists; removing from chain", provider, s.Model)
			continue
		}
		kept = append(kept, s)
		if s.Provider == provider {
			providerHas = true
		}
	}
	return kept, dropped, providerHas
}

func (c *Client) pickReplacementSlot(provider string, ids []string, p config.AIProvider) Slot {
	repl := ids[0]
	if provider == "openrouter" {
		for _, id := range ids {
			if strings.HasSuffix(id, ":free") {
				repl = id
				break
			}
		}
	}
	base := strings.TrimRight(p.BaseURL, "/")
	cost := -1.0
	if c.isFreeModel(provider, repl) {
		cost = 0
	}
	log.Printf("ai: self-heal — added %s replacement %q (untested; run the benchmark to re-optimize)", provider, repl)
	return Slot{ID: provider + ":" + repl, Provider: provider, Model: repl, BaseURL: base, apiKey: p.APIKey, Free: cost == 0, Local: localModel(provider, repl), CostPer1M: cost}
}
