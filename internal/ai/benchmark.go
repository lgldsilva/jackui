package ai

import (
	"context"
	"encoding/json"
	"log"
	"math"
	"net/http"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/luizg/jackui/internal/config"
)

// BenchmarkCase is one labelled example: a raw torrent/release name and the
// title we expect the model to extract. The set is user-editable (persisted in
// the benchmark store) — that's the "modifiable" part: tune it to the kind of
// releases you actually download and the chain re-ranks for them.
type BenchmarkCase struct {
	Raw    string `json:"raw"`
	Expect string `json:"expect"`
}

// SlotScore is one model's aggregate result over the whole case set.
type SlotScore struct {
	SlotID        string  `json:"slotId"`
	Provider      string  `json:"provider"`
	Model         string  `json:"model"`
	Accuracy      float64 `json:"accuracy"`     // 0..1 mean over cases
	AvgLatencyMs  int64   `json:"avgLatencyMs"` // mean wall-clock per call
	Composite     float64 `json:"composite"`    // accuracy / sqrt(latencySeconds)
	Samples       int     `json:"samples"`      // cases that produced a usable reply
	FailureReason string  `json:"failureReason,omitempty"`
}

// DefaultBenchmarkCases seeds a fresh store. Picked to exercise the hard parts:
// dotted names, scene tags, season/episode packs, non-English, and bracketed
// release-group noise.
var DefaultBenchmarkCases = []BenchmarkCase{
	{Raw: "Inception.2010.1080p.BluRay.x264-SPARKS", Expect: "Inception"},
	{Raw: "The.Matrix.1999.2160p.UHD.BluRay.x265-TERMINAL", Expect: "The Matrix"},
	{Raw: "Breaking.Bad.S03E07.720p.HDTV.x264-CTU", Expect: "Breaking Bad"},
	{Raw: "Dune.Part.Two.2024.1080p.WEB-DL.DDP5.1.Atmos.H.264-FLUX", Expect: "Dune Part Two"},
	{Raw: "[Erai-raws] Frieren - 01 [1080p][Multiple Subtitle]", Expect: "Frieren"},
	{Raw: "O.Auto.da.Compadecida.2000.DUBLADO.1080p", Expect: "O Auto da Compadecida"},
}

// compositeScore mirrors SelfAgent's ranking: quality divided by the square root
// of latency in seconds. The sqrt softens the latency penalty so a slightly
// slower but more accurate model can still win. A 0.3s floor stops a sub-300ms
// call from inflating the score to nonsense.
func compositeScore(accuracy float64, avgLatencyMs int64) float64 {
	seconds := math.Max(0.3, float64(avgLatencyMs)/1000.0)
	return accuracy / math.Sqrt(seconds)
}

var alnumRe = regexp.MustCompile(`[^a-z0-9]+`)

// titleAccuracy scores how well `got` matches `expect`: 1.0 for an exact match
// (after normalization), otherwise the Jaccard overlap of word tokens so a
// near-miss ("Dune" vs "Dune Part Two") still earns partial credit.
func titleAccuracy(got, expect string) float64 {
	ng, ne := normalizeTitle(got), normalizeTitle(expect)
	if ne == "" {
		return 0
	}
	if ng == ne {
		return 1
	}
	gt, et := tokenSet(ng), tokenSet(ne)
	if len(gt) == 0 {
		return 0
	}
	inter := 0
	for w := range gt {
		if et[w] {
			inter++
		}
	}
	union := len(gt) + len(et) - inter
	if union == 0 {
		return 0
	}
	return float64(inter) / float64(union)
}

func normalizeTitle(s string) string {
	return strings.Trim(alnumRe.ReplaceAllString(strings.ToLower(s), " "), " ")
}

func tokenSet(s string) map[string]bool {
	set := map[string]bool{}
	for _, w := range strings.Fields(s) {
		set[w] = true
	}
	return set
}

// Run benchmarks the configured chain. See RunSlots.
func (c *Client) Run(ctx context.Context, cases []BenchmarkCase) []SlotScore {
	return c.RunSlots(ctx, c.slots, cases)
}

