package ai

import (
	"context"
	"encoding/json"
	"math"
	"net/http"
	"regexp"
	"sort"
	"strings"
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
	var scores []SlotScore
	for _, s := range slots {
		score := SlotScore{SlotID: s.ID, Provider: s.Provider, Model: s.Model}
		// Warmup: an untimed throwaway call so local/cold models load into memory
		// (VRAM) before we measure — otherwise the first timed call eats the load
		// time (or times out) and the model looks slow/broken. Generous timeout.
		warmCtx, warmCancel := context.WithTimeout(ctx, 120*time.Second)
		_, _, _ = c.identifyWithSlot(warmCtx, s, "warmup")
		warmCancel()

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
		scores = append(scores, score)
	}
	sort.SliceStable(scores, func(i, j int) bool { return scores[i].Composite > scores[j].Composite })
	return scores
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