// RunSlots benchmarks the given slots against the case set and returns scores
// sorted by composite (best first). Each slot is called directly (bypassing the
// breaker) so a parked model still gets measured. Used with the configured chain
// AND with discovered local Ollama models.
func (c *Client) RunSlots(ctx context.Context, slots []Slot, cases []BenchmarkCase) []SlotScore {
	if len(cases) == 0 {
		cases = DefaultBenchmarkCases
	}
	// Local (Ollama) and remote slots are benchmarked differently:
	//   - Local: SEQUENTIAL. Each needs a VRAM warmup, and Ollama serves one model
	//     at a time — running several at once would thrash models in/out of memory,
	//     wrecking both the warmup and the latency measurement.
	//   - Remote: PARALLEL. Independent HTTP endpoints with no shared-memory
	//     constraint, so we fan out to cut wall-clock (one slow vendor no longer
	//     blocks the rest). Their free tiers are rate-limited, so no warmup.
	var local, remote []Slot
	for _, s := range slots {
		if s.Provider == "ollama" {
			local = append(local, s)
		} else {
			remote = append(remote, s)
		}
	}

	scores := make([]SlotScore, 0, len(slots))

	if len(remote) > 0 {
		results := make([]SlotScore, len(remote))
		var wg sync.WaitGroup
		for i, s := range remote {
			wg.Add(1)
			go func(i int, s Slot) {
				defer wg.Done()
				results[i] = c.scoreSlot(ctx, s, cases, false)
			}(i, s)
		}
		wg.Wait()
		scores = append(scores, results...)
	}

	for _, s := range local {
		scores = append(scores, c.scoreSlot(ctx, s, cases, true))
	}

	sort.SliceStable(scores, func(i, j int) bool { return scores[i].Composite > scores[j].Composite })
	return scores
}

// scoreSlot runs the full case set against one slot and aggregates the result.
// When warmup is true (local Ollama) it issues one untimed priming call first so
// the model is resident in VRAM before the timed cases run.
func (c *Client) scoreSlot(ctx context.Context, s Slot, cases []BenchmarkCase, warmup bool) SlotScore {
	score := SlotScore{SlotID: s.ID, Provider: s.Provider, Model: s.Model}
	if warmup {
		warmCtx, warmCancel := context.WithTimeout(ctx, 120*time.Second)
		_, _, _ = c.identifyWithSlot(warmCtx, s, "warmup")
		warmCancel()
	}
	var accSum float64
	var latSum time.Duration
	for _, tc := range cases {
		res, latency, err := c.identifyWithSlot(ctx, s, tc.Raw)
		if err != nil {
			if score.FailureReason == "" {
				score.FailureReason = err.Error()
			}
			continue
		}
		score.Samples++
		latSum += latency
		if res != nil {
			accSum += titleAccuracy(res.Title, tc.Expect)
		}
	}
	if score.Samples > 0 {
		score.Accuracy = accSum / float64(score.Samples)
		score.AvgLatencyMs = (latSum / time.Duration(score.Samples)).Milliseconds()
		score.Composite = compositeScore(score.Accuracy, score.AvgLatencyMs)
	}
	return score
}

// DiscoverOllamaModels queries the local Ollama (/api/tags) for installed models
// and returns a Slot per model that isn't already in the chain — so the benchmark
// can test EVERY local model, not just the one wired into the chain. Cloud models
// aren't listed by /api/tags (they're remote), so they stay explicit in config.
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
	var out []Slot
	for _, m := range tags.Models {
		if m.Name == "" || existing["ollama|"+m.Name] {
			continue
		}
		existing["ollama|"+m.Name] = true
		out = append(out, Slot{ID: "ollama:" + m.Name, Provider: "ollama", Model: m.Name, BaseURL: base, apiKey: key})
	}
	return out
}

// DiscoverModels lists candidate models across ALL providers so the benchmark
// can test more than the one model wired per provider in the chain:
//   - ollama:     every locally-installed model (/api/tags)
//   - openrouter: only FREE models (":free"/price 0), capped (huge catalog + the
//     free tier is rate-limited — don't blow the daily quota)
//   - groq + others: all advertised models, capped (Groq free is generous)
//
// Skips models already in the chain.
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
		freeOnly := name == "openrouter"
		capN := 20
		if freeOnly {
			capN = 10
		}
		out = append(out, c.discoverViaModelsAPI(ctx, name, p, freeOnly, capN, existing)...)
	}
	return out
}

// discoverViaModelsAPI hits the OpenAI-compatible GET /models on a provider.
func (c *Client) discoverViaModelsAPI(ctx context.Context, name string, p config.AIProvider, freeOnly bool, capN int, existing map[string]bool) []Slot {
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
		if freeOnly {
			isFree := strings.HasSuffix(m.ID, ":free") ||
				((m.Pricing.Prompt == "" || m.Pricing.Prompt == "0") && (m.Pricing.Completion == "" || m.Pricing.Completion == "0"))
			if !isFree {
				continue
			}
		}
		existing[name+"|"+m.ID] = true
		out = append(out, Slot{ID: name + ":" + m.ID, Provider: name, Model: m.ID, BaseURL: base, apiKey: p.APIKey})
	}
	return out
}

// listModels returns the model ids currently advertised by a provider (Ollama:
// /api/tags; others: OpenAI-compatible /models). Used by self-heal to verify a
// failing model really is gone and to pick a replacement. Empty on any error.
func (c *Client) listModels(ctx context.Context, provider string, p config.AIProvider) []string {
	base := strings.TrimRight(p.BaseURL, "/")
	if provider == "ollama" {
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
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, base+"/models", nil)
	if p.APIKey != "" {
		req.Header.Set("Authorization", "Bearer "+p.APIKey)
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
		return // couldn't verify — leave the chain untouched
	}
	avail := map[string]bool{}
	for _, id := range ids {
		avail[id] = true
	}

	c.mu.Lock()
	defer c.mu.Unlock()
	var kept []Slot
	dropped := false
	providerHas := false
	for _, s := range c.slots {
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
	if !dropped {
		return
	}
	if !providerHas {
		repl := ids[0]
		if provider == "openrouter" { // prefer a free replacement
			for _, id := range ids {
				if strings.HasSuffix(id, ":free") {
					repl = id
					break
				}
			}
		}
		base := strings.TrimRight(p.BaseURL, "/")
		kept = append(kept, Slot{ID: provider + ":" + repl, Provider: provider, Model: repl, BaseURL: base, apiKey: p.APIKey})
		log.Printf("ai: self-heal — added %s replacement %q (untested; run the benchmark to re-optimize)", provider, repl)
	}
	c.slots = kept
}

// AdoptBenchmark rebuilds the live chain from benchmark scores: every model that
// produced a usable reply (Samples>0), ordered best-first by composite. This is
// what the user wants — "use the best benchmark" — while keeping the free local
// models in the chain as low-ranked fallbacks (the breaker skips a rate-limited
// vendor at runtime, falling through to the next, ultimately the free local).
func (c *Client) AdoptBenchmark(scores []SlotScore) {
	ranked := make([]SlotScore, 0, len(scores))
	for _, s := range scores {
		if s.Samples > 0 {
			ranked = append(ranked, s)
		}
	}
	sort.SliceStable(ranked, func(i, j int) bool { return ranked[i].Composite > ranked[j].Composite })
	defs := make([]config.AIChainSlot, 0, len(ranked))
	for _, s := range ranked {
		defs = append(defs, config.AIChainSlot{ID: s.SlotID, Provider: s.Provider, Model: s.Model})
	}
	c.ApplyChain(defs)
}

// ApplyOrder re-sorts the live chain to the given slot-id order (best first).
// Unknown ids are ignored; slots not named keep their relative order at the end.
func (c *Client) ApplyOrder(order []string) {
	rank := map[string]int{}
	for i, id := range order {
		rank[id] = i
	}
	sort.SliceStable(c.slots, func(i, j int) bool {
		ri, oki := rank[c.slots[i].ID]
		rj, okj := rank[c.slots[j].ID]
		if oki && okj {
			return ri < rj
		}
		return oki && !okj // ranked slots before unranked
	})
}
